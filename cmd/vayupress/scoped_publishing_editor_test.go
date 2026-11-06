// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/users"
)

func scopedSave(a *App, d domain.Domain, body string) *httptest.ResponseRecorder {
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/d/"+d.ID+"/api/settings", strings.NewReader(body)), admin)
	ctx := context.WithValue(req.Context(), ctxScopedDomainKey, d)
	ctx = context.WithValue(ctx, ctxScopeKey, settings.ForDomain(d.ID))
	rec := httptest.NewRecorder()
	a.handleOSScopedSettingsSave(rec, req.WithContext(ctx))
	return rec
}

// A hosted site's comments, membership buttons, hero, author box, main colour
// and stylesheet are its own: saved on its settings page, they are what its
// public pages are drawn with, and the install's own site is not touched.
func TestAHostedSitesPublishingChoicesAreItsOwn(t *testing.T) {
	a, _, blog := hostedBlogApp(t)
	rec := scopedSave(a, blog, `{"settings":{
		"`+settings.KeyFeatureComments+`":"off",
		"`+settings.KeyMembershipButtons+`":"true",
		"`+settings.KeyHomeHero+`":"true",
		"`+settings.KeyAuthorBio+`":"Writes about notes.",
		"`+settings.KeyThemePrimaryLight+`":"#123456",
		"`+settings.KeyThemeCustomCSS+`":"body{letter-spacing:0}"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	s, ok := a.brandForRequest(requestFor("/", blog))
	if !ok {
		t.Fatal("no settings for the site")
	}
	if s.CommentsEnabled || !s.ShowMembership || !s.ShowHero || s.AuthorBio != "Writes about notes." ||
		s.PrimaryLight != "#123456" || s.CustomCSS != "body{letter-spacing:0}" {
		t.Errorf("the site is not drawn with what was saved for it: %+v", s)
	}
	if v := a.siteSettings.Get(context.Background(), settings.ForPrimary(), settings.KeyThemeCustomCSS); v != "" {
		t.Errorf("the install's own stylesheet became %q", v)
	}
}

// A switch takes its two values and nothing else, and a refused value saves
// nothing: "yes" for comments would have read as on and membership as off.
func TestASwitchRefusesAValueItDoesNotTake(t *testing.T) {
	a, _, blog := hostedBlogApp(t)
	rec := scopedSave(a, blog, `{"settings":{"`+settings.KeySiteName+`":"Notes","`+settings.KeyMembershipButtons+`":"yes"}}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "bad-value") {
		t.Fatalf("a switch sent \"yes\": %d %s", rec.Code, rec.Body.String())
	}
	if v := a.siteSettings.Get(context.Background(), settings.ForDomain(blog.ID), settings.KeySiteName); v == "Notes" {
		t.Error("the name beside a refused switch was saved")
	}
}

// The page draws each switch with its stored state, the author box and the
// stylesheet, and every section's Save button saves.
func TestThePublishingSectionShowsTheSitesOwnChoices(t *testing.T) {
	page := scopedSettingsBody("d1", "notes.example.com", map[string]string{
		settings.KeyFeatureComments:   "off",
		settings.KeyMembershipButtons: "true",
		settings.KeyThemeCustomCSS:    "h1{color:red}",
	}, presCustom)
	for _, want := range []string{
		`data-scoped-key="` + settings.KeyMembershipButtons + `" data-on="true" data-off="false" checked>`,
		`data-scoped-key="` + settings.KeyFeatureComments + `" data-on="on" data-off="off">`,
		`data-scoped-key="` + settings.KeyAuthorBio + `"`,
		`h1{color:red}</textarea>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
	if n := strings.Count(page, "data-scoped-save"); n != 4 {
		t.Errorf("%d Save buttons, want one for each of Identity, Colour, Publishing and Stylesheet", n)
	}
	if src := scopedSettingsScript("n"); !strings.Contains(src, "querySelectorAll('[data-scoped-save]')") ||
		!strings.Contains(src, "getAttribute('data-off')") {
		t.Error("the script does not wire every Save button or send a cleared switch's off value")
	}
}
