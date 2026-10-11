package ingest

import (
	"bytes"
	"context"
	"errors"
	"net/url"

	"github.com/dsychin/RedditBruneiNewsBot/internal/extract"
	"github.com/dsychin/RedditBruneiNewsBot/internal/fetch"
)

// ArticlePreviewer fetches and formats the article for an eligible submission.
type ArticlePreviewer struct {
	Fetcher *fetch.Fetcher
	Version string
}

// Preview returns the exact reply text that would be posted.
func (p *ArticlePreviewer) Preview(ctx context.Context, submission Submission) (string, error) {
	if p.Fetcher == nil {
		return "", errors.New("article fetcher is not configured")
	}
	page, err := p.Fetcher.Fetch(ctx, submission.URL)
	if err != nil {
		return "", err
	}
	if page.URL == nil {
		parsedURL, err := url.Parse(submission.URL)
		if err != nil {
			return "", err
		}
		page.URL = parsedURL
	}
	return extract.Reply(bytes.NewReader(page.Body), page.URL, p.Version)
}
