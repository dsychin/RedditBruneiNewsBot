package ingest

import (
	"context"
	"fmt"
	"log/slog"
)

// HandledReader reads terminal post state without changing it.
type HandledReader interface {
	IsHandled(context.Context, string) (bool, error)
}

// Previewer fetches and extracts an eligible submission into its would-be
// reply, without submitting it to Reddit.
type Previewer interface {
	Preview(context.Context, Submission) (string, error)
}

// DryRunHandler logs the reply each eligible, unhandled submission would
// receive. It never posts and never writes stored state.
type DryRunHandler struct {
	State     HandledReader
	Previewer Previewer
	Logger    *slog.Logger

	previewed map[string]struct{}
}

// NewDryRunHandler creates a handler that remembers successful previews for
// the lifetime of the process, since dry-run cannot persist handled state.
func NewDryRunHandler(state HandledReader, previewer Previewer, logger *slog.Logger) *DryRunHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &DryRunHandler{
		State:     state,
		Previewer: previewer,
		Logger:    logger,
		previewed: make(map[string]struct{}),
	}
}

// Handle implements Handler.
func (h *DryRunHandler) Handle(ctx context.Context, post Submission) error {
	if _, previewed := h.previewed[post.ID]; previewed {
		return nil
	}
	handled, err := h.State.IsHandled(ctx, post.ID)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrState, err)
	}
	if handled {
		return nil
	}

	reply, err := h.Previewer.Preview(ctx, post)
	if err != nil {
		h.Logger.Error(
			"submission preview failed",
			slog.String("subreddit", post.Subreddit),
			slog.String("post_id", post.ID),
			slog.String("url", post.URL),
			slog.Any("error", err),
		)
		return nil
	}
	h.Logger.Info(
		"submission reply preview",
		slog.String("subreddit", post.Subreddit),
		slog.String("post_id", post.ID),
		slog.String("url", post.URL),
		slog.String("reply", reply),
	)
	h.previewed[post.ID] = struct{}{}
	return nil
}
