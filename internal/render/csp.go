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
)

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
const cspBaseline = "default-src 'self'; font-src 'self'; style-src 'self'; " +
	"script-src 'self' 'nonce-%s'; img-src 'self' data:; connect-src 'self'; " +
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
		return base
	}
	return base + "; frame-src 'self' " + strings.Join(valid, " ")
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
