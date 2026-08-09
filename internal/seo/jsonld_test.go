// SPDX-License-Identifier: Apache-2.0

package seo

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Structured data fails silently: a malformed block is dropped by every consumer
// and the page still looks correct to a person. So these tests parse the output
// rather than pattern-match it — the only way to know a machine can read it.

func parseLD(t *testing.T, h string) map[string]any {
	t.Helper()
	const open = `<script type="application/ld+json">`
	const closeTag = `</script>`
	if !strings.HasPrefix(h, open) || !strings.HasSuffix(h, closeTag) {
		t.Fatalf("output is not a complete ld+json element: %.80s", h)
	}
	body := h[len(open) : len(h)-len(closeTag)]
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("emitted JSON-LD does not parse: %v\n%s", err, body)
	}
	return out
}

func sampleHome() HomeDoc {
	return HomeDoc{
		Origin:      "https://example.com",
		Canonical:   "/",
		SiteName:    "Example",
		Description: "Writing about things.",
		SearchPath:  "/search",
		Posts: []HomePost{{
			Title:       "First post",
			Slug:        "first-post",
			Excerpt:     "An excerpt.",
			Published:   time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			Author:      "A Person",
			AbsoluteURL: "https://example.com/first-post",
		}},
	}
}

func TestHomeJSONLDParsesAndCarriesTheGraph(t *testing.T) {
	doc := parseLD(t, string(HomeJSONLD(sampleHome())))
	if doc["@context"] != "https://schema.org" {
		t.Errorf("@context = %v", doc["@context"])
	}
	graph, ok := doc["@graph"].([]any)
	if !ok || len(graph) < 3 {
		t.Fatalf("expected a @graph with WebSite, Organization and Blog; got %v", doc["@graph"])
	}
	types := map[string]bool{}
	for _, n := range graph {
		if m, ok := n.(map[string]any); ok {
			types[m["@type"].(string)] = true
		}
	}
	for _, want := range []string{"WebSite", "Organization", "Blog"} {
		if !types[want] {
			t.Errorf("@graph has no %s node", want)
		}
	}
}

// A SearchAction pointing at an endpoint that does not exist is worse than none:
// it is a promise to a search engine that 404s.
func TestHomeJSONLDOnlyClaimsSearchWhenSearchExists(t *testing.T) {
	with := parseLD(t, string(HomeJSONLD(sampleHome())))
	if !strings.Contains(mustJSON(t, with), "SearchAction") {
		t.Error("search is enabled but no SearchAction was emitted")
	}

	d := sampleHome()
	d.SearchPath = ""
	without := parseLD(t, string(HomeJSONLD(d)))
	if strings.Contains(mustJSON(t, without), "SearchAction") {
		t.Error("search is OFF but a SearchAction was emitted — it would 404")
	}
}

// The reason this is built with encoding/json rather than string interpolation.
func TestHomeJSONLDCannotBreakOutOfTheScriptElement(t *testing.T) {
	d := sampleHome()
	d.SiteName = `Evil</script><script>alert(1)</script>`
	d.Posts[0].Title = `Also "quoted" </SCRIPT> and \ backslash`

	out := string(HomeJSONLD(d))
	// Exactly one opening and one closing tag: the payload must not have created
	// another.
	if strings.Count(out, "<script") != 1 || strings.Count(out, "</script>") != 1 {
		t.Fatalf("payload escaped the script element:\n%s", out)
	}
	doc := parseLD(t, out)
	// And it must still be readable — escaping that corrupts the value is its
	// own failure.
	if !strings.Contains(mustJSON(t, doc), "Evil") {
		t.Error("the site name was lost entirely rather than escaped")
	}
}

func TestHomeJSONLDRefusesToGuess(t *testing.T) {
	d := sampleHome()
	d.Origin = ""
	if HomeJSONLD(d) != "" {
		t.Error("no origin should emit nothing rather than a relative graph")
	}

	d = sampleHome()
	d.Posts = append(d.Posts, HomePost{Title: "No URL"}, HomePost{AbsoluteURL: "https://example.com/x"})
	doc := parseLD(t, string(HomeJSONLD(d)))
	for _, n := range doc["@graph"].([]any) {
		m := n.(map[string]any)
		if m["@type"] != "Blog" {
			continue
		}
		if got := len(m["blogPost"].([]any)); got != 1 {
			t.Errorf("blogPost listed %d entries; incomplete posts must be skipped", got)
		}
	}
}

func TestBreadcrumbJSONLD(t *testing.T) {
	doc := parseLD(t, string(BreadcrumbJSONLD("https://example.com", "Example", "First post", "https://example.com/first-post")))
	if doc["@type"] != "BreadcrumbList" {
		t.Fatalf("@type = %v", doc["@type"])
	}
	items := doc["itemListElement"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 crumbs, got %d", len(items))
	}
	first := items[0].(map[string]any)
	if first["position"].(float64) != 1 || first["item"] != "https://example.com/" {
		t.Errorf("first crumb is wrong: %v", first)
	}

	if BreadcrumbJSONLD("", "x", "y", "z") != "" {
		t.Error("no origin should emit nothing")
	}
	if BreadcrumbJSONLD("https://example.com", "x", "", "z") != "" {
		t.Error("no title should emit nothing")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	return string(b)
}
