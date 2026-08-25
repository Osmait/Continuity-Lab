package observability

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

type Event struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
	Value  float64           `json:"value,omitempty"`
}

func Emit(dataDir string, event Event) error {
	dir := filepath.Join(dataDir, "metrics")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "events.lock"), os.O_CREATE|os.O_RDWR, 0o640)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	file, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	if err := encoder.Encode(event); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func RunEventCollector(ctx context.Context, node, dataDir string) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		drainEvents(node, dataDir)
		select {
		case <-ctx.Done():
			drainEvents(node, dataDir)
			return
		case <-ticker.C:
		}
	}
}

func drainEvents(node, dataDir string) {
	dir := filepath.Join(dataDir, "metrics")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return
	}
	lock, err := os.OpenFile(filepath.Join(dir, "events.lock"), os.O_CREATE|os.O_RDWR, 0o640)
	if err != nil {
		return
	}
	defer lock.Close()
	if unix.Flock(int(lock.Fd()), unix.LOCK_EX) != nil {
		return
	}
	path := filepath.Join(dir, "events.jsonl")
	body, readErr := os.ReadFile(path)
	if readErr == nil {
		_ = os.WriteFile(path, nil, 0o640)
	}
	_ = unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		var event Event
		if json.Unmarshal(scanner.Bytes(), &event) == nil {
			applyEvent(node, event)
		}
	}
	updateLocalRepos(node, dataDir)
}

func updateLocalRepos(node, dataDir string) {
	states := []string{"materializing", "ready", "syncing", "needs_rebuild", "evicting", "corrupt"}
	counts := make(map[string]float64, len(states))
	entries, _ := filepath.Glob(filepath.Join(dataDir, "state", "*.json"))
	for _, path := range entries {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var state struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(body, &state) == nil {
			counts[state.Status]++
		}
	}
	for _, state := range states {
		LocalRepos.WithLabelValues(node, state).Set(counts[state])
	}
}

func applyEvent(node string, event Event) {
	value := event.Value
	if value == 0 {
		value = 1
	}
	switch event.Name {
	case "cas_attempt":
		CASAttempts.WithLabelValues(node, event.Labels["result"]).Add(value)
	case "cas_retry":
		CASRetries.WithLabelValues(node).Add(value)
	case "materialization":
		Materializations.WithLabelValues(node, event.Labels["result"]).Add(value)
	case "materialization_duration":
		MaterializationDuration.WithLabelValues(node).Observe(event.Value)
	case "replay_entries":
		ReplayEntries.WithLabelValues(node).Add(value)
	case "gossip_sent":
		GossipSent.WithLabelValues(node).Add(value)
	case "gossip_received":
		GossipReceived.WithLabelValues(node, event.Labels["result"]).Add(value)
	case "strong_read":
		StrongReadChecks.WithLabelValues(node, event.Labels["result"]).Add(value)
	case "compaction":
		Compactions.WithLabelValues(node, event.Labels["result"]).Add(value)
	case "invariant_failure":
		InvariantFailures.WithLabelValues(node, event.Labels["invariant"]).Add(value)
	case "push":
		Pushes.WithLabelValues(node, event.Labels["result"]).Add(value)
	}
}
