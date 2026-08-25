package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/continuity-lab/continuity-lab/internal/config"
	"github.com/continuity-lab/continuity-lab/internal/githooks/postreceive"
	"github.com/continuity-lab/continuity-lab/internal/githooks/prereceive"
	"github.com/continuity-lab/continuity-lab/internal/githooks/procreceive"
	"github.com/continuity-lab/continuity-lab/internal/objectstore"
	"github.com/continuity-lab/continuity-lab/internal/observability"
)

func main() {
	slog.SetDefault(observability.NewLoggerTo(os.Stderr, "continuity-hook", ""))
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: continuity-hook <pre-receive|proc-receive|post-receive>")
		os.Exit(2)
	}
	cfg, err := config.Load("continuity-hook")
	if err != nil {
		fail(err)
	}
	logger := observability.NewLoggerTo(os.Stderr, "continuity-hook", cfg.NodeID).With(
		"hook", os.Args[1], "request_id", os.Getenv("CONTINUITY_REQUEST_ID"), "push_id", os.Getenv("CONTINUITY_PUSH_ID"),
		"repo_id", os.Getenv("CONTINUITY_REPO_ID"), "repo_name", os.Getenv("CONTINUITY_REPO_NAME"),
	)
	slog.SetDefault(logger)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	store, err := objectstore.NewS3(ctx, cfg)
	if err != nil {
		fail(err)
	}
	switch os.Args[1] {
	case "pre-receive":
		err = prereceive.Run(ctx, os.Stdin, os.Stderr, cfg, store)
	case "proc-receive":
		err = procreceive.Run(ctx, os.Stdin, os.Stdout, os.Stderr, cfg, store)
	case "post-receive":
		err = postreceive.Run(ctx, os.Stdin, os.Stdout, cfg, store)
	default:
		err = fmt.Errorf("unknown hook %q", os.Args[1])
	}
	if err != nil {
		fail(err)
	}
}

func fail(err error) { slog.Error("hook failed", "error", err); os.Exit(1) }
