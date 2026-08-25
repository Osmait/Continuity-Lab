package gossip

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/continuity-lab/continuity-lab/internal/admin"
	"github.com/continuity-lab/continuity-lab/internal/config"
	"github.com/continuity-lab/continuity-lab/internal/locks"
	"github.com/continuity-lab/continuity-lab/internal/model"
	"github.com/continuity-lab/continuity-lab/internal/objectstore"
	"github.com/continuity-lab/continuity-lab/internal/observability"
	"github.com/continuity-lab/continuity-lab/internal/repository"
	"github.com/oklog/ulid/v2"
)

type Service struct {
	Config  config.Config
	Store   objectstore.ObjectStore
	Repos   *repository.Manager
	Locks   *locks.Manager
	mu      sync.Mutex
	seen    map[string]time.Time
	pending map[string]bool
}

func NewService(cfg config.Config, store objectstore.ObjectStore, repos *repository.Manager, lockManager *locks.Manager) *Service {
	return &Service{Config: cfg, Store: store, Repos: repos, Locks: lockManager, seen: make(map[string]time.Time), pending: make(map[string]bool)}
}

func (s *Service) Run(ctx context.Context) error {
	connection, err := net.ListenPacket("udp", s.Config.GossipAddr)
	if err != nil {
		return err
	}
	defer connection.Close()
	go func() { <-ctx.Done(); _ = connection.Close() }()
	buffer := make([]byte, MaxDatagram+1)
	for {
		n, _, err := connection.ReadFrom(buffer)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			continue
		}
		message, err := Verify(buffer[:n], []byte(s.Config.GossipSecret), time.Now())
		if err != nil {
			observability.GossipReceived.WithLabelValues(s.Config.NodeID, "invalid").Inc()
			continue
		}
		if message.Sender == s.Config.NodeID {
			observability.GossipReceived.WithLabelValues(s.Config.NodeID, "self").Inc()
			continue
		}
		if !s.accept(message) {
			observability.GossipReceived.WithLabelValues(s.Config.NodeID, "duplicate").Inc()
			continue
		}
		observability.GossipReceived.WithLabelValues(s.Config.NodeID, "accepted").Inc()
		statePath := s.Repos.StatePath(message.RepoID)
		var state model.LocalState
		if repository.ReadJSON(statePath, &state) != nil || state.AppliedSequence >= message.Sequence {
			continue
		}
		s.mu.Lock()
		if s.pending[message.RepoID] {
			s.mu.Unlock()
			continue
		}
		s.pending[message.RepoID] = true
		s.mu.Unlock()
		go s.catchUp(ctx, message.RepoID)
	}
}

func (s *Service) accept(message Message) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.seen[message.Nonce]; exists {
		return false
	}
	s.seen[message.Nonce] = time.Now()
	if len(s.seen) > 4096 {
		cutoff := time.Now().Add(-10 * time.Minute)
		for nonce, seenAt := range s.seen {
			if seenAt.Before(cutoff) {
				delete(s.seen, nonce)
			}
		}
	}
	return true
}

func (s *Service) catchUp(ctx context.Context, repoID string) {
	defer func() { s.mu.Lock(); delete(s.pending, repoID); s.mu.Unlock() }()
	manifest, _, err := admin.GetJSON[model.Manifest](ctx, s.Store, model.ManifestKey(repoID))
	if err != nil {
		return
	}
	lockCtx, cancel := context.WithTimeout(ctx, s.Config.LockTimeout)
	defer cancel()
	guard, err := s.Locks.Acquire(lockCtx, repoID, locks.Exclusive)
	if err != nil {
		return
	}
	defer guard.Close()
	_, _, _, _ = s.Repos.EnsureFresh(ctx, manifest.Name)
}

func Send(ctx context.Context, cfg config.Config, receipt model.Receipt, revision uint64) error {
	if drop(cfg.DataDir) {
		return nil
	}
	message := Message{Version: 1, RepoID: receipt.RepoID, Sequence: receipt.Sequence, HeadRevision: revision, HeadETag: receipt.HeadETag, Sender: cfg.NodeID, SentAtUnixMS: time.Now().UnixMilli(), Nonce: ulid.Make().String()}
	encoded, err := Sign(message, []byte(cfg.GossipSecret))
	if err != nil {
		return err
	}
	connection, err := net.ListenPacket("udp", "")
	if err != nil {
		return err
	}
	defer connection.Close()
	sent := 0
	for _, peer := range cfg.GossipPeers {
		address, err := net.ResolveUDPAddr("udp", peer)
		if err != nil {
			continue
		}
		_ = connection.SetWriteDeadline(time.Now().Add(time.Second))
		if _, err := connection.WriteTo(encoded, address); err == nil {
			sent++
		}
	}
	if sent > 0 {
		_ = observability.Emit(cfg.DataDir, observability.Event{Name: "gossip_sent", Value: float64(sent)})
	}
	return nil
}

func drop(dataDir string) bool {
	for _, name := range []string{"CONTINUITY_FAILPOINTS"} {
		if os.Getenv(name) == "drop_all_gossip" {
			return true
		}
	}
	path := filepath.Join(dataDir, "failpoints", "drop_all_gossip")
	body, err := os.ReadFile(path)
	if err == nil {
		var value any
		_ = json.Unmarshal(body, &value)
		_ = os.Remove(path)
		return true
	}
	return false
}
