package observability

import (
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	GitRequests             = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "continuity_git_requests_total", Help: "Git Smart HTTP requests."}, []string{"node", "service", "status"})
	GitDuration             = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "continuity_git_request_duration_seconds", Help: "Git request duration."}, []string{"node", "service"})
	Pushes                  = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "continuity_pushes_total", Help: "Push outcomes."}, []string{"node", "result"})
	PushDuration            = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "continuity_push_duration_seconds", Help: "Push duration."}, []string{"node"})
	CASAttempts             = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "continuity_cas_attempts_total", Help: "CAS outcomes."}, []string{"node", "result"})
	CASRetries              = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "continuity_cas_retries_total", Help: "CAS retries."}, []string{"node"})
	LocalRepos              = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "continuity_local_repos", Help: "Local repositories by state."}, []string{"node", "state"})
	Materializations        = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "continuity_materializations_total", Help: "Materialization outcomes."}, []string{"node", "result"})
	MaterializationDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "continuity_materialization_duration_seconds", Help: "Materialization duration."}, []string{"node"})
	ReplayEntries           = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "continuity_replay_entries_total", Help: "Replayed WAL entries."}, []string{"node"})
	GossipSent              = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "continuity_gossip_sent_total", Help: "Gossip datagrams sent."}, []string{"node"})
	GossipReceived          = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "continuity_gossip_received_total", Help: "Gossip receive outcomes."}, []string{"node", "result"})
	StrongReadChecks        = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "continuity_strong_read_checks_total", Help: "Strong-read outcomes."}, []string{"node", "result"})
	Compactions             = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "continuity_compactions_total", Help: "Compaction outcomes."}, []string{"node", "result"})
	LockWait                = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "continuity_lock_wait_seconds", Help: "Repository lock wait."}, []string{"node", "mode"})
	InvariantFailures       = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "continuity_invariant_failures_total", Help: "Invariant failures."}, []string{"node", "invariant"})
)

func init() {
	prometheus.MustRegister(GitRequests, GitDuration, Pushes, PushDuration, CASAttempts, CASRetries, LocalRepos, Materializations, MaterializationDuration, ReplayEntries, GossipSent, GossipReceived, StrongReadChecks, Compactions, LockWait, InvariantFailures)
}

func InitNode(node string) {
	GitRequests.WithLabelValues(node, "upload-pack", "init").Add(0)
	GitRequests.WithLabelValues(node, "receive-pack", "init").Add(0)
	Pushes.WithLabelValues(node, "init").Add(0)
	CASAttempts.WithLabelValues(node, "init").Add(0)
	CASRetries.WithLabelValues(node).Add(0)
	LocalRepos.WithLabelValues(node, "ready").Set(0)
	Materializations.WithLabelValues(node, "init").Add(0)
	ReplayEntries.WithLabelValues(node).Add(0)
	GossipSent.WithLabelValues(node).Add(0)
	GossipReceived.WithLabelValues(node, "init").Add(0)
	StrongReadChecks.WithLabelValues(node, "init").Add(0)
	Compactions.WithLabelValues(node, "init").Add(0)
	InvariantFailures.WithLabelValues(node, "init").Add(0)
}

func Handler() http.Handler { return promhttp.Handler() }

func NewLogger(component, nodeID string) *slog.Logger {
	return NewLoggerTo(os.Stdout, component, nodeID)
}

func NewLoggerTo(writer io.Writer, component, nodeID string) *slog.Logger {
	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
		if attr.Key == slog.TimeKey {
			attr.Key = "timestamp"
		}
		return attr
	}})
	logger := slog.New(handler).With("component", component)
	if nodeID != "" {
		logger = logger.With("node_id", nodeID)
	}
	return logger
}
