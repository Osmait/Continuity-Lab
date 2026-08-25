package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/continuity-lab/continuity-lab/internal/config"
	"github.com/continuity-lab/continuity-lab/internal/gossip"
	"github.com/continuity-lab/continuity-lab/internal/health"
	"github.com/continuity-lab/continuity-lab/internal/node"
	"github.com/continuity-lab/continuity-lab/internal/objectstore"
	"github.com/continuity-lab/continuity-lab/internal/observability"
)

func main() {
	logger := observability.NewLogger("node", "")
	slog.SetDefault(logger)
	cfg, err := config.Load("node")
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	logger = observability.NewLogger("node", cfg.NodeID)
	slog.SetDefault(logger)
	if err := checkGit(); err != nil {
		logger.Error("unsupported Git installation", "error", err)
		os.Exit(1)
	}
	store, err := objectstore.NewS3(context.Background(), cfg)
	if err != nil {
		logger.Error("configure object store", "error", err)
		os.Exit(1)
	}
	state := &health.State{}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	nodeServer, err := node.New(cfg, store, state)
	if err != nil {
		logger.Error("initialize node", "error", err)
		os.Exit(1)
	}
	go observability.RunEventCollector(ctx, cfg.NodeID, cfg.DataDir)
	go func() {
		if err := gossip.NewService(cfg, store, nodeServer.Repos, nodeServer.Locks).Run(ctx); err != nil && ctx.Err() == nil {
			logger.Error("gossip listener failed", "error", err)
		}
	}()
	go func() {
		if err := health.Wait(ctx, func(check context.Context) error {
			if err := store.EnsureBucket(check); err != nil {
				return err
			}
			return objectstore.Conformance(check, store)
		}); err == nil {
			state.SetReady(true)
			logger.Info("node ready", "node_id", cfg.NodeID)
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					check, cancel := context.WithTimeout(ctx, time.Second)
					err := store.EnsureBucket(check)
					cancel()
					state.SetReady(err == nil)
				}
			}
		}
	}()
	server := &http.Server{Addr: cfg.ListenAddr, Handler: nodeServer.Router(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	logger.Info("node listening", "address", cfg.ListenAddr, "node_id", cfg.NodeID)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func checkGit() error {
	output, err := exec.Command("git", "version").Output()
	if err != nil {
		return err
	}
	fields := strings.Fields(string(output))
	if len(fields) < 3 {
		return fmt.Errorf("unexpected git version output %q", output)
	}
	parts := strings.Split(fields[2], ".")
	if len(parts) < 2 {
		return fmt.Errorf("unexpected git version %q", fields[2])
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if majorErr != nil || minorErr != nil || major < 2 || major == 2 && minor < 39 {
		return fmt.Errorf("Git >= 2.39 with proc-receive is required; found %s", fields[2])
	}
	return nil
}
