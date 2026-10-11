// Package ingest discovers eligible Reddit submissions for processing.
package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	listingPageSize    = 100
	maxRedditBodyBytes = 10 << 20
	maxRedditErrorSize = 1024
	defaultRequestTime = 30 * time.Second
	defaultOAuthURL    = "https://www.reddit.com/api/v1/access_token"
	defaultAPIBaseURL  = "https://oauth.reddit.com"
	redditUserAgent    = "RedditBruneiNewsBot/2.00"
)

var defaultRedditHTTPClient = &http.Client{Timeout: defaultRequestTime}

var ErrAuthentication = errors.New("Reddit authentication failed")

// Submission is the Reddit listing data needed by eligibility filtering and
// article previewing.
type Submission struct {
	ID        string
	Subreddit string
	URL       string
	CreatedAt time.Time
	IsSelf    bool
	Removed   bool
	Deleted   bool
	Locked    bool
	Archived  bool
}

// Listing is one page of Reddit's newest-submission listing.
type Listing struct {
	Submissions []Submission
	After       string
}

// Client loads a page of newest submissions for a subreddit. count is the
// number of listing entries already traversed in the current pagination.
type Client interface {
	NewSubmissions(ctx context.Context, subreddit, after string, count int) (Listing, error)
}

// APIError describes a failed Reddit listing request.
type APIError struct {
	StatusCode int
	RetryAfter time.Duration
	Err        error
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Reddit API returned HTTP %d: %v", e.StatusCode, e.Err)
}

func (e *APIError) Unwrap() error { return e.Err }

// RedditClient performs OAuth-authenticated Reddit listing requests.
type RedditClient struct {
	ClientID     string
	ClientSecret string
	RefreshToken string
	HTTPClient   *http.Client
	OAuthURL     string
	APIBaseURL   string

	mu            sync.Mutex
	accessToken   string
	tokenValidTil time.Time
	now           func() time.Time
}

// NewRedditClient creates a Reddit API client using refresh-token OAuth.
func NewRedditClient(clientID, clientSecret, refreshToken string) *RedditClient {
	return &RedditClient{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RefreshToken: refreshToken,
		OAuthURL:     defaultOAuthURL,
		APIBaseURL:   defaultAPIBaseURL,
		now:          time.Now,
	}
}

// New fetches a page from Reddit's /new listing.
func (c *RedditClient) NewSubmissions(ctx context.Context, subreddit, after string, count int) (Listing, error) {
	baseURL := c.APIBaseURL
	if baseURL == "" {
		baseURL = defaultAPIBaseURL
	}
	endpoint, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return Listing{}, fmt.Errorf("build Reddit listing URL: %w", err)
	}
	basePath := strings.TrimRight(endpoint.Path, "/")
	baseRawPath := strings.TrimRight(endpoint.EscapedPath(), "/")
	endpoint.Path = basePath + "/r/" + subreddit + "/new"
	endpoint.RawPath = baseRawPath + "/r/" + url.PathEscape(subreddit) + "/new"
	query := endpoint.Query()
	query.Set("limit", strconv.Itoa(listingPageSize))
	query.Set("count", strconv.Itoa(count))
	query.Set("raw_json", "1")
	if after != "" {
		query.Set("after", after)
	}
	endpoint.RawQuery = query.Encode()

	target := endpoint.String()
	body, err := c.do(ctx, func(token string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, fmt.Errorf("create Reddit listing request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", redditUserAgent)
		return req, nil
	})
	if err != nil {
		return Listing{}, err
	}
	var response redditListingResponse
	if err := decodeJSON(body, &response); err != nil {
		return Listing{}, fmt.Errorf("decode Reddit listing: %w", err)
	}

	listing := Listing{Submissions: make([]Submission, 0, len(response.Data.Children))}
	if response.Data.After != nil {
		listing.After = *response.Data.After
	}
	for _, child := range response.Data.Children {
		post, err := child.Data.submission()
		if err != nil {
			return Listing{}, fmt.Errorf("decode Reddit submission: %w", err)
		}
		listing.Submissions = append(listing.Submissions, post)
	}
	return listing, nil
}

// PostError describes a failed comment submission.
type PostError struct {
	Retryable  bool
	RetryAfter time.Duration
	Err        error
}

func (e *PostError) Error() string { return fmt.Sprintf("post comment: %v", e.Err) }

func (e *PostError) Unwrap() error { return e.Err }

// permanentCommentErrors are Reddit error codes that retrying cannot fix.
var permanentCommentErrors = map[string]bool{
	"THREAD_LOCKED":            true,
	"DELETED_LINK":             true,
	"DELETED_COMMENT":          true,
	"TOO_OLD":                  true,
	"SUBREDDIT_NOTALLOWED":     true,
	"SUBREDDIT_NOEXIST":        true,
	"USER_BLOCKED":             true,
	"BANNED_FROM_SUBREDDIT":    true,
	"NOT_AUTHOR":               true,
	"SUBREDDIT_OUTBOUND_LINKS": true,
	"TOO_LONG":                 true,
	"NO_TEXT":                  true,
}

// PostComment submits text as a top-level comment on the submission postID.
// Failures are *PostError values classified as retryable or permanent.
func (c *RedditClient) PostComment(ctx context.Context, postID, text string) error {
	baseURL := c.APIBaseURL
	if baseURL == "" {
		baseURL = defaultAPIBaseURL
	}
	target := strings.TrimRight(baseURL, "/") + "/api/comment"
	form := url.Values{
		"api_type": {"json"},
		"thing_id": {"t3_" + postID},
		"text":     {text},
	}

	body, err := c.do(ctx, func(token string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
		if err != nil {
			return nil, fmt.Errorf("create Reddit comment request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("User-Agent", redditUserAgent)
		return req, nil
	})
	if err != nil {
		return classifyPostError(err)
	}

	var response struct {
		JSON struct {
			Errors [][]any `json:"errors"`
		} `json:"json"`
	}
	if err := decodeJSON(body, &response); err != nil {
		// The comment may have been accepted; a retry risks a duplicate, but an
		// undecodable response cannot be trusted as success either.
		return &PostError{Retryable: true, Err: fmt.Errorf("decode Reddit comment response: %w", err)}
	}
	if len(response.JSON.Errors) == 0 {
		return nil
	}
	first := response.JSON.Errors[0]
	code, _ := first[0].(string)
	message := code
	if len(first) > 1 {
		if text, ok := first[1].(string); ok {
			message = code + ": " + text
		}
	}
	return &PostError{
		Retryable: !permanentCommentErrors[code],
		Err:       errors.New("Reddit rejected comment (" + message + ")"),
	}
}

func classifyPostError(err error) error {
	var apiErr *APIError
	switch {
	case errors.As(err, &apiErr):
		retryable := apiErr.StatusCode == http.StatusTooManyRequests ||
			apiErr.StatusCode == http.StatusRequestTimeout ||
			apiErr.StatusCode >= 500
		return &PostError{Retryable: retryable, RetryAfter: apiErr.RetryAfter, Err: err}
	default:
		// Authentication and transport failures may clear up on their own.
		return &PostError{Retryable: true, Err: err}
	}
}

// do sends the request built by build, refreshing the access token once if
// Reddit rejects it. It returns the body of a 200 response.
func (c *RedditClient) do(ctx context.Context, build func(token string) (*http.Request, error)) ([]byte, error) {
	for attempt := 0; attempt < 2; attempt++ {
		token, err := c.token(ctx)
		if err != nil {
			return nil, err
		}
		req, err := build(token)
		if err != nil {
			return nil, err
		}

		resp, err := c.httpClient().Do(req)
		if err != nil {
			return nil, fmt.Errorf("request Reddit: %w", err)
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			_ = resp.Body.Close()
			c.invalidateToken(token)
			continue
		}
		body, readErr := readBody(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read Reddit response: %w", readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close Reddit response: %w", closeErr)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("%w: Reddit rejected the refreshed access token", ErrAuthentication)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, &APIError{
				StatusCode: resp.StatusCode,
				RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), c.clock()),
				Err:        errors.New(responseError(body)),
			}
		}
		return body, nil
	}
	return nil, fmt.Errorf("%w: access token refresh did not authorize request", ErrAuthentication)
}

func (c *RedditClient) token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.clock()
	if c.accessToken != "" && now.Before(c.tokenValidTil) {
		return c.accessToken, nil
	}
	token, expiresIn, err := c.refresh(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrAuthentication, err)
	}
	skew := min(time.Minute, expiresIn/10)
	if skew < 0 {
		skew = 0
	}
	c.accessToken = token
	c.tokenValidTil = now.Add(expiresIn - skew)
	return token, nil
}

func (c *RedditClient) refresh(ctx context.Context) (string, time.Duration, error) {
	oauthURL := c.OAuthURL
	if oauthURL == "" {
		oauthURL = defaultOAuthURL
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {c.RefreshToken},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("create Reddit OAuth request: %w", err)
	}
	req.SetBasicAuth(c.ClientID, c.ClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", redditUserAgent)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("request Reddit OAuth token: %w", err)
	}
	body, readErr := readBody(resp.Body)
	closeErr := resp.Body.Close()
	if readErr != nil {
		return "", 0, fmt.Errorf("read Reddit OAuth response: %w", readErr)
	}
	if closeErr != nil {
		return "", 0, fmt.Errorf("close Reddit OAuth response: %w", closeErr)
	}
	if resp.StatusCode != http.StatusOK {
		// Reddit's OAuth error code (e.g. invalid_grant) is safe to log and
		// distinguishes a bad refresh token from other failures.
		var oauthErr struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &oauthErr) == nil && oauthErr.Error != "" {
			return "", 0, fmt.Errorf("Reddit OAuth returned HTTP %s (%s)", resp.Status, oauthErr.Error)
		}
		return "", 0, fmt.Errorf("Reddit OAuth returned HTTP %s", resp.Status)
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := decodeJSON(body, &result); err != nil {
		return "", 0, fmt.Errorf("decode Reddit OAuth response: %w", err)
	}
	if result.AccessToken == "" || result.ExpiresIn <= 0 {
		return "", 0, errors.New("Reddit OAuth response has no usable access token")
	}
	return result.AccessToken, time.Duration(result.ExpiresIn) * time.Second, nil
}

func (c *RedditClient) invalidateToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.accessToken == token {
		c.tokenValidTil = time.Time{}
	}
}

func (c *RedditClient) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return defaultRedditHTTPClient
}

func (c *RedditClient) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func readBody(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxRedditBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxRedditBodyBytes {
		return nil, errors.New("Reddit response exceeds size limit")
	}
	return data, nil
}

func decodeJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func responseError(body []byte) string {
	message := strings.TrimSpace(string(body))
	if message == "" {
		return "no response details"
	}
	if len(message) > maxRedditErrorSize {
		message = message[:maxRedditErrorSize] + "..."
	}
	return message
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		return max(retryAt.Sub(now), 0)
	}
	return 0
}

type redditListingResponse struct {
	Data struct {
		After    *string `json:"after"`
		Children []struct {
			Data redditSubmission `json:"data"`
		} `json:"children"`
	} `json:"data"`
}

type redditSubmission struct {
	ID                string      `json:"id"`
	Subreddit         string      `json:"subreddit"`
	URL               string      `json:"url"`
	CreatedUTC        json.Number `json:"created_utc"`
	IsSelf            bool        `json:"is_self"`
	Author            string      `json:"author"`
	RemovedByCategory string      `json:"removed_by_category"`
	Locked            bool        `json:"locked"`
	Archived          bool        `json:"archived"`
}

func (s redditSubmission) submission() (Submission, error) {
	createdSeconds, err := strconv.ParseFloat(s.CreatedUTC.String(), 64)
	if err != nil {
		return Submission{}, fmt.Errorf("parse created_utc: %w", err)
	}
	seconds, fraction := math.Modf(createdSeconds)
	createdAt := time.Unix(int64(seconds), int64(fraction*float64(time.Second))).UTC()
	author := strings.TrimSpace(s.Author)
	return Submission{
		ID:        s.ID,
		Subreddit: s.Subreddit,
		URL:       s.URL,
		CreatedAt: createdAt,
		IsSelf:    s.IsSelf,
		Removed:   strings.TrimSpace(s.RemovedByCategory) != "",
		Deleted:   author == "" || strings.EqualFold(author, "[deleted]"),
		Locked:    s.Locked,
		Archived:  s.Archived,
	}, nil
}
