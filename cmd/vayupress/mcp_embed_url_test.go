// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/blockrender"
	"github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/embeds"
	"github.com/johalputt/vayupress/internal/render"
)

// publishBody renders a real article whose body is content and returns the page.
// The whole point is to go through the same path a reader does rather than call
// the sanitiser directly, because the defect this file exists for lived in the
// gap between the two.
func publishBody(t *testing.T, content string) string {
	t.Helper()
	render.Init(t.TempDir())
	out, err := render.RenderArticleWithLayout(
		db.Article{Title: "T", Slug: "t", Content: content, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		render.ArticleLayoutType(""), nil)
	if err != nil {
		t.Fatalf("RenderArticleWithLayout: %v", err)
	}
	return out
}

// facadeDivRe extracts the facade's opening tag specifically. A whole-page
// search for "video-facade" matches the stylesheet and would pass on a page
// whose body was stripped bare.
var facadeDivRe = regexp.MustCompile(`<div[^>]*class="video-facade"[^>]*>`)

// The point of embed_url is that a post written entirely through the connector
// gets a working video without anyone hand-writing markup. That claim spans two
// packages — blockrender builds the facade, and the article sanitiser in
// internal/render decides what a reader is finally served — and it is only true
// if BOTH agree.
//
// This test walks the whole distance: resolve → block → rendered HTML → publish
// → sanitised article body. Testing only the first half is what let the facade
// ship broken; the block renderer's own output was always correct.
func TestEmbedURLOutputSurvivesPublication(t *testing.T) {
	key, embedSrc := embeds.Detect("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	if key != "youtube" || embedSrc == "" {
		t.Fatalf("Detect returned (%q, %q); the rest of this test is meaningless without it", key, embedSrc)
	}

	// Exactly what the tool builds from a resolved embed.
	blocks, err := json.Marshal([]blockrender.Block{{
		Type:     "embed",
		URL:      "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Title:    "A talk about something",
		Provider: embeds.Name(key),
		ThumbURL: "/media/0123456789abcdef0123456789abcdef.jpg",
		Kind:     "video",
		EmbedSrc: embedSrc,
	}})
	if err != nil {
		t.Fatal(err)
	}
	toolHTML, _, err := blockrender.Render(string(blocks))
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if !strings.Contains(toolHTML, `data-embed-src="`+embedSrc+`"`) {
		t.Fatalf("the connector would return markup with no embed source:\n%s", toolHTML)
	}

	// Now publish it: this is what create_post's content goes through.
	published := publishBody(t, toolHTML)
	tag := facadeDivRe.FindString(published)
	if tag == "" {
		t.Fatalf("the facade div did not survive publication at all:\n\nreturned:\n%s", toolHTML)
	}

	if !strings.Contains(tag, `data-embed-src="`+embedSrc+`"`) {
		t.Errorf("the connector's own output does not survive publication:\n\nreturned:\n%s\n\npublished:\n%s\n\n"+
			"An operator would paste exactly what this tool told them to paste and get a poster with a "+
			"dead play button. A tool whose output the publishing path strips is worse than no tool: it "+
			"reports success.", toolHTML, published)
	}
	// The published page carries the site's own markup too, so this asks about
	// the facade element rather than the document: a click-to-load embed must
	// contain no frame until the reader acts.
	if strings.Contains(tag, "iframe") {
		t.Errorf("an iframe reached the facade — the embed must not load before the reader clicks:\n%s", tag)
	}
}

// A URL that is not a video must still produce something publishable. The link
// card is the fallback for every other platform, and it is styled entirely by
// classes the sanitiser used to strip.
func TestEmbedURLLinkCardSurvivesPublication(t *testing.T) {
	blocks, err := json.Marshal([]blockrender.Block{{
		Type:        "embed",
		URL:         "https://example.com/an-article",
		Title:       "An article",
		Description: "What it is about.",
		Provider:    "Example",
		ThumbURL:    "/media/0123456789abcdef0123456789abcdef.jpg",
		Kind:        "link",
	}})
	if err != nil {
		t.Fatal(err)
	}
	toolHTML, _, err := blockrender.Render(string(blocks))
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	published := publishBody(t, toolHTML)

	for _, want := range []string{`class="embed-card"`, `class="embed-card__title"`, "An article", "example.com/an-article"} {
		if !strings.Contains(published, want) {
			t.Errorf("the published link card lost %q:\n%s", want, published)
		}
	}
}
