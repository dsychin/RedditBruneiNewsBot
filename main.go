package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dsychin/RedditBruneiNewsBot/internal/config"
	"github.com/dsychin/RedditBruneiNewsBot/internal/fetch"
	"github.com/dsychin/RedditBruneiNewsBot/internal/health"
	"github.com/dsychin/RedditBruneiNewsBot/internal/ingest"
	"github.com/dsychin/RedditBruneiNewsBot/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// "healthcheck" is the container liveness probe; it does not start the bot.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := health.Check(health.HeartbeatPath(cfg.DBPath), health.MaxAge, time.Now()); err != nil {
			fmt.Fprintln(os.Stderr, "unhealthy:", err)
			os.Exit(1)
		}
		return
	}

	reddit := ingest.NewRedditClient(cfg.RedditClientID, cfg.RedditClientSecret, cfg.RedditRefreshToken)
	previewer := &ingest.ArticlePreviewer{Fetcher: &fetch.Fetcher{SidecarURL: cfg.SidecarURL}, Version: "v2.00"}

	var handler ingest.Handler
	if cfg.DryRun {
		state, err := store.OpenReadOnly(cfg.DBPath)
		if err != nil {
			logger.Error("open state store read-only", slog.String("error", err.Error()))
			os.Exit(1)
		}
		defer func() {
			if err := state.Close(); err != nil {
				logger.Error("close read-only state store", slog.String("error", err.Error()))
			}
		}()
		handler = ingest.NewDryRunHandler(state, previewer, logger)
	} else {
		db, err := store.Open(cfg.DBPath)
		if err != nil {
			logger.Error("open state store", slog.String("error", err.Error()))
			os.Exit(1)
		}
		defer func() {
			if err := db.Close(); err != nil {
				logger.Error("close state store", slog.String("error", err.Error()))
			}
		}()
		handler = &ingest.ReplyHandler{
			Store:     db,
			Previewer: previewer,
			Poster:    reddit,
			Policy:    fetch.Policy{Base: cfg.RetryBaseDelay, Window: cfg.RetryWindow},
			Logger:    logger,
		}
	}
	runner := ingest.NewRunner(reddit, handler, cfg.Subreddits, logger)
	heartbeatPath := health.HeartbeatPath(cfg.DBPath)
	heartbeatFailed := false
	runner.Heartbeat = func() {
		if err := health.Touch(heartbeatPath, time.Now()); err != nil && !heartbeatFailed {
			heartbeatFailed = true
			logger.Warn("heartbeat unavailable", slog.String("error", err.Error()))
		}
	}

	logger.Info(
		"submission ingestion started",
		slog.Any("subreddits", cfg.Subreddits),
		slog.Duration("poll_interval", ingest.PollInterval),
		slog.String("db_path", cfg.DBPath),
		slog.Bool("dry_run", cfg.DryRun),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runner.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("submission ingestion stopped", slog.String("error", err.Error()))
		os.Exit(1)
	}
}
