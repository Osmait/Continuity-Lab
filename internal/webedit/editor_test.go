package webedit

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitEditsAndCreatesFiles(t *testing.T) {
	gatewayRoot, remote, base := createRemote(t)
	editor := Editor{GatewayURL: "file://" + gatewayRoot, TempRoot: t.TempDir()}
	identity := Input{
		Branch:        "refs/heads/main",
		BaseCommit:    base,
		CommitMessage: "Update README from web",
		AuthorName:    "Web Editor",
		AuthorEmail:   "web@example.test",
	}

	edit := identity
	edit.Path = "README.md"
	edit.Content = "# Edited\n"
	result, err := editor.Commit(context.Background(), "acme/demo", edit)
	if err != nil {
		t.Fatal(err)
	}
	if result.Created || result.CommitOID == base {
		t.Fatalf("unexpected edit result: %#v", result)
	}
	if got := gitOutput(t, "", "--git-dir", remote, "show", result.CommitOID+":README.md"); got != "# Edited\n" {
		t.Fatalf("unexpected edited content %q", got)
	}

	create := identity
	create.BaseCommit = result.CommitOID
	create.Path = "docs/notes.txt"
	create.Content = "created in the browser\n"
	create.CommitMessage = "Create browser notes"
	create.Create = true
	created, err := editor.Commit(context.Background(), "acme/demo", create)
	if err != nil {
		t.Fatal(err)
	}
	if !created.Created {
		t.Fatalf("expected create result: %#v", created)
	}
	if got := gitOutput(t, "", "--git-dir", remote, "show", created.CommitOID+":"+create.Path); got != create.Content {
		t.Fatalf("unexpected created content %q", got)
	}
	if author := strings.TrimSpace(gitOutput(t, "", "--git-dir", remote, "show", "-s", "--format=%an <%ae>", created.CommitOID)); author != "Web Editor <web@example.test>" {
		t.Fatalf("unexpected author %q", author)
	}
}

func TestCommitRejectsConflictsAndUnsafeInput(t *testing.T) {
	gatewayRoot, _, base := createRemote(t)
	editor := Editor{GatewayURL: "file://" + gatewayRoot, TempRoot: t.TempDir()}
	valid := Input{
		Branch:        "refs/heads/main",
		Path:          "README.md",
		Content:       "first edit\n",
		BaseCommit:    base,
		CommitMessage: "First edit",
		AuthorName:    "Web Editor",
		AuthorEmail:   "web@example.test",
	}
	if _, err := editor.Commit(context.Background(), "acme/demo", valid); err != nil {
		t.Fatal(err)
	}
	valid.Content = "stale edit\n"
	if _, err := editor.Commit(context.Background(), "acme/demo", valid); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}

	for _, unsafePath := range []string{"../secret", "/absolute", ".git/config", "src\\file.go", "src/../README.md"} {
		input := valid
		input.Path = unsafePath
		if _, err := editor.Commit(context.Background(), "acme/demo", input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("expected invalid path %q, got %v", unsafePath, err)
		}
	}
}

func createRemote(t *testing.T) (gatewayRoot, remote, base string) {
	t.Helper()
	gatewayRoot = t.TempDir()
	remote = filepath.Join(gatewayRoot, "git", "acme", "demo.git")
	if err := os.MkdirAll(filepath.Dir(remote), 0o750); err != nil {
		t.Fatal(err)
	}
	gitOutput(t, "", "init", "--quiet", "--bare", remote)

	source := t.TempDir()
	gitOutput(t, source, "init", "--quiet", "-b", "main")
	gitOutput(t, source, "config", "user.name", "Fixture")
	gitOutput(t, source, "config", "user.email", "fixture@example.test")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("# Initial\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	gitOutput(t, source, "add", "README.md")
	gitOutput(t, source, "commit", "--quiet", "-m", "Initial")
	gitOutput(t, source, "remote", "add", "origin", remote)
	gitOutput(t, source, "push", "--quiet", "origin", "main")
	base = strings.TrimSpace(gitOutput(t, source, "rev-parse", "HEAD"))
	return gatewayRoot, remote, base
}

func gitOutput(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	if directory != "" {
		command.Dir = directory
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return string(output)
}
