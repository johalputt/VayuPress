// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/microcosm-cc/bluemonday"

	"github.com/johalputt/vayupress/internal/ads"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/users"
)

func siteAdsRequest(method, body string, d domain.Domain) *http.Request {
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
	req := withUser(httptest.NewRequest(method, "/os/d/"+d.ID+"/ads", strings.NewReader(body)), admin)
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), ctxScopedDomainKey, d)
	ctx = context.WithValue(ctx, ctxScopeKey, settings.ForDomain(d.ID))
	return req.WithContext(ctx)
}

// A site's Ads page lists its own slots, edits them at its own address, and
// saves its own settings; the install's slots and settings stay out of it.
func TestASitesAdsPageIsItsOwn(t *testing.T) {
	a, _, blog := hostedBlogApp(t)
	a.ads, a.policy = ads.New(dbpkg.DB), bluemonday.UGCPolicy()
	ctx := context.Background()
	if _, err := a.ads.Create(ctx, "", ads.SlotInput{Name: "Install banner", Placement: ads.PlacementHeader, Kind: ads.KindImage, ImageURL: "/i.png", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ads.Create(ctx, blog.ID, ads.SlotInput{Name: "Site banner", Placement: ads.PlacementHeader, Kind: ads.KindImage, ImageURL: "/s.png", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	a.handleOSAds(rec, siteAdsRequest(http.MethodGet, "", blog))
	page := rec.Body.String()
	for _, want := range []string{"Site banner", `data-base="/os/d/` + blog.ID + `/api/ads"`, `data-site-ad-key="` + settings.KeyAdsenseClient + `"`, "data-ads-copy"} {
		if !strings.Contains(page, want) {
			t.Errorf("the site's Ads page lacks %s", want)
		}
	}
	if strings.Contains(page, "Install banner") || strings.Contains(page, "'/os/api/ads") || strings.Contains(page, `data-setting-key="`+settings.KeyAdsenseClient+`"`) {
		t.Error("the site's Ads page carries the install's slots, address or settings")
	}

	save := func(body string) int {
		rec := httptest.NewRecorder()
		a.handleOSSiteAdSettingsSave(rec, siteAdsRequest(http.MethodPost, body, blog))
		return rec.Code
	}
	if code := save(`{"settings":{"` + settings.KeyFeatureAds + `":"on","` + settings.KeyAdsenseClient + `":"ca-pub-2222222222222222"}}`); code != http.StatusOK {
		t.Fatalf("save: %d", code)
	}
	if got := a.adsenseClient(ctx, settings.ForDomain(blog.ID)); got != "ca-pub-2222222222222222" || !a.adsEnabled(ctx, settings.ForDomain(blog.ID)) {
		t.Errorf("the site's settings after its save: %q", got)
	}
	if a.adsenseClient(ctx, settings.ForPrimary()) != "" {
		t.Error("the site's save wrote the install's publisher ID")
	}
	for name, body := range map[string]string{
		"a malformed publisher ID": `{"settings":{"` + settings.KeyAdsenseClient + `":"pub-123"}}`,
		"a switch's third value":   `{"settings":{"` + settings.KeyFeatureAds + `":"yes"}}`,
		"a key not an ads key":     `{"settings":{"` + settings.KeySiteName + `":"Taken"}}`,
	} {
		if code := save(body); code != http.StatusBadRequest {
			t.Errorf("%s: %d, want refused", name, code)
		}
	}
}

// "Copy my ads to this site" copies the install's own slots (not members')
// and its ads settings, once, and leaves the install's as they were.
func TestCopyingMyAdsToASite(t *testing.T) {
	a, _, blog := hostedBlogApp(t)
	a.ads = ads.New(dbpkg.DB)
	ctx := context.Background()
	for _, n := range []string{"Top", "Bottom"} {
		if _, err := a.ads.Create(ctx, "", ads.SlotInput{Name: n, Placement: ads.PlacementHeader, Kind: ads.KindImage, ImageURL: "/x.png", Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.ads.CreateMemberAd(ctx, ads.MemberAdInput{OwnerEmail: "m@example.com", Placement: ads.PlacementHeader, ImageURL: "/m.png"}); err != nil {
		t.Fatal(err)
	}
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{settings.KeyFeatureAds: "on", settings.KeyAdsenseClient: "ca-pub-1111111111111111"}); err != nil {
		t.Fatal(err)
	}
	copyOnce := func() int {
		rec := httptest.NewRecorder()
		a.handleOSSiteAdsCopy(rec, siteAdsRequest(http.MethodPost, "", blog))
		return rec.Code
	}
	if code := copyOnce(); code != http.StatusOK {
		t.Fatalf("copy: %d", code)
	}
	got, _ := a.ads.List(ctx, blog.ID)
	if len(got) != 2 || !a.adsEnabled(ctx, settings.ForDomain(blog.ID)) || a.adsenseClient(ctx, settings.ForDomain(blog.ID)) != "ca-pub-1111111111111111" {
		t.Errorf("after the copy the site has %d slots and settings %v / %q", len(got), a.adsEnabled(ctx, settings.ForDomain(blog.ID)), a.adsenseClient(ctx, settings.ForDomain(blog.ID)))
	}
	if code := copyOnce(); code != http.StatusConflict {
		t.Errorf("a second copy: %d, want refused", code)
	}
	if mine, _ := a.ads.List(ctx, ""); len(mine) != 3 {
		t.Errorf("the install's own slots after the copy: %d", len(mine))
	}
}
