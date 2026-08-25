package snapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/continuity-lab/continuity-lab/internal/admin"
	"github.com/continuity-lab/continuity-lab/internal/model"
	"github.com/continuity-lab/continuity-lab/internal/objectstore"
	"github.com/continuity-lab/continuity-lab/internal/observability"
	"github.com/continuity-lab/continuity-lab/internal/repository"
	"github.com/oklog/ulid/v2"
)

type Compactor struct {
	NodeID string
	Store  objectstore.ObjectStore
	Repos  *repository.Manager
}

func (c Compactor) Compact(ctx context.Context, name string) (model.Snapshot, objectstore.ETag, error) {
	result := "error"
	defer func() {
		_ = observability.Emit(c.Repos.Config.DataDir, observability.Event{Name: "compaction", Labels: map[string]string{"result": result}})
	}()
	manifest, head, etag, err := c.Repos.EnsureFresh(ctx, name)
	if err != nil {
		return model.Snapshot{}, "", err
	}
	packPath, sha, size, err := generatePack(ctx, c.Repos.RepoPath(manifest.RepoID), head.Refs)
	if err != nil {
		return model.Snapshot{}, "", err
	}
	defer os.Remove(packPath)
	packKey := model.SnapshotPackKey(manifest.RepoID, sha)
	file, err := os.Open(packPath)
	if err != nil {
		return model.Snapshot{}, "", err
	}
	_, _, putErr := c.Store.PutImmutable(ctx, packKey, file, size, "application/x-git-packed-objects")
	_ = file.Close()
	if putErr != nil {
		return model.Snapshot{}, "", putErr
	}
	id := ulid.Make().String()
	metadata := model.Snapshot{SchemaVersion: model.SchemaVersion, SnapshotID: id, RepoID: manifest.RepoID, Sequence: head.Sequence, CreatedAt: time.Now().UTC(), NodeID: c.NodeID, PackKey: packKey, PackSHA256: sha, PackSize: size, Refs: head.Refs}
	metadataKey := model.SnapshotMetadataKey(manifest.RepoID, id)
	body, _ := model.Marshal(metadata)
	if _, _, err := c.Store.PutImmutable(ctx, metadataKey, bytes.NewReader(body), int64(len(body)), "application/json"); err != nil {
		return model.Snapshot{}, "", err
	}
	current, currentETag, err := c.Repos.Head(ctx, manifest.RepoID)
	if err != nil {
		return model.Snapshot{}, "", err
	}
	if current.Sequence != head.Sequence || currentETag != etag {
		return model.Snapshot{}, "", errors.New("head changed during compaction")
	}
	current.Revision++
	current.Snapshot = &model.SnapshotPointer{SnapshotID: id, Sequence: metadata.Sequence, MetadataKey: metadataKey, PackKey: packKey, PackSHA256: sha}
	current.UpdatedAt = time.Now().UTC()
	headBody, _ := model.Marshal(current)
	newETag, err := c.Store.PutIfMatch(ctx, model.HeadKey(manifest.RepoID), currentETag, bytes.NewReader(headBody), int64(len(headBody)), "application/json")
	if err != nil {
		return model.Snapshot{}, "", err
	}
	manifestStored, manifestETag, err := admin.GetJSON[model.Manifest](ctx, c.Store, model.ManifestKey(manifest.RepoID))
	if err == nil {
		_ = c.Repos.SaveReady(ctx, manifestStored, manifestETag, current, newETag)
	}
	result = "success"
	return metadata, newETag, nil
}

func generatePack(ctx context.Context, repoPath string, refs map[string]string) (string, string, int64, error) {
	file, err := os.CreateTemp("", "continuity-snapshot-*.pack")
	if err != nil {
		return "", "", 0, err
	}
	path := file.Name()
	cleanup := func(e error) (string, string, int64, error) {
		_ = file.Close()
		_ = os.Remove(path)
		return "", "", 0, e
	}
	keys := make([]string, 0, len(refs))
	for key := range refs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var revisions strings.Builder
	for _, key := range keys {
		revisions.WriteString(refs[key])
		revisions.WriteByte('\n')
	}
	command := exec.CommandContext(ctx, "git", "--git-dir="+repoPath, "pack-objects", "--stdout", "--revs", "--delta-base-offset")
	command.Stdin = strings.NewReader(revisions.String())
	hash := sha256.New()
	command.Stdout = io.MultiWriter(file, hash)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return cleanup(fmt.Errorf("generate snapshot pack: %w: %s", err, stderr.String()))
	}
	if err := file.Sync(); err != nil {
		return cleanup(err)
	}
	stat, err := file.Stat()
	if err != nil {
		return cleanup(err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", "", 0, err
	}
	file = nil
	sha := hex.EncodeToString(hash.Sum(nil))
	if err := validate(ctx, path); err != nil {
		_ = os.Remove(path)
		return "", "", 0, err
	}
	return path, sha, stat.Size(), nil
}

func validate(ctx context.Context, path string) error {
	dir, err := os.MkdirTemp("", "continuity-snapshot-check-*.git")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if output, err := exec.CommandContext(ctx, "git", "init", "--bare", dir).CombinedOutput(); err != nil {
		return fmt.Errorf("init snapshot validator: %w: %s", err, output)
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	command := exec.CommandContext(ctx, "git", "--git-dir="+dir, "index-pack", "--stdin", "--strict")
	command.Stdin = file
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("validate snapshot pack: %w: %s", err, output)
	}
	return nil
}
