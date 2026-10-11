package ingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCycleFiltersPostsAndSkipsHandledPosts(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	client := &fakeClient{pages: map[string][]pageResult{
		"news": {{
			listing: Listing{Submissions: []Submission{
				testPost("eligible", now.Add(-time.Hour)),
				{ID: "self", CreatedAt: now.Add(-time.Hour), IsSelf: true},
				{ID: "removed", CreatedAt: now.Add(-time.Hour), Removed: true},
				{ID: "deleted", CreatedAt: now.Add(-time.Hour), Deleted: true},
				{ID: "locked", CreatedAt: now.Add(-time.Hour), Locked: true},
				{ID: "archived", CreatedAt: now.Add(-time.Hour), Archived: true},
				testPost("handled", now.Add(-time.Hour)),
				testPost("too-old", now.Add(-25*time.Hour)),
			}},
		}},
	}}
	state := &fakeState{handled: map[string]bool{"handled": true}}
	previewer := &fakePreviewer{}
	runner := testRunner(client, state, previewer, now, "news")

	require.NoError(t, runner.RunCycle(context.Background()))
	require.Equal(t, []string{"eligible"}, previewer.posts)
	assert.Len(t, client.requests, 1)
}

func TestRunCyclePaginatesToStrict24HourCutoff(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-eligibilityWindow)
	client := &fakeClient{pages: map[string][]pageResult{
		"news": {
			{listing: Listing{Submissions: []Submission{testPost("first", now.Add(-time.Hour))}, After: "page-2"}},
			{listing: Listing{Submissions: []Submission{
				testPost("second", now.Add(-23*time.Hour)),
				testPost("exactly-cutoff", cutoff),
				testPost("older", cutoff.Add(-time.Minute)),
			}, After: "page-3"}},
		},
	}}
	previewer := &fakePreviewer{}
	runner := testRunner(client, &fakeState{}, previewer, now, "news")

	require.NoError(t, runner.RunCycle(context.Background()))
	assert.Equal(t, []string{"first", "second"}, previewer.posts)
	require.Len(t, client.requests, 2)
	assert.Equal(t, "page-2", client.requests[1].after)
	assert.Equal(t, 1, client.requests[1].count)
}

func TestRunCycleProcessesSuccessfulPagesAndContinuesAfterSubredditError(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	client := &fakeClient{pages: map[string][]pageResult{
		"first": {
			{listing: Listing{Submissions: []Submission{testPost("partial", now.Add(-time.Hour))}, After: "next"}},
			{err: errors.New("page request failed")},
		},
		"second": {
			{listing: Listing{Submissions: []Submission{testPost("other-subreddit", now.Add(-time.Hour))}}},
		},
	}}
	previewer := &fakePreviewer{}
	runner := testRunner(client, &fakeState{}, previewer, now, "first", "second")

	require.NoError(t, runner.RunCycle(context.Background()))
	assert.Equal(t, []string{"partial", "other-subreddit"}, previewer.posts)
	assert.Equal(t, []string{"first", "first", "second"}, client.subreddits())
}

func TestRunCycleAbortsAllSubredditsOnAuthenticationFailure(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	client := &fakeClient{pages: map[string][]pageResult{
		"first":  {{err: errors.Join(ErrAuthentication, errors.New("refresh failed"))}},
		"second": {{listing: Listing{Submissions: []Submission{testPost("unscanned", now)}}}},
	}}
	runner := testRunner(client, &fakeState{}, &fakePreviewer{}, now, "first", "second")

	require.ErrorIs(t, runner.RunCycle(context.Background()), ErrAuthentication)
	assert.Equal(t, []string{"first"}, client.subreddits())
}

func TestRunCycleAbortsWhenStoredStateCannotBeRead(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	client := &fakeClient{pages: map[string][]pageResult{
		"first":  {{listing: Listing{Submissions: []Submission{testPost("post", now.Add(-time.Hour))}}}},
		"second": {{listing: Listing{Submissions: []Submission{testPost("unscanned", now.Add(-time.Hour))}}}},
	}}
	state := &fakeState{err: errors.New("database read failed")}
	runner := testRunner(client, state, &fakePreviewer{}, now, "first", "second")

	require.ErrorContains(t, runner.RunCycle(context.Background()), "read stored post state")
	assert.Equal(t, []string{"first"}, client.subreddits())
}

func TestRunCycleDefersOnlyRateLimitedSubreddit(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	client := &fakeClient{pages: map[string][]pageResult{
		"limited": {{
			err: &APIError{StatusCode: 429, RetryAfter: 2 * time.Minute, Err: errors.New("slow down")},
		}},
		"other": {{listing: Listing{Submissions: []Submission{testPost("other", now.Add(-time.Hour))}}}},
	}}
	runner := testRunner(client, &fakeState{}, &fakePreviewer{}, now, "limited", "other")

	require.NoError(t, runner.RunCycle(context.Background()))
	assert.Equal(t, []string{"limited", "other"}, client.subreddits())

	client.calls = nil
	runner.Now = func() time.Time { return now.Add(time.Minute) }
	require.NoError(t, runner.RunCycle(context.Background()))
	assert.Equal(t, []string{"limited", "other", "other"}, client.subreddits())
}

func TestRunCycleRetriesFailedPreviewAndDeduplicatesSuccessfulPreview(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	client := &fakeClient{pages: map[string][]pageResult{
		"news": {{listing: Listing{Submissions: []Submission{testPost("post", now.Add(-time.Hour))}}}},
	}}
	previewer := &fakePreviewer{failuresRemaining: 1}
	runner := testRunner(client, &fakeState{}, previewer, now, "news")

	require.NoError(t, runner.RunCycle(context.Background()))
	client.calls = nil
	require.NoError(t, runner.RunCycle(context.Background()))
	client.calls = nil
	require.NoError(t, runner.RunCycle(context.Background()))
	assert.Equal(t, []string{"post", "post"}, previewer.posts)
}

func TestWaitForNextTickDropsTicksThatOccurredDuringCycle(t *testing.T) {
	finished := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	ticks := make(chan time.Time, 2)
	ticks <- finished
	ticks <- finished.Add(time.Second)

	require.NoError(t, waitForNextTick(context.Background(), ticks, finished))
	assert.Empty(t, ticks)
}

func TestRunStartsTheFirstCycleImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &cancelClient{cancel: cancel}
	runner := testRunner(client, &fakeState{}, &fakePreviewer{}, time.Now(), "news")

	err := runner.Run(ctx)
	require.ErrorIs(t, err, context.Canceled)
	assert.True(t, client.called)
}

func testRunner(client Client, state HandledReader, previewer Previewer, now time.Time, subreddits ...string) *Runner {
	runner := NewRunner(client, state, previewer, subreddits, slog.New(slog.NewTextHandler(io.Discard, nil)))
	runner.Now = func() time.Time { return now }
	return runner
}

type cancelClient struct {
	cancel context.CancelFunc
	called bool
}

func (c *cancelClient) NewSubmissions(context.Context, string, string, int) (Listing, error) {
	c.called = true
	c.cancel()
	return Listing{}, nil
}

func testPost(id string, createdAt time.Time) Submission {
	return Submission{
		ID:        id,
		Subreddit: "news",
		URL:       "https://example.com/" + id,
		CreatedAt: createdAt,
	}
}

type pageResult struct {
	listing Listing
	err     error
}

type fakeClient struct {
	pages    map[string][]pageResult
	requests []clientRequest
	calls    map[string]int
}

type clientRequest struct {
	subreddit string
	after     string
	count     int
}

func (c *fakeClient) NewSubmissions(_ context.Context, subreddit, after string, count int) (Listing, error) {
	if c.calls == nil {
		c.calls = make(map[string]int)
	}
	c.requests = append(c.requests, clientRequest{subreddit: subreddit, after: after, count: count})
	index := c.calls[subreddit]
	c.calls[subreddit]++
	pages := c.pages[subreddit]
	if index >= len(pages) {
		return Listing{}, nil
	}
	return pages[index].listing, pages[index].err
}

func (c *fakeClient) subreddits() []string {
	result := make([]string, 0, len(c.requests))
	for _, request := range c.requests {
		result = append(result, request.subreddit)
	}
	return result
}

type fakeState struct {
	handled map[string]bool
	err     error
}

func (s *fakeState) IsHandled(_ context.Context, postID string) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	return s.handled[postID], nil
}

type fakePreviewer struct {
	posts             []string
	failuresRemaining int
}

func (p *fakePreviewer) Preview(_ context.Context, submission Submission) (string, error) {
	p.posts = append(p.posts, submission.ID)
	if p.failuresRemaining > 0 {
		p.failuresRemaining--
		return "", errors.New("preview failed")
	}
	return "reply for " + submission.ID, nil
}
