// Package webedit creates Git commits for the browser editor and publishes them
// through the repository's real Smart HTTP receive path.
package webedit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/continuity-lab/continuity-lab/internal/model"
)

const (
	MaxContentSize = 4 << 20
	maxMessageSize = 4000
	maxPathSize    = 1024
)

var (
	ErrInvalidInput = errors.New("invalid web edit input")
	ErrConflict     = errors.New("the branch changed while editing")
	ErrFileExists   = errors.New("the file already exists")
	ErrFileNotFound = errors.New("the file does not exist")
	ErrPublish      = errors.New("the web edit could not be published")
)

type Input struct {
	Branch        string `json:"branch"`
	Path          string `json:"path"`
	Content       string `json:"content"`
	BaseCommit    string `json:"base_commit"`
	CommitMessage string `json:"commit_message"`
	AuthorName    string `json:"author_name"`
	AuthorEmail   string `json:"author_email"`
	Create        bool   `json:"create"`
}

type Result struct {
	CommitOID string `json:"commit_oid"`
	Branch    string `json:"branch"`
	Path      string `json:"path"`
	Created   bool   `json:"created"`
}

type Editor struct {
	GatewayURL string
	TempRoot   string
}

func (e Editor) Commit(ctx context.Context, repository string, input Input) (Result, error) {
	if err := validateInput(input); err != nil {
		return Result{}, err
	}
	if _, err := model.CanonicalName(repository); err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	work, err := os.MkdirTemp(e.TempRoot, "continuity-web-edit-")
	if err != nil {
		return Result{}, fmt.Errorf("create temporary editor repository: %w", err)
	}
	defer os.RemoveAll(work)

	repoPath := filepath.Join(work, "repository.git")
	remote := strings.TrimRight(e.GatewayURL, "/") + "/git/" + repository + ".git"
	shortBranch := strings.TrimPrefix(input.Branch, "refs/heads/")
	if _, err := run(ctx, "", nil, nil, "clone", "--quiet", "--bare", "--single-branch", "--branch", shortBranch, remote, repoPath); err != nil {
		return Result{}, fmt.Errorf("%w: clone selected branch: %v", ErrPublish, err)
	}

	currentOutput, err := run(ctx, repoPath, nil, nil, "rev-parse", "--verify", input.Branch)
	if err != nil {
		return Result{}, fmt.Errorf("%w: selected branch is unavailable", ErrFileNotFound)
	}
	current := strings.TrimSpace(string(currentOutput))
	if current != input.BaseCommit {
		return Result{}, ErrConflict
	}

	mode, exists, err := fileMode(ctx, repoPath, input.BaseCommit, input.Path)
	if err != nil {
		return Result{}, err
	}
	if input.Create && exists {
		return Result{}, ErrFileExists
	}
	if !input.Create && !exists {
		return Result{}, ErrFileNotFound
	}
	if !exists {
		mode = "100644"
	}
	if mode != "100644" && mode != "100755" {
		return Result{}, fmt.Errorf("%w: only regular files can be edited", ErrInvalidInput)
	}

	if _, err := run(ctx, repoPath, nil, nil, "read-tree", input.BaseCommit); err != nil {
		return Result{}, fmt.Errorf("prepare Git index: %w", err)
	}
	blobOutput, err := run(ctx, repoPath, []byte(input.Content), nil, "hash-object", "-w", "--stdin")
	if err != nil {
		return Result{}, fmt.Errorf("write Git blob: %w", err)
	}
	blobOID := strings.TrimSpace(string(blobOutput))
	if !model.ValidOID(blobOID) {
		return Result{}, errors.New("git returned an invalid blob object ID")
	}
	if _, err := run(ctx, repoPath, nil, nil, "update-index", "--add", "--cacheinfo", mode, blobOID, input.Path); err != nil {
		return Result{}, fmt.Errorf("update Git index: %w", err)
	}
	treeOutput, err := run(ctx, repoPath, nil, nil, "write-tree")
	if err != nil {
		return Result{}, fmt.Errorf("write Git tree: %w", err)
	}
	treeOID := strings.TrimSpace(string(treeOutput))
	identity := []string{
		"GIT_AUTHOR_NAME=" + input.AuthorName,
		"GIT_AUTHOR_EMAIL=" + input.AuthorEmail,
		"GIT_COMMITTER_NAME=" + input.AuthorName,
		"GIT_COMMITTER_EMAIL=" + input.AuthorEmail,
	}
	commitOutput, err := run(ctx, repoPath, []byte(strings.TrimSpace(input.CommitMessage)+"\n"), identity, "commit-tree", treeOID, "-p", input.BaseCommit)
	if err != nil {
		return Result{}, fmt.Errorf("create Git commit: %w", err)
	}
	result := Result{CommitOID: strings.TrimSpace(string(commitOutput)), Branch: input.Branch, Path: input.Path, Created: input.Create}
	if !model.ValidOID(result.CommitOID) {
		return Result{}, errors.New("git returned an invalid commit object ID")
	}
	if _, err := run(ctx, repoPath, nil, nil, "push", "--porcelain", "origin", result.CommitOID+":"+input.Branch); err != nil {
		return result, fmt.Errorf("%w: %v", ErrPublish, err)
	}
	return result, nil
}

func validateInput(input Input) error {
	if !strings.HasPrefix(input.Branch, "refs/heads/") || model.ValidateRefName(input.Branch) != nil {
		return fmt.Errorf("%w: edits require a branch ref", ErrInvalidInput)
	}
	if !model.ValidOID(input.BaseCommit) || input.BaseCommit == model.ZeroOID {
		return fmt.Errorf("%w: invalid base commit", ErrInvalidInput)
	}
	if err := validatePath(input.Path); err != nil {
		return err
	}
	if len(input.Content) > MaxContentSize || !utf8.ValidString(input.Content) || strings.ContainsRune(input.Content, 0) {
		return fmt.Errorf("%w: content must be UTF-8 text no larger than 4 MiB", ErrInvalidInput)
	}
	message := strings.TrimSpace(input.CommitMessage)
	if message == "" || len(message) > maxMessageSize || containsControl(message, true) {
		return fmt.Errorf("%w: commit message must contain 1 to %d printable bytes", ErrInvalidInput, maxMessageSize)
	}
	if strings.TrimSpace(input.AuthorName) == "" || len(input.AuthorName) > 200 || containsControl(input.AuthorName, false) {
		return fmt.Errorf("%w: invalid author name", ErrInvalidInput)
	}
	address, err := mail.ParseAddress(input.AuthorEmail)
	if err != nil || address.Address != input.AuthorEmail || len(input.AuthorEmail) > 254 || containsControl(input.AuthorEmail, false) {
		return fmt.Errorf("%w: invalid author email", ErrInvalidInput)
	}
	return nil
}

func validatePath(raw string) error {
	if raw == "" || len(raw) > maxPathSize || strings.HasPrefix(raw, "/") || strings.Contains(raw, "\\") || path.Clean(raw) != raw {
		return fmt.Errorf("%w: invalid repository path", ErrInvalidInput)
	}
	for _, segment := range strings.Split(raw, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.EqualFold(segment, ".git") || len(segment) > 255 || containsControl(segment, false) {
			return fmt.Errorf("%w: invalid repository path", ErrInvalidInput)
		}
	}
	return nil
}

func containsControl(value string, allowNewline bool) bool {
	return strings.ContainsFunc(value, func(character rune) bool {
		return unicode.IsControl(character) && !(allowNewline && (character == '\n' || character == '\t'))
	})
}

func fileMode(ctx context.Context, repoPath, commit, filePath string) (string, bool, error) {
	output, err := run(ctx, repoPath, nil, nil, "ls-tree", "-z", commit, "--", ":(literal)"+filePath)
	if err != nil {
		return "", false, fmt.Errorf("inspect Git tree: %w", err)
	}
	output = bytes.TrimSuffix(output, []byte{0})
	if len(output) == 0 {
		return "", false, nil
	}
	metadata, actualPath, found := bytes.Cut(output, []byte{'\t'})
	fields := strings.Fields(string(metadata))
	if !found || string(actualPath) != filePath || len(fields) != 3 || fields[1] != "blob" {
		return "", false, fmt.Errorf("%w: path is not a regular blob", ErrInvalidInput)
	}
	return fields[0], true, nil
}

func run(ctx context.Context, gitDir string, stdin []byte, extraEnv []string, args ...string) ([]byte, error) {
	commandArgs := args
	if gitDir != "" {
		commandArgs = append([]string{"--git-dir", gitDir}, args...)
	}
	command := exec.CommandContext(ctx, "git", commandArgs...)
	command.Env = append(cleanGitEnvironment(os.Environ()), extraEnv...)
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return nil, errors.New(message)
	}
	return output, nil
}

func cleanGitEnvironment(environment []string) []string {
	blocked := map[string]bool{
		"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_INDEX_FILE": true,
		"GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
		"GIT_QUARANTINE_PATH": true, "GIT_CONFIG_GLOBAL": true,
		"GIT_CONFIG_SYSTEM": true, "GIT_CONFIG_NOSYSTEM": true,
		"GIT_AUTHOR_NAME": true, "GIT_AUTHOR_EMAIL": true, "GIT_AUTHOR_DATE": true,
		"GIT_COMMITTER_NAME": true, "GIT_COMMITTER_EMAIL": true, "GIT_COMMITTER_DATE": true,
	}
	clean := make([]string, 0, len(environment)+2)
	for _, item := range environment {
		key := item
		if separator := strings.IndexByte(item, '='); separator >= 0 {
			key = item[:separator]
		}
		if !blocked[key] {
			clean = append(clean, item)
		}
	}
	return append(clean, "GIT_OPTIONAL_LOCKS=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
}
