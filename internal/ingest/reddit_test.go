package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedditClientRefreshesOAuthAndParsesListing(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	var tokenRequests, listingRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/access_token":
			tokenRequests++
			assert.Equal(t, "client-id", basicAuthUser(t, r))
			assert.Equal(t, redditUserAgent, r.Header.Get("User-Agent"))
			assert.NoError(t, r.ParseForm())
			assert.Equal(t, "refresh_token", r.Form.Get("grant_type"))
			assert.Equal(t, "refresh-token", r.Form.Get("refresh_token"))
			_, _ = fmt.Fprint(w, `{"access_token":"access-a","expires_in":3600}`)
		case "/r/testingground4bots/new":
			listingRequests++
			assert.Equal(t, "Bearer access-a", r.Header.Get("Authorization"))
			assert.Equal(t, redditUserAgent, r.Header.Get("User-Agent"))
			assert.Equal(t, "100", r.URL.Query().Get("limit"))
			assert.Equal(t, "1", r.URL.Query().Get("count"))
			assert.Equal(t, "t3_previous", r.URL.Query().Get("after"))
			_, _ = fmt.Fprintf(w, `{"data":{"after":"t3_next","children":[
				{"kind":"t3","data":{"id":"post-1","subreddit":"testingground4bots","url":"https://example.com/story","created_utc":%d,"is_self":false,"author":"author","removed_by_category":null,"locked":false,"archived":false}},
				{"kind":"t3","data":{"id":"post-2","subreddit":"testingground4bots","url":"https://example.com/removed","created_utc":%d,"is_self":false,"author":"[deleted]","removed_by_category":"moderator","locked":true,"archived":true}}
			]}}`, now.Add(-time.Hour).Unix(), now.Add(-2*time.Hour).Unix())
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newTestRedditClient(server)
	listing, err := client.NewSubmissions(context.Background(), "testingground4bots", "t3_previous", 1)
	require.NoError(t, err)
	assert.Equal(t, 1, tokenRequests)
	assert.Equal(t, 1, listingRequests)
	assert.Equal(t, "t3_next", listing.After)
	require.Len(t, listing.Submissions, 2)

	first := listing.Submissions[0]
	assert.Equal(t, "post-1", first.ID)
	assert.Equal(t, "https://example.com/story", first.URL)
	assert.Equal(t, now.Add(-time.Hour), first.CreatedAt)
	assert.False(t, first.IsSelf)
	assert.False(t, first.Removed)
	assert.False(t, first.Deleted)

	second := listing.Submissions[1]
	assert.True(t, second.Removed)
	assert.True(t, second.Deleted)
	assert.True(t, second.Locked)
	assert.True(t, second.Archived)
}

func TestRedditClientRefreshesRejectedAccessTokenOnce(t *testing.T) {
	var tokenRequests, listingRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/access_token":
			tokenRequests++
			_, _ = fmt.Fprintf(w, `{"access_token":"access-%d","expires_in":3600}`, tokenRequests)
		case "/r/news/new":
			listingRequests++
			if r.Header.Get("Authorization") == "Bearer access-1" {
				http.Error(w, "expired", http.StatusUnauthorized)
				return
			}
			_, _ = fmt.Fprint(w, `{"data":{"after":null,"children":[]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newTestRedditClient(server)
	_, err := client.NewSubmissions(context.Background(), "news", "", 0)
	require.NoError(t, err)
	assert.Equal(t, 2, tokenRequests)
	assert.Equal(t, 2, listingRequests)
}

func TestRedditClientReturnsRateLimitDelay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/access_token" {
			_, _ = fmt.Fprint(w, `{"access_token":"access-a","expires_in":3600}`)
			return
		}
		w.Header().Set("Retry-After", "120")
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := newTestRedditClient(server)
	_, err := client.NewSubmissions(context.Background(), "news", "", 0)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
	assert.Equal(t, 2*time.Minute, apiErr.RetryAfter)
}

func TestRedditClientMarksOAuthFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
	}))
	defer server.Close()

	client := newTestRedditClient(server)
	_, err := client.NewSubmissions(context.Background(), "news", "", 0)
	require.ErrorIs(t, err, ErrAuthentication)
	assert.Contains(t, err.Error(), "HTTP 401")
}

func TestSubmissionTimestampRejectsInvalidValue(t *testing.T) {
	submission, err := (redditSubmission{CreatedUTC: json.Number("not-a-timestamp")}).submission()
	require.Error(t, err)
	assert.Empty(t, submission.ID)
}

func newTestRedditClient(server *httptest.Server) *RedditClient {
	client := NewRedditClient("client-id", "client-secret", "refresh-token")
	client.HTTPClient = server.Client()
	client.OAuthURL = server.URL + "/api/v1/access_token"
	client.APIBaseURL = server.URL
	client.now = func() time.Time { return time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC) }
	return client
}

func basicAuthUser(t *testing.T, request *http.Request) string {
	t.Helper()
	user, password, ok := request.BasicAuth()
	assert.True(t, ok)
	assert.Equal(t, "client-secret", password)
	return user
}

func TestParseRetryAfterHTTPDate(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	value := now.Add(45 * time.Second).Format(http.TimeFormat)
	assert.Equal(t, 45*time.Second, parseRetryAfter(value, now))
	assert.Zero(t, parseRetryAfter("invalid", now))
}

func TestAuthErrorDoesNotLeakCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "refresh failed", http.StatusUnauthorized)
	}))
	defer server.Close()
	client := newTestRedditClient(server)
	client.ClientSecret = "sensitive-secret"
	client.RefreshToken = "sensitive-refresh"

	_, err := client.NewSubmissions(context.Background(), "news", "", 0)
	require.Error(t, err)
	assert.NotContains(t, strings.ToLower(err.Error()), "sensitive-secret")
	assert.NotContains(t, strings.ToLower(err.Error()), "sensitive-refresh")
}

func TestListingURLDoesNotTreatSubredditAsPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/access_token" {
			_, _ = fmt.Fprint(w, `{"access_token":"access-a","expires_in":3600}`)
			return
		}
		assert.True(t, strings.Contains(r.URL.EscapedPath(), "%2F"))
		_, _ = fmt.Fprint(w, `{"data":{"after":null,"children":[]}}`)
	}))
	defer server.Close()
	client := newTestRedditClient(server)

	_, err := client.NewSubmissions(context.Background(), "news/other", "", 0)
	require.NoError(t, err)
}
