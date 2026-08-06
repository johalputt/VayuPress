// SPDX-License-Identifier: Apache-2.0

package embeds

import (
	"regexp"
	"strings"
	"testing"
)

// TestEveryProviderAgreesWithItself is the gate this package exists to provide.
//
// The bug it prevents is not a crash. It is the silent disagreement that used to
// be possible when the origin list, the id pattern and the URL barrier lived in
// three packages: one of them learns about a provider, the others do not, and
// the result is a play button that produces an iframe the page's own CSP then
// refuses. Nothing logs, nothing 500s, the video just never plays.
//
// So for every provider in the table, the URL that EmbedSrc builds must pass the
// barrier that ValidEmbedSrc applies, and the origin must be recognised both by
// AllowedOrigin and by the scanner that extends a page's CSP. If any consumer
// ever drifts from the table again, this fails.
func TestEveryProviderAgreesWithItself(t *testing.T) {
	for _, p := range providers {
		t.Run(p.Key, func(t *testing.T) {
			id := sampleID(t, p.IDPat)

			src := EmbedSrc(p.Key, id)
			if src == "" {
				t.Fatalf("EmbedSrc(%q, %q) built nothing from a sample id its own pattern %q accepts", p.Key, id, p.IDPat)
			}
			if !strings.HasPrefix(src, p.Origin+"/") {
				t.Fatalf("EmbedSrc built %q, which is not rooted at the table's origin %q — "+
					"the origin is the entire trust decision and must never come from anywhere else", src, p.Origin)
			}
			if ValidEmbedSrc(src) == "" {
				t.Errorf("the barrier rejects a URL the table itself built: %q.\n"+
					"blockrender emits data-embed-src only for URLs that pass this, so this provider "+
					"would be silently unrenderable.", src)
			}
			if !AllowedOrigin(p.Origin) {
				t.Errorf("origin %q is not allowed by the CSP check, so BuildCSP would drop it and the "+
					"reader's click would be refused by the page's own policy", p.Origin)
			}
			html := `<div class="video-facade" data-embed-src="` + src + `"></div>`
			got := OriginsInHTML(html)
			if len(got) != 1 || got[0] != p.Origin {
				t.Errorf("OriginsInHTML(rendered facade) = %v, want exactly [%q] — this is what "+
					"extends the page CSP, so a miss here is a video that never plays", got, p.Origin)
			}
			if Name(p.Key) == "" {
				t.Errorf("provider %q has no display name, so the link-card fallback would show a blank provider", p.Key)
			}
		})
	}
}

// sampleID returns a concrete id that matches pat, so the round-trip test above
// does not need a hand-written fixture per provider (which is exactly the kind
// of per-provider duplication this package removes).
func sampleID(t *testing.T, pat string) string {
	t.Helper()
	re := regexp.MustCompile(`^(?:` + pat + `)$`)
	for _, cand := range []string{
		"dQw4w9WgXcQ",
		"123456789",
		"x7tgad0",
		strings.Repeat("a", 32),
		"abc123defg",
	} {
		if re.MatchString(cand) {
			return cand
		}
	}
	t.Fatalf("no sample id matches %q — add one to sampleID when adding a provider", pat)
	return ""
}

// TestDetectRefusesHostConfusion is the attack the parsed-hostname rule exists
// for: put a provider's name somewhere in a URL the attacker controls and see
// whether anything matches on it as a substring.
//
// If any of these resolved, an attacker who can get a URL into a post could
// choose the origin of a frame on the reader's page.
func TestDetectRefusesHostConfusion(t *testing.T) {
	for _, raw := range []string{
		"https://evil.com/?x=youtube.com/dQw4w9WgXcQ",
		"https://evil.com/youtube.com/watch?v=dQw4w9WgXcQ",
		"https://evil.com/#youtu.be/dQw4w9WgXcQ",
		"https://youtube.com.evil.com/watch?v=dQw4w9WgXcQ",
		"https://notyoutube.com/watch?v=dQw4w9WgXcQ",
		"https://youtube.com@evil.com/watch?v=dQw4w9WgXcQ", // userinfo, not host
		"https://evil.com/vimeo.com/123456789",
		"https://vimeo.com.evil.com/123456789",
		// The suffix entry is the one shape that is easy to get wrong, so it is
		// attacked from both sides: a longer domain that ENDS in the suffix
		// text, and a label that merely contains it.
		"https://wistia.com.evil.com/medias/abc123defg",
		"https://evilwistia.com/medias/abc123defg",
		"https://notwistia.com/medias/abc123defg",
		"https://wistia.com.attacker.net/medias/abc123defg",
		// Schemes that are not web fetches at all.
		//
		// The first two below are the ones that matter, and the reason this
		// block is not just decoration: `javascript://host/path` parses with a
		// REAL Host, so the opaque-URL cases above never reach the scheme check
		// and removing it survived the first version of this test. A resolved
		// match here does not corrupt the embed URL — that is built from the
		// table — but the source URL becomes the facade's link href and the
		// card's title href, so a javascript: URI resolving to a "valid video"
		// is a script URI one sanitiser slip away from the reader.
		"javascript://youtube.com/watch?v=dQw4w9WgXcQ",
		"ftp://youtube.com/watch?v=dQw4w9WgXcQ",
		"javascript:alert(1)//youtube.com/watch?v=dQw4w9WgXcQ",
		"data:text/html,<b>youtube.com/watch?v=dQw4w9WgXcQ",
		"file:///etc/passwd",
		"",
		"not a url at all",
	} {
		if key, src := Detect(raw); key != "" || src != "" {
			t.Errorf("Detect(%q) resolved to provider %q, src %q.\n"+
				"Anything that resolves here becomes a third-party origin in the reader's frame-src, "+
				"so a URL an attacker controls must never reach one.", raw, key, src)
		}
	}
}

// TestDetectAcceptsTheRealShareURLs pins the shapes an operator actually pastes.
// A detector that is safe and recognises nothing is not a feature.
func TestDetectAcceptsTheRealShareURLs(t *testing.T) {
	cases := []struct{ raw, key, src string }{
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "youtube", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"https://youtu.be/dQw4w9WgXcQ", "youtube", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"https://m.youtube.com/watch?v=dQw4w9WgXcQ", "youtube", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"https://www.youtube.com/shorts/dQw4w9WgXcQ", "youtube", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"https://www.youtube.com/live/dQw4w9WgXcQ", "youtube", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		// A share link with tracking parameters is the common paste, and the
		// extra query must not change which id is taken.
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=42s&si=abcdef", "youtube", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"https://vimeo.com/123456789", "vimeo", "https://player.vimeo.com/video/123456789"},
		{"https://vimeo.com/channels/staffpicks/123456789", "vimeo", "https://player.vimeo.com/video/123456789"},
		{"https://player.vimeo.com/video/123456789", "vimeo", "https://player.vimeo.com/video/123456789"},
		{"https://www.dailymotion.com/video/x7tgad0", "dailymotion", "https://www.dailymotion.com/embed/video/x7tgad0"},
		// Dailymotion appends a title slug after an underscore; taking the whole
		// segment would fail the anchored id pattern and lose a valid video.
		{"https://www.dailymotion.com/video/x7tgad0_some-title-slug", "dailymotion", "https://www.dailymotion.com/embed/video/x7tgad0"},
		{"https://dai.ly/x7tgad0", "dailymotion", "https://www.dailymotion.com/embed/video/x7tgad0"},
		{"https://www.loom.com/share/" + strings.Repeat("a", 32), "loom", "https://www.loom.com/embed/" + strings.Repeat("a", 32)},
		{"https://acme.wistia.com/medias/abc123defg", "wistia", "https://fast.wistia.net/embed/iframe/abc123defg"},
		{"https://fast.wistia.net/embed/iframe/abc123defg", "wistia", "https://fast.wistia.net/embed/iframe/abc123defg"},
	}
	for _, c := range cases {
		key, src := Detect(c.raw)
		if key != c.key || src != c.src {
			t.Errorf("Detect(%q) = (%q, %q), want (%q, %q)", c.raw, key, src, c.key, c.src)
		}
	}
}

// TestDetectRefusesNonVideoPagesOnAProviderHost. Being on a provider's domain is
// not the same as being a video: a channel page, a search or a playlist has no
// id, and turning one into an embed URL would frame something the operator did
// not paste.
func TestDetectRefusesNonVideoPagesOnAProviderHost(t *testing.T) {
	for _, raw := range []string{
		"https://www.youtube.com/",
		"https://www.youtube.com/results?search_query=cats",
		"https://www.youtube.com/@somechannel",
		"https://www.youtube.com/playlist?list=PLabcdef",
		"https://www.youtube.com/watch?v=short", // 5 chars, under the id floor
		"https://vimeo.com/staffpicks",
		"https://vimeo.com/12345", // 5 digits, under the id floor
		"https://www.dailymotion.com/vayupress",
		"https://www.loom.com/share/not-32-hex",
		"https://acme.wistia.com/projects/abc123defg",
	} {
		if key, src := Detect(raw); key != "" || src != "" {
			t.Errorf("Detect(%q) = (%q, %q), want no match — this URL carries no video id", raw, key, src)
		}
	}
}

// TestValidEmbedSrcRefusesEverythingTheTableCannotBuild. The barrier is applied
// to a stored block document, which is data and only ever as trustworthy as
// whatever last wrote it. It must re-derive rather than trust.
func TestValidEmbedSrcRefusesEverythingTheTableCannotBuild(t *testing.T) {
	for _, s := range []string{
		"https://evil.com/embed/dQw4w9WgXcQ",
		"http://www.youtube-nocookie.com/embed/dQw4w9WgXcQ",   // http, not https
		"https://www.youtube-nocookie.com/embed/",             // no id
		"https://www.youtube-nocookie.com/embed/a/../../evil", // second segment
		"https://www.youtube-nocookie.com/embed/abc?x=1",      // query smuggled on
		"https://www.youtube-nocookie.com/embed/abc#frag",
		"https://www.youtube-nocookie.com.evil.com/embed/dQw4w9WgXcQ",
		"https://www.youtube-nocookie.com/watch?v=dQw4w9WgXcQ", // wrong path
		"https://player.vimeo.com/video/notdigits",
		"https://fast.wistia.net/embed/iframe/ABC123DEFG", // uppercase id
		" https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ",
		"https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ ",
		"https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ\nhttps://evil.com",
		"",
	} {
		if got := ValidEmbedSrc(s); got != "" {
			t.Errorf("ValidEmbedSrc(%q) = %q, want \"\".\n"+
				"This value goes straight into a data-embed-src attribute and then into the reader's "+
				"frame-src, so anything the table could not have built must be refused.", s, got)
		}
	}
}

// TestOriginsInHTMLOnlyReturnsTableOrigins. This function decides what gets added
// to a page's CSP, and its input is rendered article HTML — which contains
// operator-authored markup. A forged attribute must not widen the policy.
func TestOriginsInHTMLOnlyReturnsTableOrigins(t *testing.T) {
	html := `<div class="video-facade" data-embed-src="https://evil.com/embed/x"></div>` +
		`<div data-embed-src="https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"></div>` +
		`<div data-embed-src="https://attacker.example/player/1"></div>`
	got := OriginsInHTML(html)
	if len(got) != 1 || got[0] != "https://www.youtube-nocookie.com" {
		t.Fatalf("OriginsInHTML returned %v — a forged data-embed-src must never reach frame-src", got)
	}
	if OriginsInHTML("<p>an ordinary post</p>") != nil {
		t.Error("a post with no facade must leave the strict baseline policy alone")
	}
}

// TestIDPatternsAreAnchored. Every pattern is anchored centrally by init, and
// this is what proves it: an unanchored pattern would match a prefix of a longer
// hostile id and let a second path segment through.
func TestIDPatternsAreAnchored(t *testing.T) {
	for _, p := range providers {
		id := sampleID(t, p.IDPat)
		for _, hostile := range []string{id + "/../../evil", id + "?x=1", id + "#f", "../" + id, id + " "} {
			if p.ID.MatchString(hostile) {
				t.Errorf("%s: id pattern accepted %q, so an attacker could append a path segment "+
					"to the embed URL the table builds", p.Key, hostile)
			}
		}
	}
}
