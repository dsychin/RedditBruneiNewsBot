# Reddit Brunei News Bot

![GitHub release (latest by date)](https://img.shields.io/github/v/release/dsychin/RedditBruneiNewsBot)
![GitHub code size in bytes](https://img.shields.io/github/languages/code-size/dsychin/RedditBruneiNewsBot)
![GitHub issues](https://img.shields.io/github/issues-raw/dsychin/RedditBruneiNewsBot)
![GitHub last commit](https://img.shields.io/github/last-commit/dsychin/RedditBruneiNewsBot)

The Reddit bot monitors configured subreddits for news links, extracts article text, and replies with a formatted summary. The Go rewrite is in progress. Submission ingestion scans recent posts, filters ineligible submissions, and supports read-only dry-run previews. Normal-mode processing and reply posting are not available until the later posting/retry step.

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
| `DRY_RUN` | `false` | Set to `true` to preview eligible submissions without posting or writing SQLite state |
| `RETRY_BASE_DELAY` | `1m` | Base retry delay, in Go duration syntax |
| `RETRY_WINDOW` | `8h` | Maximum retry window, in Go duration syntax |
| `CF_CLEARANCE_SCRAPER_URL` | `http://cf-clearance-scraper:3000/cf-clearance-scraper` | Sidecar source endpoint |
| `DB_PATH` | `./data/bot.db` | SQLite database file path |

The configured subreddit list determines the bot's targets; use `testingground4bots` while validating. There is no separate test-mode setting.

## Run

```sh
go run .
```

Set `DRY_RUN=true` to start submission ingestion. The bot scans immediately and then on 60-second wall-clock ticks, reading handled-post state from SQLite in read-only mode when the database exists. A missing database is treated as empty; an unreadable database is an error. Dry-run fetches and extracts eligible articles, logs each successful reply preview once per process, and does not post or write SQLite state.

Each scan paginates newest submissions until it reaches the strict 24-hour cutoff; exactly 24-hour-old posts are excluded. Only link posts that are not removed/deleted, locked, archived, or already replied/skipped in SQLite are previewed. Subreddit scans are sequential; partial results are processed when a later page fails, and rate limits defer only the affected subreddit.

With `DRY_RUN=false`, the program exits with an explicit error until reply posting and retry handling are implemented. Invalid settings or state-store failures produce a structured error log and a non-zero exit status.

## Development

```sh
go test ./...
```
