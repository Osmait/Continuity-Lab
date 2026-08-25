package snapshot

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/continuity-lab/continuity-lab/internal/admin"
	"github.com/continuity-lab/continuity-lab/internal/model"
	"github.com/continuity-lab/continuity-lab/internal/objectstore"
)

type GCResult struct {
	DryRun   bool     `json:"dry_run"`
	Deleted  []string `json:"deleted"`
	Retained int      `json:"retained"`
}

func GC(ctx context.Context, store objectstore.ObjectStore, repoID string, grace time.Duration, dryRun bool) (GCResult, error) {
	head, _, err := admin.GetJSON[model.Head](ctx, store, model.HeadKey(repoID))
	if err != nil {
		return GCResult{}, err
	}
	reachable := map[string]bool{model.ManifestKey(repoID): true, model.HeadKey(repoID): true}
	base := uint64(0)
	if head.Snapshot != nil {
		base = head.Snapshot.Sequence
		reachable[head.Snapshot.MetadataKey], reachable[head.Snapshot.PackKey] = true, true
	}
	key, sequence := head.LatestEntryKey, head.Sequence
	for sequence > base && key != "" {
		reachable[key] = true
		entry, _, err := admin.GetJSON[model.WALEntry](ctx, store, key)
		if err != nil {
			return GCResult{}, err
		}
		if entry.Payload != nil {
			reachable[entry.Payload.PackKey] = true
		}
		key, sequence = entry.ParentEntryKey, entry.ParentSequence
	}
	objects, err := store.List(ctx, "repos/"+repoID+"/")
	if err != nil {
		return GCResult{}, err
	}
	result := GCResult{DryRun: dryRun}
	cutoff := time.Now().Add(-grace)
	for _, object := range objects {
		if reachable[object.Key] || !object.LastModified.Before(cutoff) || strings.HasSuffix(object.Key, "/manifest.json") || strings.HasSuffix(object.Key, "/head.json") {
			result.Retained++
			continue
		}
		if !dryRun {
			if err := store.Delete(ctx, object.Key); err != nil {
				return result, err
			}
		}
		result.Deleted = append(result.Deleted, object.Key)
	}
	return result, nil
}

func ParseDryRun(value string) bool { parsed, _ := strconv.ParseBool(value); return parsed }
