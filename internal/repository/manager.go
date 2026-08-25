package repository

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/continuity-lab/continuity-lab/internal/admin"
	"github.com/continuity-lab/continuity-lab/internal/config"
	"github.com/continuity-lab/continuity-lab/internal/model"
	"github.com/continuity-lab/continuity-lab/internal/objectstore"
	"github.com/continuity-lab/continuity-lab/internal/observability"
	"github.com/oklog/ulid/v2"
)

type Manager struct {
	Config config.Config
	Store  objectstore.ObjectStore
}

func (m *Manager) RepoPath(repoID string) string {
	return filepath.Join(m.Config.DataDir, "repos", repoID+".git")
}
func (m *Manager) StatePath(repoID string) string {
	return filepath.Join(m.Config.DataDir, "state", repoID+".json")
}
func (m *Manager) PendingPath(repoID, pushID string) string {
	return filepath.Join(m.Config.DataDir, "pending", repoID, pushID+".json")
}
func (m *Manager) ReceiptPath(repoID, pushID string) string {
	return filepath.Join(m.Config.DataDir, "pending", repoID, pushID+".receipt.json")
}

func (m *Manager) PrepareDirs() error {
	for _, name := range []string{"repos", "state", "locks", "pending", "staging", "quarantine-metadata"} {
		if err := os.MkdirAll(filepath.Join(m.Config.DataDir, name), 0o750); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) EnsureFresh(ctx context.Context, name string) (model.Manifest, model.Head, objectstore.ETag, error) {
	return m.ensureFresh(ctx, name, false)
}

// EnsureFreshRead permits a verified local cache only when the configured lab
// stale-read override is enabled and MinIO is unavailable. Writes and
// maintenance always call EnsureFresh and therefore remain strict.
func (m *Manager) EnsureFreshRead(ctx context.Context, name string) (model.Manifest, model.Head, objectstore.ETag, error) {
	return m.ensureFresh(ctx, name, m.Config.AllowStaleReads)
}

func (m *Manager) ensureFresh(ctx context.Context, name string, allowStale bool) (model.Manifest, model.Head, objectstore.ETag, error) {
	canonical, err := model.CanonicalName(name)
	if err != nil {
		return model.Manifest{}, model.Head{}, "", err
	}
	repoID := model.RepoID(canonical)
	var state model.LocalState
	stateErr := readJSON(m.StatePath(repoID), &state)
	_, repoErr := os.Stat(m.RepoPath(repoID))
	localReady := repoErr == nil && stateErr == nil && state.RepoID == repoID && state.Status == "ready"

	manifest, manifestETag, err := admin.GetJSON[model.Manifest](ctx, m.Store, model.ManifestKey(repoID))
	if err != nil {
		if allowStale && localReady && errors.Is(err, objectstore.ErrStoreUnavailable) {
			resultManifest, resultHead, resultETag, staleErr := m.verifiedStale(ctx, canonical, state)
			m.emitStrongRead("stale", staleErr)
			return resultManifest, resultHead, resultETag, staleErr
		}
		m.emitStrongRead("error", err)
		return model.Manifest{}, model.Head{}, "", err
	}
	if err := manifest.Validate(); err != nil || manifest.Name != canonical {
		return model.Manifest{}, model.Head{}, "", errors.New("manifest validation failed")
	}
	if !localReady {
		head, etag, err := m.materialize(ctx, manifest, manifestETag)
		m.emitStrongRead("materialized", err)
		return manifest, head, etag, err
	}
	changed, notModified, err := m.Store.GetIfChanged(ctx, model.HeadKey(repoID), objectstore.ETag(state.HeadETag))
	if err != nil {
		if allowStale && errors.Is(err, objectstore.ErrStoreUnavailable) {
			resultManifest, resultHead, resultETag, staleErr := m.verifiedStale(ctx, canonical, state)
			m.emitStrongRead("stale", staleErr)
			return resultManifest, resultHead, resultETag, staleErr
		}
		m.emitStrongRead("error", err)
		return model.Manifest{}, model.Head{}, "", err
	}
	if notModified {
		refs, err := ReadRefs(ctx, m.RepoPath(repoID))
		if err == nil && model.RefsChecksum(refs) == state.RefsSHA256 && m.verifyRepo(ctx, m.RepoPath(repoID), refs) == nil {
			state.LastAccessAt = time.Now().UTC()
			state.UpdatedAt = state.LastAccessAt
			_ = atomicJSON(m.StatePath(repoID), state, 0o640)
			m.emitStrongRead("not_modified", nil)
			return manifest, localHead(repoID, state, refs), objectstore.ETag(state.HeadETag), nil
		}
		return m.rebuild(ctx, manifest, manifestETag)
	}
	defer changed.Body.Close()
	var head model.Head
	if err := decodeObject(changed.Body, &head); err != nil || head.Validate() != nil || head.RepoID != repoID {
		return model.Manifest{}, model.Head{}, "", errors.New("authoritative head validation failed")
	}
	if state.AppliedSequence > head.Sequence {
		_ = observability.Emit(m.Config.DataDir, observability.Event{Name: "invariant_failure", Labels: map[string]string{"invariant": "local_ahead"}})
		return m.rebuild(ctx, manifest, manifestETag)
	}
	if err := m.catchUp(ctx, m.RepoPath(repoID), state.AppliedSequence, head); err != nil {
		_ = observability.Emit(m.Config.DataDir, observability.Event{Name: "invariant_failure", Labels: map[string]string{"invariant": "wal_replay"}})
		return m.rebuild(ctx, manifest, manifestETag)
	}
	if err := m.verifyAndSave(ctx, manifest, manifestETag, head, changed.ETag, m.RepoPath(repoID)); err != nil {
		_ = observability.Emit(m.Config.DataDir, observability.Event{Name: "invariant_failure", Labels: map[string]string{"invariant": "refs_or_connectivity"}})
		return m.rebuild(ctx, manifest, manifestETag)
	}
	m.emitStrongRead("changed", nil)
	return manifest, head, changed.ETag, nil
}

func (m *Manager) verifiedStale(ctx context.Context, name string, state model.LocalState) (model.Manifest, model.Head, objectstore.ETag, error) {
	repoID := model.RepoID(name)
	refs, err := ReadRefs(ctx, m.RepoPath(repoID))
	if err != nil {
		return model.Manifest{}, model.Head{}, "", fmt.Errorf("stale cache refs: %w", err)
	}
	if model.RefsChecksum(refs) != state.RefsSHA256 {
		return model.Manifest{}, model.Head{}, "", errors.New("stale cache refs checksum mismatch")
	}
	if err := m.verifyRepo(ctx, m.RepoPath(repoID), refs); err != nil {
		return model.Manifest{}, model.Head{}, "", fmt.Errorf("stale cache connectivity: %w", err)
	}
	manifest := model.Manifest{SchemaVersion: model.SchemaVersion, RepoID: repoID, Name: name, ObjectFormat: "sha1", DefaultBranch: "refs/heads/main"}
	return manifest, localHead(repoID, state, refs), objectstore.ETag(state.HeadETag), nil
}

func localHead(repoID string, state model.LocalState, refs map[string]string) model.Head {
	return model.Head{SchemaVersion: model.SchemaVersion, RepoID: repoID, Sequence: state.AppliedSequence, Revision: state.HeadRevision, LatestEntryKey: state.LatestEntryKey, Refs: refs}
}

func (m *Manager) emitStrongRead(result string, err error) {
	if err != nil && result != "error" {
		result = "error"
	}
	_ = observability.Emit(m.Config.DataDir, observability.Event{Name: "strong_read", Labels: map[string]string{"result": result}})
}

func (m *Manager) Head(ctx context.Context, repoID string) (model.Head, objectstore.ETag, error) {
	head, etag, err := admin.GetJSON[model.Head](ctx, m.Store, model.HeadKey(repoID))
	if err != nil {
		return head, etag, err
	}
	if err := head.Validate(); err != nil || head.RepoID != repoID {
		return head, etag, errors.New("invalid authoritative head")
	}
	return head, etag, nil
}

func (m *Manager) materialize(ctx context.Context, manifest model.Manifest, manifestETag objectstore.ETag) (model.Head, objectstore.ETag, error) {
	started := time.Now()
	result := "error"
	defer func() {
		_ = observability.Emit(m.Config.DataDir, observability.Event{Name: "materialization", Labels: map[string]string{"result": result}})
		_ = observability.Emit(m.Config.DataDir, observability.Event{Name: "materialization_duration", Value: time.Since(started).Seconds()})
	}()
	head, headETag, err := m.Head(ctx, manifest.RepoID)
	if err != nil {
		return head, headETag, err
	}
	staging := filepath.Join(m.Config.DataDir, "staging", manifest.RepoID+"-"+ulid.Make().String()+".git")
	if err := os.RemoveAll(staging); err != nil {
		return head, headETag, err
	}
	defer os.RemoveAll(staging)
	if err := InitBare(ctx, staging, manifest.DefaultBranch, m.Config.ReceiveMaxInputSize); err != nil {
		return head, headETag, err
	}
	base := uint64(0)
	if head.Snapshot != nil {
		metadata, _, err := admin.GetJSON[model.Snapshot](ctx, m.Store, head.Snapshot.MetadataKey)
		if err != nil || metadata.RepoID != manifest.RepoID || metadata.Sequence != head.Snapshot.Sequence || metadata.PackSHA256 != head.Snapshot.PackSHA256 {
			return head, headETag, errors.New("snapshot metadata validation failed")
		}
		if err := m.installPack(ctx, staging, metadata.PackKey, metadata.PackSHA256, metadata.PackSize); err != nil {
			return head, headETag, err
		}
		if err := SetRefs(ctx, staging, map[string]string{}, metadata.Refs); err != nil {
			return head, headETag, err
		}
		base = metadata.Sequence
	}
	if err := m.catchUp(ctx, staging, base, head); err != nil {
		return head, headETag, err
	}
	if err := m.verifyRepo(ctx, staging, head.Refs); err != nil {
		return head, headETag, err
	}
	final := m.RepoPath(manifest.RepoID)
	if err := os.MkdirAll(filepath.Dir(final), 0o750); err != nil {
		return head, headETag, err
	}
	if err := os.RemoveAll(final); err != nil {
		return head, headETag, err
	}
	if err := os.Rename(staging, final); err != nil {
		return head, headETag, err
	}
	if err := fsyncDir(filepath.Dir(final)); err != nil {
		return head, headETag, err
	}
	if err := m.verifyAndSave(ctx, manifest, manifestETag, head, headETag, final); err != nil {
		return head, headETag, err
	}
	result = "success"
	return head, headETag, nil
}

func (m *Manager) rebuild(ctx context.Context, manifest model.Manifest, manifestETag objectstore.ETag) (model.Manifest, model.Head, objectstore.ETag, error) {
	_ = os.Remove(m.StatePath(manifest.RepoID))
	_ = os.RemoveAll(m.RepoPath(manifest.RepoID))
	head, etag, err := m.materialize(ctx, manifest, manifestETag)
	return manifest, head, etag, err
}

func (m *Manager) catchUp(ctx context.Context, repoPath string, base uint64, head model.Head) error {
	if base == head.Sequence {
		return nil
	}
	entries, err := m.walkEntries(ctx, head, base)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Payload != nil {
			if err := m.installPack(ctx, repoPath, entry.Payload.PackKey, entry.Payload.PackSHA256, entry.Payload.PackSize); err != nil {
				return err
			}
		}
		if err := ApplyEntry(ctx, repoPath, entry); err != nil {
			return err
		}
	}
	_ = observability.Emit(m.Config.DataDir, observability.Event{Name: "replay_entries", Value: float64(len(entries))})
	return nil
}

func (m *Manager) walkEntries(ctx context.Context, head model.Head, base uint64) ([]model.WALEntry, error) {
	if base > head.Sequence {
		return nil, errors.New("local sequence ahead of head")
	}
	key := head.LatestEntryKey
	sequence := head.Sequence
	reverse := make([]model.WALEntry, 0, sequence-base)
	for sequence > base {
		if key == "" {
			return nil, errors.New("WAL chain ended before base")
		}
		entry, _, err := admin.GetJSON[model.WALEntry](ctx, m.Store, key)
		if err != nil {
			return nil, err
		}
		if err := entry.Validate(); err != nil || entry.RepoID != head.RepoID || entry.Sequence != sequence {
			return nil, fmt.Errorf("invalid WAL chain at sequence %d", sequence)
		}
		reverse = append(reverse, entry)
		sequence, key = entry.ParentSequence, entry.ParentEntryKey
	}
	entries := make([]model.WALEntry, len(reverse))
	for i := range reverse {
		entries[len(reverse)-1-i] = reverse[i]
	}
	return entries, nil
}

func (m *Manager) installPack(ctx context.Context, repoPath, key, expectedSHA string, expectedSize int64) error {
	if len(expectedSHA) != 64 || !strings.HasPrefix(key, "repos/") || strings.Contains(key, "..") {
		return errors.New("invalid pack metadata")
	}
	obj, err := m.Store.Get(ctx, key)
	if err != nil {
		return err
	}
	defer obj.Body.Close()
	if obj.Size != expectedSize {
		return fmt.Errorf("pack size mismatch for %s", key)
	}
	command := exec.CommandContext(ctx, "git", "--git-dir="+repoPath, "index-pack", "--stdin", "--strict")
	hash := sha256.New()
	command.Stdin = io.TeeReader(io.LimitReader(obj.Body, expectedSize+1), hash)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("index pack %s: %w: %s", key, err, output)
	}
	if hex.EncodeToString(hash.Sum(nil)) != expectedSHA {
		return fmt.Errorf("pack checksum mismatch for %s", key)
	}
	return nil
}

func (m *Manager) Verify(ctx context.Context, name string) (model.Head, error) {
	manifest, head, _, err := m.EnsureFresh(ctx, name)
	if err != nil {
		return head, err
	}
	command := exec.CommandContext(ctx, "git", "--git-dir="+m.RepoPath(manifest.RepoID), "fsck", "--full")
	if output, err := command.CombinedOutput(); err != nil {
		return head, fmt.Errorf("git fsck --full: %w: %s", err, output)
	}
	return head, nil
}

func (m *Manager) SaveReady(ctx context.Context, manifest model.Manifest, manifestETag objectstore.ETag, head model.Head, headETag objectstore.ETag) error {
	return m.verifyAndSave(ctx, manifest, manifestETag, head, headETag, m.RepoPath(manifest.RepoID))
}

func (m *Manager) verifyAndSave(ctx context.Context, manifest model.Manifest, manifestETag objectstore.ETag, head model.Head, headETag objectstore.ETag, repoPath string) error {
	if err := m.verifyRepo(ctx, repoPath, head.Refs); err != nil {
		return err
	}
	refs, _ := ReadRefs(ctx, repoPath)
	snapshotID := ""
	if head.Snapshot != nil {
		snapshotID = head.Snapshot.SnapshotID
	}
	now := time.Now().UTC()
	state := model.LocalState{SchemaVersion: model.SchemaVersion, RepoID: manifest.RepoID, ManifestETag: string(manifestETag), HeadETag: string(headETag), HeadRevision: head.Revision, AppliedSequence: head.Sequence, LatestEntryKey: head.LatestEntryKey, SnapshotID: snapshotID, RefsSHA256: model.RefsChecksum(refs), Status: "ready", LastAccessAt: now, UpdatedAt: now}
	return atomicJSON(m.StatePath(manifest.RepoID), state, 0o640)
}

func (m *Manager) verifyRepo(ctx context.Context, repoPath string, expected map[string]string) error {
	refs, err := ReadRefs(ctx, repoPath)
	if err != nil {
		return err
	}
	if !equalRefs(refs, expected) {
		return fmt.Errorf("local refs diverge from authoritative head")
	}
	command := exec.CommandContext(ctx, "git", "--git-dir="+repoPath, "fsck", "--connectivity-only")
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("git connectivity check: %w: %s", err, output)
	}
	return nil
}

func InitBare(ctx context.Context, path, defaultBranch string, maxInputSize int64) error {
	if output, err := exec.CommandContext(ctx, "git", "init", "--bare", "--object-format=sha1", path).CombinedOutput(); err != nil {
		return fmt.Errorf("git init: %w: %s", err, output)
	}
	commands := [][]string{{"symbolic-ref", "HEAD", defaultBranch}, {"config", "http.receivepack", "true"}, {"config", "receive.autogc", "false"}, {"config", "receive.fsckObjects", "true"}, {"config", "receive.maxInputSize", strconv.FormatInt(maxInputSize, 10)}, {"config", "transfer.fsckObjects", "true"}, {"config", "uploadpack.allowFilter", "true"}, {"config", "receive.advertiseAtomic", "true"}, {"config", "core.hooksPath", "/opt/continuity/hooks"}, {"config", "--add", "receive.procReceiveRefs", "refs/"}}
	for _, args := range commands {
		full := append([]string{"--git-dir=" + path}, args...)
		if output, err := exec.CommandContext(ctx, "git", full...).CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %w: %s", args, err, output)
		}
	}
	return nil
}

func ReadRefs(ctx context.Context, repoPath string) (map[string]string, error) {
	output, err := exec.CommandContext(ctx, "git", "--git-dir="+repoPath, "for-each-ref", "--format=%(refname) %(objectname)", "refs/").Output()
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), " ", 2)
		if len(parts) != 2 || model.ValidateRefName(parts[0]) != nil || !model.ValidOID(parts[1]) {
			return nil, errors.New("invalid local ref output")
		}
		refs[parts[0]] = parts[1]
	}
	return refs, scanner.Err()
}

func ApplyEntry(ctx context.Context, repoPath string, entry model.WALEntry) error {
	var input strings.Builder
	input.WriteString("start\n")
	for _, update := range entry.Updates {
		switch {
		case update.OldOID == model.ZeroOID:
			fmt.Fprintf(&input, "create %s %s\n", update.Ref, update.NewOID)
		case update.NewOID == model.ZeroOID:
			fmt.Fprintf(&input, "delete %s %s\n", update.Ref, update.OldOID)
		default:
			fmt.Fprintf(&input, "update %s %s %s\n", update.Ref, update.NewOID, update.OldOID)
		}
	}
	input.WriteString("prepare\ncommit\n")
	command := exec.CommandContext(ctx, "git", "--git-dir="+repoPath, "update-ref", "--stdin")
	command.Stdin = strings.NewReader(input.String())
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("apply WAL entry %d: %w: %s", entry.Sequence, err, output)
	}
	if !strings.Contains(string(output), "start: ok") || !strings.Contains(string(output), "prepare: ok") || !strings.Contains(string(output), "commit: ok") {
		return fmt.Errorf("unexpected update-ref transaction output: %s", output)
	}
	return nil
}

func SetRefs(ctx context.Context, repoPath string, current, target map[string]string) error {
	updates := make([]model.RefUpdate, 0)
	keys := make([]string, 0, len(current)+len(target))
	seen := map[string]bool{}
	for key := range current {
		seen[key] = true
		keys = append(keys, key)
	}
	for key := range target {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		oldOID, newOID := current[key], target[key]
		if oldOID == "" {
			oldOID = model.ZeroOID
		}
		if newOID == "" {
			newOID = model.ZeroOID
		}
		if oldOID != newOID {
			updates = append(updates, model.RefUpdate{Ref: key, OldOID: oldOID, NewOID: newOID})
		}
	}
	if len(updates) == 0 {
		return nil
	}
	return ApplyEntry(ctx, repoPath, model.WALEntry{Sequence: 1, Updates: updates})
}

func decodeObject(reader io.Reader, value any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 8<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}
func equalRefs(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}
func fsyncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func parseSequence(key string) (uint64, error) {
	base := filepath.Base(key)
	parts := strings.SplitN(base, "-", 2)
	if len(parts) != 2 {
		return 0, errors.New("invalid entry key")
	}
	return strconv.ParseUint(parts[0], 10, 64)
}
