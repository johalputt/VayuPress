// SPDX-License-Identifier: Apache-2.0

package seo

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestLLMsRenderCarriesTheEssentials(t *testing.T) {
	out := Render(LLMsDoc{
		SiteName:    "Example",
		Origin:      "https://example.com/",
		Description: "Writing about things.",
		Generated:   time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC),
		Posts: []LLMsPost{{
			Title:     "First post",
			URL:       "https://example.com/first-post",
			Summary:   "An excerpt.",
			Published: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		}},
	})
	for _, want := range []string{
		"# Example",
		"> Writing about things.",
		"- Site: https://example.com/",
		"- Feed: https://example.com/feed.xml",
		"- Sitemap: https://example.com/sitemap.xml",
		"## Posts",
		"- [First post](https://example.com/first-post): An excerpt. (2026-01-02)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("llms.txt is missing %q\n---\n%s", want, out)
		}
	}
	// The trailing slash on Origin must not produce "https://example.com//".
	if strings.Contains(out, "com//") {
		t.Error("a trailing slash on Origin produced a doubled slash")
	}
}

// The format is line-oriented: a newline inside a title would end the list item
// early and silently swallow the link.
func TestLLMsFlattensWhitespaceInTitles(t *testing.T) {
	out := Render(LLMsDoc{
		Origin: "https://example.com",
		Posts: []LLMsPost{{
			Title:   "Broken\nover\ttwo lines",
			URL:     "https://example.com/x",
			Summary: "Also\nbroken.",
		}},
	})
	body := out[strings.Index(out, "## Posts"):]
	lines := 0
	for _, l := range strings.Split(strings.TrimSpace(body), "\n") {
		if strings.HasPrefix(l, "- [") {
			lines++
		}
	}
	if lines != 1 {
		t.Errorf("expected exactly one entry line, got %d\n%s", lines, body)
	}
	if !strings.Contains(out, "Broken over two lines") {
		t.Error("the title was not flattened onto one line")
	}
}

// Clipping must not split a multi-byte rune — a broken rune makes the file
// invalid UTF-8, and some consumers reject the whole document for it.
func TestLLMsClipsOnRuneBoundaries(t *testing.T) {
	// NO SPACES, deliberately. With spaces the word-boundary cut that follows the
	// rune-boundary loop happens to land on a valid boundary anyway, so the loop
	// is never the thing doing the work — an earlier version of this test used a
	// spaced string and passed with the rune handling deleted, which is a test
	// that proves nothing.
	long := strings.Repeat("दक्षिण", 120) // Devanagari: 3 bytes per rune
	out := Render(LLMsDoc{
		Origin: "https://example.com",
		Posts:  []LLMsPost{{Title: "T", URL: "https://example.com/x", Summary: long}},
	})
	if !utf8.ValidString(out) {
		t.Fatal("clipping produced invalid UTF-8")
	}
	if !strings.Contains(out, "…") {
		t.Error("an over-long summary was not clipped")
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "- [T]") && len(l) > llmsSummaryMax+120 {
			t.Errorf("clipped line is still %d bytes", len(l))
		}
	}
}

func TestLLMsSaysSoWhenThereIsNothing(t *testing.T) {
	out := Render(LLMsDoc{Origin: "https://example.com", SiteName: "Example"})
	if !strings.Contains(out, "No posts have been published yet.") {
		t.Errorf("an empty site should say so rather than emit a bare heading:\n%s", out)
	}
	if strings.Contains(out, "## Posts") {
		t.Error("an empty site should not emit an empty Posts heading")
	}
}

func TestLLMsRefusesWithoutAnOrigin(t *testing.T) {
	if Render(LLMsDoc{SiteName: "Example"}) != "" {
		t.Error("without an origin every link would be relative — emit nothing instead")
	}
}

func TestLLMsSkipsIncompleteEntries(t *testing.T) {
	out := Render(LLMsDoc{
		Origin: "https://example.com",
		Posts: []LLMsPost{
			{Title: "Good", URL: "https://example.com/good"},
			{Title: "No URL"},
			{URL: "https://example.com/no-title"},
		},
	})
	if n := strings.Count(out, "\n- ["); n != 1 {
		t.Errorf("expected 1 complete entry, got %d\n%s", n, out)
	}
}
