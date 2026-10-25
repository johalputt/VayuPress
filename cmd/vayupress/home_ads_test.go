// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/microcosm-cc/bluemonday"

	"github.com/johalputt/vayupress/internal/ads"
	"github.com/johalputt/vayupress/internal/settings"
)

// homeSkeleton is a trimmed stand-in for the rendered homepage carrying the two
// anchors injectHomeAds targets: the top of <main> and the trending strip that
// closes the content column.
const homeSkeleton = `<!DOCTYPE html><html><head></head><body>` +
	`<main id="main-content">` +
	`<div class="vayu-post-list"></div>` +
	`<section class="vayu-trending" data-vayu-trending hidden></section>` +
	`</main></body></html>`

func newHomeAdsApp(t *testing.T) (*App, context.Context) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE site_settings (scope TEXT NOT NULL DEFAULT '', key TEXT NOT NULL, value TEXT NOT NULL DEFAULT '', updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY(scope,key))`); err != nil {
		t.Fatalf("settings schema: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE ad_slots(id TEXT PRIMARY KEY,name TEXT NOT NULL,placement TEXT NOT NULL DEFAULT 'below_post',kind TEXT NOT NULL DEFAULT 'image',image_url TEXT NOT NULL DEFAULT '',link_url TEXT NOT NULL DEFAULT '',alt_text TEXT NOT NULL DEFAULT '',html TEXT NOT NULL DEFAULT '',enabled INTEGER NOT NULL DEFAULT 1,sort INTEGER NOT NULL DEFAULT 0,owner_email TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT 'approved',order_ref TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,show_on TEXT NOT NULL DEFAULT '',show_paths TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatalf("ads schema: %v", err)
	}
	a := &App{
		siteSettings: settings.New(db),
		ads:          ads.New(db),
		policy:       bluemonday.UGCPolicy(),
	}
	return a, context.Background()
}

// TestInjectHomeAdsOffIsNoOp proves that with the Ads feature disabled the
// homepage HTML is returned byte-identical and never claims to use AdSense.
func TestInjectHomeAdsOffIsNoOp(t *testing.T) {
	a, ctx := newHomeAdsApp(t)
	out, adsense := a.injectHomeAds(ctx, "n0nce", homeSkeleton, ads.Page{Path: "/"})
	if out != homeSkeleton {
		t.Errorf("Ads off must be a no-op; HTML changed:\n%s", out)
	}
	if adsense {
		t.Error("Ads off must not report AdSense")
	}
}

// TestInjectHomeAdsPlacesHeaderAndFooter proves an enabled header slot lands at
// the top of <main> and an enabled footer slot lands just above the trending
// strip (closing the content column), while self-hosted image ads never trip the
// AdSense path (so the page can still be disk-cached).
func TestInjectHomeAdsPlacesHeaderAndFooter(t *testing.T) {
	a, ctx := newHomeAdsApp(t)
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{settings.KeyFeatureAds: "on"}); err != nil {
		t.Fatalf("enable ads: %v", err)
	}
	if _, err := a.ads.Create(ctx, ads.SlotInput{Name: "Top", Placement: ads.PlacementHeader, Kind: ads.KindImage, ImageURL: "/media/top.png", AltText: "Top", Enabled: true}); err != nil {
		t.Fatalf("create header slot: %v", err)
	}
	if _, err := a.ads.Create(ctx, ads.SlotInput{Name: "Bottom", Placement: ads.PlacementFooter, Kind: ads.KindImage, ImageURL: "/media/bottom.png", AltText: "Bottom", Enabled: true}); err != nil {
		t.Fatalf("create footer slot: %v", err)
	}

	out, adsense := a.injectHomeAds(ctx, "n0nce", homeSkeleton, ads.Page{Path: "/"})
	if adsense {
		t.Error("image ads must not report AdSense")
	}
	if !strings.Contains(out, "/media/top.png") || !strings.Contains(out, "/media/bottom.png") {
		t.Fatalf("both creatives should render:\n%s", out)
	}
	// Header ad sits immediately after the <main> open tag.
	mainIdx := strings.Index(out, `<main id="main-content">`)
	topIdx := strings.Index(out, "/media/top.png")
	listIdx := strings.Index(out, `<div class="vayu-post-list">`)
	if !(mainIdx < topIdx && topIdx < listIdx) {
		t.Errorf("header ad must sit between <main> and the post list (main=%d top=%d list=%d)", mainIdx, topIdx, listIdx)
	}
	// Footer ad sits just before the trending strip.
	bottomIdx := strings.Index(out, "/media/bottom.png")
	trendingIdx := strings.Index(out, `<section class="vayu-trending"`)
	if !(listIdx < bottomIdx && bottomIdx < trendingIdx) {
		t.Errorf("footer ad must sit between the post list and the trending strip (list=%d bottom=%d trending=%d)", listIdx, bottomIdx, trendingIdx)
	}
}

// TestInjectHomeAdsAdSenseWidensAndLoads proves an enabled AdSense header slot
// reports usesAdSense (so the caller widens the CSP + skips the cache) and emits
// the loader script into <head>.
func TestInjectHomeAdsAdSenseWidensAndLoads(t *testing.T) {
	a, ctx := newHomeAdsApp(t)
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{
		settings.KeyFeatureAds:       "on",
		settings.KeyFeatureGoogleAds: "on",
		settings.KeyAdsenseClient:    "ca-pub-9876543210",
	}); err != nil {
		t.Fatalf("enable ads: %v", err)
	}
	if _, err := a.ads.Create(ctx, ads.SlotInput{Name: "Top", Placement: ads.PlacementHeader, Kind: ads.KindAdSense, HTML: "1234567890", Enabled: true}); err != nil {
		t.Fatalf("create adsense slot: %v", err)
	}

	out, adsense := a.injectHomeAds(ctx, "n0nce", homeSkeleton, ads.Page{Path: "/"})
	if !adsense {
		t.Fatal("AdSense slot must report usesAdSense so the caller widens the CSP + skips the cache")
	}
	if !strings.Contains(out, "googlesyndication.com") || !strings.Contains(out, `nonce="n0nce"`) {
		t.Errorf("AdSense loader with the page nonce must land in <head>:\n%s", out)
	}
	if !strings.Contains(out, `data-ad-client="ca-pub-9876543210"`) {
		t.Errorf("AdSense unit missing publisher id:\n%s", out)
	}
}

// A post's and a page's ads follow the slots' choice of pages, and the
// never-list takes every ad off the pages it names, home included.
func TestAdsFollowThePageTheyAreOn(t *testing.T) {
	a, ctx := newHomeAdsApp(t)
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{settings.KeyFeatureAds: "on"}); err != nil {
		t.Fatal(err)
	}
	for _, in := range []ads.SlotInput{
		{Name: "Posts", Placement: ads.PlacementHeader, Kind: ads.KindImage, ImageURL: "/media/posts.png", Enabled: true, ShowOn: ads.ShowPosts},
		{Name: "Pages", Placement: ads.PlacementHeader, Kind: ads.KindImage, ImageURL: "/media/pages.png", Enabled: true, ShowOn: ads.ShowPages},
		{Name: "Everywhere", Placement: ads.PlacementHeader, Kind: ads.KindImage, ImageURL: "/media/all.png", Enabled: true},
	} {
		if _, err := a.ads.Create(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	article := `<html><head></head><body><main id="main-content"><article><div class="content" itemprop="articleBody">x</div></article></main></div></body></html>`
	shown := func(out string) []string {
		var got []string
		for _, img := range []string{"posts", "pages", "all"} {
			if strings.Contains(out, "/media/"+img+".png") {
				got = append(got, img)
			}
		}
		return got
	}
	post, _ := a.injectArticleAds(ctx, "n", article, ads.Page{Path: "/a-post", Post: true})
	page, _ := a.injectArticleAds(ctx, "n", article, ads.Page{Path: "/about", IsPage: true})
	home, _ := a.injectHomeAds(ctx, "n", homeSkeleton, ads.Page{Path: "/"})
	if g := strings.Join(shown(post), ","); g != "posts,all" {
		t.Errorf("a post shows %q, want posts,all", g)
	}
	if g := strings.Join(shown(page), ","); g != "pages,all" {
		t.Errorf("a page shows %q, want pages,all", g)
	}
	if g := strings.Join(shown(home), ","); g != "all" {
		t.Errorf("the home page shows %q, want all", g)
	}
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{settings.KeyAdsNever: "/a-post\n/"}); err != nil {
		t.Fatal(err)
	}
	post, _ = a.injectArticleAds(ctx, "n", article, ads.Page{Path: "/a-post", Post: true})
	home, _ = a.injectHomeAds(ctx, "n", homeSkeleton, ads.Page{Path: "/"})
	page, _ = a.injectArticleAds(ctx, "n", article, ads.Page{Path: "/about", IsPage: true})
	if g := shown(post); len(g) != 0 {
		t.Errorf("a post on the never-list shows %v", g)
	}
	if g := shown(home); len(g) != 0 {
		t.Errorf("the home page on the never-list shows %v", g)
	}
	if g := strings.Join(shown(page), ","); g != "pages,all" {
		t.Errorf("a page not on the never-list shows %q, want pages,all", g)
	}
}
