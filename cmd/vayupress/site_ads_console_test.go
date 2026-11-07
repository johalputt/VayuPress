// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/microcosm-cc/bluemonday"

	"github.com/johalputt/vayupress/internal/ads"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/payments"
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

// An ad a member buys on a hosted site is that site's: it waits for review on
// the site's own Ads page, never on the install's.
func TestAMemberAdBelongsToTheSiteItWasBoughtOn(t *testing.T) {
	a, _, blog := hostedBlogApp(t)
	a.ads, a.members, a.payments = ads.New(dbpkg.DB), members.New(dbpkg.DB), payments.New(dbpkg.DB)
	ctx := context.Background()
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{settings.KeyFeaturePayments: "on"}); err != nil {
		t.Fatal(err)
	}
	if err := a.siteSettings.SetMany(ctx, settings.ForDomain(blog.ID), map[string]string{settings.KeyFeatureAds: "on"}); err != nil {
		t.Fatal(err)
	}
	m, err := a.members.Upsert(ctx, "reader@example.com")
	if err != nil {
		t.Fatal(err)
	}
	token, err := a.members.CreateSession(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	req := requestFor("/api/v1/members/ads", blog)
	req.Method = http.MethodPost
	req.Body = io.NopCloser(strings.NewReader(`{"placement":"` + ads.PlacementSidebar + `","image_url":"/media/reader.png"}`))
	req.AddCookie(&http.Cookie{Name: memberCookie, Value: token})
	rec := httptest.NewRecorder()
	a.handleMemberAdSubmit(rec, req)
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("submit: %d %s", rec.Code, rec.Body.String())
	}
	mine, _ := a.ads.MemberAds(ctx, "reader@example.com")
	if len(mine) != 1 || mine[0].Site != blog.ID {
		t.Fatalf("the member's ad belongs to %+v, want the site %s", mine, blog.ID)
	}
	if got, _ := a.ads.List(ctx, ""); len(got) != 0 {
		t.Error("the member's ad is on the install's Ads page")
	}
}
