package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadUsesSafeDefaultsAndParsesSubreddits(t *testing.T) {
	cfg, err := load(validValues(map[string]string{
		"SUBREDDITS": " testingground4bots, brunei_news ",
	}))
	if err != nil {
		t.Fatalf("load returned an error: %v", err)
	}

	if cfg.RetryBaseDelay != time.Minute {
		t.Errorf("RetryBaseDelay = %s, want 1m", cfg.RetryBaseDelay)
	}
	if cfg.RetryWindow != 8*time.Hour {
		t.Errorf("RetryWindow = %s, want 8h", cfg.RetryWindow)
	}
	if cfg.DryRun {
		t.Error("DryRun = true, want false")
	}
	if cfg.SidecarURL != defaultSidecarURL {
		t.Errorf("SidecarURL = %q, want %q", cfg.SidecarURL, defaultSidecarURL)
	}
	if cfg.DBPath != defaultDBPath {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, defaultDBPath)
	}
	if len(cfg.Subreddits) != 2 || cfg.Subreddits[0] != "testingground4bots" || cfg.Subreddits[1] != "brunei_news" {
		t.Errorf("Subreddits = %#v, want [testingground4bots brunei_news]", cfg.Subreddits)
	}
}

func TestReadValuesUsesDefaultsWhenOptionalFileIsMissing(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("REDDIT_CLIENT_ID", "test-id")
	t.Setenv("REDDIT_CLIENT_SECRET", "test-secret")
	t.Setenv("REDDIT_REFRESH_TOKEN", "test-token")

	values, err := readValues(filepath.Join(t.TempDir(), "missing.env"))
	if err != nil {
		t.Fatalf("readValues returned an error: %v", err)
	}
	cfg, err := load(values.GetString)
	if err != nil {
		t.Fatalf("load returned an error: %v", err)
	}

	if cfg.DryRun || cfg.RetryBaseDelay != defaultRetryBaseDelay || cfg.RetryWindow != defaultRetryWindow {
		t.Errorf("unexpected defaults: dry_run=%t retry_base_delay=%s retry_window=%s", cfg.DryRun, cfg.RetryBaseDelay, cfg.RetryWindow)
	}
	if len(cfg.Subreddits) != 1 || cfg.Subreddits[0] != defaultSubreddits {
		t.Errorf("Subreddits = %#v, want [%s]", cfg.Subreddits, defaultSubreddits)
	}
	if cfg.SidecarURL != defaultSidecarURL || cfg.DBPath != defaultDBPath {
		t.Errorf("unexpected endpoint or DB defaults: sidecar=%q db=%q", cfg.SidecarURL, cfg.DBPath)
	}
}

func TestLoadValidatesRequiredValuesAndFormats(t *testing.T) {
	_, err := load(func(string) string { return "" })
	if err == nil {
		t.Fatal("load succeeded with missing required configuration")
	}

	for _, want := range []string{
		"REDDIT_CLIENT_ID is required",
		"REDDIT_CLIENT_SECRET is required",
		"REDDIT_REFRESH_TOKEN is required",
		"SUBREDDITS must contain at least one subreddit",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestLoadRejectsInvalidSettings(t *testing.T) {
	_, err := load(validValues(map[string]string{
		"DRY_RUN":                  "sometimes",
		"RETRY_BASE_DELAY":         "soon",
		"RETRY_WINDOW":             "30s",
		"CF_CLEARANCE_SCRAPER_URL": "file:///tmp/scraper",
		"SUBREDDITS":               "brunei_news, Brunei_News",
	}))
	if err == nil {
		t.Fatal("load succeeded with invalid configuration")
	}

	for _, want := range []string{
		"DRY_RUN must be a boolean",
		"RETRY_BASE_DELAY must be a positive duration",
		"RETRY_WINDOW must be greater than or equal to RETRY_BASE_DELAY",
		"CF_CLEARANCE_SCRAPER_URL must be an absolute HTTP or HTTPS URL",
		"listed more than once",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
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
	if err != nil {
		t.Fatalf("load returned an error: %v", err)
	}

	if cfg.RetryBaseDelay != 2*time.Minute || cfg.RetryWindow != 12*time.Hour {
		t.Errorf("unexpected config values: retry_base_delay=%s retry_window=%s", cfg.RetryBaseDelay, cfg.RetryWindow)
	}
	if !cfg.DryRun {
		t.Error("DryRun = false, want true")
	}
	if cfg.SidecarURL != "http://localhost:3000/cf-clearance-scraper" || cfg.DBPath != "/var/lib/reddit-bot/state.db" {
		t.Errorf("unexpected endpoint or DB path: sidecar=%q db=%q", cfg.SidecarURL, cfg.DBPath)
	}
}

func TestLoadReadsOptionalEnvFileAndProcessEnvironmentOverridesIt(t *testing.T) {
	clearConfigEnvironment(t)
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("REDDIT_CLIENT_ID=file-id\nREDDIT_CLIENT_SECRET=file-secret\nREDDIT_REFRESH_TOKEN=file-token\nSUBREDDITS=file_subreddit\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	t.Setenv("REDDIT_CLIENT_ID", "environment-id")
	t.Setenv("SUBREDDITS", "testingground4bots")

	values, err := readValues(path)
	if err != nil {
		t.Fatalf("readValues returned an error: %v", err)
	}
	cfg, err := load(values.GetString)
	if err != nil {
		t.Fatalf("load returned an error: %v", err)
	}

	if cfg.RedditClientID != "environment-id" {
		t.Errorf("RedditClientID = %q, want environment value", cfg.RedditClientID)
	}
	if cfg.RedditClientSecret != "file-secret" || cfg.RedditRefreshToken != "file-token" {
		t.Errorf("file credentials were not loaded: secret=%q refresh token=%q", cfg.RedditClientSecret, cfg.RedditRefreshToken)
	}
	if len(cfg.Subreddits) != 1 || cfg.Subreddits[0] != "testingground4bots" {
		t.Errorf("Subreddits = %#v, want environment override", cfg.Subreddits)
	}
}

func TestReadValuesAllowsMissingFileAndRejectsMalformedFile(t *testing.T) {
	if _, err := readValues(filepath.Join(t.TempDir(), "missing.env")); err != nil {
		t.Fatalf("readValues rejected a missing optional file: %v", err)
	}

	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("=malformed\n"), 0o600); err != nil {
		t.Fatalf("write malformed .env: %v", err)
	}
	if _, err := readValues(path); err == nil {
		t.Fatal("readValues accepted a malformed .env file")
	}
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
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
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
