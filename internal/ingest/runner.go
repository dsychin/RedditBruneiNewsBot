package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const (
	// PollInterval is the fixed wall-clock interval between ingestion cycles.
	PollInterval      = time.Minute
	eligibilityWindow = 24 * time.Hour
)

// ErrState marks a failure to read or update stored post state. It aborts the
// ingestion cycle, since continuing could lose or duplicate work.
var ErrState = errors.New("stored post state unavailable")

// Handler processes one eligible submission. Failures specific to the post
// are logged and recorded by the handler and returned as nil; only errors
// wrapping ErrState or ErrAuthentication end the cycle.
type Handler interface {
	Handle(context.Context, Submission) error
}

// Runner polls configured subreddits and hands eligible submissions to a
// Handler.
type Runner struct {
	Client     Client
	Handler    Handler
	Subreddits []string
	Logger     *slog.Logger
	Now        func() time.Time
	// Heartbeat, if set, is called after every handled submission and every
	// finished cycle to signal liveness.
	Heartbeat func()

	retryAfter map[string]time.Time
}

// NewRunner creates an ingestion runner with per-subreddit rate-limit state.
func NewRunner(client Client, handler Handler, subreddits []string, logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{
		Client:     client,
		Handler:    handler,
		Subreddits: append([]string(nil), subreddits...),
		Logger:     logger,
		Now:        time.Now,
		retryAfter: make(map[string]time.Time),
	}
}

// Run starts polling immediately and then follows fixed wall-clock ticks.
// Ticks that occur while a cycle is running are dropped rather than queued.
func (r *Runner) Run(ctx context.Context) error {
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()

	for {
		if err := r.RunCycle(ctx); err != nil && !errors.Is(err, context.Canceled) {
			r.Logger.Error("ingestion cycle failed", slog.Any("error", err))
		}
		r.beat()
		if err := waitForNextTick(ctx, ticker.C, r.now()); err != nil {
			return err
		}
	}
}

func waitForNextTick(ctx context.Context, ticks <-chan time.Time, cycleFinished time.Time) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case tick := <-ticks:
			if tick.After(cycleFinished) {
				return nil
			}
		}
	}
}

// RunCycle scans every configured subreddit sequentially. A Reddit
// authentication error or unreadable stored state aborts the whole cycle;
// subreddit-specific listing failures do not block the remaining subreddits.
func (r *Runner) RunCycle(ctx context.Context) error {
	if r.Client == nil {
		return errors.New("Reddit client is not configured")
	}
	if r.Handler == nil {
		return errors.New("submission handler is not configured")
	}
	now := r.now()
	cutoff := now.Add(-eligibilityWindow)
	seenThisCycle := make(map[string]struct{})

	for _, subreddit := range r.Subreddits {
		if until, limited := r.retryAfter[subreddit]; limited {
			if now.Before(until) {
				r.Logger.Info(
					"deferring subreddit after Reddit rate limit",
					slog.String("subreddit", subreddit),
					slog.Time("retry_after", until),
				)
				continue
			}
			delete(r.retryAfter, subreddit)
		}

		err := r.scanSubreddit(ctx, subreddit, cutoff, seenThisCycle)
		if err == nil {
			continue
		}
		if errors.Is(err, ErrAuthentication) || errors.Is(err, ErrState) {
			return err
		}
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
			until := r.now().Add(apiErr.RetryAfter)
			r.retryAfter[subreddit] = until
			r.Logger.Warn(
				"Reddit rate limited subreddit",
				slog.String("subreddit", subreddit),
				slog.Time("retry_after", until),
				slog.Any("error", err),
			)
			continue
		}
		r.Logger.Error(
			"subreddit scan incomplete",
			slog.String("subreddit", subreddit),
			slog.Any("error", err),
		)
	}
	return nil
}

func (r *Runner) scanSubreddit(ctx context.Context, subreddit string, cutoff time.Time, seenThisCycle map[string]struct{}) error {
	after := ""
	count := 0
	seenCursors := make(map[string]struct{})
	for {
		if after != "" {
			if _, exists := seenCursors[after]; exists {
				return fmt.Errorf("Reddit repeated pagination cursor %q", after)
			}
			seenCursors[after] = struct{}{}
		}
		listing, err := r.Client.NewSubmissions(ctx, subreddit, after, count)
		if err != nil {
			return err
		}

		reachedCutoff := false
		for _, post := range listing.Submissions {
			if post.CreatedAt.IsZero() {
				r.Logger.Warn(
					"submission has no valid creation time",
					slog.String("subreddit", subreddit),
					slog.String("post_id", post.ID),
				)
				continue
			}
			if !post.CreatedAt.After(cutoff) {
				reachedCutoff = true
				break
			}
			if !eligible(post) || post.ID == "" {
				continue
			}
			if _, seen := seenThisCycle[post.ID]; seen {
				continue
			}
			seenThisCycle[post.ID] = struct{}{}
			if err := r.Handler.Handle(ctx, post); err != nil {
				return err
			}
			r.beat()
		}

		if reachedCutoff || listing.After == "" {
			return nil
		}
		count += len(listing.Submissions)
		after = listing.After
	}
}

func eligible(post Submission) bool {
	return !post.IsSelf && !post.Removed && !post.Deleted && !post.Locked && !post.Archived
}

func (r *Runner) beat() {
	if r.Heartbeat != nil {
		r.Heartbeat()
	}
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}
