# Browser-rendering options for public JavaScript-driven news pages (Go bot)

Ticket: dsychin/RedditBruneiNewsBot#67. Scope: render public, ordinary JavaScript-driven pages (including ordinary JavaScript interstitials that resolve on their own) and pass the rendered HTML to Readeck's `codeberg.org/readeck/go-readability/v2` (the planned parser; the archived `go-shiori/go-readability` is not planned). CAPTCHA solving/evasion and paywalled content are out of scope and not assessed. Facts were checked against upstream READMEs, Dockerfiles and GitHub metadata on 2026-10-11.

## Current setup in this repo

- The C# bot calls a `zfcsoftware/cf-clearance-scraper` container at `http://localhost:3000/cf-clearance-scraper` with `mode: "source"` and parses the returned `source` field (`RedditBruneiNewsBot/Program.cs`, `README.md` in this repo).

## Options

### A. Keep `cf-clearance-scraper` as a sidecar

- **Unmaintained.** The upstream README says: "This repo will no longer receive updates" ([README](https://github.com/ZFC-Digital/cf-clearance-scraper#readme)). GitHub shows the last push as 2025-09-03 (`gh api repos/ZFC-Digital/cf-clearance-scraper`). The README still points at the floating `zfcsoftware/cf-clearance-scraper:latest` tag, which will not receive fixes.
- **Not a general renderer.** The README describes it as made "for testing and training purposes" to retrieve page source, create Cloudflare Turnstile tokens and create Cloudflare WAF sessions ([README](https://github.com/ZFC-Digital/cf-clearance-scraper#readme)). Its `package.json` keywords include "bypass", "undetected" and "stealth" and it depends on `puppeteer-real-browser` ([package.json](https://github.com/ZFC-Digital/cf-clearance-scraper/blob/main/package.json)). Challenge bypass and Turnstile tokens are out of scope here, so the bot should not be designed around them.
- **Deployment:** a Node image (`FROM node:latest`) that installs Chromium and Xvfb and runs under pm2, with unpinned `npm update` ([Dockerfile](https://github.com/ZFC-Digital/cf-clearance-scraper/blob/main/Dockerfile)). Builds are not reproducible. Concurrency is controlled by `browserLimit` ([README](https://github.com/ZFC-Digital/cf-clearance-scraper#readme)).
- **Fit:** works today, but adds a non-Go, unmaintained runtime to a Go deployment.

### B. chromedp (drive Chrome from Go)

- **Maintenance:** active. Latest release v0.20.1 (2026-10-05), MIT licence (`gh api repos/chromedp/chromedp` and `.../releases/latest`). The README says it "drives browsers that speak the Chrome DevTools Protocol from Go. It needs no external driver", the core module uses only the standard library plus `cdproto`, and it needs Go 1.25 or newer ([README](https://github.com/chromedp/chromedp#readme)).
- **Docker:** the maintainers publish `docker.io/chromedp/headless-shell`, a multi-arch image of Chrome's `headless-shell` with stable/beta/dev tags pushed daily, usable on port 9222 or as a base image ([docker-headless-shell README](https://github.com/chromedp/docker-headless-shell#readme)). Documented gotchas: pass a larger `--shm-size` if it crashes with `BUS_ADRERR`, and use `--init` to reap zombie processes (same README). Connecting to an already-running browser needs the separate `github.com/chromedp/chromedp/remote` module ([chromedp README](https://github.com/chromedp/chromedp#readme)).
- **JS rendering:** drives a real Chrome; the bot navigates, waits for the page, and reads the DOM, then hands the HTML to go-readability v2 via `readability.FromReader(io.Reader, *url.URL)` or `FromDocument(*html.Node, *url.URL)` ([v2 README](https://codeberg.org/readeck/go-readability/src/branch/v2/README.md), [readability.go](https://codeberg.org/readeck/go-readability/src/branch/v2/readability.go)). This covers ordinary client-side rendering. No upstream document claims it passes bot-detection; such pages are out of scope.

### C. go-rod

- **Maintenance:** repo pushed 2026-08, but latest release is v0.116.2 (2024-07-12) (`gh api repos/go-rod/rod/releases/latest`). MIT. The README describes a DevTools-based driver that can "Automatically find or download browser", with helpers like `WaitStable` and `WaitRequestIdle`, and no zombie processes after a crash ([README](https://github.com/go-rod/rod#readme)).
- **Docker:** the repo ships an Ubuntu-noble Dockerfile bundling `rod-manager`, Chromium dependencies and fonts ([lib/docker/Dockerfile](https://github.com/go-rod/rod/blob/main/lib/docker/Dockerfile)); `rod-manager` lives in [lib/launcher](https://github.com/go-rod/rod/tree/main/lib/launcher).
- Its examples include an `anti-bot-detection` folder ([lib/examples](https://github.com/go-rod/rod/tree/main/lib/examples)); not used or recommended here.
- **Fit:** good ergonomics; slower release cadence. A reasonable fallback.

### D. Playwright for Go (community port)

- **Not a Microsoft SDK.** It is the community project `playwright-community/playwright-go` (the API serves it as `mxschmitt/playwright-go`). Latest v0.6201.1 (2026-08-17); README badges show Chromium 151, Firefox 153, WebKit 26.5 ([README](https://github.com/mxschmitt/playwright-go#readme)).
- **Heavier deployment.** The README says it needs a matching driver per minor version, installs browsers and OS deps (`--with-deps`), and ships a Node.js-based driver (about 50 MB) that the Go code talks to over stdio; a `Dockerfile.example` is provided ([README](https://github.com/mxschmitt/playwright-go#readme)).
- **Fit:** multi-browser support is not needed for article rendering, and the Node driver reintroduces a Node runtime.

## Parser: Readeck's go-readability v2

Verified on the Codeberg `v2` branch (latest tag v2.1.3, 2026-09-30; branch head merged 2026-09-30) via the [repository](https://codeberg.org/readeck/go-readability/src/branch/v2) and the Codeberg API:

- **Module path:** `module codeberg.org/readeck/go-readability/v2`; install with `go get -u codeberg.org/readeck/go-readability/v2` ([go.mod](https://codeberg.org/readeck/go-readability/src/branch/v2/go.mod), [README](https://codeberg.org/readeck/go-readability/src/branch/v2/README.md)). `go.mod` declares `go 1.23.0`, and its dependencies are small (`golang.org/x/net`, `go-shiori/dom`, `dateparse`, and others).
- **Compatibility:** the README says it is compatible with Readability.js v0.6.0. [FORK.md](https://codeberg.org/readeck/go-readability/src/branch/v2/FORK.md) says it is a fork of go-shiori/go-readability that adds performance optimizations, Readability.js 0.6.0 compatibility and fixes such as image extraction. The old go-shiori module is archived and stops at Readability.js v0.5.0 ([go-shiori README](https://github.com/go-shiori/go-readability#readme)).
- **API for rendered HTML:** `FromReader(input io.Reader, pageURL *url.URL) (Article, error)` and `FromDocument(doc *html.Node, pageURL *url.URL) (Article, error)`; `FromURL` and `CheckDocument(*html.Node) bool` also exist ([readability.go](https://codeberg.org/readeck/go-readability/src/branch/v2/readability.go)). The browser step should therefore pass the rendered DOM string as a reader together with the final page URL, so relative links resolve.
- **Breaking change from v1:** `Article` metadata fields are now methods, e.g. `Title()`, `Byline()`, `Excerpt()`, `SiteName()`, `Language()`, `PublishedTime() (time.Time, error)`. Content is `Article.Node *html.Node` (may be nil), rendered with `RenderHTML(io.Writer)` or `RenderText(io.Writer)` ([article.go](https://codeberg.org/readeck/go-readability/src/branch/v2/article.go)). Code written against go-shiori's `article.TextContent`/`article.Content` fields needs porting.
- **Fit with browser rendering:** the parser takes any HTML, so it is independent of the renderer choice. Its README example parses a local HTML file, which matches feeding it chromedp's `OuterHTML` output. The parser has no browser or JavaScript execution of its own, so JavaScript-driven pages still need one of the renderers above.
- The README example omits the `net/url` import; copy it with that in mind.

## Comparison

| | A. cf-clearance-scraper | B. chromedp | C. go-rod | D. playwright-go |
|---|---|---|---|---|
| Upstream status | No further updates | Active (v0.20.1, 2026-10) | Last release 2024-07 | Active, community-run |
| Runtime | Node + Chromium + Xvfb | Go + Chrome | Go + Chrome | Go + Node driver + browsers |
| Docker | Own image, `latest` | Official `headless-shell` | Official Dockerfile | Own image from example |
| Plain JS rendering | Yes (`source` mode) | Yes | Yes | Yes |
| Design purpose | Cloudflare bypass tooling | General automation | General automation | General automation |

## Recommendation

1. **Use chromedp** with Chrome from `chromedp/headless-shell` (sidecar via `chromedp/remote`, or installed in the bot image). It is the most actively maintained Go option, needs no Node, and has an official daily-rebuilt image.
2. Render only as a fallback: fetch with plain HTTP first and render when readability yields little or no content, to limit Chrome load.
3. Pin the image by version tag or digest; set per-page timeouts and a concurrency cap; use a larger `shm-size` and `--init`.
4. **Retire `cf-clearance-scraper`** once the Go bot replaces the C# one: unmaintained and designed around bypass, which is out of scope.
5. Fallback: go-rod. Avoid playwright-go unless multi-browser support becomes necessary.

## Limitations

- The v2 module's behaviour on specific Brunei news sites was not tested. Pages needing CAPTCHA solving or that block automated browsers are not handled by any option; the bot should skip or report them.
- Nothing was benchmarked or run against real news sites; claims come from upstream docs and metadata. Chrome memory/CPU cost on the target host was not measured.
- Versions and dates were read on 2026-10-11 and may drift.
