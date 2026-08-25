package gitbrowse

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBrowseRepository(t *testing.T) {
	repo := createRepository(t)
	ctx := context.Background()

	refs, err := ListRefs(ctx, repo, "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs.Branches) != 1 || refs.Branches[0].Name != "refs/heads/main" {
		t.Fatalf("unexpected refs: %#v", refs)
	}

	tree, err := ListTree(ctx, repo, "refs/heads/main", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Entries) != 2 || tree.Entries[0].Name != "README.md" || tree.Entries[1].Name != "src" {
		t.Fatalf("unexpected root tree: %#v", tree.Entries)
	}

	subtree, err := ListTree(ctx, repo, "refs/heads/main", "src")
	if err != nil {
		t.Fatal(err)
	}
	if len(subtree.Entries) != 2 || subtree.Entries[0].Path != "src/app.go" {
		t.Fatalf("unexpected subtree: %#v", subtree.Entries)
	}

	blob, err := ReadBlob(ctx, repo, "refs/heads/main", "src/app.go")
	if err != nil {
		t.Fatal(err)
	}
	if blob.Binary || blob.Content != "package main\n" || blob.Commit != tree.Commit {
		t.Fatalf("unexpected text blob: %#v", blob)
	}

	binary, err := ReadBlob(ctx, repo, "refs/heads/main", "src/data.bin")
	if err != nil {
		t.Fatal(err)
	}
	if !binary.Binary || binary.Encoding != "base64" {
		t.Fatalf("expected binary blob: %#v", binary)
	}
	decoded, err := base64.StdEncoding.DecodeString(binary.Content)
	if err != nil || string(decoded) != "\x00\x01\x02" {
		t.Fatalf("unexpected binary content %q: %v", decoded, err)
	}

	commits, err := ListCommits(ctx, repo, "refs/heads/main", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 || commits[0].Subject != "add source" || commits[1].Subject != "initial" {
		t.Fatalf("unexpected commits: %#v", commits)
	}

	detail, err := GetCommit(ctx, repo, commits[0].OID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Changes) != 2 || detail.Changes[0].Status != "A" {
		t.Fatalf("unexpected commit detail: %#v", detail)
	}
}

func TestRejectsUnsafeInputs(t *testing.T) {
	repo := createRepository(t)
	ctx := context.Background()
	for _, revision := range []string{"", "main", "--all", "refs/heads/main~1"} {
		if _, err := ResolveCommit(ctx, repo, revision); err == nil {
			t.Fatalf("expected revision %q to fail", revision)
		}
	}
	for _, unsafePath := range []string{"../secret", "/absolute", "src/../README.md", "src\\app.go"} {
		if _, err := ListTree(ctx, repo, "refs/heads/main", unsafePath); err == nil {
			t.Fatalf("expected path %q to fail", unsafePath)
		}
	}
}

func createRepository(t *testing.T) string {
	t.Helper()
	work := t.TempDir()
	runGit(t, work, "init", "-q", "-b", "main")
	runGit(t, work, "config", "user.name", "Continuity Tester")
	runGit(t, work, "config", "user.email", "test@example.test")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("# Demo\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "README.md")
	runGit(t, work, "commit", "-q", "-m", "initial")
	if err := os.MkdirAll(filepath.Join(work, "src"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "src", "app.go"), []byte("package main\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "src", "data.bin"), []byte{0, 1, 2}, 0o640); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "src")
	runGit(t, work, "commit", "-q", "-m", "add source")
	bare := filepath.Join(t.TempDir(), "repo.git")
	runGit(t, work, "clone", "-q", "--bare", work, bare)
	return bare
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}
