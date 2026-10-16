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
		"# Example\n\n> Writing about things.\n\n",
		"## Posts\n\n- [First post](https://example.com/first-post): An excerpt. (2026-01-02)\n",
		"## Optional\n\n- [Home](https://example.com/): the newest writing\n" +
			"- [Feed](https://example.com/feed.xml): the newest posts, as RSS\n" +
			"- [Sitemap](https://example.com/sitemap.xml): every page on the site\n",
		"This list was made 2026-02-03.",
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
	body := section(out, "Posts")
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
	if n := strings.Count(section(out, "Posts"), "\n- ["); n != 1 {
		t.Errorf("expected 1 complete entry, got %d\n%s", n, out)
	}
}

// section is the body of one "## name" section: from its heading to the next.
func section(out, name string) string {
	i := strings.Index(out, "## "+name+"\n")
	if i < 0 {
		return ""
	}
	rest := out[i+len("## "+name+"\n"):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// The convention's shape: what to read first, the topics, the posts, then
// what may be skipped; every reference a link, and a title's square brackets
// escaped so they cannot end its link text.
func TestLLMsIsLaidOutAsTheConventionAsks(t *testing.T) {
	out := Render(LLMsDoc{
		SiteName: "Example", Origin: "https://example.com", Tagline: "Notes on systems", Description: "Writing about things.", Author: "Jane Roe",
		Pinned: []LLMsPost{{Title: "[Go] Start", URL: "https://example.com/start", Summary: "Begin here."}},
		Topics: []LLMsLink{{Name: "kubernetes", URL: "https://example.com/tags/kubernetes", Note: "12 posts"}},
		Posts:  []LLMsPost{{Title: "Newer", URL: "https://example.com/newer", Published: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}},
	})
	if got := section(out, "Start here"); got != "\n- [\\[Go\\] Start](https://example.com/start): Begin here.\n" {
		t.Errorf("Start here: %q", got)
	}
	if got := section(out, "Topics"); got != "\n- [kubernetes](https://example.com/tags/kubernetes): 12 posts\n" {
		t.Errorf("Topics: %q", got)
	}
	order := []string{"## Start here", "## Topics", "## Posts", "## Optional"}
	for i := 1; i < len(order); i++ {
		if strings.Index(out, order[i-1]) > strings.Index(out, order[i]) {
			t.Errorf("%s comes after %s", order[i-1], order[i])
		}
	}
	if !strings.Contains(out, "\n\nNotes on systems. Written by Jane Roe. ") {
		t.Errorf("the detail paragraph:\n%s", out)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "- ") && !strings.HasPrefix(l, "- [") {
			t.Errorf("a list line that is not a link: %q", l)
		}
	}
}
