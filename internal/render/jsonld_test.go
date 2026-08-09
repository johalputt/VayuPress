// SPDX-License-Identifier: Apache-2.0

package render

import (
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

// TestArticleEmitsBreadcrumbJSONLD pins the wiring, not the builder.
// seo.BreadcrumbJSONLD was written, tested and never called — the deadcode gate
// is what noticed. A unit test on the builder passes just as happily when
// nothing renders it, so this one asserts the crumb trail reaches the page, and
// that it names the same site and article the BlogPosting block beside it does.
func TestArticleEmitsBreadcrumbJSONLD(t *testing.T) {
	policy = bluemonday.UGCPolicy()
	config.Cfg.Domain = "example.com"
	SetActiveSettings(SiteSettings{Name: "Acme Press"})
	t.Cleanup(func() { SetActiveSettings(SiteSettings{}) })

	art := db.Article{
		Title: "Hello", Slug: "hello", Content: "<p>Body.</p>",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	out, err := RenderArticleWithMeta(art, ArticleLayoutDefault, nil, ArticleMetaOverrides{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, `"@type":"BreadcrumbList"`) {
		t.Fatal("the article page emits no BreadcrumbList; seo.BreadcrumbJSONLD is unreachable again")
	}
	// Assert inside the breadcrumb block, not across the whole page. The
	// BlogPosting block on the same page already carries "name":"Acme Press" as
	// its publisher, so a page-wide Contains passes even when the crumb trail
	// names nobody — a mutation that blanked the site name proved exactly that.
	crumb := ldBlockContaining(t, out, "BreadcrumbList")
	if !strings.Contains(crumb, `"name":"Acme Press"`) {
		t.Errorf("the first crumb must name the site, from the same settings the page renders; got %s", crumb)
	}
	if !strings.Contains(crumb, `"name":"Hello"`) {
		t.Errorf("the second crumb must name the article; got %s", crumb)
	}
	// The crumb must not be emitted as escaped text: html/template would turn
	// the script element into &lt;script&gt; if the field were a plain string
	// rather than template.HTML, and every consumer would see markup, not data.
	if strings.Contains(out, "&lt;script type=&#34;application/ld+json&#34;&gt;") {
		t.Error("the breadcrumb block was HTML-escaped into visible text")
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
