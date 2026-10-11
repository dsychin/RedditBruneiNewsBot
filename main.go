package main

import (
	"log/slog"
	"os"

	"github.com/dsychin/RedditBruneiNewsBot/internal/config"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger.Info(
		"bot scaffold initialized",
		slog.String("mode", cfg.Mode),
		slog.Any("subreddits", cfg.Subreddits),
		slog.Bool("dry_run", cfg.DryRun),
		slog.String("retry_base_delay", cfg.RetryBaseDelay.String()),
		slog.String("retry_window", cfg.RetryWindow.String()),
		slog.String("db_path", cfg.DBPath),
	)
}
