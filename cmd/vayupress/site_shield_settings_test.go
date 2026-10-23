// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/users"
	"github.com/johalputt/vayupress/internal/vayushield"
)

// shieldSitesApp is an install with a primary and one hosted site, c1
// (one.example), whose shield tells the sites apart by host.
func shieldSitesApp(t *testing.T) (*App, domain.Domain) {
	t.Helper()
	openMigratedDB(t)
	ctx := context.Background()
	reg := domain.New(dbpkg.DB, dbpkg.RDB)
	if err := reg.EnsurePrimary(ctx, "example.test", domain.SiteBlog); err != nil {
		t.Fatal(err)
	}
	if _, err := dbpkg.DB.Exec(`INSERT INTO domains(id,host,site_type,is_primary,status,created_at,updated_at) VALUES('c1','one.example','blog',0,'active',datetime('now'),datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	d, err := reg.ByID(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	sh := vayushield.New(vayushield.Config{Enabled: true, SiteFn: func(r *http.Request) string {
		if r.Host == "one.example" {
			return "c1"
		}
		return ""
	}})
	sh.ApplySettings(vayushield.Settings{Enabled: true})
	return &App{domains: reg, siteSettings: settings.New(dbpkg.DB), vayuShield: sh}, d
}

// saveSiteShield posts body to d's save endpoint as an administrator.
func saveSiteShield(a *App, d domain.Domain, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/os/d/"+d.ID+"/api/shield", strings.NewReader(body))
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", d.ID)
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rc)
	ctx = context.WithValue(ctx, ctxScopedDomainKey, d)
	ctx = context.WithValue(ctx, ctxUserKey, &users.User{ID: "op", Role: users.RoleAdmin})
	w := httptest.NewRecorder()
	a.handleOSScopedShieldSave(w, r.WithContext(ctx))
	return w
}

// A site's own refusal, saved on its Security page, is in force at once, on
// that site and no other.
func TestASitesSavedRefusalIsInForceAtOnce(t *testing.T) {
	a, d := shieldSitesApp(t)
	w := saveSiteShield(a, d, `{"domain_id":"c1","settings":{"`+settings.KeyShieldSiteDenyCIDRs+`":"198.51.100.0/24\nnot-a-network","`+settings.KeyShieldSiteBlock+`":"0.5"}}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "not-a-network") {
		t.Fatalf("save answered %d %s; want ok, naming the entry it could not read", w.Code, w.Body.String())
	}
	served := func(host string) bool {
		ok := false
		h := a.vayuShield.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ok = true }))
		req := httptest.NewRequest(http.MethodGet, "/article", nil)
		req.Host, req.RemoteAddr = host, "198.51.100.9:40000"
		h.ServeHTTP(httptest.NewRecorder(), req)
		return ok
	}
	if served("one.example") {
		t.Error("the site's saved refusal is not in force on the site")
	}
	if !served("example.test") {
		t.Error("the site's refusal applied to the primary too")
	}
}

// A threshold outside (0, 1], a body naming another site, and the primary
// itself are all refused, and nothing is saved.
func TestASitesShieldSaveRefusesWhatItCannotHonour(t *testing.T) {
	a, d := shieldSitesApp(t)
	for _, body := range []string{
		`{"settings":{"` + settings.KeyShieldSiteBlock + `":"7"}}`,
		`{"settings":{"` + settings.KeyShieldSitePoW + `":"high"}}`,
		`{"domain_id":"someone-else","settings":{"` + settings.KeyShieldSiteDenyCIDRs + `":"192.0.2.0/24"}}`,
	} {
		if w := saveSiteShield(a, d, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s answered %d, want 400", body, w.Code)
		}
	}
	primary, _ := a.domains.Primary(context.Background())
	if w := saveSiteShield(a, primary, `{"settings":{"`+settings.KeyShieldSiteBlock+`":"0.5"}}`); w.Code != http.StatusNotFound {
		t.Errorf("a save for the primary answered %d, want 404: the primary's settings are the install's", w.Code)
	}
	if _, set := a.siteShieldSettings(context.Background(), "c1"); set {
		t.Error("a refused save stored something")
	}
}

// The form shows each of the site's values beside the install's, so whether
// a value is the site's own or inherited is never a guess.
func TestASitesShieldFormShowsWhatItInherits(t *testing.T) {
	a, d := shieldSitesApp(t)
	ctx := context.Background()
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{settings.KeyShieldBlock: "0.85", settings.KeyShieldDenyCIDRs: "203.0.113.0/24"}); err != nil {
		t.Fatal(err)
	}
	if err := a.siteSettings.SetMany(ctx, settings.ForDomain("c1"), map[string]string{settings.KeyShieldSitePoW: "0.3"}); err != nil {
		t.Fatal(err)
	}
	form := string(a.scopedShieldForm(ctx, d))
	for _, want := range []string{`value="0.3"`, `placeholder="0.85"`,
		"Set for this site, in place of the install&#39;s 0.4.", // the PoW threshold this site set
		"Inherited: the install&#39;s 0.85.",                    // the refusal threshold it did not
		"Inherited: the install&#39;s 1 entry apply here.",      // the install's deny list
		"Inherited: the built-in header.",                       // a header left alone
	} {
		if !strings.Contains(form, want) {
			t.Errorf("the form has no %q", want)
		}
	}
	if strings.Contains(form, "style=") || strings.Contains(form, "<script") {
		t.Error("the form carries inline style or script")
	}
}
