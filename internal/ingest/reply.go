package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dsychin/RedditBruneiNewsBot/internal/extract"
	"github.com/dsychin/RedditBruneiNewsBot/internal/fetch"
	"github.com/dsychin/RedditBruneiNewsBot/internal/store"
)

const markRepliedAttempts = 3

// Poster submits a top-level comment on a submission.
type Poster interface {
	PostComment(ctx context.Context, postID, text string) error
}

// PostStore is the persisted per-post state used while replying.
type PostStore interface {
	Claim(ctx context.Context, postID, subreddit string, now time.Time) (bool, error)
	Get(ctx context.Context, postID string) (store.Post, error)
	RecordFailure(ctx context.Context, postID string, failedAt time.Time, nextAttemptAt *time.Time) error
	MarkReplied(ctx context.Context, postID string, repliedAt time.Time) error
}

// ReplyHandler claims each eligible submission, builds the reply, posts it and
// records the outcome. Retryable failures are scheduled in the store and picked
// up again by a later scan once due, so no work blocks other posts. Posts are
// handled one at a time, which also caps sidecar concurrency at 1.
type ReplyHandler struct {
	Store     PostStore
	Previewer Previewer
	Poster    Poster
	Policy    fetch.Policy
	Logger    *slog.Logger
	Now       func() time.Time
}

// Handle implements Handler.
func (h *ReplyHandler) Handle(ctx context.Context, post Submission) error {
	now := h.now()
	claimed, err := h.Store.Claim(ctx, post.ID, post.Subreddit, now)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrState, err)
	}
	if !claimed {
		return nil
	}

	reply, err := h.Previewer.Preview(ctx, post)
	if err != nil {
		return h.fail(ctx, post, err)
	}
	if err := h.Poster.PostComment(ctx, post.ID, reply); err != nil {
		return h.fail(ctx, post, err)
	}

	// The comment is live, so losing this write would allow a duplicate reply
	// after a restart; retry briefly rather than give up on the first error.
	var markErr error
	for range markRepliedAttempts {
		if markErr = h.Store.MarkReplied(ctx, post.ID, h.now()); markErr == nil {
			break
		}
	}
	if markErr != nil {
		h.Logger.Error(
			"reply posted but not recorded; a restart may reply again",
			slog.String("post_id", post.ID),
			slog.String("url", post.URL),
			slog.Any("error", markErr),
		)
		return fmt.Errorf("%w: %w", ErrState, markErr)
	}
	h.Logger.Info(
		"reply posted",
		slog.String("subreddit", post.Subreddit),
		slog.String("post_id", post.ID),
		slog.String("url", post.URL),
	)
	return nil
}

// fail records a failed attempt, scheduling a retry when the failure is
// retryable and the policy still allows one.
func (h *ReplyHandler) fail(ctx context.Context, post Submission, cause error) error {
	failure := classify(cause)
	now := h.now()

	stored, err := h.Store.Get(ctx, post.ID)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrState, err)
	}
	attempt := stored.Attempts + 1
	firstFailedAt := now
	if stored.FirstFailedAt != nil {
		firstFailedAt = *stored.FirstFailedAt
	}

	var next *time.Time
	if failure.retryable {
		if at, ok := h.Policy.Next(now, firstFailedAt, post.CreatedAt, attempt, failure.retryAfter); ok {
			next = &at
		}
	}
	if err := h.Store.RecordFailure(ctx, post.ID, now, next); err != nil {
		return fmt.Errorf("%w: %w", ErrState, err)
	}

	attrs := []any{
		slog.String("subreddit", post.Subreddit),
		slog.String("post_id", post.ID),
		slog.String("url", post.URL),
		slog.String("stage", failure.stage),
		slog.Int("attempt", attempt),
		slog.Any("error", cause),
	}
	if next != nil {
		h.Logger.Warn("reply attempt failed; retry scheduled", append(attrs, slog.Time("next_attempt_at", *next))...)
	} else {
		h.Logger.Error("reply attempt failed; post skipped", append(attrs, slog.Bool("retryable", failure.retryable))...)
	}

	// A rejected token affects every post, so end the cycle.
	if errors.Is(cause, ErrAuthentication) {
		return cause
	}
	return nil
}

func (h *ReplyHandler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

type failure struct {
	stage      string
	retryable  bool
	retryAfter time.Duration
}

func classify(err error) failure {
	var fetchErr *fetch.Error
	var postErr *PostError
	switch {
	case errors.As(err, &fetchErr):
		return failure{stage: string(fetchErr.Stage), retryable: fetchErr.Retryable, retryAfter: fetchErr.RetryAfter}
	case errors.As(err, &postErr):
		return failure{stage: "post", retryable: postErr.Retryable, retryAfter: postErr.RetryAfter}
	case errors.Is(err, extract.ErrNoTitle), errors.Is(err, extract.ErrTextTooShort):
		return failure{stage: "validate"}
	default:
		return failure{stage: "parse"}
	}
}
