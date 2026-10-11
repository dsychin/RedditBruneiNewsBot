package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/dsychin/RedditBruneiNewsBot/internal/config"
	"github.com/dsychin/RedditBruneiNewsBot/internal/fetch"
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

	if !cfg.DryRun {
		logger.Error(
			"normal processing is unavailable until reply posting is implemented",
			slog.String("hint", "set DRY_RUN=true to preview submissions"),
		)
		os.Exit(1)
	}

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

	reddit := ingest.NewRedditClient(cfg.RedditClientID, cfg.RedditClientSecret, cfg.RedditRefreshToken)
	articleFetcher := &fetch.Fetcher{SidecarURL: cfg.SidecarURL}
	previewer := &ingest.ArticlePreviewer{Fetcher: articleFetcher, Version: "v2.00"}
	runner := ingest.NewRunner(reddit, state, previewer, cfg.Subreddits, logger)

	logger.Info(
		"submission ingestion started",
		slog.Any("subreddits", cfg.Subreddits),
		slog.Duration("poll_interval", ingest.PollInterval),
		slog.String("db_path", cfg.DBPath),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runner.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("submission ingestion stopped", slog.String("error", err.Error()))
		os.Exit(1)
	}
}
