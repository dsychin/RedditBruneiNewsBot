// Package sites holds per-publisher knowledge shared by the fetcher and the extractor.
package sites

// CloudflareHosts are hosts known to sit behind a Cloudflare challenge. They are
// fetched through the sidecar directly, skipping a plain GET that would fail.
// This is an optimisation, not an allowlist: other hosts fall back to the
// sidecar when they present a challenge.
var CloudflareHosts = map[string]bool{
	"borneobulletin.com.bn": true,
	"www.theborneopost.com": true,
}

// TitleSuffixes are site names appended to <title> values.
var TitleSuffixes = []string{
	"Borneo Bulletin Online",
	"Borneo Bulletin",
	"Borneo Post Online",
	"The Borneo Post",
	"Borneo Post",
}

// TitleSeparators may precede a site-name suffix.
var TitleSeparators = []string{" | ", " - ", " – ", " — ", " :: "}
