package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Component              string
	NodeID                 string
	ListenAddr             string
	GatewayURL             string
	DataDir                string
	Bucket                 string
	S3Endpoint             string
	S3Region               string
	S3AccessKey            string
	S3SecretKey            string
	S3ForcePathStyle       bool
	Nodes                  []Node
	GossipAddr             string
	GossipPeers            []string
	GossipSecret           string
	AllowStaleReads        bool
	CASMaxRetries          int
	ReadReplicaCount       int
	MaxRefsPerPush         int
	ReceiveMaxInputSize    int64
	PendingTTL             time.Duration
	LockTimeout            time.Duration
	SnapshotEntryThreshold uint64
	SnapshotByteThreshold  int64
	GCGracePeriod          time.Duration
	LabMode                bool
}

type Node struct {
	ID  string
	URL string
}

func Load(component string) (Config, error) {
	cfg := Config{
		Component:              component,
		NodeID:                 env("CONTINUITY_NODE_ID", component),
		ListenAddr:             env("CONTINUITY_LISTEN_ADDR", ":8080"),
		GatewayURL:             env("CONTINUITY_GATEWAY_URL", "http://localhost:8080"),
		DataDir:                env("CONTINUITY_DATA_DIR", "/var/lib/continuity"),
		Bucket:                 env("CONTINUITY_BUCKET", "continuity-lab"),
		S3Endpoint:             env("S3_ENDPOINT", "http://localhost:9000"),
		S3Region:               env("S3_REGION", "us-east-1"),
		S3AccessKey:            env("MINIO_ROOT_USER", "continuity"),
		S3SecretKey:            env("MINIO_ROOT_PASSWORD", "continuity-local-password"),
		S3ForcePathStyle:       envBool("S3_FORCE_PATH_STYLE", true),
		GossipAddr:             env("CONTINUITY_GOSSIP_ADDR", ":7946"),
		GossipPeers:            split(env("CONTINUITY_GOSSIP_PEERS", "")),
		GossipSecret:           env("CONTINUITY_GOSSIP_SECRET", "local-development-secret-change-me"),
		AllowStaleReads:        envBool("CONTINUITY_ALLOW_STALE_READS", false),
		CASMaxRetries:          envInt("CONTINUITY_CAS_MAX_RETRIES", 16),
		ReadReplicaCount:       envInt("CONTINUITY_READ_REPLICA_COUNT", 3),
		MaxRefsPerPush:         envInt("CONTINUITY_MAX_REFS_PER_PUSH", 1024),
		ReceiveMaxInputSize:    int64(envInt("CONTINUITY_RECEIVE_MAX_INPUT_SIZE", 1073741824)),
		PendingTTL:             envDuration("CONTINUITY_PENDING_TTL", 30*time.Minute),
		LockTimeout:            envDuration("CONTINUITY_LOCK_TIMEOUT", 2*time.Minute),
		SnapshotEntryThreshold: uint64(envInt("CONTINUITY_SNAPSHOT_ENTRY_THRESHOLD", 50)),
		SnapshotByteThreshold:  int64(envInt("CONTINUITY_SNAPSHOT_BYTE_THRESHOLD", 268435456)),
		GCGracePeriod:          envDuration("CONTINUITY_GC_GRACE_PERIOD", time.Hour),
		LabMode:                envBool("CONTINUITY_LAB_MODE", false),
	}
	for _, item := range split(env("CONTINUITY_NODES", "node-a=http://node-a:8080,node-b=http://node-b:8080,node-c=http://node-c:8080")) {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return Config{}, fmt.Errorf("invalid CONTINUITY_NODES item %q", item)
		}
		cfg.Nodes = append(cfg.Nodes, Node{ID: parts[0], URL: strings.TrimRight(parts[1], "/")})
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.Bucket == "" || c.S3Endpoint == "" || c.S3Region == "" || c.DataDir == "" {
		return errors.New("bucket, S3 endpoint, region, and data directory are required")
	}
	if c.CASMaxRetries < 1 || c.ReadReplicaCount < 1 || c.MaxRefsPerPush < 1 || c.ReceiveMaxInputSize < 1 {
		return errors.New("retry, replica, and ref limits must be positive")
	}
	if len(c.GossipSecret) < 16 {
		return errors.New("gossip secret must contain at least 16 bytes")
	}
	return nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func envBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
func envDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
func split(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	raw := strings.Split(value, ",")
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}
