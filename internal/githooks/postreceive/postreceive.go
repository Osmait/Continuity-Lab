package postreceive

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/continuity-lab/continuity-lab/internal/admin"
	"github.com/continuity-lab/continuity-lab/internal/config"
	"github.com/continuity-lab/continuity-lab/internal/gossip"
	"github.com/continuity-lab/continuity-lab/internal/model"
	"github.com/continuity-lab/continuity-lab/internal/objectstore"
	"github.com/continuity-lab/continuity-lab/internal/repository"
	"github.com/continuity-lab/continuity-lab/internal/routing"
	"github.com/continuity-lab/continuity-lab/internal/snapshot"
)

func Run(ctx context.Context, input io.Reader, output io.Writer, cfg config.Config, store objectstore.ObjectStore) error {
	scanner := bufio.NewScanner(input)
	for scanner.Scan() {
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	pushID, repoID := os.Getenv("CONTINUITY_PUSH_ID"), os.Getenv("CONTINUITY_REPO_ID")
	manager := &repository.Manager{Config: cfg, Store: store}
	var receipt model.Receipt
	if err := repository.ReadJSON(manager.ReceiptPath(repoID, pushID), &receipt); err != nil {
		return fmt.Errorf("committed receipt unavailable: %w", err)
	}
	refs, err := repository.ReadRefs(ctx, manager.RepoPath(repoID))
	if err != nil {
		return err
	}
	entry, _, err := admin.GetJSON[model.WALEntry](ctx, store, receipt.EntryKey)
	if err != nil {
		return err
	}
	for _, update := range entry.Updates {
		if update.NewOID == model.ZeroOID {
			if _, exists := refs[update.Ref]; exists {
				return fmt.Errorf("post-receive ref %s was not deleted", update.Ref)
			}
		} else if refs[update.Ref] != update.NewOID {
			return fmt.Errorf("post-receive ref %s does not match sequence %d", update.Ref, receipt.Sequence)
		}
	}
	head, _, err := manager.Head(ctx, repoID)
	if err != nil {
		return err
	}
	_ = gossip.Send(ctx, cfg, receipt, head.Revision)
	if preferred(cfg, repoID) && shouldCompact(ctx, cfg, store, head) {
		_, _, _ = (snapshot.Compactor{NodeID: cfg.NodeID, Store: store, Repos: manager}).Compact(ctx, os.Getenv("CONTINUITY_REPO_NAME"))
	}
	_ = os.Remove(manager.PendingPath(repoID, pushID))
	_ = os.Remove(manager.ReceiptPath(repoID, pushID))
	fmt.Fprintf(output, "continuity: committed push %s at sequence %d\n", pushID, receipt.Sequence)
	return nil
}

func preferred(cfg config.Config, repoID string) bool {
	nodes := make([]routing.Node, 0, len(cfg.Nodes))
	for _, node := range cfg.Nodes {
		nodes = append(nodes, routing.Node{ID: node.ID, URL: node.URL})
	}
	ranked := routing.Rank(repoID, nodes)
	return len(ranked) > 0 && ranked[0].ID == cfg.NodeID
}

func shouldCompact(ctx context.Context, cfg config.Config, store objectstore.ObjectStore, head model.Head) bool {
	base := uint64(0)
	if head.Snapshot != nil {
		base = head.Snapshot.Sequence
	}
	if head.Sequence-base >= cfg.SnapshotEntryThreshold {
		return true
	}
	var bytesSince int64
	key, sequence := head.LatestEntryKey, head.Sequence
	for key != "" && sequence > base {
		entry, _, err := admin.GetJSON[model.WALEntry](ctx, store, key)
		if err != nil {
			return false
		}
		if entry.Payload != nil {
			bytesSince += entry.Payload.PackSize
		}
		if bytesSince >= cfg.SnapshotByteThreshold {
			return true
		}
		key, sequence = entry.ParentEntryKey, entry.ParentSequence
	}
	return false
}
