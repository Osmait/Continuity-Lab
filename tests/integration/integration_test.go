package integration

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/continuity-lab/continuity-lab/internal/config"
	"github.com/continuity-lab/continuity-lab/internal/model"
	"github.com/continuity-lab/continuity-lab/internal/objectstore"
	"github.com/continuity-lab/continuity-lab/internal/wal"
)

func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("CONTINUITY_INTEGRATION") != "1" {
		t.Skip("set CONTINUITY_INTEGRATION=1")
	}
}

func TestRealMinIOConditionalOperations(t *testing.T) {
	requireIntegration(t)
	cfg, err := config.Load("integration")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := objectstore.NewS3(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	if err := objectstore.Conformance(ctx, store); err != nil {
		t.Fatal(err)
	}
}

func TestRealGitUpdateRefPrepareCommitAndAbort(t *testing.T) {
	requireIntegration(t)
	repo := filepath.Join(t.TempDir(), "repo.git")
	git(t, "init", "--bare", repo)
	commitBody := "tree " + emptyTree(t, repo) + "\nauthor Continuity Lab <lab@example.test> 0 +0000\ncommitter Continuity Lab <lab@example.test> 0 +0000\n\nmessage\n"
	commit := strings.TrimSpace(gitInput(t, repo, []byte(commitBody), "hash-object", "-t", "commit", "-w", "--stdin"))
	update := model.RefUpdate{Ref: "refs/heads/main", OldOID: model.ZeroOID, NewOID: commit}
	tx, err := wal.StartRefTransaction(context.Background(), repo, []model.RefUpdate{update})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Abort(); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(git(t, "--git-dir="+repo, "show-ref")); got != "" {
		t.Fatalf("abort left refs: %s", got)
	}
	tx, err = wal.StartRefTransaction(context.Background(), repo, []model.RefUpdate{update})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(git(t, "--git-dir="+repo, "rev-parse", "refs/heads/main")); got != commit {
		t.Fatalf("commit ref=%s want %s", got, commit)
	}
}

func emptyTree(t *testing.T, repo string) string {
	t.Helper()
	return strings.TrimSpace(gitInput(t, repo, nil, "mktree"))
}
func git(t *testing.T, args ...string) string {
	t.Helper()
	output, err := exec.Command("git", args...).CombinedOutput()
	if err != nil && len(bytes.TrimSpace(output)) != 0 && !strings.Contains(string(output), "No refs") {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return string(output)
}
func gitInput(t *testing.T, repo string, input []byte, args ...string) string {
	t.Helper()
	full := append([]string{"--git-dir=" + repo}, args...)
	command := exec.Command("git", full...)
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return string(output)
}
