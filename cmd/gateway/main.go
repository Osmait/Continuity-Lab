package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/continuity-lab/continuity-lab/internal/config"
	"github.com/continuity-lab/continuity-lab/internal/gateway"
	"github.com/continuity-lab/continuity-lab/internal/health"
	"github.com/continuity-lab/continuity-lab/internal/objectstore"
	"github.com/continuity-lab/continuity-lab/internal/observability"
)

func main() {
	logger := observability.NewLogger("gateway", "")
	slog.SetDefault(logger)
	cfg, err := config.Load("gateway")
	if err != nil {
		logger.Error("invalid configuration", "error", err)
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
	gatewayServer := gateway.New(cfg, store, state)
	go func() {
		if err := health.Wait(ctx, func(check context.Context) error {
			if err := store.EnsureBucket(check); err != nil {
				return err
			}
			return objectstore.Conformance(check, store)
		}); err == nil {
			gatewayServer.RunHealthChecks(ctx)
		}
	}()
	server := &http.Server{Addr: cfg.ListenAddr, Handler: gatewayServer.Router(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	logger.Info("gateway listening", "address", cfg.ListenAddr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}
