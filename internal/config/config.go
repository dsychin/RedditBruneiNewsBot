package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

const (
	defaultMode           = "test"
	defaultSubreddits     = "testingground4bots"
	defaultRetryBaseDelay = time.Minute
	defaultRetryWindow    = 8 * time.Hour
	defaultSidecarURL     = "http://cf-clearance-scraper:3000/cf-clearance-scraper"
	defaultDBPath         = "./data/bot.db"
)

// Config contains the runtime settings required to initialize the bot.
type Config struct {
	RedditClientID     string
	RedditClientSecret string
	RedditRefreshToken string
	Subreddits         []string
	Mode               string
	DryRun             bool
	RetryBaseDelay     time.Duration
	RetryWindow        time.Duration
	SidecarURL         string
	DBPath             string
}

// Load reads and validates configuration from an optional .env file and the
// process environment. Process environment values take precedence.
func Load() (Config, error) {
	values, err := readValues(".env")
	if err != nil {
		return Config{}, err
	}
	return load(values.GetString)
}

func readValues(path string) (*viper.Viper, error) {
	values := viper.NewWithOptions(
		viper.EnvKeyReplacer(strings.NewReplacer(".", "_")),
	)
	values.AllowEmptyEnv(true)
	values.SetConfigFile(path)
	values.SetConfigType("env")
	values.AutomaticEnv()
	values.SetDefault("BOT_MODE", defaultMode)
	values.SetDefault("SUBREDDITS", defaultSubreddits)
	values.SetDefault("DRY_RUN", false)
	values.SetDefault("RETRY_BASE_DELAY", defaultRetryBaseDelay.String())
	values.SetDefault("RETRY_WINDOW", defaultRetryWindow.String())
	values.SetDefault("CF_CLEARANCE_SCRAPER_URL", defaultSidecarURL)
	values.SetDefault("DB_PATH", defaultDBPath)

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return values, nil
		}
		return nil, fmt.Errorf("inspect config file %q: %w", path, err)
	}
	if err := values.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config file %q: %w", path, err)
	}
	return values, nil
}

func load(get stringGetter) (Config, error) {
	cfg := Config{
		RedditClientID:     strings.TrimSpace(get("REDDIT_CLIENT_ID")),
		RedditClientSecret: strings.TrimSpace(get("REDDIT_CLIENT_SECRET")),
		RedditRefreshToken: strings.TrimSpace(get("REDDIT_REFRESH_TOKEN")),
		Mode:               strings.ToLower(strings.TrimSpace(get("BOT_MODE"))),
		SidecarURL:         strings.TrimSpace(get("CF_CLEARANCE_SCRAPER_URL")),
		DBPath:             strings.TrimSpace(get("DB_PATH")),
	}
	var errs []string

	if cfg.RedditClientID == "" {
		errs = append(errs, "REDDIT_CLIENT_ID is required")
	}
	if cfg.RedditClientSecret == "" {
		errs = append(errs, "REDDIT_CLIENT_SECRET is required")
	}
	if cfg.RedditRefreshToken == "" {
		errs = append(errs, "REDDIT_REFRESH_TOKEN is required")
	}

	cfg.Subreddits, errs = parseSubreddits(get("SUBREDDITS"), errs)

	if cfg.Mode == "" {
		cfg.Mode = defaultMode
	}
	if cfg.Mode != "test" && cfg.Mode != "production" {
		errs = append(errs, "BOT_MODE must be either \"test\" or \"production\"")
	}

	dryRun := strings.TrimSpace(get("DRY_RUN"))
	if dryRun != "" {
		value, err := strconv.ParseBool(dryRun)
		if err != nil {
			errs = append(errs, "DRY_RUN must be a boolean (true or false)")
		} else {
			cfg.DryRun = value
		}
	}

	cfg.RetryBaseDelay = defaultRetryBaseDelay
	if value := strings.TrimSpace(get("RETRY_BASE_DELAY")); value != "" {
		delay, err := time.ParseDuration(value)
		if err != nil || delay <= 0 {
			errs = append(errs, "RETRY_BASE_DELAY must be a positive duration (for example, \"1m\")")
		} else {
			cfg.RetryBaseDelay = delay
		}
	}

	cfg.RetryWindow = defaultRetryWindow
	if value := strings.TrimSpace(get("RETRY_WINDOW")); value != "" {
		window, err := time.ParseDuration(value)
		if err != nil || window <= 0 {
			errs = append(errs, "RETRY_WINDOW must be a positive duration (for example, \"8h\")")
		} else {
			cfg.RetryWindow = window
		}
	}
	if cfg.RetryWindow < cfg.RetryBaseDelay {
		errs = append(errs, "RETRY_WINDOW must be greater than or equal to RETRY_BASE_DELAY")
	}

	if cfg.SidecarURL == "" {
		cfg.SidecarURL = defaultSidecarURL
	}
	parsedSidecarURL, err := url.ParseRequestURI(cfg.SidecarURL)
	if err != nil || parsedSidecarURL.Host == "" || (parsedSidecarURL.Scheme != "http" && parsedSidecarURL.Scheme != "https") {
		errs = append(errs, "CF_CLEARANCE_SCRAPER_URL must be an absolute HTTP or HTTPS URL")
	}

	if cfg.DBPath == "" {
		cfg.DBPath = defaultDBPath
	}
	if len(errs) != 0 {
		return Config{}, fmt.Errorf("configuration errors: %s", strings.Join(errs, "; "))
	}
	return cfg, nil
}

type stringGetter func(string) string

func parseSubreddits(value string, errs []string) ([]string, []string) {
	if strings.TrimSpace(value) == "" {
		return nil, append(errs, "SUBREDDITS must contain at least one subreddit")
	}

	parts := strings.Split(value, ",")
	subreddits := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			errs = append(errs, "SUBREDDITS must not contain empty entries")
			continue
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			errs = append(errs, strconv.Quote(name)+" is listed more than once in SUBREDDITS")
			continue
		}
		seen[key] = struct{}{}
		subreddits = append(subreddits, name)
	}
	return subreddits, errs
}
