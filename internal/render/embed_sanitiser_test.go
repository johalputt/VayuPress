// SPDX-License-Identifier: Apache-2.0

package render

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/db"
)

// The finding these tests pin: the article sanitiser was deleting the block
// renderer's output contract, and the click-to-load video facade therefore did
// not work on any published post.
//
// bluemonday's UGC policy allows neither `class` nor `data-*`. The facade is
// built from both. So `<div class="video-facade" data-embed-src="…">` reached
// the reader as `<div>`, and video-facade.js — which queries for
// `.video-facade[data-embed-src]` — found nothing to bind to. The poster
// rendered, the play button had no handler, and clicking did nothing at all.
//
// Everything around it was correct, which is why it survived: the CSS shipped,
// the loader shipped, the CSP was extended for exactly the right origin, and
// the block renderer's own sanitiser pass admitted the attribute. Four parts of
// one feature, three of them real.
//
// These assertions extract the ONE element under test and read its attributes,
// rather than searching the whole page for a substring — the rendered page also
// contains the stylesheet, which mentions every class here and would make a
// naive strings.Contains pass against a completely stripped body.

const facadeBody = `<div class="video-facade" data-embed-src="https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ" data-embed-title="A talk">` +
	`<img class="video-facade__poster" src="/media/0123456789abcdef0123456789abcdef.jpg" alt="" loading="lazy">` +
	`<span class="video-facade__play" aria-hidden="true"></span>` +
	`<a class="video-facade__label" href="https://www.youtube.com/watch?v=dQw4w9WgXcQ">A talk</a></div>`

// facadeDivRe extracts the opening tag of the facade div specifically, so an
// assertion can never be satisfied by the stylesheet or by some other element.
var facadeDivRe = regexp.MustCompile(`<div[^>]*class="video-facade"[^>]*>`)

func renderedArticle(t *testing.T, content string) string {
	t.Helper()
	Init(t.TempDir())
	art := db.Article{
		Title: "T", Slug: "t", Content: content,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	out, err := RenderArticleWithLayout(art, ArticleLayoutType(""), nil)
	if err != nil {
		t.Fatalf("RenderArticleWithLayout: %v", err)
	}
	return out
}

func TestPublishedArticleKeepsTheVideoFacadeItWasGiven(t *testing.T) {
	out := renderedArticle(t, facadeBody)

	tag := facadeDivRe.FindString(out)
	if tag == "" {
		t.Fatalf("no <div class=\"video-facade\"> survived to the rendered page.\n"+
			"Without the class, video-facade.js never selects the element and the reader gets an "+
			"inert poster.\n\npage:\n%s", out)
	}
	if !strings.Contains(tag, `data-embed-src="https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"`) {
		t.Errorf("the facade div lost its embed source: %s\n\n"+
			"This attribute IS the video. Without it the loader has nothing to point an iframe at, "+
			"so clicking the play button does nothing and reports nothing.", tag)
	}
	if !strings.Contains(tag, `data-embed-title=`) {
		t.Errorf("the facade div lost its accessible title: %s", tag)
	}
	// The poster is what the reader actually sees before clicking.
	if !strings.Contains(out, `class="video-facade__poster"`) {
		t.Error("the poster image lost its class, so the facade renders unstyled")
	}
	// Click-to-load is the privacy property: no third-party request happens
	// until the reader asks for one.
	if strings.Contains(out, "<iframe") {
		t.Error("an iframe reached the page — the embed must not load before the reader clicks")
	}
}

// The narrowness of the fix is the other half of it. Allowing the block
// renderer's contract must not become a hole for operator-supplied HTML.
func TestTheSanitiserStillRefusesWhatItAlwaysRefused(t *testing.T) {
	hostile := `<div class="video-facade" data-embed-src="https://evil.example/embed/x" data-embed-title="pwn">` +
		`<span class="video-facade__play" onclick="alert(1)"></span>` +
		`<img class="totally-not-a-block-class" src="/media/x.jpg">` +
		`<div class="admin-panel secret">styled as something else</div>` +
		`</div>` +
		`<script>alert(1)</script>` +
		`<iframe src="https://evil.example/"></iframe>` +
		`<a href="javascript:alert(1)">click</a>` +
		`<div style="position:fixed;inset:0">overlay</div>`

	// Deliberately renderContentHTML and not the full page. The rendered page
	// carries the site's own <script> tags (the theme toggle, the facade
	// loader), so a whole-page search for "<script" passes on a page that
	// stripped nothing and fails on one that stripped everything — it cannot
	// say which element it matched. This function's output IS the article body,
	// so an assertion on it means what it says.
	Init(t.TempDir())
	out := renderContentHTML(hostile)

	for _, forbidden := range []struct{ needle, why string }{
		{"evil.example", "an origin outside the closed provider table reached a data-embed-src, " +
			"which is the value that decides what the reader's browser is asked to frame"},
		{"onclick", "an event handler survived"},
		{"<script", "a script element survived"},
		{"<iframe", "an iframe survived"},
		{"javascript:", "a javascript: URI survived"},
		{`style="position:fixed`, "an inline style survived, which is a click-jacking surface"},
		{"totally-not-a-block-class", "a class outside the component vocabulary survived"},
		{"admin-panel", "a class outside the component vocabulary survived"},
	} {
		if strings.Contains(out, forbidden.needle) {
			t.Errorf("%q reached the rendered page — %s", forbidden.needle, forbidden.why)
		}
	}
}

// A forged embed source must not merely be stripped from the markup: it must
// also never reach the page's CSP, which is computed from the STORED content
// before sanitising. Both paths read the same closed table; this pins that they
// agree.
func TestAForgedEmbedSourceNeverReachesTheCSP(t *testing.T) {
	if got := FrameOriginsInHTML(`<div data-embed-src="https://evil.example/embed/x"></div>`); got != nil {
		t.Errorf("FrameOriginsInHTML admitted %v from a forged attribute — "+
			"this is what extends frame-src, so it must re-derive from the table rather than trust the document", got)
	}
	got := FrameOriginsInHTML(facadeBody)
	if len(got) != 1 || got[0] != "https://www.youtube-nocookie.com" {
		t.Errorf("FrameOriginsInHTML(genuine facade) = %v, want the one allowlisted origin — "+
			"if this returns nothing the CSP is never extended and the reader's click is refused by policy", got)
	}
}

// The link card is the same contract for every non-video URL, and for video in
// a Tor Space. It is styled entirely by these classes.
func TestPublishedArticleKeepsTheLinkCard(t *testing.T) {
	card := `<div class="embed-card"><div class="embed-card__body">` +
		`<span class="embed-card__provider">Example</span>` +
		`<a href="https://example.com/x" class="embed-card__title">A page</a>` +
		`<p class="embed-card__desc">What it is about.</p>` +
		`<span class="embed-card__url">https://example.com/x</span></div></div>`
	out := renderedArticle(t, card)

	for _, cls := range []string{"embed-card", "embed-card__body", "embed-card__provider", "embed-card__title", "embed-card__desc"} {
		if !strings.Contains(out, `class="`+cls+`"`) {
			t.Errorf("the link card lost class %q, so it renders as unstyled loose text", cls)
		}
	}
}

// Figure/gallery blocks carry a multi-class value, which is the case a
// single-token pattern would silently drop.
func TestMultiClassBlockValuesSurvive(t *testing.T) {
	out := renderedArticle(t, `<figure class="vp-figure vp-figure--wide"><img src="/media/x.jpg" alt=""></figure>`)
	if !strings.Contains(out, `class="vp-figure vp-figure--wide"`) {
		t.Errorf("a multi-class block value was dropped, so wide figures render at body width.\npage:\n%s", out)
	}
}
