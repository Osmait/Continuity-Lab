// Package gitbrowse exposes a bounded, read-only view of a bare Git repository.
package gitbrowse

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/continuity-lab/continuity-lab/internal/model"
)

const (
	MaxBlobSize = 4 << 20
	MaxLogLimit = 100
)

var (
	ErrInvalidRevision = errors.New("invalid Git revision")
	ErrInvalidPath     = errors.New("invalid repository path")
	ErrNotFound        = errors.New("Git object not found")
)

type Ref struct {
	Name       string `json:"name"`
	ShortName  string `json:"short_name"`
	OID        string `json:"oid"`
	ObjectType string `json:"object_type"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	Subject    string `json:"subject,omitempty"`
}

type Refs struct {
	DefaultBranch string `json:"default_branch"`
	Branches      []Ref  `json:"branches"`
	Tags          []Ref  `json:"tags"`
}

type TreeEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	OID  string `json:"oid"`
	Size int64  `json:"size,omitempty"`
}

type Tree struct {
	Revision string      `json:"revision"`
	Commit   string      `json:"commit"`
	Path     string      `json:"path"`
	Entries  []TreeEntry `json:"entries"`
}

type Blob struct {
	Revision  string `json:"revision"`
	Commit    string `json:"commit"`
	Path      string `json:"path"`
	OID       string `json:"oid"`
	Size      int64  `json:"size"`
	Encoding  string `json:"encoding"`
	Content   string `json:"content"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
}

type Commit struct {
	OID         string   `json:"oid"`
	Parents     []string `json:"parents"`
	AuthorName  string   `json:"author_name"`
	AuthorEmail string   `json:"author_email"`
	AuthoredAt  string   `json:"authored_at"`
	Subject     string   `json:"subject"`
}

type Change struct {
	Status string `json:"status"`
	Path   string `json:"path"`
}

type CommitDetail struct {
	Commit
	Body    string   `json:"body"`
	Changes []Change `json:"changes"`
}

func ListRefs(ctx context.Context, repoPath, defaultBranch string) (Refs, error) {
	output, err := run(ctx, repoPath, "for-each-ref", "--sort=-creatordate", "--format=%(refname)%00%(objectname)%00%(objecttype)%00%(creatordate:iso-strict)%00%(subject)%00", "refs/heads", "refs/tags")
	if err != nil {
		return Refs{}, err
	}
	result := Refs{DefaultBranch: defaultBranch, Branches: []Ref{}, Tags: []Ref{}}
	for _, record := range bytes.Split(bytes.TrimSpace(output), []byte{'\n'}) {
		fields := bytes.Split(record, []byte{0})
		if len(fields) < 5 {
			continue
		}
		name := string(fields[0])
		item := Ref{Name: name, OID: string(fields[1]), ObjectType: string(fields[2]), UpdatedAt: string(fields[3]), Subject: string(fields[4])}
		switch {
		case strings.HasPrefix(name, "refs/heads/"):
			item.ShortName = strings.TrimPrefix(name, "refs/heads/")
			result.Branches = append(result.Branches, item)
		case strings.HasPrefix(name, "refs/tags/"):
			item.ShortName = strings.TrimPrefix(name, "refs/tags/")
			result.Tags = append(result.Tags, item)
		}
	}
	return result, nil
}

func ListTree(ctx context.Context, repoPath, revision, requestedPath string) (Tree, error) {
	commit, err := ResolveCommit(ctx, repoPath, revision)
	if err != nil {
		return Tree{}, err
	}
	cleanPath, err := validatePath(requestedPath)
	if err != nil {
		return Tree{}, err
	}
	treeOID, err := resolvePathObject(ctx, repoPath, commit, cleanPath, "tree")
	if err != nil {
		return Tree{}, err
	}
	output, err := run(ctx, repoPath, "ls-tree", "-z", "-l", treeOID)
	if err != nil {
		return Tree{}, classifyGitError(err)
	}
	result := Tree{Revision: revision, Commit: commit, Path: cleanPath, Entries: []TreeEntry{}}
	for _, record := range bytes.Split(output, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		metadata, name, found := bytes.Cut(record, []byte{'\t'})
		if !found {
			return Tree{}, errors.New("unexpected git ls-tree output")
		}
		fields := strings.Fields(string(metadata))
		if len(fields) != 4 || !model.ValidOID(fields[2]) {
			return Tree{}, errors.New("invalid git ls-tree metadata")
		}
		size := int64(0)
		if fields[3] != "-" {
			size, err = strconv.ParseInt(fields[3], 10, 64)
			if err != nil || size < 0 {
				return Tree{}, errors.New("invalid git object size")
			}
		}
		entryName := string(name)
		entryPath := entryName
		if cleanPath != "" {
			entryPath = cleanPath + "/" + entryName
		}
		result.Entries = append(result.Entries, TreeEntry{Name: entryName, Path: entryPath, Mode: fields[0], Type: fields[1], OID: fields[2], Size: size})
	}
	return result, nil
}

func ReadBlob(ctx context.Context, repoPath, revision, requestedPath string) (Blob, error) {
	commit, err := ResolveCommit(ctx, repoPath, revision)
	if err != nil {
		return Blob{}, err
	}
	cleanPath, err := validatePath(requestedPath)
	if err != nil || cleanPath == "" {
		return Blob{}, ErrInvalidPath
	}
	oid, err := resolvePathObject(ctx, repoPath, commit, cleanPath, "blob")
	if err != nil {
		return Blob{}, err
	}
	sizeOutput, err := run(ctx, repoPath, "cat-file", "-s", oid)
	if err != nil {
		return Blob{}, classifyGitError(err)
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(sizeOutput)), 10, 64)
	if err != nil {
		return Blob{}, fmt.Errorf("parse blob size: %w", err)
	}
	if size < 0 {
		return Blob{}, errors.New("invalid blob size")
	}
	result := Blob{Revision: revision, Commit: commit, Path: cleanPath, OID: oid, Size: size, Encoding: "utf-8"}
	if size > MaxBlobSize {
		result.Truncated = true
		return result, nil
	}
	content, err := run(ctx, repoPath, "cat-file", "blob", oid)
	if err != nil {
		return Blob{}, classifyGitError(err)
	}
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		result.Binary = true
		result.Encoding = "base64"
		result.Content = base64.StdEncoding.EncodeToString(content)
		return result, nil
	}
	result.Content = string(content)
	return result, nil
}

func ListCommits(ctx context.Context, repoPath, revision string, limit int) ([]Commit, error) {
	commit, err := ResolveCommit(ctx, repoPath, revision)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 30
	}
	if limit > MaxLogLimit {
		limit = MaxLogLimit
	}
	output, err := run(ctx, repoPath, "log", "-z", "-n", strconv.Itoa(limit), "--format=%H%x00%P%x00%an%x00%ae%x00%aI%x00%s%x00", commit)
	if err != nil {
		return nil, classifyGitError(err)
	}
	return parseCommits(output)
}

func GetCommit(ctx context.Context, repoPath, oid string) (CommitDetail, error) {
	if !model.ValidOID(oid) {
		return CommitDetail{}, ErrInvalidRevision
	}
	commits, err := ListCommits(ctx, repoPath, oid, 1)
	if err != nil {
		return CommitDetail{}, err
	}
	if len(commits) != 1 || commits[0].OID != oid {
		return CommitDetail{}, ErrNotFound
	}
	bodyOutput, err := run(ctx, repoPath, "show", "-s", "--format=%B", oid)
	if err != nil {
		return CommitDetail{}, classifyGitError(err)
	}
	changesOutput, err := run(ctx, repoPath, "diff-tree", "--root", "--no-commit-id", "--no-renames", "--name-status", "-r", "-z", oid)
	if err != nil {
		return CommitDetail{}, classifyGitError(err)
	}
	fields := bytes.Split(changesOutput, []byte{0})
	changes := make([]Change, 0, len(fields)/2)
	for index := 0; index+1 < len(fields); index += 2 {
		if len(fields[index]) == 0 || len(fields[index+1]) == 0 {
			continue
		}
		changes = append(changes, Change{Status: string(fields[index]), Path: string(fields[index+1])})
	}
	return CommitDetail{Commit: commits[0], Body: strings.TrimSpace(string(bodyOutput)), Changes: changes}, nil
}

func ResolveCommit(ctx context.Context, repoPath, revision string) (string, error) {
	if revision == "" {
		return "", ErrInvalidRevision
	}
	if !model.ValidOID(revision) {
		if model.ValidateRefName(revision) != nil || (!strings.HasPrefix(revision, "refs/heads/") && !strings.HasPrefix(revision, "refs/tags/")) {
			return "", ErrInvalidRevision
		}
	}
	output, err := run(ctx, repoPath, "rev-parse", "--verify", "--end-of-options", revision+"^{commit}")
	if err != nil {
		return "", ErrNotFound
	}
	oid := strings.TrimSpace(string(output))
	if !model.ValidOID(oid) {
		return "", ErrNotFound
	}
	return oid, nil
}

func resolvePathObject(ctx context.Context, repoPath, commit, cleanPath, expectedType string) (string, error) {
	spec := commit + "^{tree}"
	if cleanPath != "" {
		spec = commit + ":" + cleanPath
	}
	output, err := run(ctx, repoPath, "rev-parse", "--verify", "--end-of-options", spec)
	if err != nil {
		return "", ErrNotFound
	}
	oid := strings.TrimSpace(string(output))
	if !model.ValidOID(oid) {
		return "", ErrNotFound
	}
	typeOutput, err := run(ctx, repoPath, "cat-file", "-t", oid)
	if err != nil || strings.TrimSpace(string(typeOutput)) != expectedType {
		return "", ErrNotFound
	}
	return oid, nil
}

func parseCommits(output []byte) ([]Commit, error) {
	result := make([]Commit, 0)
	for offset := 0; offset < len(output); {
		for offset < len(output) && (output[offset] == 0 || output[offset] == '\n') {
			offset++
		}
		if offset == len(output) {
			break
		}
		fields := make([]string, 6)
		for index := range fields {
			end := bytes.IndexByte(output[offset:], 0)
			if end < 0 {
				return nil, errors.New("unexpected git log output")
			}
			fields[index] = string(output[offset : offset+end])
			offset += end + 1
		}
		if !model.ValidOID(fields[0]) {
			return nil, errors.New("invalid commit object ID")
		}
		parents := strings.Fields(fields[1])
		for _, parent := range parents {
			if !model.ValidOID(parent) {
				return nil, errors.New("invalid commit parent")
			}
		}
		result = append(result, Commit{OID: fields[0], Parents: parents, AuthorName: fields[2], AuthorEmail: fields[3], AuthoredAt: fields[4], Subject: fields[5]})
	}
	return result, nil
}

func validatePath(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if strings.ContainsRune(raw, 0) || strings.Contains(raw, "\\") || strings.HasPrefix(raw, "/") || path.Clean(raw) != raw {
		return "", ErrInvalidPath
	}
	for _, segment := range strings.Split(raw, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", ErrInvalidPath
		}
	}
	return raw, nil
}

func classifyGitError(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return fmt.Errorf("%w: %s", ErrNotFound, strings.TrimSpace(string(exitErr.Stderr)))
	}
	return err
}

func run(ctx context.Context, repoPath string, args ...string) ([]byte, error) {
	commandArgs := append([]string{"--git-dir", repoPath}, args...)
	command := exec.CommandContext(ctx, "git", commandArgs...)
	command.Env = cleanGitEnvironment(os.Environ())
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	return output, nil
}

func cleanGitEnvironment(environment []string) []string {
	blocked := map[string]bool{
		"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_INDEX_FILE": true,
		"GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
		"GIT_QUARANTINE_PATH": true,
	}
	clean := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		key := item
		if separator := strings.IndexByte(item, '='); separator >= 0 {
			key = item[:separator]
		}
		if !blocked[key] {
			clean = append(clean, item)
		}
	}
	return append(clean, "GIT_OPTIONAL_LOCKS=0")
}
