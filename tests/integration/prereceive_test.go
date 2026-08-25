package integration

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/continuity-lab/continuity-lab/internal/config"
	"github.com/continuity-lab/continuity-lab/internal/githooks/prereceive"
	"github.com/continuity-lab/continuity-lab/internal/model"
	"github.com/continuity-lab/continuity-lab/internal/objectstore"
	"github.com/continuity-lab/continuity-lab/internal/repository"
)

func TestPreReceiveSeesQuarantineAndUploadsInstallablePack(t *testing.T) {
	requireIntegration(t)
	cfg, err := config.Load("integration")
	if err != nil {
		t.Fatal(err)
	}
	cfg.DataDir = t.TempDir()
	cfg.NodeID = "integration-node"
	cfg.MaxRefsPerPush = 16
	name := "integration/quarantine"
	repoID := model.RepoID(name)
	repoPath := filepath.Join(cfg.DataDir, "repos", repoID+".git")
	if err := os.MkdirAll(filepath.Dir(repoPath), 0o750); err != nil {
		t.Fatal(err)
	}
	git(t, "init", "--bare", repoPath)

	work := filepath.Join(t.TempDir(), "work")
	git(t, "init", "-b", "main", work)
	git(t, "-C", work, "config", "user.name", "Continuity Lab")
	git(t, "-C", work, "config", "user.email", "lab@example.test")
	if err := os.WriteFile(filepath.Join(work, "data"), []byte("one\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	git(t, "-C", work, "add", ".")
	git(t, "-C", work, "commit", "-m", "one")
	git(t, "-C", work, "push", repoPath, "main:main")
	oldOID := strings.TrimSpace(git(t, "-C", work, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(work, "data"), []byte("one\ntwo\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	git(t, "-C", work, "commit", "-am", "two")
	newOID := strings.TrimSpace(git(t, "-C", work, "rev-parse", "HEAD"))

	objects := git(t, "-C", work, "rev-list", "--objects", newOID, "^"+oldOID)
	var objectIDs strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(objects), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			objectIDs.WriteString(fields[0])
			objectIDs.WriteByte('\n')
		}
	}
	packCommand := exec.Command("git", "-C", work, "pack-objects", "--stdout", "--delta-base-offset")
	packCommand.Stdin = strings.NewReader(objectIDs.String())
	pack, err := packCommand.Output()
	if err != nil {
		t.Fatal(err)
	}
	quarantine := filepath.Join(cfg.DataDir, "quarantine")
	if err := os.MkdirAll(quarantine, 0o750); err != nil {
		t.Fatal(err)
	}
	index := exec.Command("git", "--git-dir="+repoPath, "index-pack", "--stdin", "--strict")
	index.Env = append(os.Environ(), "GIT_OBJECT_DIRECTORY="+quarantine, "GIT_ALTERNATE_OBJECT_DIRECTORIES="+filepath.Join(repoPath, "objects"))
	index.Stdin = bytes.NewReader(pack)
	if output, err := index.CombinedOutput(); err != nil {
		t.Fatalf("index quarantine pack: %v: %s", err, output)
	}

	pushID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	t.Setenv("GIT_DIR", repoPath)
	t.Setenv("GIT_OBJECT_DIRECTORY", quarantine)
	t.Setenv("GIT_ALTERNATE_OBJECT_DIRECTORIES", filepath.Join(repoPath, "objects"))
	t.Setenv("CONTINUITY_PUSH_ID", pushID)
	t.Setenv("CONTINUITY_REPO_ID", repoID)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := objectstore.NewS3(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	commands := oldOID + " " + newOID + " refs/heads/main\n"
	if err := prereceive.Run(ctx, strings.NewReader(commands), io.Discard, cfg, store); err != nil {
		t.Fatal(err)
	}
	manager := repository.Manager{Config: cfg, Store: store}
	var pending model.Pending
	if err := repository.ReadJSON(manager.PendingPath(repoID, pushID), &pending); err != nil {
		t.Fatal(err)
	}
	if pending.Payload == nil || pending.Payload.PackSize == 0 {
		t.Fatal("pre-receive did not persist a payload")
	}
	object, err := store.Get(ctx, pending.Payload.PackKey)
	if err != nil {
		t.Fatal(err)
	}
	defer object.Body.Close()
	durablePack, err := io.ReadAll(object.Body)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "destination.git")
	cleanEnv := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
	clone := exec.Command("git", "clone", "--bare", repoPath, destination)
	clone.Env = cleanEnv
	if output, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("clone baseline: %v: %s", err, output)
	}
	install := exec.Command("git", "--git-dir="+destination, "index-pack", "--stdin", "--strict")
	install.Env = cleanEnv
	install.Stdin = bytes.NewReader(durablePack)
	if output, err := install.CombinedOutput(); err != nil {
		t.Fatalf("install durable pack: %v: %s", err, output)
	}
	verify := exec.Command("git", "--git-dir="+destination, "cat-file", "-e", newOID+"^{commit}")
	verify.Env = cleanEnv
	if output, err := verify.CombinedOutput(); err != nil {
		t.Fatalf("new commit missing from durable pack: %v: %s", err, output)
	}
}
