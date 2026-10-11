# Reddit Brunei News Bot

![GitHub release (latest by date)](https://img.shields.io/github/v/release/dsychin/RedditBruneiNewsBot)
![GitHub code size in bytes](https://img.shields.io/github/languages/code-size/dsychin/RedditBruneiNewsBot)
![GitHub issues](https://img.shields.io/github/issues-raw/dsychin/RedditBruneiNewsBot)
![GitHub last commit](https://img.shields.io/github/last-commit/dsychin/RedditBruneiNewsBot)

The Reddit bot monitors configured subreddits for news links, extracts article text, and replies with a formatted summary. The Go rewrite is in progress. Submission ingestion scans recent posts, filters ineligible submissions, posts replies, and retries transient failures. A read-only dry-run mode previews replies without posting.

## Requirements

- Go 1.27 or newer (or Docker/Podman for the containerized deployment)
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

The bot scans immediately and then on 60-second wall-clock ticks, reading handled-post state from SQLite in read-only mode when the database exists. A missing database is treated as empty; an unreadable database is an error. Dry-run fetches and extracts eligible articles, logs each successful reply preview once per process, and does not post or write SQLite state.

Each scan paginates newest submissions until it reaches the strict 24-hour cutoff; exactly 24-hour-old posts are excluded. Only link posts that are not removed/deleted, locked, archived, or already replied/skipped in SQLite are previewed. Subreddit scans are sequential; partial results are processed when a later page fails, and rate limits defer only the affected subreddit.

With `DRY_RUN=false` (the default), the bot claims each eligible post in SQLite (single instance, enforced by a file lock), fetches and extracts the article, posts the reply, and records it as replied. Retryable failures (network, Reddit rate limits, 5xx, sidecar errors) are scheduled with exponential backoff and jitter from `RETRY_BASE_DELAY` within `RETRY_WINDOW`, and are picked up by a later scan once due; permanent failures are skipped. Posts older than 24 hours are no longer retried. Each failure log includes post id, URL, stage and attempt number. A crash or lost response between posting and recording could cause a duplicate reply. Invalid settings or state-store failures produce a structured error log and a non-zero exit status.

## Deployment

Both deployments run the bot and the pinned `cf-clearance-scraper` sidecar on a private network (the sidecar publishes no ports), restart them automatically, keep the SQLite database and lock in a named volume mounted at `/data`, and read configuration only from an env file. Write the env file as plain `KEY=value` lines without quotes, starting from `.env.example`. `DB_PATH` and `CF_CLEARANCE_SCRAPER_URL` are fixed by the deployment files.

The bot image is built from the multi-stage `Dockerfile` (static Go binary on a non-root distroless image). Its healthcheck is `/bot healthcheck`, which fails when the bot has not finished a poll or handled a post within 10 minutes (`/data/bot.db.heartbeat`). The sidecar healthcheck probes its HTTP port, and the bot starts only after the sidecar is healthy.

**Docker Compose**

```sh
cp .env.example .env   # then fill in the Reddit credentials
docker compose up -d --build
```

**Podman Quadlet** (rootless; units in `deploy/quadlet/`)

```sh
mkdir -p ~/.config/redditbruneinewsbot ~/.config/containers/systemd
cp .env.example ~/.config/redditbruneinewsbot/.env   # then fill in the Reddit credentials
chmod 600 ~/.config/redditbruneinewsbot/.env
cp deploy/quadlet/* ~/.config/containers/systemd/
systemctl --user daemon-reload
systemctl --user start redditbruneinewsbot.service
```

The Quadlet bot unit pulls `ghcr.io/dsychin/redditbruneinewsbot:latest`, which the `Publish image` GitHub Actions workflow builds and pushes on every push to `main` (plus `sha-*` and, for `v*` tags, version tags). It pulls a newer image on each start, and `AutoUpdate=registry` lets `systemctl --user enable --now podman-auto-update.timer` roll updates automatically. The GHCR package must be public (GitHub package settings) for an unauthenticated pull; otherwise run `podman login ghcr.io` first. For services to survive logout run `loginctl enable-linger $USER`. Switching to production is a change to `SUBREDDITS` in the env file followed by a restart.

## Development

```sh
go test ./...
```
