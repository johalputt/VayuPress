// SPDX-License-Identifier: Apache-2.0

// Package embeds is the single source of truth for which third-party origins a
// reader's page may ever frame, and for turning a pasted video URL into one.
//
// # Why this package exists
//
// The same closed allowlist used to be written out four times: a provider→origin
// map and an origin-extracting regexp in internal/render, a third regexp in
// internal/blockrender that had to agree with both, and a switch over hostnames
// in the unfurl handler. Adding a provider meant four edits across three
// packages, and the failure mode of forgetting one is silent in the direction
// that matters — blockrender emits data-embed-src, render does not recognise the
// origin, the page CSP is never extended, and the reader clicks a play button
// that produces an iframe the page's own policy refuses. Nothing errors; the
// video simply never plays.
//
// So the table below is the only place a provider is described, and every
// consumer derives what it needs from it. A provider that is not in this table
// cannot be framed by any page this server renders.
//
// # What makes an entry safe
//
//   - The embed origin is fixed here, never taken from the pasted URL. An
//     attacker who controls the URL controls at most the id.
//   - The id is validated by a FULLY ANCHORED pattern, so a crafted path or
//     query fragment cannot smuggle a second path segment into the embed URL.
//   - Hosts are matched against the PARSED hostname by exact equality, never as
//     a substring of the raw URL — https://evil.com/?x=youtube.com/ID parses to
//     host evil.com and matches nothing.
//   - Nothing third-party loads until the reader clicks. Every provider here is
//     rendered as a click-to-load facade (see internal/blockrender), so being on
//     this list is permission to be framed on request, not permission to run on
//     page load.
package embeds

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Provider describes one embeddable video platform.
type Provider struct {
	// Key is the stable identifier stored in a block document and returned to
	// the editor. Never change one: existing posts carry it.
	Key string

	// Name is what a reader sees on the link-card fallback.
	Name string

	// Origin is the scheme+host the iframe is rooted at. This is the value that
	// reaches frame-src, so it is the whole trust decision.
	Origin string

	// Path is the fixed prefix the id is appended to, e.g. "/embed/".
	Path string

	// IDPat is the id pattern, written UNANCHORED. It is anchored on the way
	// into ID and embedded verbatim into the derived whole-URL pattern, which is
	// why it is kept as a string rather than reverse-engineered out of a
	// compiled regexp: stripping "^" and "$" back off a pattern is the kind of
	// surgery that works until someone writes an alternation.
	IDPat string

	// ID is IDPat, anchored. Built by init from IDPat — never set by hand.
	ID *regexp.Regexp

	// Hosts are the exact hostnames (with any leading "www." already stripped)
	// whose URLs resolve to this provider.
	Hosts []string

	// HostSuffix, when non-empty, additionally matches any hostname ending in
	// it. Used only where a platform issues a per-account subdomain, and applied
	// to the PARSED hostname so "wistia.com.evil.com" does not match.
	HostSuffix string

	// extract pulls the id out of a source URL, or returns "" if this URL is not
	// one of the provider's video links. It never has to validate the id — the
	// caller checks it against ID.
	extract func(u *url.URL) string
}

// providers is the closed allowlist. Every entry is click-to-load.
//
// Deliberately absent, with reasons, so the same candidates are not re-litigated
// every time this list is read:
//
//   - Twitch requires the embedding domain in a `parent=` query parameter, so
//     the embed URL is not a function of the video alone and would have to be
//     rebuilt per host. It is buildable; it is not buildable from a fixed table.
//   - Rumble's iframe takes an internal numeric id that is not derivable from a
//     share URL without an API round trip at paste time.
//   - PeerTube and other self-hosted platforms have no fixed origin, which is
//     the one thing an allowlist cannot do without.
//   - Code sandboxes execute author-supplied script inside the frame. That is a
//     different risk class from a video player and should not ride in on a table
//     designed for one.
var providers = []Provider{
	{
		Key: "youtube", Name: "YouTube",
		Origin: "https://www.youtube-nocookie.com", Path: "/embed/",
		IDPat: `[A-Za-z0-9_-]{6,64}`,
		Hosts: []string{"youtube.com", "m.youtube.com", "music.youtube.com", "youtu.be"},
		extract: func(u *url.URL) string {
			if strings.EqualFold(u.Hostname(), "youtu.be") {
				return nthSegment(u.Path, 0)
			}
			// /watch?v=ID
			if v := u.Query().Get("v"); v != "" {
				return v
			}
			// /embed/ID, /shorts/ID, /v/ID, /live/ID
			switch nthSegment(u.Path, 0) {
			case "embed", "shorts", "v", "live":
				return nthSegment(u.Path, 1)
			}
			return ""
		},
	},
	{
		Key: "vimeo", Name: "Vimeo",
		Origin: "https://player.vimeo.com", Path: "/video/",
		IDPat: `\d{6,15}`,
		Hosts: []string{"vimeo.com", "player.vimeo.com"},
		extract: func(u *url.URL) string {
			// /ID, /video/ID, and /channels/<name>/ID.
			if seg := nthSegment(u.Path, 0); seg == "video" || seg == "channels" {
				if seg == "channels" {
					return nthSegment(u.Path, 2)
				}
				return nthSegment(u.Path, 1)
			}
			return nthSegment(u.Path, 0)
		},
	},
	{
		Key: "dailymotion", Name: "Dailymotion",
		Origin: "https://www.dailymotion.com", Path: "/embed/video/",
		IDPat: `[A-Za-z0-9]{5,20}`,
		Hosts: []string{"dailymotion.com", "dai.ly"},
		extract: func(u *url.URL) string {
			if strings.EqualFold(u.Hostname(), "dai.ly") {
				return nthSegment(u.Path, 0)
			}
			// /video/ID and /embed/video/ID. The share URL appends a slug after
			// the id with an underscore, which the anchored ID pattern rejects,
			// so it is trimmed here rather than loosening the pattern.
			var id string
			switch nthSegment(u.Path, 0) {
			case "video":
				id = nthSegment(u.Path, 1)
			case "embed":
				if nthSegment(u.Path, 1) == "video" {
					id = nthSegment(u.Path, 2)
				}
			}
			if i := strings.IndexByte(id, '_'); i >= 0 {
				id = id[:i]
			}
			return id
		},
	},
	{
		Key: "loom", Name: "Loom",
		Origin: "https://www.loom.com", Path: "/embed/",
		IDPat: `[a-f0-9]{32}`,
		Hosts: []string{"loom.com"},
		extract: func(u *url.URL) string {
			switch nthSegment(u.Path, 0) {
			case "share", "embed":
				return nthSegment(u.Path, 1)
			}
			return ""
		},
	},
	{
		Key: "wistia", Name: "Wistia",
		Origin: "https://fast.wistia.net", Path: "/embed/iframe/",
		IDPat: `[a-z0-9]{10}`,
		// Wistia issues a per-account subdomain (acme.wistia.com), so this is the
		// one entry that needs a suffix. It is applied to the parsed hostname
		// with a leading dot, so wistia.com.evil.com and evilwistia.com are both
		// refused — pinned by a test, because a suffix match is exactly the shape
		// that is easy to get wrong.
		Hosts: []string{"wistia.com", "wistia.net", "fast.wistia.net"}, HostSuffix: ".wistia.com",
		extract: func(u *url.URL) string {
			switch nthSegment(u.Path, 0) {
			case "medias":
				return nthSegment(u.Path, 1)
			case "embed":
				// /embed/iframe/ID and /embed/medias/ID.json
				id := nthSegment(u.Path, 2)
				return strings.TrimSuffix(id, ".json")
			}
			return ""
		},
	},
}

// init anchors every IDPat exactly once, so no entry can be written with a
// half-anchored pattern that matches a substring of a longer id.
func init() {
	for i := range providers {
		providers[i].ID = regexp.MustCompile(`^(?:` + providers[i].IDPat + `)$`)
	}
}

// byKey indexes providers for O(1) lookup.
var byKey = func() map[string]*Provider {
	m := make(map[string]*Provider, len(providers))
	for i := range providers {
		m[providers[i].Key] = &providers[i]
	}
	return m
}()

// originSet is the set of framable origins, derived from the table.
var originSet = func() map[string]bool {
	m := make(map[string]bool, len(providers))
	for i := range providers {
		m[providers[i].Origin] = true
	}
	return m
}()

// AllowedOrigin reports whether origin may appear in a frame-src.
func AllowedOrigin(origin string) bool { return originSet[origin] }

// Name returns the display name for a provider key, or "" if unknown.
func Name(key string) string {
	if p := byKey[key]; p != nil {
		return p.Name
	}
	return ""
}

// EmbedSrc builds the embed URL for a provider key and id, or "" if the key is
// unknown or the id does not match that provider's anchored pattern.
//
// The origin and path come from the table and never from caller input, so the
// worst a caller can do with a hostile id is fail this check.
func EmbedSrc(key, id string) string {
	p := byKey[key]
	if p == nil || !p.ID.MatchString(id) {
		return ""
	}
	return p.Origin + p.Path + id
}

// srcRe is the anchored pattern matching every embed URL this table can build,
// compiled from the table itself so it can never drift from EmbedSrc. It is the
// barrier applied at render time and again by the sanitiser.
var srcRe = func() *regexp.Regexp {
	alts := make([]string, 0, len(providers))
	for _, p := range providers {
		alts = append(alts, regexp.QuoteMeta(p.Origin+p.Path)+`(?:`+p.IDPat+`)`)
	}
	sort.Strings(alts)
	return regexp.MustCompile(`^(?:` + strings.Join(alts, "|") + `)$`)
}()

// SrcPattern is srcRe, for callers that need the barrier as a pattern rather
// than a predicate — the sanitiser applies it as an attribute policy.
func SrcPattern() *regexp.Regexp { return srcRe }

// ValidEmbedSrc returns s when it is an embed URL this table could have built,
// else "".
//
// Note that this is deliberately a re-derivation rather than a lookup of what
// was stored: a block document is data, and a stored embedSrc is only ever as
// trustworthy as whatever last wrote it.
func ValidEmbedSrc(s string) string {
	if srcRe.MatchString(s) {
		return s
	}
	return ""
}

// originRe extracts the origin from a data-embed-src attribute in rendered HTML.
// Built from the table for the same reason as srcRe.
var originRe = func() *regexp.Regexp {
	alts := make([]string, 0, len(originSet))
	for o := range originSet {
		alts = append(alts, regexp.QuoteMeta(o))
	}
	sort.Strings(alts)
	return regexp.MustCompile(`data-embed-src="(` + strings.Join(alts, "|") + `)/`)
}()

// OriginsInHTML scans rendered article HTML for facade embed sources and returns
// the distinct allowlisted origins present, so the caller can extend that page's
// CSP by exactly what the page contains and nothing more. HTML with no facade
// returns nil, leaving the strict baseline in place.
func OriginsInHTML(html string) []string {
	if !strings.Contains(html, "data-embed-src=") {
		return nil
	}
	var out []string
	seen := make(map[string]bool)
	for _, m := range originRe.FindAllStringSubmatch(html, -1) {
		if o := m[1]; originSet[o] && !seen[o] {
			seen[o] = true
			out = append(out, o)
		}
	}
	sort.Strings(out)
	return out
}

// Detect resolves a pasted URL to a provider key and embed URL, or ("", "") if
// it is not a recognised video link.
//
// Only https and http URLs are considered. The host is taken from the parsed
// URL, so credentials, ports and query strings cannot influence which provider
// is selected.
func Detect(rawURL string) (key, embedSrc string) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "", ""
	}
	if s := strings.ToLower(u.Scheme); s != "https" && s != "http" {
		return "", ""
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")

	for i := range providers {
		p := &providers[i]
		if !p.matchesHost(host) {
			continue
		}
		// Built by EmbedSrc rather than concatenated here, so there is exactly one
		// place a URL this server will frame comes into existence — and it is the
		// place that checks the id.
		src := EmbedSrc(p.Key, p.extract(u))
		if src == "" {
			// The host is right and the URL is not a video — a channel page, a
			// search, a playlist. Returning here rather than continuing is
			// correct: no other provider claims this host.
			return "", ""
		}
		return p.Key, src
	}
	return "", ""
}

// matchesHost reports whether host (lowercased, "www." stripped) belongs to p.
func (p *Provider) matchesHost(host string) bool {
	for _, h := range p.Hosts {
		if host == h {
			return true
		}
	}
	// The suffix carries its own leading dot, so it can only match a real
	// subdomain label boundary.
	return p.HostSuffix != "" && strings.HasSuffix(host, p.HostSuffix)
}

// nthSegment returns the n-th (0-based) non-empty path segment, or "".
func nthSegment(p string, n int) string {
	segs := strings.FieldsFunc(p, func(r rune) bool { return r == '/' })
	if n >= 0 && n < len(segs) {
		return segs[n]
	}
	return ""
}
