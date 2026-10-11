package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const articleHTML = "<html><head><title>Story</title></head><body><p>Hello</p></body></html>"

func htmlHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, body)
	}
}

// fakeSidecar answers like cf-clearance-scraper in source mode and counts calls.
func fakeSidecar(t *testing.T, code int, source string) (*httptest.Server, *atomic.Int32, *sidecarRequest) {
	t.Helper()
	var calls atomic.Int32
	var last sidecarRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&last))
		_ = json.NewEncoder(w).Encode(sidecarResponse{Source: source, Code: code})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls, &last
}

func requireFetchError(t *testing.T, err error, stage Stage, retry bool) *Error {
	t.Helper()
	var fe *Error
	require.ErrorAs(t, err, &fe)
	assert.Equal(t, stage, fe.Stage)
	assert.Equal(t, retry, fe.Retryable)
	return fe
}

func TestPlainGetSendsBrowserUserAgentAndReturnsFinalURL(t *testing.T) {
	var ua string
	var origin *httptest.Server
	origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusFound)
			return
		}
		ua = r.Header.Get("User-Agent")
		htmlHandler(articleHTML)(w, r)
	}))
	defer origin.Close()

	page, err := (&Fetcher{}).Fetch(context.Background(), origin.URL+"/old")
	require.NoError(t, err)

	assert.Contains(t, ua, "Mozilla/5.0")
	assert.Equal(t, articleHTML, string(page.Body))
	assert.Equal(t, origin.URL+"/new", page.URL.String())
}

func TestChallengeFallsBackToSidecar(t *testing.T) {
	tests := map[string]http.HandlerFunc{
		"cf-mitigated header": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("cf-mitigated", "challenge")
			w.WriteHeader(http.StatusForbidden)
		},
		"just a moment body": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, "<title>Just a moment...</title>")
		},
	}
	for name, handler := range tests {
		t.Run(name, func(t *testing.T) {
			origin := httptest.NewServer(handler)
			defer origin.Close()
			sidecar, calls, last := fakeSidecar(t, 200, articleHTML)

			page, err := (&Fetcher{SidecarURL: sidecar.URL}).Fetch(context.Background(), origin.URL+"/a")
			require.NoError(t, err)

			assert.EqualValues(t, 1, calls.Load())
			assert.Equal(t, "source", last.Mode)
			assert.Equal(t, origin.URL+"/a", last.URL)
			assert.Equal(t, articleHTML, string(page.Body))
			assert.Equal(t, origin.URL+"/a", page.URL.String())
		})
	}
}

func TestKnownCloudflareHostSkipsPlainGet(t *testing.T) {
	var plainHits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { plainHits.Add(1) }))
	defer origin.Close()
	sidecar, calls, _ := fakeSidecar(t, 200, articleHTML)
	ou, _ := url.Parse(origin.URL)

	f := &Fetcher{SidecarURL: sidecar.URL, CloudflareHosts: map[string]bool{ou.Hostname(): true}}
	_, err := f.Fetch(context.Background(), origin.URL+"/a")
	require.NoError(t, err)

	assert.Zero(t, plainHits.Load())
	assert.EqualValues(t, 1, calls.Load())
}

func TestSidecarOutageForKnownHostDoesNotFallBackToPlainGet(t *testing.T) {
	var plainHits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { plainHits.Add(1) }))
	defer origin.Close()
	down := httptest.NewServer(http.NotFoundHandler())
	sidecarURL := down.URL
	down.Close()
	ou, _ := url.Parse(origin.URL)

	f := &Fetcher{SidecarURL: sidecarURL, CloudflareHosts: map[string]bool{ou.Hostname(): true}}
	_, err := f.Fetch(context.Background(), origin.URL)

	requireFetchError(t, err, StageSidecar, true)
	assert.Zero(t, plainHits.Load())
}

func TestSidecarFailureClassification(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		retry   bool
	}{
		{"HTTP 500", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }, true},
		{"invalid JSON", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "nope") }, true},
		{"page code 500", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"code":500,"source":""}`) }, true},
		{"page code 404", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"code":404,"source":""}`) }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sidecar := httptest.NewServer(tt.handler)
			defer sidecar.Close()
			f := &Fetcher{SidecarURL: sidecar.URL, CloudflareHosts: map[string]bool{"example.com": true}}

			_, err := f.Fetch(context.Background(), "https://example.com/a")

			requireFetchError(t, err, StageSidecar, tt.retry)
		})
	}
}

func TestSidecarTimeoutIsRetryable(t *testing.T) {
	sidecar := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// Reading the body lets the server notice the client hanging up.
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer sidecar.Close()
	f := &Fetcher{SidecarURL: sidecar.URL, SidecarTimeout: 20 * time.Millisecond, CloudflareHosts: map[string]bool{"example.com": true}}

	_, err := f.Fetch(context.Background(), "https://example.com/a")

	requireFetchError(t, err, StageSidecar, true)
}

func TestPlainGetFailureClassification(t *testing.T) {
	tests := []struct {
		name  string
		code  int
		retry bool
	}{
		{"404", 404, false},
		{"410", 410, false},
		{"plain 403", 403, false},
		{"401", 401, false},
		{"500", 500, true},
		{"503 without challenge", 503, true},
		{"408", 408, true},
		{"429", 429, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tt.code) }))
			defer origin.Close()

			_, err := (&Fetcher{}).Fetch(context.Background(), origin.URL)

			requireFetchError(t, err, StageFetch, tt.retry)
		})
	}
}

func TestRateLimitedHonoursRetryAfter(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer origin.Close()

	_, err := (&Fetcher{}).Fetch(context.Background(), origin.URL)

	fe := requireFetchError(t, err, StageFetch, true)
	assert.Equal(t, 2*time.Minute, fe.RetryAfter)
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, 30*time.Second, parseRetryAfter("30", now))
	assert.Equal(t, time.Minute, parseRetryAfter(now.Add(time.Minute).Format(http.TimeFormat), now))
	assert.Zero(t, parseRetryAfter(now.Add(-time.Minute).Format(http.TimeFormat), now))
	assert.Zero(t, parseRetryAfter("soon", now))
}

func TestNonHTMLContentTypeIsPermanent(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		fmt.Fprint(w, "%PDF")
	}))
	defer origin.Close()

	_, err := (&Fetcher{}).Fetch(context.Background(), origin.URL)

	requireFetchError(t, err, StageFetch, false)
}

func TestXHTMLContentTypeIsAccepted(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xhtml+xml")
		fmt.Fprint(w, articleHTML)
	}))
	defer origin.Close()

	_, err := (&Fetcher{}).Fetch(context.Background(), origin.URL)

	require.NoError(t, err)
}

func TestBodyOverCapIsPermanentAndExactCapIsAccepted(t *testing.T) {
	body := strings.Repeat("a", 100)
	origin := httptest.NewServer(htmlHandler(body))
	defer origin.Close()

	_, err := (&Fetcher{MaxBodyBytes: 100}).Fetch(context.Background(), origin.URL)
	require.NoError(t, err)

	_, err = (&Fetcher{MaxBodyBytes: 99}).Fetch(context.Background(), origin.URL)
	requireFetchError(t, err, StageFetch, false)
}

func TestSidecarSourceOverCapIsPermanent(t *testing.T) {
	sidecar, _, _ := fakeSidecar(t, 200, strings.Repeat("a", 100))
	f := &Fetcher{SidecarURL: sidecar.URL, MaxBodyBytes: 99, CloudflareHosts: map[string]bool{"example.com": true}}

	_, err := f.Fetch(context.Background(), "https://example.com/a")

	requireFetchError(t, err, StageSidecar, false)
}

func TestRedirectLimit(t *testing.T) {
	var hops atomic.Int32
	var origin *httptest.Server
	origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hops.Add(1)
		if n <= 5 {
			http.Redirect(w, r, fmt.Sprintf("/hop%d", n), http.StatusFound)
			return
		}
		if n == 6 {
			http.Redirect(w, r, "/more", http.StatusFound)
			return
		}
		htmlHandler(articleHTML)(w, r)
	}))
	defer origin.Close()

	// The first request plus 5 redirects succeeds; a 6th redirect fails.
	hops.Store(1)
	_, err := (&Fetcher{}).Fetch(context.Background(), origin.URL)
	require.NoError(t, err)

	hops.Store(0)
	_, err = (&Fetcher{}).Fetch(context.Background(), origin.URL)
	requireFetchError(t, err, StageFetch, false)
}

func TestNetworkErrorAndTimeoutAreRetryable(t *testing.T) {
	down := httptest.NewServer(http.NotFoundHandler())
	downURL := down.URL
	down.Close()
	_, err := (&Fetcher{}).Fetch(context.Background(), downURL)
	requireFetchError(t, err, StageFetch, true)

	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	_, err = (&Fetcher{GetTimeout: 20 * time.Millisecond}).Fetch(context.Background(), slow.URL)
	requireFetchError(t, err, StageFetch, true)
}

func TestInvalidURLIsPermanent(t *testing.T) {
	for _, raw := range []string{"", "ftp://example.com/a", "not a url", "/relative"} {
		_, err := (&Fetcher{}).Fetch(context.Background(), raw)
		requireFetchError(t, err, StageFetch, false)
	}
}

func TestIsRetryable(t *testing.T) {
	assert.True(t, IsRetryable(fmt.Errorf("wrapped: %w", retryable(StageFetch, assert.AnError))))
	assert.False(t, IsRetryable(permanent(StageFetch, assert.AnError)))
	assert.False(t, IsRetryable(assert.AnError))
}
