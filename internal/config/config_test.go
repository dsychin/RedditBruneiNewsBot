package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadUsesSafeDefaultsAndParsesSubreddits(t *testing.T) {
	cfg, err := load(validValues(map[string]string{
		"SUBREDDITS": " testingground4bots, brunei_news ",
	}))
	require.NoError(t, err)

	assert.Equal(t, time.Minute, cfg.RetryBaseDelay)
	assert.Equal(t, 8*time.Hour, cfg.RetryWindow)
	assert.False(t, cfg.DryRun)
	assert.Equal(t, defaultSidecarURL, cfg.SidecarURL)
	assert.Equal(t, defaultDBPath, cfg.DBPath)
	assert.Equal(t, []string{"testingground4bots", "brunei_news"}, cfg.Subreddits)
}

func TestReadValuesUsesDefaultsWhenOptionalFileIsMissing(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("REDDIT_CLIENT_ID", "test-id")
	t.Setenv("REDDIT_CLIENT_SECRET", "test-secret")
	t.Setenv("REDDIT_REFRESH_TOKEN", "test-token")

	values, err := readValues(filepath.Join(t.TempDir(), "missing.env"))
	require.NoError(t, err)
	cfg, err := load(values.GetString)
	require.NoError(t, err)

	assert.False(t, cfg.DryRun)
	assert.Equal(t, defaultRetryBaseDelay, cfg.RetryBaseDelay)
	assert.Equal(t, defaultRetryWindow, cfg.RetryWindow)
	assert.Equal(t, []string{defaultSubreddits}, cfg.Subreddits)
	assert.Equal(t, defaultSidecarURL, cfg.SidecarURL)
	assert.Equal(t, defaultDBPath, cfg.DBPath)
}

func TestLoadValidatesRequiredValuesAndFormats(t *testing.T) {
	_, err := load(func(string) string { return "" })
	require.Error(t, err)

	assert.ErrorContains(t, err, "REDDIT_CLIENT_ID is required")
	assert.ErrorContains(t, err, "REDDIT_CLIENT_SECRET is required")
	assert.ErrorContains(t, err, "REDDIT_REFRESH_TOKEN is required")
	assert.ErrorContains(t, err, "SUBREDDITS must contain at least one subreddit")
}

func TestLoadRejectsInvalidSettings(t *testing.T) {
	_, err := load(validValues(map[string]string{
		"DRY_RUN":                  "sometimes",
		"RETRY_BASE_DELAY":         "soon",
		"RETRY_WINDOW":             "30s",
		"CF_CLEARANCE_SCRAPER_URL": "file:///tmp/scraper",
		"SUBREDDITS":               "brunei_news, Brunei_News",
	}))
	require.Error(t, err)

	assert.ErrorContains(t, err, "DRY_RUN must be a boolean")
	assert.ErrorContains(t, err, "RETRY_BASE_DELAY must be a positive duration")
	assert.ErrorContains(t, err, "RETRY_WINDOW must be greater than or equal to RETRY_BASE_DELAY")
	assert.ErrorContains(t, err, "CF_CLEARANCE_SCRAPER_URL must be an absolute HTTP or HTTPS URL")
	assert.ErrorContains(t, err, "listed more than once")
}

func TestLoadAcceptsConfiguredValues(t *testing.T) {
	cfg, err := load(validValues(map[string]string{
		"DRY_RUN":                  "true",
		"RETRY_BASE_DELAY":         "2m",
		"RETRY_WINDOW":             "12h",
		"CF_CLEARANCE_SCRAPER_URL": "http://localhost:3000/cf-clearance-scraper",
		"DB_PATH":                  "/var/lib/reddit-bot/state.db",
		"SUBREDDITS":               "testingground4bots",
	}))
	require.NoError(t, err)

	assert.Equal(t, 2*time.Minute, cfg.RetryBaseDelay)
	assert.Equal(t, 12*time.Hour, cfg.RetryWindow)
	assert.True(t, cfg.DryRun)
	assert.Equal(t, "http://localhost:3000/cf-clearance-scraper", cfg.SidecarURL)
	assert.Equal(t, "/var/lib/reddit-bot/state.db", cfg.DBPath)
}

func TestLoadReadsOptionalEnvFileAndProcessEnvironmentOverridesIt(t *testing.T) {
	clearConfigEnvironment(t)
	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(path, []byte("REDDIT_CLIENT_ID=file-id\nREDDIT_CLIENT_SECRET=file-secret\nREDDIT_REFRESH_TOKEN=file-token\nSUBREDDITS=file_subreddit\n"), 0o600))
	t.Setenv("REDDIT_CLIENT_ID", "environment-id")
	t.Setenv("SUBREDDITS", "testingground4bots")

	values, err := readValues(path)
	require.NoError(t, err)
	cfg, err := load(values.GetString)
	require.NoError(t, err)

	assert.Equal(t, "environment-id", cfg.RedditClientID)
	assert.Equal(t, "file-secret", cfg.RedditClientSecret)
	assert.Equal(t, "file-token", cfg.RedditRefreshToken)
	assert.Equal(t, []string{"testingground4bots"}, cfg.Subreddits)
}

func TestReadValuesAllowsMissingFileAndRejectsMalformedFile(t *testing.T) {
	_, err := readValues(filepath.Join(t.TempDir(), "missing.env"))
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(path, []byte("=malformed\n"), 0o600))
	_, err = readValues(path)
	assert.Error(t, err)
}

func clearConfigEnvironment(t *testing.T) {
	t.Helper()
	keys := []string{
		"REDDIT_CLIENT_ID",
		"REDDIT_CLIENT_SECRET",
		"REDDIT_REFRESH_TOKEN",
		"SUBREDDITS",
		"DRY_RUN",
		"RETRY_BASE_DELAY",
		"RETRY_WINDOW",
		"CF_CLEARANCE_SCRAPER_URL",
		"DB_PATH",
	}
	for _, key := range keys {
		value, exists := os.LookupEnv(key)
		require.NoError(t, os.Unsetenv(key))
		t.Cleanup(func() {
			if exists {
				_ = os.Setenv(key, value)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
}

func validValues(overrides map[string]string) stringGetter {
	values := map[string]string{
		"REDDIT_CLIENT_ID":         "client-id",
		"REDDIT_CLIENT_SECRET":     "client-secret",
		"REDDIT_REFRESH_TOKEN":     "refresh-token",
		"SUBREDDITS":               defaultSubreddits,
		"DRY_RUN":                  "false",
		"RETRY_BASE_DELAY":         defaultRetryBaseDelay.String(),
		"RETRY_WINDOW":             defaultRetryWindow.String(),
		"CF_CLEARANCE_SCRAPER_URL": defaultSidecarURL,
		"DB_PATH":                  defaultDBPath,
	}
	for key, value := range overrides {
		values[key] = value
	}
	return func(key string) string {
		return values[key]
	}
}
