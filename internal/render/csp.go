package render

// csp.go — single source of truth for the Content-Security-Policy and the
// closed allowlist of privacy-preserving video-embed origins (ADR-0070, Phase 2).
//
// The reader's baseline CSP never carries a third-party frame-src. A page that
// contains a click-to-load video facade narrowly extends frame-src to exactly
// the vetted privacy origins it needs — and nothing else. The allowlist here is
// closed: callers pass origins, but BuildCSP drops anything not in the map, so a
// bug or a tampered block document can never widen the policy.

import (
	"regexp"
	"sort"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
)

// applyOnionCSP is the single CSP chokepoint for Tor/anonymous mode (ADR-0141).
// In OnionMode it removes EVERY external origin from EVERY directive — the bare
// `https:` img scheme and any `https://…`/`http://…` source (video embeds, ad
// networks, anything) — so an onion page can never emit a directive that lets a
// reader's Tor Browser reach off-onion and leak its IP. Both BuildCSP and
// BuildAdCSP pass through here, so a new page type or ad integration cannot
// reintroduce a clearnet origin into a Tor Space (enforced by
// TestApplyOnionCSPStripsAllExternalOrigins). In clearnet mode the policy is
// returned unchanged, so clearnet Spaces stay byte-identical.
func applyOnionCSP(csp string) string {
	if !config.Cfg.OnionMode {
		return csp
	}
	directives := strings.Split(csp, ";")
	for i, d := range directives {
		fields := strings.Fields(d)
		kept := fields[:0]
		for _, f := range fields {
			if f == "https:" || strings.HasPrefix(f, "https://") || strings.HasPrefix(f, "http://") {
				continue // drop external scheme / origin — no off-onion egress
			}
			kept = append(kept, f)
		}
		directives[i] = strings.Join(kept, " ")
	}
	return strings.Join(directives, "; ")
}

// privacyFrameOrigins maps a provider key to its cookie-free embed origin. These
// are the only third-party origins that may ever appear in a frame-src.
var privacyFrameOrigins = map[string]string{
	"youtube": "https://www.youtube-nocookie.com",
	"vimeo":   "https://player.vimeo.com",
}

// allowedFrameOrigins is the set form of privacyFrameOrigins for O(1) checks.
var allowedFrameOrigins = func() map[string]bool {
	m := make(map[string]bool, len(privacyFrameOrigins))
	for _, o := range privacyFrameOrigins {
		m[o] = true
	}
	return m
}()

// cspBaseline is the strict policy applied to every response. %s is the nonce.
//
// img-src admits any https: origin so operators can hotlink images by URL
// (Unsplash, Pixabay, …) straight from the editor. Images are passive,
// non-executable content — scripts, styles, frames and fetches all remain
// locked to 'self', so this does not weaken the execution sandbox.
//
// style-src-attr 'unsafe-inline' permits inline `style="…"` ATTRIBUTES only —
// stylesheets and `<style>` elements stay 'self' (style-src), and the
// XSS-relevant script surface stays nonce-locked (script-src 'self' 'nonce'). An
// inline style attribute is passive, developer-authored layout (flex/gap/width)
// and cannot execute script; without this, the admin UI's attributes were being
// BLOCKED — degrading layout and emitting a steady stream of CSP-violation
// reports (the governance-breach budget) on every operator page view.
const cspBaseline = "default-src 'self'; font-src 'self'; style-src 'self'; style-src-attr 'unsafe-inline'; " +
	"script-src 'self' 'nonce-%s'; img-src 'self' data: https:; connect-src 'self'; " +
	"frame-ancestors 'none'; base-uri 'self'; form-action 'self'; report-uri /csp-report"

// BuildCSP returns the page Content-Security-Policy for the given nonce. When
// frameOrigins is non-empty it narrowly extends frame-src to exactly those
// origins (plus 'self'); every other directive stays at the strict baseline.
// Each origin is validated against the closed allowlist — unknown entries are
// dropped — so a caller can never widen the policy beyond the vetted privacy
// origins.
func BuildCSP(nonce string, frameOrigins []string) string {
	base := strings.Replace(cspBaseline, "%s", nonce, 1)
	valid := validFrameOrigins(frameOrigins)
	if len(valid) == 0 {
		return applyOnionCSP(base)
	}
	return applyOnionCSP(base + "; frame-src 'self' " + strings.Join(valid, " "))
}

// validFrameOrigins filters the input to the closed allowlist and de-duplicates,
// returning a stable sorted slice.
func validFrameOrigins(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	var out []string
	for _, o := range in {
		if allowedFrameOrigins[o] && !seen[o] {
			seen[o] = true
			out = append(out, o)
		}
	}
	sort.Strings(out)
	return out
}

// AllowedFrameOrigin reports whether origin is one of the vetted privacy origins.
func AllowedFrameOrigin(origin string) bool { return allowedFrameOrigins[origin] }

// ── Advertising network CSP (Google AdSense) ──────────────────────────────────
//
// Advertising via Google AdSense requires loading scripts and frames from a
// fixed set of Google ad origins, which the strict baseline deliberately
// forbids. BuildAdCSP returns a policy that admits ONLY those vetted origins,
// in addition to any allowlisted video-embed frame origins. It is applied per
// page, and only on pages that actually render an AdSense unit (the Google Ads
// module is opt-in), so the baseline for every other page is untouched.

// adScriptOrigins are the origins AdSense loads executable code from.
var adScriptOrigins = []string{
	"https://pagead2.googlesyndication.com",
	"https://partner.googleadservices.com",
	"https://tpc.googlesyndication.com",
	"https://www.googletagservices.com",
}

// adFrameOrigins are the origins AdSense renders ad iframes from.
var adFrameOrigins = []string{
	"https://googleads.g.doubleclick.net",
	"https://tpc.googlesyndication.com",
	"https://www.google.com",
}

// adImgConnectOrigins are origins AdSense fetches images/beacons from.
var adImgConnectOrigins = []string{
	"https://pagead2.googlesyndication.com",
	"https://googleads.g.doubleclick.net",
	"https://tpc.googlesyndication.com",
	"https://www.google.com",
}

// BuildAdCSP returns a Content-Security-Policy for a page that renders Google
// AdSense, widening script-src, frame-src, img-src and connect-src to exactly
// the vetted Google ad origins (and merging in any allowlisted video-embed
// frame origins). 'self' and the per-request nonce are always preserved.
func BuildAdCSP(nonce string, frameOrigins []string) string {
	scriptSrc := "script-src 'self' 'nonce-" + nonce + "' " + strings.Join(adScriptOrigins, " ")
	imgSrc := "img-src 'self' data: https: " + strings.Join(adImgConnectOrigins, " ")
	connectSrc := "connect-src 'self' " + strings.Join(adImgConnectOrigins, " ")
	frames := append([]string{}, adFrameOrigins...)
	frames = append(frames, validFrameOrigins(frameOrigins)...)
	frameSrc := "frame-src 'self' " + strings.Join(dedupeSorted(frames), " ")
	// Route through applyOnionCSP so a Tor Space strips every Google ad origin
	// (script/frame/img/connect) — the ad loader is CSP-blocked and never leaks
	// the reader's IP to Google (audit M6). injectArticleAds/injectHomeAds also
	// skip AdSense entirely in OnionMode, so this is the backstop, not the only
	// guard.
	return applyOnionCSP("default-src 'self'; font-src 'self'; style-src 'self'; style-src-attr 'unsafe-inline'; " +
		scriptSrc + "; " + imgSrc + "; " + connectSrc + "; " + frameSrc + "; " +
		"frame-ancestors 'none'; base-uri 'self'; form-action 'self'; report-uri /csp-report")
}

// dedupeSorted returns the input de-duplicated and sorted (stable output).
func dedupeSorted(in []string) []string {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, v := range in {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// videoIDRe constrains a provider video id to a safe character set so a crafted
// id can never break out of the constructed embed URL.
var videoIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// VideoEmbedSrc builds the cookie-free embed URL for a provider + video id, or
// "" if the provider is not allowlisted or the id is unsafe. The returned URL is
// always rooted at an allowlisted privacy origin.
func VideoEmbedSrc(provider, id string) string {
	if !videoIDRe.MatchString(id) {
		return ""
	}
	switch provider {
	case "youtube":
		return "https://www.youtube-nocookie.com/embed/" + id
	case "vimeo":
		return "https://player.vimeo.com/video/" + id
	}
	return ""
}

// embedSrcOriginRe extracts the origin from a data-embed-src attribute value in
// rendered article HTML. Only the two allowlisted hosts can match.
var embedSrcOriginRe = regexp.MustCompile(
	`data-embed-src="(https://(?:www\.youtube-nocookie\.com|player\.vimeo\.com))/`)

// FrameOriginsInHTML scans rendered article HTML for video-facade embed sources
// and returns the distinct allowlisted frame origins present, so the caller can
// extend the page CSP. Pages without facades return nil (strict policy stays).
func FrameOriginsInHTML(html string) []string {
	if !strings.Contains(html, "data-embed-src=") {
		return nil
	}
	var out []string
	seen := make(map[string]bool)
	for _, m := range embedSrcOriginRe.FindAllStringSubmatch(html, -1) {
		o := m[1]
		if allowedFrameOrigins[o] && !seen[o] {
			seen[o] = true
			out = append(out, o)
		}
	}
	sort.Strings(out)
	return out
}
