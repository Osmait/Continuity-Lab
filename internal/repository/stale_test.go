package repository

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/continuity-lab/continuity-lab/internal/config"
	"github.com/continuity-lab/continuity-lab/internal/model"
	"github.com/continuity-lab/continuity-lab/internal/objectstore"
)

type unavailableStore struct{}

func (unavailableStore) EnsureBucket(context.Context) error { return objectstore.ErrStoreUnavailable }
func (unavailableStore) Get(context.Context, string) (objectstore.Object, error) {
	return objectstore.Object{}, objectstore.ErrStoreUnavailable
}
func (unavailableStore) GetIfChanged(context.Context, string, objectstore.ETag) (*objectstore.Object, bool, error) {
	return nil, false, objectstore.ErrStoreUnavailable
}
func (unavailableStore) Head(context.Context, string) (objectstore.ETag, int64, error) {
	return "", 0, objectstore.ErrStoreUnavailable
}
func (unavailableStore) PutImmutable(context.Context, string, io.Reader, int64, string) (bool, objectstore.ETag, error) {
	return false, "", objectstore.ErrStoreUnavailable
}
func (unavailableStore) PutIfMatch(context.Context, string, objectstore.ETag, io.Reader, int64, string) (objectstore.ETag, error) {
	return "", objectstore.ErrStoreUnavailable
}
func (unavailableStore) Delete(context.Context, string) error { return objectstore.ErrStoreUnavailable }
func (unavailableStore) List(context.Context, string) ([]objectstore.ObjectInfo, error) {
	return nil, objectstore.ErrStoreUnavailable
}

func TestStaleReadOverrideNeverAppliesToStrictFreshness(t *testing.T) {
	dataDir := t.TempDir()
	name := "acme/stale"
	repoID := model.RepoID(name)
	repoPath := filepath.Join(dataDir, "repos", repoID+".git")
	if output, err := exec.Command("git", "init", "--bare", repoPath).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	now := time.Now().UTC()
	state := model.LocalState{SchemaVersion: model.SchemaVersion, RepoID: repoID, HeadETag: `"cached"`, RefsSHA256: model.RefsChecksum(map[string]string{}), Status: "ready", LastAccessAt: now, UpdatedAt: now}
	manager := Manager{Config: config.Config{DataDir: dataDir, AllowStaleReads: true}, Store: unavailableStore{}}
	if err := atomicJSON(manager.StatePath(repoID), state, 0o640); err != nil {
		t.Fatal(err)
	}
	manifest, head, etag, err := manager.EnsureFreshRead(context.Background(), name)
	if err != nil {
		t.Fatalf("stale read rejected: %v", err)
	}
	if manifest.Name != name || head.Sequence != 0 || etag != objectstore.ETag(`"cached"`) {
		t.Fatalf("unexpected stale result: %#v %#v %q", manifest, head, etag)
	}
	if _, _, _, err := manager.EnsureFresh(context.Background(), name); err == nil {
		t.Fatal("strict freshness unexpectedly used stale cache")
	}
}
