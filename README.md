# Reddit Brunei News Bot

![GitHub release (latest by date)](https://img.shields.io/github/v/release/dsychin/RedditBruneiNewsBot)
![GitHub code size in bytes](https://img.shields.io/github/languages/code-size/dsychin/RedditBruneiNewsBot)
![GitHub issues](https://img.shields.io/github/issues-raw/dsychin/RedditBruneiNewsBot)
![GitHub last commit](https://img.shields.io/github/last-commit/dsychin/RedditBruneiNewsBot)

The Reddit bot monitors configured subreddits for news links, extracts article text, and replies with a formatted summary. The Go rewrite is in progress; this scaffold currently validates configuration, logs safe operational settings, and exits. Reddit intake, persistence, fetching, extraction, and deployment are implemented in subsequent steps.

## Requirements

- Go 1.27 or newer
- Reddit OAuth app credentials for the bot account

## Configuration

Copy `.env.example` to `.env` and provide the Reddit OAuth client ID, client secret, and refresh token. The program optionally reads `.env` from its working directory; process environment variables override file values. Do not commit `.env` or real credentials.

| Variable | Default | Description |
| --- | --- | --- |
| `REDDIT_CLIENT_ID` | — | Reddit OAuth application client ID |
| `REDDIT_CLIENT_SECRET` | — | Reddit OAuth application client secret |
| `REDDIT_REFRESH_TOKEN` | — | Refresh token for the bot account |
| `SUBREDDITS` | `testingground4bots` | Comma-separated subreddit names |
| `DRY_RUN` | `false` | Whether to preview work without posting or persisting state |
| `RETRY_BASE_DELAY` | `1m` | Base retry delay, in Go duration syntax |
| `RETRY_WINDOW` | `8h` | Maximum retry window, in Go duration syntax |
| `CF_CLEARANCE_SCRAPER_URL` | `http://cf-clearance-scraper:3000/cf-clearance-scraper` | Sidecar source endpoint |
| `DB_PATH` | `./data/bot.db` | SQLite database file path |

The configured subreddit list determines where the bot posts; use `testingground4bots` while validating the bot.

## Run

```sh
go run .
```

The scaffold validates settings and emits structured JSON logs to stdout without logging credentials. It exits after initialization until the bot's worker loop is wired in. Invalid settings produce a structured error log and a non-zero exit status.

## Development

```sh
go test ./...
```
