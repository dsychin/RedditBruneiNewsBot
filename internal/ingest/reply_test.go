package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dsychin/RedditBruneiNewsBot/internal/extract"
	"github.com/dsychin/RedditBruneiNewsBot/internal/fetch"
	"github.com/dsychin/RedditBruneiNewsBot/internal/store"
)

type fakePoster struct {
	calls int
	errs  []error
}

func (p *fakePoster) PostComment(context.Context, string, string) error {
	p.calls++
	if len(p.errs) > 0 {
		err := p.errs[0]
		p.errs = p.errs[1:]
		return err
	}
	return nil
}

type replyFixture struct {
	t       *testing.T
	path    string
	db      *store.Store
	now     time.Time
	preview *fakePreviewer
	poster  *fakePoster
}

func newReplyFixture(t *testing.T) *replyFixture {
	t.Helper()
	f := &replyFixture{
		t:       t,
		path:    filepath.Join(t.TempDir(), "bot.db"),
		now:     time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC),
		preview: &fakePreviewer{},
		poster:  &fakePoster{},
	}
	f.open()
	return f
}

func (f *replyFixture) open() {
	db, err := store.Open(f.path)
	require.NoError(f.t, err)
	f.db = db
	f.t.Cleanup(func() { _ = f.db.Close() })
}

func (f *replyFixture) restart() {
	require.NoError(f.t, f.db.Close())
	f.open()
}

func (f *replyFixture) handler() *ReplyHandler {
	return &ReplyHandler{
		Store:     f.db,
		Previewer: f.preview,
		Poster:    f.poster,
		Policy:    fetch.Policy{Base: time.Minute, Window: 8 * time.Hour, Rand: func() float64 { return 0.5 }},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:       func() time.Time { return f.now },
	}
}

func (f *replyFixture) post() Submission {
	return Submission{ID: "abc", Subreddit: "news", URL: "https://example.com/a", CreatedAt: f.now.Add(-time.Hour)}
}

func (f *replyFixture) status(id string) store.Status {
	p, err := f.db.Get(context.Background(), id)
	require.NoError(f.t, err)
	return p.Status
}

func TestReplyHandlerPostsOnceAcrossRestart(t *testing.T) {
	f := newReplyFixture(t)
	require.NoError(t, f.handler().Handle(context.Background(), f.post()))
	assert.Equal(t, 1, f.poster.calls)
	assert.Equal(t, store.StatusReplied, f.status("abc"))

	f.restart()
	require.NoError(t, f.handler().Handle(context.Background(), f.post()))
	assert.Equal(t, 1, f.poster.calls)
}

func TestReplyHandlerRetriesOnlyOnceDue(t *testing.T) {
	f := newReplyFixture(t)
	f.preview.failuresRemaining = 1
	h := f.handler()

	require.NoError(t, h.Handle(context.Background(), f.post()))
	assert.Equal(t, store.StatusPending, f.status("abc"))
	assert.Equal(t, 0, f.poster.calls)

	// Not due yet: the claim is refused and nothing is attempted.
	f.now = f.now.Add(10 * time.Second)
	require.NoError(t, h.Handle(context.Background(), f.post()))
	assert.Equal(t, 0, f.poster.calls)

	f.now = f.now.Add(5 * time.Minute)
	require.NoError(t, h.Handle(context.Background(), f.post()))
	assert.Equal(t, 1, f.poster.calls)
	assert.Equal(t, store.StatusReplied, f.status("abc"))
}

func TestReplyHandlerSkipsPermanentFailure(t *testing.T) {
	f := newReplyFixture(t)
	f.preview.err = extract.ErrTextTooShort
	require.NoError(t, f.handler().Handle(context.Background(), f.post()))
	assert.Equal(t, store.StatusSkipped, f.status("abc"))
	assert.Equal(t, 0, f.poster.calls)
}

func TestReplyHandlerRetriesRetryablePostFailure(t *testing.T) {
	f := newReplyFixture(t)
	f.poster.errs = []error{&PostError{Retryable: true, Err: errors.New("boom")}}
	h := f.handler()

	require.NoError(t, h.Handle(context.Background(), f.post()))
	assert.Equal(t, store.StatusPending, f.status("abc"))

	f.now = f.now.Add(5 * time.Minute)
	require.NoError(t, h.Handle(context.Background(), f.post()))
	assert.Equal(t, 2, f.poster.calls)
	assert.Equal(t, store.StatusReplied, f.status("abc"))
}

func TestReplyHandlerSkipsPermanentPostFailure(t *testing.T) {
	f := newReplyFixture(t)
	f.poster.errs = []error{&PostError{Err: errors.New("THREAD_LOCKED")}}
	require.NoError(t, f.handler().Handle(context.Background(), f.post()))
	assert.Equal(t, store.StatusSkipped, f.status("abc"))
}

func TestReplyHandlerSkipsWhenRetryWouldPassPostAgeLimit(t *testing.T) {
	f := newReplyFixture(t)
	f.preview.failuresRemaining = 1
	post := f.post()
	post.CreatedAt = f.now.Add(-24*time.Hour + 30*time.Second)
	require.NoError(t, f.handler().Handle(context.Background(), post))
	assert.Equal(t, store.StatusSkipped, f.status("abc"))
}

func TestReplyHandlerAbortsOnAuthenticationFailure(t *testing.T) {
	f := newReplyFixture(t)
	f.poster.errs = []error{classifyPostError(fmt.Errorf("%w: HTTP 401", ErrAuthentication))}
	err := f.handler().Handle(context.Background(), f.post())
	require.ErrorIs(t, err, ErrAuthentication)
	assert.Equal(t, store.StatusPending, f.status("abc"))
}

func TestPostCommentSuccess(t *testing.T) {
	var form string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/access_token" {
			_, _ = fmt.Fprint(w, `{"access_token":"a","expires_in":3600}`)
			return
		}
		require.NoError(t, r.ParseForm())
		form = r.PostForm.Get("thing_id") + "|" + r.PostForm.Get("text")
		_, _ = fmt.Fprint(w, `{"json":{"errors":[],"data":{}}}`)
	}))
	defer server.Close()

	require.NoError(t, newTestRedditClient(server).PostComment(context.Background(), "abc", "hello"))
	assert.Equal(t, "t3_abc|hello", form)
}

func TestPostCommentClassifiesFailures(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		retryable bool
	}{
		{"ratelimit", 200, `{"json":{"errors":[["RATELIMIT","slow down","ratelimit"]]}}`, true},
		{"thread locked", 200, `{"json":{"errors":[["THREAD_LOCKED","locked","parent"]]}}`, false},
		{"too many requests", 429, `slow`, true},
		{"server error", 503, `down`, true},
		{"forbidden", 403, `no`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/access_token" {
					_, _ = fmt.Fprint(w, `{"access_token":"a","expires_in":3600}`)
					return
				}
				w.WriteHeader(tt.status)
				_, _ = fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			err := newTestRedditClient(server).PostComment(context.Background(), "abc", "hi")
			var postErr *PostError
			require.ErrorAs(t, err, &postErr)
			assert.Equal(t, tt.retryable, postErr.Retryable)
		})
	}
}

func TestPostCommentRefreshesRejectedToken(t *testing.T) {
	var tokens, comments int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/access_token" {
			tokens++
			_, _ = fmt.Fprintf(w, `{"access_token":"access-%d","expires_in":3600}`, tokens)
			return
		}
		comments++
		if comments == 1 {
			http.Error(w, "expired", http.StatusUnauthorized)
			return
		}
		_, _ = fmt.Fprint(w, `{"json":{"errors":[]}}`)
	}))
	defer server.Close()

	require.NoError(t, newTestRedditClient(server).PostComment(context.Background(), "abc", "hi"))
	assert.Equal(t, 2, tokens)
	assert.Equal(t, 2, comments)
}
