package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	SchemaVersion = 1
	ZeroOID       = "0000000000000000000000000000000000000000"
)

var (
	segmentRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	oidRE     = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

type Manifest struct {
	SchemaVersion int       `json:"schema_version"`
	RepoID        string    `json:"repo_id"`
	Name          string    `json:"name"`
	ObjectFormat  string    `json:"object_format"`
	DefaultBranch string    `json:"default_branch"`
	CreatedAt     time.Time `json:"created_at"`
}

type SnapshotPointer struct {
	SnapshotID  string `json:"snapshot_id"`
	Sequence    uint64 `json:"sequence"`
	MetadataKey string `json:"metadata_key"`
	PackKey     string `json:"pack_key"`
	PackSHA256  string `json:"pack_sha256"`
}

type Head struct {
	SchemaVersion  int               `json:"schema_version"`
	RepoID         string            `json:"repo_id"`
	Revision       uint64            `json:"revision"`
	Sequence       uint64            `json:"sequence"`
	LatestEntryKey string            `json:"latest_entry_key"`
	Refs           map[string]string `json:"refs"`
	Snapshot       *SnapshotPointer  `json:"snapshot"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

type RefUpdate struct {
	Ref    string `json:"ref"`
	OldOID string `json:"old_oid"`
	NewOID string `json:"new_oid"`
	Force  bool   `json:"force"`
}

type PackPayload struct {
	PackKey    string `json:"pack_key"`
	PackSHA256 string `json:"pack_sha256"`
	PackSize   int64  `json:"pack_size"`
}

type WALEntry struct {
	SchemaVersion  int          `json:"schema_version"`
	EntryID        string       `json:"entry_id"`
	PushID         string       `json:"push_id"`
	RepoID         string       `json:"repo_id"`
	Sequence       uint64       `json:"sequence"`
	ParentSequence uint64       `json:"parent_sequence"`
	ParentEntryKey string       `json:"parent_entry_key"`
	CreatedAt      time.Time    `json:"created_at"`
	NodeID         string       `json:"node_id"`
	Payload        *PackPayload `json:"payload"`
	Updates        []RefUpdate  `json:"updates"`
}

type Snapshot struct {
	SchemaVersion int               `json:"schema_version"`
	SnapshotID    string            `json:"snapshot_id"`
	RepoID        string            `json:"repo_id"`
	Sequence      uint64            `json:"sequence"`
	CreatedAt     time.Time         `json:"created_at"`
	NodeID        string            `json:"node_id"`
	PackKey       string            `json:"pack_key"`
	PackSHA256    string            `json:"pack_sha256"`
	PackSize      int64             `json:"pack_size"`
	Refs          map[string]string `json:"refs"`
}

type LocalState struct {
	SchemaVersion   int       `json:"schema_version"`
	RepoID          string    `json:"repo_id"`
	ManifestETag    string    `json:"manifest_etag"`
	HeadETag        string    `json:"head_etag"`
	HeadRevision    uint64    `json:"head_revision"`
	AppliedSequence uint64    `json:"applied_sequence"`
	LatestEntryKey  string    `json:"latest_entry_key"`
	SnapshotID      string    `json:"snapshot_id,omitempty"`
	RefsSHA256      string    `json:"refs_sha256"`
	Status          string    `json:"status"`
	LastAccessAt    time.Time `json:"last_access_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Pending struct {
	SchemaVersion int          `json:"schema_version"`
	PushID        string       `json:"push_id"`
	RepoID        string       `json:"repo_id"`
	NodeID        string       `json:"node_id"`
	CreatedAt     time.Time    `json:"created_at"`
	PID           int          `json:"pid"`
	State         string       `json:"state"`
	Payload       *PackPayload `json:"payload"`
	Updates       []RefUpdate  `json:"updates"`
}

type Receipt struct {
	PushID      string    `json:"push_id"`
	RepoID      string    `json:"repo_id"`
	Sequence    uint64    `json:"sequence"`
	HeadETag    string    `json:"head_etag"`
	EntryKey    string    `json:"entry_key"`
	CommittedAt time.Time `json:"committed_at"`
}

func CanonicalName(raw string) (string, error) {
	if strings.Contains(raw, "\\") || strings.Contains(strings.ToLower(raw), "%2f") || strings.Contains(strings.ToLower(raw), "%5c") {
		return "", errors.New("repository name contains a forbidden separator")
	}
	name := strings.TrimSuffix(raw, ".git")
	if name == "" || len(name) > 200 {
		return "", errors.New("repository name must contain 1 to 200 bytes")
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "" || segment == "." || segment == ".." || !segmentRE.MatchString(segment) {
			return "", fmt.Errorf("invalid repository path segment %q", segment)
		}
	}
	return name, nil
}

func RepoID(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

func ManifestKey(repoID string) string { return "repos/" + repoID + "/manifest.json" }
func HeadKey(repoID string) string     { return "repos/" + repoID + "/wal/head.json" }
func EntryKey(repoID string, sequence uint64, entryID string) string {
	return fmt.Sprintf("repos/%s/wal/entries/%020d-%s.json", repoID, sequence, entryID)
}
func PackKey(repoID, sha string) string { return "repos/" + repoID + "/packs/" + sha + ".pack" }
func SnapshotMetadataKey(repoID, snapshotID string) string {
	return "repos/" + repoID + "/snapshots/" + snapshotID + ".json"
}
func SnapshotPackKey(repoID, sha string) string {
	return "repos/" + repoID + "/snapshots/" + sha + ".pack"
}

func ValidOID(oid string) bool { return oidRE.MatchString(oid) }

func (m Manifest) Validate() error {
	name, err := CanonicalName(m.Name)
	if err != nil || name != m.Name || m.SchemaVersion != SchemaVersion || m.ObjectFormat != "sha1" || m.RepoID != RepoID(m.Name) {
		return errors.New("invalid manifest")
	}
	if !strings.HasPrefix(m.DefaultBranch, "refs/heads/") {
		return errors.New("invalid default branch")
	}
	return nil
}

func (h Head) Validate() error {
	if h.SchemaVersion != SchemaVersion || len(h.RepoID) != 64 || h.Refs == nil {
		return errors.New("invalid head metadata")
	}
	if h.Sequence == 0 && h.LatestEntryKey != "" {
		return errors.New("sequence zero has a WAL entry")
	}
	if h.Sequence > 0 && h.LatestEntryKey == "" {
		return errors.New("nonzero sequence lacks a WAL entry")
	}
	for ref, oid := range h.Refs {
		if err := ValidateRefName(ref); err != nil || !ValidOID(oid) || oid == ZeroOID {
			return fmt.Errorf("invalid head ref %q", ref)
		}
	}
	return nil
}

func (e WALEntry) Validate() error {
	if e.SchemaVersion != SchemaVersion || e.EntryID == "" || e.PushID == "" || e.Sequence == 0 || e.ParentSequence+1 != e.Sequence {
		return errors.New("invalid WAL entry metadata")
	}
	if e.ParentSequence == 0 && e.ParentEntryKey != "" {
		return errors.New("invalid WAL root parent")
	}
	seen := map[string]bool{}
	for _, update := range e.Updates {
		if err := update.Validate(); err != nil || seen[update.Ref] {
			return errors.New("invalid WAL updates")
		}
		seen[update.Ref] = true
	}
	if e.Payload != nil && (len(e.Payload.PackSHA256) != 64 || e.Payload.PackSize < 0 || e.Payload.PackKey == "") {
		return errors.New("invalid WAL payload")
	}
	return nil
}

func (u RefUpdate) Validate() error {
	if err := ValidateRefName(u.Ref); err != nil || !ValidOID(u.OldOID) || !ValidOID(u.NewOID) || u.NewOID == ZeroOID && u.OldOID == ZeroOID {
		return errors.New("invalid ref update")
	}
	return nil
}

func ValidateRefName(ref string) error {
	if !strings.HasPrefix(ref, "refs/") || strings.ContainsAny(ref, " ~^:?*[\\") || strings.Contains(ref, "..") || strings.Contains(ref, "@{") || strings.HasSuffix(ref, ".") || strings.HasSuffix(ref, "/") || strings.Contains(ref, "//") {
		return fmt.Errorf("invalid ref name %q", ref)
	}
	return nil
}

func ApplyUpdates(refs map[string]string, updates []RefUpdate) (map[string]string, error) {
	next := make(map[string]string, len(refs))
	for k, v := range refs {
		next[k] = v
	}
	seen := map[string]bool{}
	for _, update := range updates {
		if err := update.Validate(); err != nil || seen[update.Ref] {
			return nil, errors.New("invalid or duplicate update")
		}
		seen[update.Ref] = true
		current := next[update.Ref]
		if current == "" {
			current = ZeroOID
		}
		if current != update.OldOID {
			return nil, fmt.Errorf("stale ref %s: expected %s, have %s", update.Ref, update.OldOID, current)
		}
		if update.NewOID == ZeroOID {
			delete(next, update.Ref)
		} else {
			next[update.Ref] = update.NewOID
		}
	}
	return next, nil
}

func RefsChecksum(refs map[string]string) string {
	keys := make([]string, 0, len(refs))
	for key := range refs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, key := range keys {
		fmt.Fprintf(h, "%s\x00%s\n", key, refs[key])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func Marshal(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }
