// Package fetch downloads article HTML, using a Cloudflare-solving sidecar when needed.
package fetch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/dsychin/RedditBruneiNewsBot/internal/sites"
)

const (
	DefaultGetTimeout     = 30 * time.Second
	DefaultSidecarTimeout = 90 * time.Second
	DefaultMaxBodyBytes   = 10 << 20
	maxRedirects          = 5
	browserUserAgent      = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
)

var errTooManyRedirects = errors.New("stopped after 5 redirects")

// Page is a fetched article.
type Page struct {
	Body []byte
	// URL is the final URL after redirects, to use as the base for parsing.
	URL *url.URL
}

// Fetcher downloads pages directly or through the cf-clearance-scraper sidecar.
// The zero value is not usable; set SidecarURL at least.
type Fetcher struct {
	SidecarURL     string
	GetTimeout     time.Duration
	SidecarTimeout time.Duration
	MaxBodyBytes   int64
	// CloudflareHosts defaults to sites.CloudflareHosts.
	CloudflareHosts map[string]bool
	// Client is used for plain GETs and the sidecar. Defaults to a new client.
	Client *http.Client
}

// Fetch returns the HTML of rawURL. Failures are *Error values.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (Page, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Page{}, permanent(StageFetch, fmt.Errorf("invalid article URL %q", rawURL))
	}

	if f.hosts()[u.Hostname()] {
		return f.viaSidecar(ctx, u)
	}

	page, challenged, err := f.plainGet(ctx, u)
	if challenged {
		return f.viaSidecar(ctx, u)
	}
	return page, err
}

func (f *Fetcher) hosts() map[string]bool {
	if f.CloudflareHosts != nil {
		return f.CloudflareHosts
	}
	return sites.CloudflareHosts
}

func (f *Fetcher) maxBody() int64 {
	if f.MaxBodyBytes > 0 {
		return f.MaxBodyBytes
	}
	return DefaultMaxBodyBytes
}

func timeoutOr(d, fallback time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return fallback
}

func (f *Fetcher) plainGet(ctx context.Context, u *url.URL) (page Page, challenged bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, timeoutOr(f.GetTimeout, DefaultGetTimeout))
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Page{}, false, permanent(StageFetch, err)
	}
	req.Header.Set("User-Agent", browserUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.5")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	client := f.httpClient()
	client.CheckRedirect = func(_ *http.Request, via []*http.Request) error {
		if len(via) > maxRedirects {
			return errTooManyRedirects
		}
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, errTooManyRedirects) {
			return Page{}, false, permanent(StageFetch, err)
		}
		return Page{}, false, retryable(StageFetch, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Read a little of the body to recognise a challenge page.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if isChallenge(resp, snippet) {
			return Page{}, true, nil
		}
		return Page{}, false, classifyStatus(resp)
	}

	if !isHTML(resp.Header.Get("Content-Type")) {
		return Page{}, false, permanent(StageFetch, fmt.Errorf("unsupported content type %q", resp.Header.Get("Content-Type")))
	}
	body, err := readCapped(resp.Body, f.maxBody())
	if err != nil {
		return Page{}, false, bodyError(StageFetch, err)
	}
	return Page{Body: body, URL: resp.Request.URL}, false, nil
}

func isChallenge(resp *http.Response, body []byte) bool {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusServiceUnavailable {
		return false
	}
	return strings.EqualFold(resp.Header.Get("cf-mitigated"), "challenge") ||
		bytes.Contains(body, []byte("Just a moment..."))
}

func classifyStatus(resp *http.Response) *Error {
	err := fmt.Errorf("unexpected HTTP status %s", resp.Status)
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		e := retryable(StageFetch, err)
		e.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
		return e
	case resp.StatusCode == http.StatusRequestTimeout, resp.StatusCode >= 500:
		return retryable(StageFetch, err)
	default:
		return permanent(StageFetch, err)
	}
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if secs, err := strconv.Atoi(value); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(value); err == nil {
		return max(t.Sub(now), 0)
	}
	return 0
}

func isHTML(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && (mediaType == "text/html" || mediaType == "application/xhtml+xml")
}

var errBodyTooLarge = errors.New("response body exceeds size limit")

func readCapped(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errBodyTooLarge
	}
	return body, nil
}

func bodyError(stage Stage, err error) *Error {
	if errors.Is(err, errBodyTooLarge) {
		return permanent(stage, err)
	}
	return retryable(stage, err)
}

type sidecarRequest struct {
	URL  string `json:"url"`
	Mode string `json:"mode"`
}

type sidecarResponse struct {
	Source string `json:"source"`
	Code   int    `json:"code"`
}

func (f *Fetcher) viaSidecar(ctx context.Context, u *url.URL) (Page, error) {
	ctx, cancel := context.WithTimeout(ctx, timeoutOr(f.SidecarTimeout, DefaultSidecarTimeout))
	defer cancel()

	payload, err := json.Marshal(sidecarRequest{URL: u.String(), Mode: "source"})
	if err != nil {
		return Page{}, permanent(StageSidecar, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.SidecarURL, bytes.NewReader(payload))
	if err != nil {
		return Page{}, permanent(StageSidecar, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := f.httpClient().Do(req)
	if err != nil {
		return Page{}, retryable(StageSidecar, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Page{}, retryable(StageSidecar, fmt.Errorf("sidecar returned HTTP status %s", resp.Status))
	}
	// Allow for JSON escaping overhead on top of the source size.
	raw, err := readCapped(resp.Body, 2*f.maxBody())
	if err != nil {
		return Page{}, bodyError(StageSidecar, err)
	}
	var out sidecarResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return Page{}, retryable(StageSidecar, fmt.Errorf("decode sidecar response: %w", err))
	}
	if out.Code != http.StatusOK {
		err := fmt.Errorf("sidecar reported page status %d", out.Code)
		if out.Code == http.StatusNotFound || out.Code == http.StatusGone {
			return Page{}, permanent(StageSidecar, err)
		}
		return Page{}, retryable(StageSidecar, err)
	}
	if int64(len(out.Source)) > f.maxBody() {
		return Page{}, permanent(StageSidecar, errBodyTooLarge)
	}
	return Page{Body: []byte(out.Source), URL: u}, nil
}

// httpClient returns a shallow copy so per-request redirect policy does not leak.
func (f *Fetcher) httpClient() *http.Client {
	if f.Client == nil {
		return &http.Client{}
	}
	c := *f.Client
	return &c
}
