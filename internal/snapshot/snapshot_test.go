package snapshot

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/continuity-lab/continuity-lab/internal/repository"
)

func TestFullSnapshotPackRestoresEmptyRepository(t *testing.T) {
	work := filepath.Join(t.TempDir(), "work")
	runGit(t, nil, "init", "-b", "main", work)
	runGit(t, nil, "-C", work, "config", "user.name", "Continuity Lab")
	runGit(t, nil, "-C", work, "config", "user.email", "lab@example.test")
	if err := os.WriteFile(filepath.Join(work, "data"), []byte("one\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	runGit(t, nil, "-C", work, "add", ".")
	runGit(t, nil, "-C", work, "commit", "-m", "one")
	if err := os.WriteFile(filepath.Join(work, "data"), []byte("one\ntwo\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	runGit(t, nil, "-C", work, "commit", "-am", "two")
	source := filepath.Join(t.TempDir(), "source.git")
	runGit(t, nil, "clone", "--bare", work, source)
	refs, err := repository.ReadRefs(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	packPath, _, _, err := generatePack(context.Background(), source, refs)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(packPath)
	pack, err := os.ReadFile(packPath)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "destination.git")
	runGit(t, nil, "init", "--bare", destination)
	runGit(t, pack, "--git-dir="+destination, "index-pack", "--stdin", "--strict")
	if err := repository.SetRefs(context.Background(), destination, map[string]string{}, refs); err != nil {
		t.Fatal(err)
	}
	runGit(t, nil, "--git-dir="+destination, "fsck", "--full")
	restored, err := repository.ReadRefs(context.Background(), destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != len(refs) {
		t.Fatalf("restored refs=%v want %v", restored, refs)
	}
	for ref, oid := range refs {
		if restored[ref] != oid {
			t.Fatalf("%s=%s want %s", ref, restored[ref], oid)
		}
	}
}

func runGit(t *testing.T, input []byte, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	if input != nil {
		command.Stdin = bytes.NewReader(input)
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}
