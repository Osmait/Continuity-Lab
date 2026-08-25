package wal

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/continuity-lab/continuity-lab/internal/admin"
	"github.com/continuity-lab/continuity-lab/internal/config"
	"github.com/continuity-lab/continuity-lab/internal/model"
	"github.com/continuity-lab/continuity-lab/internal/objectstore"
	"github.com/continuity-lab/continuity-lab/internal/observability"
	"github.com/continuity-lab/continuity-lab/internal/repository"
	"github.com/oklog/ulid/v2"
)

var ErrStaleRef = errors.New("stale ref update")

type Committer struct {
	Config  config.Config
	Store   objectstore.ObjectStore
	Manager *repository.Manager
}

func (c *Committer) CommitPush(ctx context.Context, commands []model.RefUpdate, pending model.Pending) (model.Receipt, error) {
	manifest, manifestETag, err := admin.GetJSON[model.Manifest](ctx, c.Store, model.ManifestKey(pending.RepoID))
	if err != nil {
		return model.Receipt{}, err
	}
	for attempt := 1; attempt <= c.Config.CASMaxRetries; attempt++ {
		head, etag, err := c.Manager.Head(ctx, pending.RepoID)
		if err != nil {
			return model.Receipt{}, err
		}
		nextRefs, err := model.ApplyUpdates(head.Refs, commands)
		if err != nil {
			return model.Receipt{}, fmt.Errorf("%w: %v", ErrStaleRef, err)
		}
		if _, _, _, err := c.Manager.EnsureFresh(ctx, manifest.Name); err != nil {
			return model.Receipt{}, fmt.Errorf("synchronize before push: %w", err)
		}
		tx, err := StartRefTransaction(ctx, c.Manager.RepoPath(pending.RepoID), commands)
		if err != nil {
			return model.Receipt{}, err
		}
		entryID := ulid.Make().String()
		entry := model.WALEntry{SchemaVersion: model.SchemaVersion, EntryID: entryID, PushID: pending.PushID, RepoID: pending.RepoID, Sequence: head.Sequence + 1, ParentSequence: head.Sequence, ParentEntryKey: head.LatestEntryKey, CreatedAt: time.Now().UTC(), NodeID: c.Config.NodeID, Payload: pending.Payload, Updates: commands}
		entryKey := model.EntryKey(pending.RepoID, entry.Sequence, entryID)
		if hit(c.Config.DataDir, "before_entry_upload") {
			_ = tx.Abort()
			return model.Receipt{}, errors.New("failpoint before_entry_upload")
		}
		entryBody, _ := model.Marshal(entry)
		if _, _, err := c.Store.PutImmutable(ctx, entryKey, bytes.NewReader(entryBody), int64(len(entryBody)), "application/json"); err != nil {
			_ = tx.Abort()
			return model.Receipt{}, fmt.Errorf("upload WAL entry: %w", err)
		}
		if hit(c.Config.DataDir, "after_entry_upload") {
			_ = tx.Abort()
			return model.Receipt{}, errors.New("failpoint after_entry_upload")
		}
		nextHead := head
		nextHead.Revision++
		nextHead.Sequence++
		nextHead.LatestEntryKey = entryKey
		nextHead.Refs = nextRefs
		nextHead.UpdatedAt = time.Now().UTC()
		headBody, _ := model.Marshal(nextHead)
		if hit(c.Config.DataDir, "before_head_cas") {
			_ = tx.Abort()
			return model.Receipt{}, errors.New("failpoint before_head_cas")
		}
		newETag, err := c.Store.PutIfMatch(ctx, model.HeadKey(pending.RepoID), etag, bytes.NewReader(headBody), int64(len(headBody)), "application/json")
		if errors.Is(err, objectstore.ErrPrecondition) || errors.Is(err, objectstore.ErrConflict) {
			_ = observability.Emit(c.Config.DataDir, observability.Event{Name: "cas_attempt", Labels: map[string]string{"result": "conflict"}})
			_ = observability.Emit(c.Config.DataDir, observability.Event{Name: "cas_retry"})
			if abortErr := tx.Abort(); abortErr != nil {
				return model.Receipt{}, abortErr
			}
			if _, _, _, syncErr := c.Manager.EnsureFresh(ctx, manifest.Name); syncErr != nil {
				return model.Receipt{}, syncErr
			}
			delay := time.Duration(1<<min(attempt, 8))*time.Millisecond + time.Duration(rand.IntN(10))*time.Millisecond
			select {
			case <-ctx.Done():
				return model.Receipt{}, ctx.Err()
			case <-time.After(delay):
			}
			continue
		}
		if err != nil {
			_ = observability.Emit(c.Config.DataDir, observability.Event{Name: "cas_attempt", Labels: map[string]string{"result": "error"}})
			_ = tx.Abort()
			return model.Receipt{}, fmt.Errorf("CAS authoritative head: %w", err)
		}
		_ = observability.Emit(c.Config.DataDir, observability.Event{Name: "cas_attempt", Labels: map[string]string{"result": "success"}})
		slog.Info("authoritative head committed", "sequence", nextHead.Sequence, "head_revision", nextHead.Revision, "head_etag", newETag, "cas_attempt", attempt)
		// The push is committed authoritatively from this point onward.
		if hit(c.Config.DataDir, "after_head_cas") {
			_ = tx.CloseWithoutDecision()
			return model.Receipt{}, errors.New("failpoint after_head_cas")
		}
		if hit(c.Config.DataDir, "before_local_ref_commit") {
			_ = tx.CloseWithoutDecision()
			return model.Receipt{}, errors.New("failpoint before_local_ref_commit")
		}
		if err := tx.Commit(); err != nil {
			return model.Receipt{}, fmt.Errorf("authoritative commit succeeded but local ref commit failed: %w", err)
		}
		if hit(c.Config.DataDir, "after_local_ref_commit") {
			return model.Receipt{}, errors.New("failpoint after_local_ref_commit")
		}
		receipt := model.Receipt{PushID: pending.PushID, RepoID: pending.RepoID, Sequence: nextHead.Sequence, HeadETag: string(newETag), EntryKey: entryKey, CommittedAt: time.Now().UTC()}
		if err := repository.AtomicJSON(c.Manager.ReceiptPath(pending.RepoID, pending.PushID), receipt, 0o640); err != nil {
			return receipt, fmt.Errorf("write committed receipt: %w", err)
		}
		if err := c.Manager.SaveReady(ctx, manifest, manifestETag, nextHead, newETag); err != nil {
			return receipt, fmt.Errorf("authoritative commit succeeded but local state reconciliation failed: %w", err)
		}
		if hit(c.Config.DataDir, "before_http_success") {
			return receipt, errors.New("failpoint before_http_success")
		}
		slog.Info("push locally reconciled", "sequence", nextHead.Sequence, "head_revision", nextHead.Revision, "head_etag", newETag, "cas_attempt", attempt)
		return receipt, nil
	}
	return model.Receipt{}, errors.New("CAS retry limit exceeded")
}

type RefTransaction struct {
	command  *exec.Cmd
	stdin    io.WriteCloser
	stdout   *bufio.Reader
	stderr   bytes.Buffer
	finished bool
}

func StartRefTransaction(ctx context.Context, repoPath string, commands []model.RefUpdate) (*RefTransaction, error) {
	command := exec.CommandContext(ctx, "git", "--git-dir="+repoPath, "update-ref", "--stdin")
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdoutPipe, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	tx := &RefTransaction{command: command, stdin: stdin, stdout: bufio.NewReader(stdoutPipe)}
	command.Stderr = &tx.stderr
	if err := command.Start(); err != nil {
		return nil, err
	}
	if err := tx.sendExpect("start", "start: ok"); err != nil {
		_ = tx.CloseWithoutDecision()
		return nil, err
	}
	for _, update := range commands {
		var line string
		switch {
		case update.OldOID == model.ZeroOID:
			line = fmt.Sprintf("create %s %s", update.Ref, update.NewOID)
		case update.NewOID == model.ZeroOID:
			line = fmt.Sprintf("delete %s %s", update.Ref, update.OldOID)
		default:
			line = fmt.Sprintf("update %s %s %s", update.Ref, update.NewOID, update.OldOID)
		}
		if _, err := fmt.Fprintln(tx.stdin, line); err != nil {
			_ = tx.CloseWithoutDecision()
			return nil, err
		}
	}
	if err := tx.sendExpect("prepare", "prepare: ok"); err != nil {
		_ = tx.CloseWithoutDecision()
		return nil, err
	}
	return tx, nil
}

func (t *RefTransaction) Commit() error { return t.finish("commit", "commit: ok") }
func (t *RefTransaction) Abort() error  { return t.finish("abort", "abort: ok") }
func (t *RefTransaction) finish(command, expected string) error {
	if t.finished {
		return errors.New("ref transaction already finished")
	}
	if err := t.sendExpect(command, expected); err != nil {
		_ = t.stdin.Close()
		_ = t.command.Wait()
		t.finished = true
		return err
	}
	if err := t.stdin.Close(); err != nil {
		return err
	}
	err := t.command.Wait()
	t.finished = true
	if err != nil {
		return fmt.Errorf("git update-ref %s: %w: %s", command, err, t.stderr.String())
	}
	return nil
}
func (t *RefTransaction) sendExpect(command, expected string) error {
	if _, err := fmt.Fprintln(t.stdin, command); err != nil {
		return err
	}
	line, err := t.stdout.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read update-ref response: %w: %s", err, t.stderr.String())
	}
	if strings.TrimSpace(line) != expected {
		return fmt.Errorf("update-ref response %q, expected %q", strings.TrimSpace(line), expected)
	}
	return nil
}
func (t *RefTransaction) CloseWithoutDecision() error {
	if t.finished {
		return nil
	}
	_ = t.stdin.Close()
	err := t.command.Wait()
	t.finished = true
	return err
}

func hit(dataDir, name string) bool {
	for _, item := range strings.Split(os.Getenv("CONTINUITY_FAILPOINTS"), ",") {
		if strings.TrimSpace(item) == name {
			return true
		}
	}
	path := fmt.Sprintf("%s/failpoints/%s", dataDir, name)
	if _, err := os.Stat(path); err == nil {
		_ = os.Remove(path)
		return true
	}
	return false
}
