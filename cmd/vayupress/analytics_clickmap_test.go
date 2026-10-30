// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/users"
)

func clickMapApp(t *testing.T) *App {
	t.Helper()
	openMigratedDB(t)
	return &App{analytics: analytics.New(dbpkg.DB), siteSettings: settings.New(dbpkg.DB)}
}

// clickBeacon sends one click on /post, of element, with header set when
// given, and returns the answer's status.
func clickBeacon(a *App, element string, header ...string) int {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/clicks",
		strings.NewReader(`{"u":"/post","w":"desktop","c":[{"e":"`+element+`","x":1,"y":2}]}`))
	if len(header) == 2 {
		r.Header.Set(header[0], header[1])
	}
	w := httptest.NewRecorder()
	a.handleAnalyticsClicks(w, r)
	return w.Code
}

// Clicks are taken from a site whose operator turned its click map on, and
// from no other: the server decides, not the page, so another site's
// setting, a cached copy of the recorder or a hand-made beacon records
// nothing; the recorder is served to that site alone; a reader's opt-out
// is honoured.
func TestClicksAreTakenOnlyFromASiteThatTurnedItsMapOn(t *testing.T) {
	a := clickMapApp(t)
	ctx := context.Background()
	script := func() string {
		w := httptest.NewRecorder()
		a.handleAnalyticsScript(w, httptest.NewRequest(http.MethodGet, "/static/vp-analytics.js", nil))
		return w.Body.String()
	}
	if err := a.siteSettings.SetMany(ctx, settings.ForDomain("h1"), map[string]string{settings.KeyAnalyticsClickMap: "1"}); err != nil {
		t.Fatal(err)
	}
	clickBeacon(a, "off")
	if strings.Contains(script(), "/api/v1/analytics/clicks") {
		t.Error("the recorder is served to a site whose click map is off")
	}
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{settings.KeyAnalyticsClickMap: "4"}); err != nil {
		t.Fatal(err)
	}
	if s := script(); !strings.Contains(s, "Math.random()*4>=1") || !strings.Contains(s, "/api/v1/analytics/clicks") {
		t.Error("the recorder is not served, one page load in 4, once the site turned its map on")
	}
	clickBeacon(a, "on")
	clickBeacon(a, "dnt", "DNT", "1")
	clickBeacon(a, "gpc", "Sec-GPC", "1")
	if err := a.analytics.FlushEvents(ctx); err != nil {
		t.Fatal(err)
	}
	els, err := a.analytics.ClickMapScoped(ctx, "", "/post", "desktop", 7, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(els) != 1 || els[0].Element != "on" || els[0].Cells[12] != 1 {
		t.Errorf("clicks kept = %+v, want the one sent while the map was on, a tenth across and two down", els)
	}
}

// Only an administrator turns a click map on, at one of the shares offered,
// for the site of the address; Goals offers the setting to an administrator
// alone.
func TestOnlyAnAdministratorTurnsTheClickMapOn(t *testing.T) {
	a := clickMapApp(t)
	t.Cleanup(func() { forgetCache(t, adminDash) })
	save := func(role, body string, d *domain.Domain) int {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/os/analytics/clickmap", strings.NewReader(body))
		ctx := context.WithValue(r.Context(), ctxUserKey, &users.User{Role: role})
		if d != nil {
			ctx = context.WithValue(ctx, ctxScopedDomainKey, *d)
		}
		w := httptest.NewRecorder()
		a.handleOSClickMapSave(w, r.WithContext(ctx))
		return w.Code
	}
	ctx := context.Background()
	if code := save(users.RoleEditor, `{"sample":4}`, nil); code != http.StatusForbidden || a.clickMapSample(ctx, "") != 0 {
		t.Errorf("an editor's save: %d, map at %d; want 403 and off", code, a.clickMapSample(ctx, ""))
	}
	if code := save(users.RoleAdmin, `{"sample":3}`, nil); code != http.StatusBadRequest || a.clickMapSample(ctx, "") != 0 {
		t.Errorf("a share not offered: %d, map at %d; want 400 and off", code, a.clickMapSample(ctx, ""))
	}
	if code := save(users.RoleAdmin, `{"sample":4}`, nil); code != http.StatusOK || a.clickMapSample(ctx, "") != 4 {
		t.Errorf("an administrator's save: %d, map at %d; want 200 and 4", code, a.clickMapSample(ctx, ""))
	}
	hosted := domain.Domain{ID: "h1", Host: "h1.test"}
	if code := save(users.RoleAdmin, `{"sample":10}`, &hosted); code != http.StatusOK || a.clickMapSample(ctx, "h1") != 10 || a.clickMapSample(ctx, "") != 4 {
		t.Errorf("a hosted site's save: %d, its map %d, the primary's %d; want 200, 10, 4", code, a.clickMapSample(ctx, "h1"), a.clickMapSample(ctx, ""))
	}
	if code := save(users.RoleAdmin, `{"sample":0}`, nil); code != http.StatusOK || a.clickMapSample(ctx, "") != 0 {
		t.Errorf("turning it off: %d, map at %d", code, a.clickMapSample(ctx, ""))
	}
	goals := func(role string) string {
		r := httptest.NewRequest(http.MethodGet, "/os/analytics?view=goals&days=7", nil)
		r = r.WithContext(context.WithValue(r.Context(), ctxUserKey, &users.User{Role: role}))
		w := httptest.NewRecorder()
		a.handleOSAnalytics(w, r)
		return w.Body.String()
	}
	if !strings.Contains(goals(users.RoleAdmin), `id="click-map"`) {
		t.Error("an administrator's Goals has no click map setting")
	}
	if strings.Contains(goals(users.RoleEditor), `id="click-map"`) {
		t.Error("an editor's Goals offers the click map setting")
	}
}

// A page's click map lists its elements most clicked first, with the
// bursts and the clicks on nothing, at the address's width; the primary's
// pages are drawn in a frame of the console's with the clicks over them, a
// hosted site's are not; a narrowed page says the map is every reader's;
// with the map off and nothing kept, an administrator is told where to turn
// it on and an editor is shown nothing.
func TestAPagesClickMapIsItsElements(t *testing.T) {
	a := clickMapApp(t)
	ctx := context.Background()
	report := func(addr string, v analyticsView, per analyticsSpan, admin bool) string {
		return a.osClickMap(httptest.NewRequest(http.MethodGet, addr, nil), v, per, admin)
	}
	per := analyticsSpan{days: 7, page: "/post"}
	if got := report("/os/analytics?view=page&page=/post", primaryView, per, false); got != "" {
		t.Errorf("an editor is shown an empty click map:\n%s", got)
	}
	if got := report("/os/analytics?view=page&page=/post", primaryView, per, true); !strings.Contains(got, "The click map is off for this site") || !strings.Contains(got, `view=goals#click-map`) {
		t.Errorf("an administrator is not told where to turn it on:\n%s", got)
	}
	a.analytics.RecordClicks("", "/post", "desktop", []analytics.Click{
		{Element: "main>article>p:3>a", X: 2, Y: 5}, {Element: "main>article>p:3>a", X: 2, Y: 5, Rage: true}, {Element: "main>article>img", Dead: true},
	})
	a.analytics.RecordClicks("", "/post", "phone", []analytics.Click{{Element: "nav", X: 0, Y: 0}})
	a.analytics.RecordClicks("h1", "/post", "desktop", []analytics.Click{{Element: "footer", X: 0, Y: 0}})
	if err := a.analytics.FlushEvents(ctx); err != nil {
		t.Fatal(err)
	}
	got := report("/os/analytics?view=page&page=/post", primaryView, per, false)
	for _, want := range []string{
		`<span title="main&gt;article&gt;p:3&gt;a">… › article › p 3 › a</span></td><td>2</td><td>1</td><td>0</td>`,
		`<span title="main&gt;article&gt;img">main › article › img</span></td><td>1</td><td>0</td><td>1</td>`,
		`<strong aria-current="true">Desktop</strong>`, `href="/os/analytics?days=7&amp;page=%2Fpost&amp;view=page&amp;cw=phone">Phone</a>`,
		`data-heat-width="1280"`, `sandbox="allow-same-origin"`, `src="/os/analytics/frame?page=%2Fpost"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the click map has no %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "p 3 › a") > strings.Index(got, "article › img") {
		t.Error("the elements are not most clicked first")
	}
	if strings.Contains(got, ">nav<") || strings.Contains(got, "footer") {
		t.Error("the desktop map counts a phone's click or another site's")
	}
	if phone := report("/os/analytics?view=page&page=/post&cw=phone", primaryView, per, false); !strings.Contains(phone, `title="nav"`) || !strings.Contains(phone, `data-heat-width="390"`) {
		t.Errorf("the phone's map:\n%s", phone)
	}
	if hosted := report("/os/d/h1/analytics?view=page&page=/post", hostedView(domain.Domain{ID: "h1", Host: "h1.test"}), per, false); !strings.Contains(hosted, `title="footer"`) || strings.Contains(hosted, "sa-heat\"") {
		t.Errorf("a hosted site's map is its own, with no frame:\n%s", hosted)
	}
	narrowed := per
	narrowed.focus = []analytics.ExploreFilter{{Dim: "country", Op: analytics.FilterIs, Value: "IN"}}
	if got := report("/os/analytics?view=page&page=/post", primaryView, narrowed, false); !strings.Contains(got, "every reader is counted here, since a click is not kept with its visit") {
		t.Error("a narrowed page's click map does not say it is every reader's")
	}
}

// The frame is the page as the site serves it, framed by the console alone,
// its own policy otherwise kept; a page that is not one, or no page, is
// none.
func TestTheClickMapsFrameIsThePageFramedByTheConsoleAlone(t *testing.T) {
	a := &App{}
	a.setRootHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/post":
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'nonce-abc'; frame-ancestors 'none'")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<html><body><p>the post</p></body></html>"))
		case "/feed.xml":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte("<rss/>"))
		default:
			http.NotFound(w, r)
		}
	}))
	frame := func(page string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		a.handleOSClickMapFrame(w, httptest.NewRequest(http.MethodGet, "/os/analytics/frame?page="+page, nil))
		return w
	}
	w := frame("/post")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "the post") {
		t.Fatalf("the frame: %d %q", w.Code, w.Body.String())
	}
	if csp := w.Header().Get("Content-Security-Policy"); csp != "default-src 'self'; script-src 'nonce-abc'; frame-ancestors 'self'" {
		t.Errorf("the frame's policy = %q, want the page's, framed by this origin alone", csp)
	}
	if xfo := w.Header().Get("X-Frame-Options"); xfo != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options = %q, want SAMEORIGIN", xfo)
	}
	for _, page := range []string{"/feed.xml", "/missing", "", "javascript:x"} {
		if code := frame(page).Code; code != http.StatusNotFound {
			t.Errorf("the frame of %q: %d, want 404", page, code)
		}
	}
}
