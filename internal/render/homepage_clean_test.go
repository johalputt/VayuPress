package render

import (
	"strings"
	"testing"
)

// TestHomepageCleanByDefault locks the "clean homepage" behaviour: with the
// hero toggle off (the default) the homepage shows NO hero, and none of the
// removed runtime/stats chrome ever appears.
func TestHomepageCleanByDefault(t *testing.T) {
	SetActiveSettings(SiteSettings{Name: "Acme", Tagline: "A tagline", Description: "A description"})
	t.Cleanup(func() { SetActiveSettings(SiteSettings{}) })

	out, err := RenderHome("example.com", "1.0.0", nil, 0, 1, 1)
	if err != nil {
		t.Fatalf("RenderHome: %v", err)
	}
	for _, banned := range []string{
		"vayu-hero",                    // hero block hidden by default
		"vayu-stats",                   // published/stats wall removed
		"Sovereign Publishing Runtime", // old eyebrow default
		"runtime · normal",             // nav status pill removed
		"vayu-footer-badge",            // runtime · governed badge removed
	} {
		if strings.Contains(out, banned) {
			t.Errorf("clean homepage must not contain %q", banned)
		}
	}
}

// TestHomepageHeroOptIn proves the hero renders once the operator turns it on.
func TestHomepageHeroOptIn(t *testing.T) {
	SetActiveSettings(SiteSettings{Name: "Acme", Tagline: "Welcome", Description: "Words.", ShowHero: true})
	t.Cleanup(func() { SetActiveSettings(SiteSettings{}) })

	out, err := RenderHome("example.com", "1.0.0", nil, 0, 1, 1)
	if err != nil {
		t.Fatalf("RenderHome: %v", err)
	}
	if !strings.Contains(out, "vayu-hero") {
		t.Error("hero should render when ShowHero is true")
	}
	if !strings.Contains(out, "Welcome") {
		t.Error("hero should show the tagline as the headline")
	}
}

// TestHomepagePagination verifies the feed pager: absent on a single page,
// present with Newer/Older links + a page-aware canonical when multi-page.
func TestHomepagePagination(t *testing.T) {
	SetActiveSettings(SiteSettings{Name: "Acme"})
	t.Cleanup(func() { SetActiveSettings(SiteSettings{}) })

	arts := []HomeArticle{{Title: "A", Slug: "a"}}

	// Single page → no pagination control.
	out, err := RenderHome("example.com", "1.0.0", arts, 1, 1, 1)
	if err != nil {
		t.Fatalf("RenderHome: %v", err)
	}
	if strings.Contains(out, "vayu-pagination") {
		t.Error("single-page feed must not render a pager")
	}

	// Page 2 of 3 → pager with a Newer link back to "/", an Older link to
	// /page/3, a page-aware canonical, and rel=prev/next hints.
	out2, err := RenderHome("example.com", "1.0.0", arts, 90, 2, 3)
	if err != nil {
		t.Fatalf("RenderHome page 2: %v", err)
	}
	for _, want := range []string{
		"vayu-pagination",
		"Page 2 of 3",
		`href="/page/3"`,
		`<link rel="canonical" href="https://example.com/page/2">`,
		`<link rel="prev" href="https://example.com/">`,
		`<link rel="next" href="https://example.com/page/3">`,
	} {
		if !strings.Contains(out2, want) {
			t.Errorf("page 2 output missing %q", want)
		}
	}
}

// TestHomepageHasSearchBox confirms the public nav exposes a search box wired to
// the /search page when search is enabled, and hides it when disabled (the box
// is tied to the Meilisearch toggle).
func TestHomepageHasSearchBox(t *testing.T) {
	SetActiveSettings(SiteSettings{Name: "Acme"})
	t.Cleanup(func() { SetActiveSettings(SiteSettings{}) })

	SetSearchEnabled(true)
	out, err := RenderHome("example.com", "1.0.0", nil, 0, 1, 1)
	if err != nil {
		t.Fatalf("RenderHome: %v", err)
	}
	for _, want := range []string{`class="vayu-search"`, `action="/search"`, `name="q"`} {
		if !strings.Contains(out, want) {
			t.Errorf("homepage nav missing search box element %q", want)
		}
	}

	SetSearchEnabled(false)
	t.Cleanup(func() { SetSearchEnabled(true) })
	off, err := RenderHome("example.com", "1.0.0", nil, 0, 1, 1)
	if err != nil {
		t.Fatalf("RenderHome (search off): %v", err)
	}
	if strings.Contains(off, `class="vayu-search"`) {
		t.Error("search box must be hidden when search is disabled")
	}
}

// TestRenderSearch verifies the results page lists hits and prefills the query,
// and shows a friendly empty state for a no-match query.
func TestRenderSearch(t *testing.T) {
	SetActiveSettings(SiteSettings{Name: "Acme"})
	t.Cleanup(func() { SetActiveSettings(SiteSettings{}) })

	hits := []SearchHit{{Title: "Hello World", Slug: "hello-world"}}
	out, err := RenderSearch("example.com", "1.0.0", "hello", hits)
	if err != nil {
		t.Fatalf("RenderSearch: %v", err)
	}
	for _, want := range []string{"Hello World", `href="/hello-world"`, `value="hello"`, "noindex"} {
		if !strings.Contains(out, want) {
			t.Errorf("search page missing %q", want)
		}
	}

	empty, err := RenderSearch("example.com", "1.0.0", "zzz", nil)
	if err != nil {
		t.Fatalf("RenderSearch empty: %v", err)
	}
	if !strings.Contains(empty, "No posts found") {
		t.Error("expected an empty-state message for a no-match query")
	}
}

// TestHomepageTrendingWidgetWired guards the fix for the trending & pinned-posts
// widget that silently never loaded: the homepage must carry the widget
// container AND reference the widget script through a CONTENT-VERSIONED URL.
// The bare, unversioned /static/js/trending.js had no server route (404) and a
// CDN had cached that 404, so neither trending nor pinned posts ever rendered.
func TestHomepageTrendingWidgetWired(t *testing.T) {
	SetActiveSettings(SiteSettings{Name: "Acme"})
	t.Cleanup(func() { SetActiveSettings(SiteSettings{}) })

	out, err := RenderHome("example.com", "1.0.0", nil, 0, 1, 1)
	if err != nil {
		t.Fatalf("RenderHome: %v", err)
	}
	if !strings.Contains(out, "data-vayu-trending") {
		t.Error("homepage must include the trending/pinned widget container")
	}
	if !strings.Contains(out, `src="/static/js/trending.js?v=`) {
		t.Error("homepage must reference the content-versioned trending widget script")
	}
	if strings.Contains(out, `src="/static/js/trending.js"`) {
		t.Error("trending widget script must not use a bare, unversioned URL (CDN-cacheable 404)")
	}
}
