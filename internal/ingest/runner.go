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

var errStateRead = errors.New("read stored post state")

// HandledReader reads terminal post state without changing it.
type HandledReader interface {
	IsHandled(context.Context, string) (bool, error)
}

// Previewer fetches and extracts an eligible submission into its would-be
// reply, without submitting it to Reddit.
type Previewer interface {
	Preview(context.Context, Submission) (string, error)
}

// Runner polls configured subreddits and previews eligible submissions.
type Runner struct {
	Client     Client
	State      HandledReader
	Previewer  Previewer
	Subreddits []string
	Logger     *slog.Logger
	Now        func() time.Time

	previewed  map[string]struct{}
	retryAfter map[string]time.Time
}

// NewRunner creates an ingestion runner with process-local preview and
// per-subreddit rate-limit state.
func NewRunner(client Client, state HandledReader, previewer Previewer, subreddits []string, logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{
		Client:     client,
		State:      state,
		Previewer:  previewer,
		Subreddits: append([]string(nil), subreddits...),
		Logger:     logger,
		Now:        time.Now,
		previewed:  make(map[string]struct{}),
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
	if r.State == nil {
		return errors.New("read-only post state is not configured")
	}
	if r.Previewer == nil {
		return errors.New("submission previewer is not configured")
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
		if errors.Is(err, ErrAuthentication) || errors.Is(err, errStateRead) {
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
			if _, previewed := r.previewed[post.ID]; previewed {
				continue
			}

			handled, err := r.State.IsHandled(ctx, post.ID)
			if err != nil {
				return fmt.Errorf("%w: %w", errStateRead, err)
			}
			if handled {
				continue
			}
			reply, err := r.Previewer.Preview(ctx, post)
			if err != nil {
				r.Logger.Error(
					"submission preview failed",
					slog.String("subreddit", subreddit),
					slog.String("post_id", post.ID),
					slog.String("url", post.URL),
					slog.Any("error", err),
				)
				continue
			}
			r.Logger.Info(
				"submission reply preview",
				slog.String("subreddit", subreddit),
				slog.String("post_id", post.ID),
				slog.String("url", post.URL),
				slog.String("reply", reply),
			)
			r.previewed[post.ID] = struct{}{}
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

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}
