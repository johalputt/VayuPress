// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/users"
)

// Outside services (render.SitePolicy): a site's own allowances, merged into
// its public pages' policy at one point, never into a page that carries a
// session, and never into another site's.

func servicesApp(t *testing.T) *App {
	t.Helper()
	openMigratedDB(t)
	return &App{siteSettings: settings.New(dbpkg.DB)}
}

func storePolicy(t *testing.T, a *App, sc settings.Scope, p render.SitePolicy) {
	t.Helper()
	raw, _ := json.Marshal(p)
	if err := a.siteSettings.SetMany(context.Background(), sc, map[string]string{settings.KeyCSPPolicy: string(raw)}); err != nil {
		t.Fatal(err)
	}
}

// servedCSP runs a request through the real header chain — the baseline, then
// the site middleware — to a handler that may set a page's own policy first.
func servedCSP(a *App, path string, d *domain.Domain, page func(http.ResponseWriter, *http.Request)) http.Header {
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if page != nil {
			page(w, r)
		}
		_, _ = w.Write([]byte("<p>page</p>"))
	})
	h := securityHeadersMiddleware(a.siteCSPMiddleware(final))
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if d != nil {
		req = req.WithContext(context.WithValue(req.Context(), ctxKeyDomain{}, *d))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Header()
}

func TestAllowancesReachEveryPublicPageAndNoStrictOne(t *testing.T) {
	a := servicesApp(t)
	storePolicy(t, a, settings.ForPrimary(), render.SitePolicy{Mode: render.PolicyCustom, Services: []string{"stripe"}})

	if got := servedCSP(a, "/hello-world", nil, nil).Get("Content-Security-Policy"); !strings.Contains(got, "https://js.stripe.com") {
		t.Errorf("a public page does not get the site's allowance: %s", got)
	}
	// A page that builds its own policy (an embed) gets the allowance too, and
	// keeps its own widening: the merge is after every handler, not instead.
	embed := servedCSP(a, "/a-post", nil, func(w http.ResponseWriter, r *http.Request) {
		setEmbedCSP(w, r, []string{"https://www.youtube-nocookie.com"})
	}).Get("Content-Security-Policy")
	if !strings.Contains(embed, "https://js.stripe.com") || !strings.Contains(embed, "https://www.youtube-nocookie.com") {
		t.Errorf("an embed page lost the allowance or its own frame origin: %s", embed)
	}
	// One seed per kind of strict path: the console, a member page.
	for _, p := range []string{"/os/posts", "/members/login"} {
		if got := servedCSP(a, p, nil, nil).Get("Content-Security-Policy"); strings.Contains(got, "stripe") {
			t.Errorf("%s carries a session and got the site's allowance: %s", p, got)
		}
	}
}

func TestAHostedSitesAllowancesAreItsOwn(t *testing.T) {
	a := servicesApp(t)
	storePolicy(t, a, settings.ForDomain("d1"), render.SitePolicy{Mode: render.PolicyCustom, Services: []string{"vimeo"}})
	hosted := &domain.Domain{ID: "d1", Host: "bakery.example"}
	if got := servedCSP(a, "/", hosted, nil).Get("Content-Security-Policy"); !strings.Contains(got, "https://player.vimeo.com") {
		t.Errorf("the hosted site does not get its own allowance: %s", got)
	}
	if got := servedCSP(a, "/", &domain.Domain{ID: "p", IsPrimary: true}, nil).Get("Content-Security-Policy"); strings.Contains(got, "vimeo") {
		t.Errorf("a hosted site's allowance reached the primary: %s", got)
	}
	if got := servedCSP(a, "/", &domain.Domain{ID: "d2", Host: "studio.example"}, nil).Get("Content-Security-Policy"); strings.Contains(got, "vimeo") {
		t.Errorf("a hosted site's allowance reached another hosted site: %s", got)
	}
}

func TestReportOnlyBlocksNothingUntilItEnds(t *testing.T) {
	a := servicesApp(t)
	storePolicy(t, a, settings.ForPrimary(), render.SitePolicy{Mode: render.PolicyReport, Services: []string{"stripe"}, ReportUntil: time.Now().Add(time.Hour)})
	h := servedCSP(a, "/hello-world", nil, nil)
	if h.Get("Content-Security-Policy") != "" || !strings.Contains(h.Get("Content-Security-Policy-Report-Only"), "js.stripe.com") {
		t.Errorf("report-only still enforces, or reports without the allowances: %v", h)
	}
	storePolicy(t, a, settings.ForPrimary(), render.SitePolicy{Mode: render.PolicyReport, Services: []string{"stripe"}, ReportUntil: time.Now().Add(-time.Minute)})
	h = servedCSP(a, "/hello-world", nil, nil)
	if !strings.Contains(h.Get("Content-Security-Policy"), "js.stripe.com") || h.Get("Content-Security-Policy-Report-Only") != "" {
		t.Errorf("report-only outlived its end: %v", h)
	}
}

func TestAnUnreadablePolicyIsTheStrictBaseline(t *testing.T) {
	a := servicesApp(t)
	for _, raw := range []string{
		`{not json`,
		`{"mode":"custom","sources":{"script-src":["https://evil.example"]}}`,
		`{"mode":"open","services":["stripe"]}`,
	} {
		if err := a.siteSettings.SetMany(context.Background(), settings.ForPrimary(), map[string]string{settings.KeyCSPPolicy: raw}); err != nil {
			t.Fatal(err)
		}
		got := servedCSP(a, "/hello-world", nil, nil).Get("Content-Security-Policy")
		if got != render.BuildCSP(nonceIn(got), nil) {
			t.Errorf("stored %s widened the policy: %s", raw, got)
		}
	}
}

// nonceIn lifts the per-request nonce out of a header, so a served policy can
// be compared with the baseline built for the same nonce.
func nonceIn(csp string) string {
	_, rest, _ := strings.Cut(csp, "'nonce-")
	n, _, _ := strings.Cut(rest, "'")
	return n
}

func TestSavingAPolicyValidatesAndOwnsTheReportDeadline(t *testing.T) {
	a := servicesApp(t)
	admin := &users.User{ID: "u1", Email: "a@example.com", Role: users.RoleAdmin}
	post := func(u *users.User, body string, d *domain.Domain) *httptest.ResponseRecorder {
		req := withUser(httptest.NewRequest(http.MethodPost, "/os/api/website/csp", strings.NewReader(body)), u)
		if d != nil {
			req = req.WithContext(context.WithValue(req.Context(), ctxScopedDomainKey, *d))
		}
		rec := httptest.NewRecorder()
		a.handleOSServicesSave(rec, req)
		return rec
	}
	if rec := post(&users.User{ID: "u2", Role: users.RoleAuthor}, `{"mode":"custom"}`, nil); rec.Code != http.StatusForbidden {
		t.Errorf("an author saved a site's policy: %d", rec.Code)
	}
	rec := post(admin, `{"mode":"custom","sources":{"script-src":["https://cdn.example.com"]}}`, nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "only from the listed services") {
		t.Errorf("a typed script origin was not refused for its reason: %d %s", rec.Code, rec.Body.String())
	}
	// The client cannot choose how long report-only lasts.
	if rec := post(admin, `{"mode":"report","report_until":"2099-01-01T00:00:00Z"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	p := a.sitePolicyIn(context.Background(), settings.ForPrimary())
	if left := time.Until(p.ReportUntil); left > render.ReportOnlyFor || left < render.ReportOnlyFor-time.Hour {
		t.Errorf("report-only ends in %v, want about %v, whatever the client sent", left, render.ReportOnlyFor)
	}
	// A hosted site's page saves to that site, not the primary.
	if rec := post(admin, `{"mode":"custom","services":["vimeo"]}`, &domain.Domain{ID: "d1", Host: "bakery.example"}); rec.Code != http.StatusOK {
		t.Fatalf("scoped save: %d %s", rec.Code, rec.Body.String())
	}
	if got := a.sitePolicyIn(context.Background(), settings.ForDomain("d1")); strings.Join(got.Services, ",") != "vimeo" {
		t.Errorf("the hosted site's save did not land on it: %+v", got)
	}
	if got := a.sitePolicyIn(context.Background(), settings.ForPrimary()); got.Mode != render.PolicyReport {
		t.Errorf("the hosted site's save changed the primary: %+v", got)
	}
}

// An origin is offered only once two visitors have reported it: reports are
// unauthenticated, and one forged report must not put an address of an
// attacker's choosing beside an Allow button.
func TestABlockedOriginIsOfferedOnlyOnceTwoVisitorsReportIt(t *testing.T) {
	cspBlocksMu.Lock()
	cspBlocks = map[string]*cspBlock{}
	cspBlocksMu.Unlock()
	now := time.Now()
	recordCSPBlock("https://johal.in/post", "img-src", "https://cdn.example.com/a.png", "1.1.1.1", now)
	recordCSPBlock("https://johal.in/other", "img-src", "https://cdn.example.com/b.png", "1.1.1.1", now)
	if got := cspBlocksFor([]string{"johal.in"}); len(got) != 0 {
		t.Fatalf("one visitor's reports are offered: %+v", got)
	}
	recordCSPBlock("https://johal.in/post", "img-src", "https://cdn.example.com/a.png", "2.2.2.2", now)
	recordCSPBlock("https://bakery.example/", "img-src", "https://cdn.example.com/a.png", "3.3.3.3", now)
	got := cspBlocksFor([]string{"johal.in"})
	if len(got) != 1 || got[0].Origin != "https://cdn.example.com" || got[0].Count != 3 || got[0].Directive != "img-src" {
		t.Errorf("blocked on johal.in: %+v, want the one origin, three reports, by its directive", got)
	}
	recordCSPBlock("https://johal.in/", "script-src-elem", "https://js.stripe.com/v3", "1.1.1.1", now)
	recordCSPBlock("https://johal.in/", "script-src-elem", "https://js.stripe.com/v3", "2.2.2.2", now)
	for _, b := range cspBlocksFor([]string{"johal.in"}) {
		if b.Origin == "https://js.stripe.com" && b.Directive != "script-src" {
			t.Errorf("the element variant was not folded into its directive: %+v", b)
		}
	}
}

func TestEachBlockedOriginIsAnsweredTheOnlyWayItCanBe(t *testing.T) {
	for _, c := range []struct {
		b    cspBlock
		want string
	}{
		{cspBlock{Directive: "img-src", Origin: "https://cdn.example.com"}, `data-csp-allow="img-src" data-origin="https://cdn.example.com"`},
		{cspBlock{Directive: "script-src", Origin: "https://js.stripe.com"}, `data-csp-turn-on="stripe"`},
		{cspBlock{Directive: "connect-src", Origin: "https://region1.google-analytics.com"}, `data-csp-turn-on="google-analytics"`},
		{cspBlock{Directive: "script-src", Origin: "https://cdn.jsdelivr.net"}, "Scripts come only from the services above"},
		{cspBlock{Directive: "script-src", Origin: "inline"}, "never allowed. Move it into a file"},
	} {
		if got := string(cspBlockAction(c.b)); !strings.Contains(got, c.want) {
			t.Errorf("%s %s answered %s, want %s", c.b.Directive, c.b.Origin, got, c.want)
		}
	}
}

func TestTheBellRemembersASiteInReportOnly(t *testing.T) {
	a := servicesApp(t)
	storePolicy(t, a, settings.ForPrimary(), render.SitePolicy{Mode: render.PolicyReport, ReportUntil: time.Now().Add(time.Hour)})
	titles := func(level int) string {
		var out []string
		for _, n := range a.osNotifications(context.Background(), &osSettings{AccessLevel: level}) {
			out = append(out, n.Title+" → "+n.Href)
		}
		return strings.Join(out, "\n")
	}
	if got := titles(accessAdmin); !strings.Contains(got, "Outside services: report only → /os/website/services") {
		t.Errorf("an administrator is not told the site blocks nothing:\n%s", got)
	}
	if got := titles(accessAuthor); strings.Contains(got, "Outside services") {
		t.Errorf("an author is pointed at an administrator's page:\n%s", got)
	}
}

func TestTheServicesPageShowsWhatIsSaved(t *testing.T) {
	a := servicesApp(t)
	storePolicy(t, a, settings.ForPrimary(), render.SitePolicy{Mode: render.PolicyCustom, Services: []string{"stripe"},
		Sources: map[string][]string{"font-src": {"https://fonts.example.com"}}})
	rec := httptest.NewRecorder()
	a.handleOSServices(rec, httptest.NewRequest(http.MethodGet, "/os/website/services", nil))
	page := rec.Body.String()
	for _, want := range []string{
		`value="custom" checked`,
		`data-csp-service="stripe" checked`,
		`data-csp-source="font-src" data-origin="https://fonts.example.com"`,
		`Runs code on your pages.`,
		`data-csp-save="/os/api/website/csp"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
	if strings.Contains(page, `data-csp-service="vimeo" checked`) {
		t.Error("a service that is off shows as on")
	}
}
