// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/microcosm-cc/bluemonday"

	"github.com/johalputt/vayupress/internal/ads"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
)

const siteAdsPage = `<!DOCTYPE html><html><head></head><body><main id="main-content">` +
	`<div class="content" itemprop="articleBody"></div></article></main></body></html>`

// Each site shows its own ads and only its own: a hosted site that has set up
// none shows none (it showed the install's slots, AdSense ID and disclosure),
// and the install never shows a site's.
func TestEachSiteShowsOnlyItsOwnAds(t *testing.T) {
	a, _, blog := hostedBlogApp(t)
	a.ads, a.policy = ads.New(dbpkg.DB), bluemonday.UGCPolicy()
	ctx := context.Background()
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{
		settings.KeyFeatureAds: "on", settings.KeyFeatureGoogleAds: "on", settings.KeyFeatureAffiliate: "on",
		settings.KeyAdsenseClient: "ca-pub-1111111111111111", settings.KeyAffiliateDisclosure: "Install disclosure text"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ads.Create(ctx, "", ads.SlotInput{Name: "Install banner", Placement: ads.PlacementHeader, Kind: ads.KindImage, ImageURL: "/media/install.png", AltText: "install-ad", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ads.Create(ctx, "", ads.SlotInput{Name: "Install unit", Placement: ads.PlacementFooter, Kind: ads.KindAdSense, HTML: "1234567890", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	install := ads.Page{Path: "/hello", Post: true}
	site := ads.Page{Site: blog.ID, Path: "/hello", Post: true}

	out, adsense := a.injectArticleAds(ctx, "n", siteAdsPage, site)
	if out != siteAdsPage || adsense {
		t.Errorf("a site with no ads of its own shows the install's:\n%s", out)
	}

	if err := a.siteSettings.SetMany(ctx, settings.ForDomain(blog.ID), map[string]string{settings.KeyFeatureAds: "on"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ads.Create(ctx, blog.ID, ads.SlotInput{Name: "Site banner", Placement: ads.PlacementHeader, Kind: ads.KindImage, ImageURL: "/media/site.png", AltText: "site-ad", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	out, adsense = a.injectArticleAds(ctx, "n", siteAdsPage, site)
	if !strings.Contains(out, "site-ad") || strings.Contains(out, "install-ad") || adsense ||
		strings.Contains(out, "ca-pub-1111111111111111") || strings.Contains(out, "Install disclosure text") {
		t.Errorf("the site's page is not drawn with its own ads alone:\n%s", out)
	}
	if out, _ := a.injectHomeAds(ctx, "n", siteAdsPage, ads.Page{Site: blog.ID, Path: "/"}); strings.Contains(out, "install-ad") || !strings.Contains(out, "site-ad") {
		t.Errorf("the site's home is not drawn with its own ads alone:\n%s", out)
	}

	out, adsense = a.injectArticleAds(ctx, "n", siteAdsPage, install)
	if !strings.Contains(out, "install-ad") || strings.Contains(out, "site-ad") || !adsense || !strings.Contains(out, "Install disclosure text") {
		t.Errorf("the install's own page lost its ads or shows the site's:\n%s", out)
	}
}
