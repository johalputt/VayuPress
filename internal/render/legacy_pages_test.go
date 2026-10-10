// SPDX-License-Identifier: Apache-2.0

package render

import (
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/theme"
)

// legacyPages renders every public page the shared templates draw, from fixed
// inputs, so the output can be pinned byte for byte.
func legacyPages(t *testing.T) map[string]string {
	t.Helper()
	fixed := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	s := SiteSettings{Name: "Johal.in", Tagline: "Notes", Description: "A description", Author: "Ankush",
		AuthorBio: "Builder.", ShowMembership: true, CommentsEnabled: true,
		NavJSON:    `[{"label":"Home","href":"/"},{"label":"Sponsor","href":"https://example.com/s"}]`,
		FooterJSON: `{"tagline":"Hub","columns":[{"title":"Explore","links":[{"label":"Home","href":"/"}]}],"copyright":"© 2026 {site}"}`}
	prev, prevDomain := getActiveSettings(), config.Cfg.Domain
	SetActiveSettings(s)
	// The domain the golden pages were rendered with; other tests in the
	// package set their own.
	config.Cfg.Domain = ""
	t.Cleanup(func() { SetActiveSettings(prev); config.Cfg.Domain = prevDomain })
	body, err := os.ReadFile("testdata/halcyon/technical-debt-visible-paydown-guide.html")
	if err != nil {
		t.Fatal(err)
	}
	art := db.Article{ID: "1", Title: "Technical Debt", Slug: "debt", Content: string(body),
		Tags: []string{"engineering-culture", "architecture"}, CreatedAt: fixed, UpdatedAt: fixed}
	related := []RelatedArticle{{Title: "Code Review", Slug: "review", CreatedAt: fixed}}
	cards := []HomeArticle{{Title: "One", Slug: "one", Excerpt: "An excerpt.", Tags: []string{"devops"}, CreatedAt: fixed},
		{Title: "Two", Slug: "two", Image: "/media/x.png", Author: "Ankush", CreatedAt: fixed}}
	out := map[string]string{}
	var e error
	if out["home"], e = RenderHomeWithSettings(s, "example.com", "v1", cards, 2, 1, 2); e != nil {
		t.Fatal(e)
	}
	if out["article"], e = RenderArticleWithMeta(art, ArticleLayoutDefault, related, ArticleMetaOverrides{}); e != nil {
		t.Fatal(e)
	}
	if out["search"], e = RenderSearch("example.com", "v1", "debt", []SearchHit{{Title: "One", Slug: "one", Tags: []string{"a"}, CreatedAt: fixed}}); e != nil {
		t.Fatal(e)
	}
	out["notfound"] = Render404("example.com", "v1")
	if out["tags"], e = RenderTagIndex("example.com", "v1", []TagInfo{{Name: "devops", Count: 3}}, 3); e != nil {
		t.Fatal(e)
	}
	if out["tag"], e = RenderTagPage("example.com", "v1", "devops", cards, 2); e != nil {
		t.Fatal(e)
	}
	return out
}

// scriptVersions are the content hashes of the three shared widget scripts
// whose pictographs moved into aria-hidden spans with Halcyon. Their ?v=
// names the new bytes, and that is the only difference the shared pages may
// show.
var scriptVersions = regexp.MustCompile(`/static/js/(trending|comments|search)\.js\?v=[0-9a-f]+`)

func normaliseLegacy(s string) string { return scriptVersions.ReplaceAllString(s, "/static/js/$1.js?v=") }

func TestWriteLegacyGolden(t *testing.T) {
	dir := os.Getenv("LEGACY_GOLDEN_OUT")
	if dir == "" {
		t.Skip("set LEGACY_GOLDEN_OUT")
	}
	Init(t.TempDir())
	for name, html := range legacyPages(t) {
		if err := os.WriteFile(dir+"/"+name+".html", []byte(normaliseLegacy(html)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Every theme but Halcyon renders exactly as it did before Halcyon existed.
// testdata/legacy holds each public page as the commit before Halcyon rendered
// it from these inputs (TestWriteLegacyGolden, run there); this build must
// match it byte for byte, the three widget script versions aside.
func TestSharedTemplatesUnchangedByHalcyon(t *testing.T) {
	Init(t.TempDir())
	ActivateTheme(theme.Default(), "")
	for name, html := range legacyPages(t) {
		want, err := os.ReadFile("testdata/legacy/" + name + ".html")
		if err != nil {
			t.Fatal(err)
		}
		if got := normaliseLegacy(html); got != string(want) {
			i := 0
			for i < len(got) && i < len(want) && got[i] == want[i] {
				i++
			}
			t.Errorf("%s differs from the pre-Halcyon page at byte %d:\n got …%s…\nwant …%s…", name, i,
				got[max(0, i-80):min(len(got), i+80)], string(want)[max(0, i-80):min(len(want), i+80)])
		}
	}
}
