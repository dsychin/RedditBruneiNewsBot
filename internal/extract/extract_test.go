package extract

import (
	"net/url"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fixtures are synthetic pages shaped like each supported site, not real captures.
func TestReplyFromSupportedSiteFixtures(t *testing.T) {
	tests := []struct {
		file, pageURL, title, firstWords string
	}{
		{"borneobulletin.html", "https://borneobulletin.com.bn/national-day", "Brunei marks National Day with parade", "BANDAR SERI BEGAWAN"},
		{"borneopost.html", "https://www.theborneopost.com/trade-summit", "Brunei to host regional trade summit", "Brunei will host"},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			f, err := os.Open("testdata/" + tt.file)
			require.NoError(t, err)
			defer f.Close()
			u, err := url.Parse(tt.pageURL)
			require.NoError(t, err)

			reply, err := Reply(f, u, "v2.00")
			require.NoError(t, err)

			assert.True(t, strings.HasPrefix(reply, "**"+tt.title+"**\n\n"+tt.firstWords), reply)
			assert.True(t, strings.HasSuffix(reply, "\n\n***\n\n^(v2.00)"), reply)
			assert.NotContains(t, reply, "Truncated")
			assert.NotContains(t, reply, "Copyright")
		})
	}
}

func TestReplySkipsMissingTitleAndShortText(t *testing.T) {
	u, _ := url.Parse("https://example.com/a")

	_, err := Reply(strings.NewReader("<html><body><p>"+strings.Repeat("word ", 80)+"</p></body></html>"), u, "v2.00")
	assert.ErrorIs(t, err, ErrNoTitle)

	_, err = Reply(strings.NewReader("<html><head><title>Hi</title></head><body><p>Too short.</p></body></html>"), u, "v2.00")
	assert.ErrorIs(t, err, ErrTextTooShort)
}

func TestReplyTextLengthBoundary(t *testing.T) {
	u, _ := url.Parse("https://example.com/a")
	page := func(n int) string {
		return "<html><head><title>Story</title></head><body><p>" + strings.Repeat("a", n) + "</p></body></html>"
	}

	_, err := Reply(strings.NewReader(page(MinTextRunes-1)), u, "v2.00")
	assert.ErrorIs(t, err, ErrTextTooShort)

	_, err = Reply(strings.NewReader(page(MinTextRunes)), u, "v2.00")
	assert.NoError(t, err)
}

func TestReplyStaysWithinRedditLimitWithLongTitleAndURL(t *testing.T) {
	u, err := url.Parse("https://example.com/" + strings.Repeat("x", 1500))
	require.NoError(t, err)
	title := strings.Repeat("T", 400)
	para := "<p>" + strings.Repeat("This is a sentence. ", 40) + "</p>"
	page := "<html><head><title>" + title + "</title></head><body><article>" + strings.Repeat(para, 20) + "</article></body></html>"

	reply, err := Reply(strings.NewReader(page), u, "v2.00")
	require.NoError(t, err)

	assert.LessOrEqual(t, utf8.RuneCountInString(reply), MaxReplyRunes)
	assert.Contains(t, reply, "[Truncated - read the full article at "+u.String()+"]")
}

func TestReplyTruncatesLongArticleWithURLMarker(t *testing.T) {
	u, _ := url.Parse("https://example.com/long")
	para := "<p>" + strings.Repeat("This is a sentence. ", 40) + "</p>"
	page := "<html><head><title>Long story</title></head><body><article>" + strings.Repeat(para, 20) + "</article></body></html>"

	reply, err := Reply(strings.NewReader(page), u, "v2.00")
	require.NoError(t, err)

	assert.Contains(t, reply, "\n\n[Truncated - read the full article at https://example.com/long]\n\n***\n\n^(v2.00)")
	assert.LessOrEqual(t, utf8.RuneCountInString(reply), MaxReplyRunes)
}

func TestCleanTitle(t *testing.T) {
	tests := map[string]string{
		"Story | Borneo Bulletin Online":    "Story",
		"Story - Borneo Post Online":        "Story",
		"Story – The Borneo Post":           "Story",
		"  Story \n  | Borneo Bulletin  ":   "Story",
		"Story » Borneo Bulletin Online":    "Story",
		"Story - Some Other Site":           "Story - Some Other Site",
		"Borneo Post Online":                "Borneo Post Online",
		"Why Borneo Post Online is popular": "Why Borneo Post Online is popular",
		"":                                  "",
	}
	for in, want := range tests {
		assert.Equal(t, want, CleanTitle(in), in)
	}
}

func TestTruncate(t *testing.T) {
	t.Run("under cap is unchanged", func(t *testing.T) {
		got, truncated := Truncate("Short.", 10)
		assert.Equal(t, "Short.", got)
		assert.False(t, truncated)
	})
	t.Run("exactly at cap is unchanged", func(t *testing.T) {
		got, truncated := Truncate("Ten chars.", 10)
		assert.Equal(t, "Ten chars.", got)
		assert.False(t, truncated)
	})
	t.Run("cuts at last sentence boundary", func(t *testing.T) {
		got, truncated := Truncate("One. Two. Three is long", 15)
		assert.Equal(t, "One. Two.", got)
		assert.True(t, truncated)
	})
	t.Run("sentence ending exactly at cap", func(t *testing.T) {
		got, truncated := Truncate("One. Two. Three", 9)
		assert.Equal(t, "One. Two.", got)
		assert.True(t, truncated)
	})
	t.Run("no sentence boundary hard cuts", func(t *testing.T) {
		got, truncated := Truncate("abcdefghij klmnop", 5)
		assert.Equal(t, "abcde", got)
		assert.True(t, truncated)
	})
	t.Run("decimal point is not a boundary", func(t *testing.T) {
		got, _ := Truncate("Price is 3.5 million dollars", 14)
		assert.Equal(t, "Price is 3.5 m", got)
	})
	t.Run("counts runes not bytes", func(t *testing.T) {
		got, truncated := Truncate("日本語です。日本語です", 8)
		assert.Equal(t, "日本語です。", got)
		assert.True(t, truncated)

		got, _ = Truncate("éééééééééé", 4)
		assert.Equal(t, "éééé", got)
		assert.True(t, utf8.ValidString(got))
	})
}

func TestReplyOmitsImageCaptions(t *testing.T) {
	paragraph := strings.Repeat("The department will continue monitoring air quality nationwide. ", 5)
	page := "<html><head><title>Haze update</title></head><body><article><h1>Haze update</h1>" +
		"<p>" + paragraph + "</p>" +
		"<figure><img src=\"a.jpg\"><figcaption>FILE – The haze in September. PHOTO: SOMEONE</figcaption></figure>" +
		"</article></body></html>"
	u, err := url.Parse("https://example.com/haze")
	require.NoError(t, err)

	reply, err := Reply(strings.NewReader(page), u, "v2.00")
	require.NoError(t, err)
	assert.Contains(t, reply, "air quality nationwide")
	assert.NotContains(t, reply, "PHOTO")
	assert.NotContains(t, reply, "FILE –")
}
