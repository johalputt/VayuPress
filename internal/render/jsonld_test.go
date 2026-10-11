// SPDX-License-Identifier: Apache-2.0

package render

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/db"
	"github.com/microcosm-cc/bluemonday"
)

// TestArticleJSONLDUsesSettings proves the BlogPosting JSON-LD reflects the
// operator's site author + name rather than the old hardcoded values, and that
// the share image is included when present.
func TestArticleJSONLDUsesSettings(t *testing.T) {
	policy = bluemonday.UGCPolicy()
	config.Cfg.Domain = "example.com"
	SetActiveSettings(SiteSettings{Name: "Acme Press", Author: "Jane Writer"})
	t.Cleanup(func() { SetActiveSettings(SiteSettings{}) })

	art := db.Article{
		Title: "Hello", Slug: "hello", Content: "<p>Body.</p>",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	out, err := RenderArticleWithMeta(art, ArticleLayoutDefault, nil, ArticleMetaOverrides{OGImage: "https://example.com/share.png"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, `"@type":"BlogPosting"`) {
		t.Error("expected BlogPosting JSON-LD type")
	}
	if !strings.Contains(out, `"name":"Jane Writer"`) {
		t.Error("author name should come from settings (Jane Writer)")
	}
	if !strings.Contains(out, `"name":"Acme Press"`) {
		t.Error("publisher name should come from settings (Acme Press)")
	}
	if strings.Contains(out, "Ankush Choudhary Johal") || strings.Contains(out, `"name":"VayuPress"`) {
		t.Error("JSON-LD must not contain the old hardcoded author/publisher")
	}
	// html/template JSON-escapes forward slashes (/ → \/), so match loosely.
	if !strings.Contains(out, `"image":`) || !strings.Contains(out, "share.png") {
		t.Error("JSON-LD should include the share image when present")
	}
	if !strings.Contains(out, `"mainEntityOfPage"`) {
		t.Error("JSON-LD should include mainEntityOfPage")
	}
}

// The post's graph reaches the page in every theme, built from what the page
// renders: the breadcrumb names the site and the post, the author links their
// page, and the head asks for full snippets and names the author. Parsed, not
// pattern-matched: the publisher in the same block names the site too, so a
// page-wide match would pass with the breadcrumb blank.
func TestBothThemesEmitThePostsGraph(t *testing.T) {
	policy = bluemonday.UGCPolicy()
	config.Cfg.Domain = "example.com"
	SetActiveSettings(SiteSettings{Name: "Acme Press", Author: "Jane Writer"})
	prevInfo := AuthorInfoFn
	AuthorInfoFn = func(name string) (string, string) { return "jane", "" }
	t.Cleanup(func() { SetActiveSettings(SiteSettings{}); AuthorInfoFn = prevInfo })
	art := db.Article{
		Title: "Hello", Slug: "hello", Content: "<p>Body text here.</p><h2>FAQ</h2><h3>Why?</h3><p>Because.</p>",
		Tags: []string{"site-reliability"}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	for _, name := range []string{"default", "halcyon"} {
		if name == "halcyon" {
			withHalcyon(t, nil)
			SetActiveSettings(SiteSettings{Name: "Acme Press", Author: "Jane Writer"})
		}
		out, err := RenderArticleWithMeta(art, ArticleLayoutDefault, nil, ArticleMetaOverrides{})
		if err != nil {
			t.Fatalf("%s: render: %v", name, err)
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(ldBlockContaining(t, out, `"BlogPosting"`)), &doc); err != nil {
			t.Fatalf("%s: the graph does not parse: %v", name, err)
		}
		nodes := map[string]map[string]any{}
		for _, n := range doc["@graph"].([]any) {
			m := n.(map[string]any)
			nodes[m["@type"].(string)] = m
		}
		crumbs := nodes["BreadcrumbList"]["itemListElement"].([]any)
		if crumbs[0].(map[string]any)["name"] != "Acme Press" || crumbs[1].(map[string]any)["name"] != "Hello" {
			t.Errorf("%s: breadcrumb = %v", name, crumbs)
		}
		if a := nodes["BlogPosting"]["author"].(map[string]any); a["url"] != "https://example.com/author/jane" {
			t.Errorf("%s: author = %v", name, a)
		}
		if nodes["BlogPosting"]["articleSection"] != "site reliability" {
			t.Errorf("%s: section = %v", name, nodes["BlogPosting"]["articleSection"])
		}
		if nodes["FAQPage"] == nil {
			t.Errorf("%s: the FAQ section is not in the graph", name)
		}
		for _, want := range []string{`<meta name="robots" content="max-image-preview:large, max-snippet:-1, max-video-preview:-1">`, `<meta name="author" content="Jane Writer">`} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: the head lacks %s", name, want)
			}
		}
		// html/template would turn the element into visible text if the field
		// were a plain string rather than template.HTML.
		if strings.Contains(out, "&lt;script type=&#34;application/ld+json&#34;&gt;") {
			t.Errorf("%s: the graph was HTML-escaped into visible text", name)
		}
	}
}

// ldBlockContaining returns the single ld+json script body holding needle.
// Assertions about one JSON-LD block must not be satisfied by another block on
// the same page.
func ldBlockContaining(t *testing.T, page, needle string) string {
	t.Helper()
	const open = `<script type="application/ld+json">`
	for rest := page; ; {
		i := strings.Index(rest, open)
		if i < 0 {
			t.Fatalf("no ld+json block containing %q", needle)
		}
		rest = rest[i+len(open):]
		end := strings.Index(rest, "</script>")
		if end < 0 {
			t.Fatalf("unterminated ld+json block while looking for %q", needle)
		}
		if body := rest[:end]; strings.Contains(body, needle) {
			return body
		}
		rest = rest[end:]
	}
}
