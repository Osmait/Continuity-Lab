package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"
)

type State struct{ ready atomic.Bool }

func (s *State) SetReady(ready bool) { s.ready.Store(ready) }
func (s *State) Ready() bool         { return s.ready.Load() }
func (s *State) Healthz(w http.ResponseWriter, _ *http.Request) {
	JSON(w, http.StatusOK, map[string]any{"status": "ok"})
}
func (s *State) Readyz(w http.ResponseWriter, _ *http.Request) {
	status := http.StatusServiceUnavailable
	label := "not_ready"
	if s.Ready() {
		status, label = http.StatusOK, "ready"
	}
	JSON(w, status, map[string]any{"status": label})
}

func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func Wait(ctx context.Context, check func(context.Context) error) error {
	delay := 100 * time.Millisecond
	for {
		attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := check(attempt)
		cancel()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		if delay < 5*time.Second {
			delay *= 2
		}
	}
}
