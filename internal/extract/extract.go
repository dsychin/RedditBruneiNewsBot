// Package extract turns an article's HTML into the text of a Reddit reply.
package extract

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	readability "codeberg.org/readeck/go-readability/v2"

	"github.com/dsychin/RedditBruneiNewsBot/internal/sites"
)

const (
	// MaxBodyRunes is the cap on article text, in runes.
	MaxBodyRunes = 9500
	// MaxReplyRunes is Reddit's comment length limit, in runes.
	MaxReplyRunes = 10000
	// MinTextRunes is the minimum article text length worth replying with.
	MinTextRunes = 200
)

var (
	ErrNoTitle      = errors.New("article has no title")
	ErrTextTooShort = errors.New("article text is too short")
)

var blankLines = regexp.MustCompile(`\n\s*\n+`)

// Reply builds the reply markdown for an article page fetched from pageURL,
// ending with a small footer that shows only the bot version.
// It returns ErrNoTitle or ErrTextTooShort when the article should be skipped.
func Reply(page io.Reader, pageURL *url.URL, version string) (string, error) {
	article, err := readability.FromReader(page, pageURL)
	if err != nil {
		return "", fmt.Errorf("parse article: %w", err)
	}

	title := CleanTitle(article.Title())
	if title == "" {
		return "", ErrNoTitle
	}

	var text bytes.Buffer
	if article.Node != nil {
		if err := article.RenderText(&text); err != nil {
			return "", fmt.Errorf("render article text: %w", err)
		}
	}
	body := normalizeParagraphs(text.String())
	if utf8.RuneCountInString(body) < MinTextRunes {
		return "", ErrTextTooShort
	}

	header := "**" + title + "**\n\n"
	marker := "\n\n[Truncated - read the full article at " + pageURL.String() + "]"
	footer := "\n\n***\n\n^(" + version + ")"

	// Reserve room for the marker so a long title or URL cannot push the reply over Reddit's limit.
	overhead := utf8.RuneCountInString(header + marker + footer)
	bodyCap := min(MaxBodyRunes, MaxReplyRunes-overhead)
	if bodyCap < MinTextRunes {
		return "", ErrTextTooShort
	}

	body, truncated := Truncate(body, bodyCap)
	reply := header + body
	if truncated {
		reply += marker
	}
	return reply + footer, nil
}

// CleanTitle trims whitespace and strips a trailing known site-name suffix.
func CleanTitle(title string) string {
	title = strings.Join(strings.Fields(title), " ")
	for _, site := range sites.TitleSuffixes {
		for _, sep := range sites.TitleSeparators {
			if cut, ok := strings.CutSuffix(title, sep+site); ok {
				return strings.TrimSpace(cut)
			}
		}
	}
	return title
}

func normalizeParagraphs(text string) string {
	paragraphs := blankLines.Split(strings.ReplaceAll(text, "\r\n", "\n"), -1)
	kept := paragraphs[:0]
	for _, p := range paragraphs {
		if p = strings.Join(strings.Fields(p), " "); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n\n")
}

// Truncate limits text to maxRunes, cutting at the last sentence boundary
// within the limit, or at the limit itself when there is none.
func Truncate(text string, maxRunes int) (string, bool) {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text, false
	}
	window := runes[:maxRunes]
	for i := maxRunes - 1; i >= 0; i-- {
		if !isSentenceEnd(window[i], runes, i) {
			continue
		}
		return string(window[:i+1]), true
	}
	return strings.TrimRightFunc(string(window), unicode.IsSpace), true
}

// isSentenceEnd reports whether runes[i] ends a sentence. Latin terminators
// must be followed by whitespace so decimals like "3.5" are not split.
func isSentenceEnd(r rune, runes []rune, i int) bool {
	switch r {
	case '。':
		return true
	case '.', '!', '?':
		return i+1 < len(runes) && unicode.IsSpace(runes[i+1])
	}
	return false
}
