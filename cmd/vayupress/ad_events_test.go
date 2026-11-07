// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/ads"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/settings"
)

// A site's pages count their own enabled slots and nothing else: another
// site's slot, a switched-off slot, a repeat and more than a page shows are
// not counted, and a reader who asks not to be tracked is not counted at all.
func TestAdBeaconsCountOnlyTheSitesOwnSlots(t *testing.T) {
	a, shop, blog := hostedBlogApp(t)
	a.ads, a.adCounts = ads.New(dbpkg.DB), ads.NewCounter(dbpkg.DB)
	ctx := context.Background()
	mk := func(site, name string, on bool) string {
		sl, err := a.ads.Create(ctx, site, ads.SlotInput{Name: name, Placement: ads.PlacementHeader, Kind: ads.KindImage, ImageURL: "/x.png", Enabled: on})
		if err != nil {
			t.Fatal(err)
		}
		return sl.ID
	}
	mine, off, theirs := mk(blog.ID, "Mine", true), mk(blog.ID, "Off", false), mk(shop.ID, "Theirs", true)
	send := func(body string, d domain.Domain, dnt bool) {
		r2 := httptest.NewRequest(http.MethodPost, "/api/v1/ads/events", strings.NewReader(body)).
			WithContext(requestFor("/", d).Context())
		if dnt {
			r2.Header.Set("DNT", "1")
		}
		a.handleAdEvents(httptest.NewRecorder(), r2)
	}
	send(`{"seen":["`+mine+`","`+mine+`","`+off+`","`+theirs+`"]}`, blog, false)
	send(`{"click":"`+mine+`"}`, blog, false)
	send(`{"click":"`+theirs+`"}`, blog, false)
	send(`{"seen":["`+mine+`"]}`, blog, true)
	if err := a.adCounts.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := a.ads.Totals(ctx, blog.ID, 30, time.Now())
	if got[mine] != (ads.Counts{Seen: 1, Clicks: 1}) || len(got) != 1 {
		t.Errorf("the site's counts: %+v", got)
	}
	if other, _ := a.ads.Totals(ctx, shop.ID, 30, time.Now()); len(other) != 0 {
		t.Errorf("another site's slot was counted from this site's pages: %+v", other)
	}
}

// The analytics script carries the ad beacon on a site whose Advertising is
// on, and not on one whose is off.
func TestTheAdBeaconRidesOnlyWhereAdsAreOn(t *testing.T) {
	a, shop, blog := hostedBlogApp(t)
	a.ads = ads.New(dbpkg.DB)
	if err := a.siteSettings.SetMany(context.Background(), settings.ForDomain(shop.ID), map[string]string{settings.KeyFeatureAds: "on"}); err != nil {
		t.Fatal(err)
	}
	script := func(d domain.Domain) string {
		rec := httptest.NewRecorder()
		a.handleAnalyticsScript(rec, requestFor("/static/js/vayu-analytics.js", d))
		return rec.Body.String()
	}
	if !strings.Contains(script(shop), "/api/v1/ads/events") || strings.Contains(script(blog), "/api/v1/ads/events") {
		t.Error("the ad beacon is not where Advertising is on alone")
	}
	if !strings.Contains(ads.Render([]ads.Slot{{ID: "ad_9", Name: "n", Placement: ads.PlacementHeader, Kind: ads.KindImage, ImageURL: "/x.png", Enabled: true}}, ads.RenderConfig{}), `data-ad-id="ad_9"`) {
		t.Error("a drawn slot does not carry its id for the beacon")
	}
}
