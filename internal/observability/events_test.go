package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestEventBridgePublishesHookMetrics(t *testing.T) {
	dataDir := t.TempDir()
	node := "event-test-node"
	if err := Emit(dataDir, Event{Name: "cas_attempt", Labels: map[string]string{"result": "success"}}); err != nil {
		t.Fatal(err)
	}
	if err := Emit(dataDir, Event{Name: "cas_retry", Value: 2}); err != nil {
		t.Fatal(err)
	}
	if err := Emit(dataDir, Event{Name: "replay_entries", Value: 3}); err != nil {
		t.Fatal(err)
	}
	drainEvents(node, dataDir)
	if got := testutil.ToFloat64(CASAttempts.WithLabelValues(node, "success")); got != 1 {
		t.Fatalf("CAS attempts=%v", got)
	}
	if got := testutil.ToFloat64(CASRetries.WithLabelValues(node)); got != 2 {
		t.Fatalf("CAS retries=%v", got)
	}
	if got := testutil.ToFloat64(ReplayEntries.WithLabelValues(node)); got != 3 {
		t.Fatalf("replay entries=%v", got)
	}
}
