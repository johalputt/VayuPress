// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/users"
)

var simViews = []analytics.PathCountryViews{
	{Path: "/article", Site: "", Country: "IN", Views: 100},
	{Path: "/wp-login.php", Site: "", Country: "IN", Views: 3},
	{Path: "/search", Site: "c1", Country: "IN", Views: 20},
	{Path: "/search/ok", Site: "c1", Country: "IN", Views: 5},
	{Path: "/article", Site: "", Country: "ZZ", Views: 7},
	{Path: "/article", Site: "", Country: "YY", Views: 11},
	{Path: "/x", Site: "", Country: "IN", Views: 9},
}

// Each line and list is judged in the middleware's order, one cause per
// verdict, and what a view cannot answer is named instead of guessed.
func TestADraftIsJudgedLineByLine(t *testing.T) {
	d := shieldDraft{
		Rules: "allow path=/search/ok\nblock path^=/wp-\nchallenge path^=/search\nblock score>=0.5 path^=/x\n" +
			"limit 1/min path^=/\nallow country=ZZ\nrefuse path=/y\nblock client=badbot path=/x\nchallenge client=human path=/x",
		DenyCountries:      []string{"ZZ"},
		ChallengeCountries: []string{"YY"},
	}
	s := simulateShield(simViews, d, false, 7)
	if s.Views != 155 || s.Refused != 10 || s.Challenged != 40 {
		t.Fatalf("views %d, refused %d, challenged %d; want 155, 10 (3 /wp- + 7 ZZ), 40 (20 /search + 11 YY + 9 /x, a reader)", s.Views, s.Refused, s.Challenged)
	}
	for cause, want := range map[string]int{"line 2: block path^=/wp-": 3, "line 3: challenge path^=/search": 20, "the country lists": 18} {
		if s.ByCause[cause] != want {
			t.Errorf("%q acted on %d views, want %d (all: %v)", cause, s.ByCause[cause], want, s.ByCause)
		}
	}
	if len(s.Unjudged) != 1 || !strings.Contains(s.Unjudged[0], "score>=0.5") {
		t.Errorf("not judged %v; want the score rule alone", s.Unjudged)
	}
	if len(s.Issues) != 1 || !strings.Contains(s.Issues[0], "line 7") {
		t.Errorf("issues %v; want line 7", s.Issues)
	}
	if s.Samples[0].path != "/search" || s.Samples[0].views != 20 {
		t.Errorf("the first sample is %+v; want the most viewed, /search", s.Samples[0])
	}
}

// In a Tor Space the country is withheld, as it is from the middleware: no
// country list or country rule acts.
func TestADraftsCountriesActOnNoOneInATorSpace(t *testing.T) {
	s := simulateShield(simViews, shieldDraft{Rules: "block country=IN", DenyCountries: []string{"ZZ"}}, true, 7)
	if s.Refused != 0 || s.Challenged != 0 {
		t.Errorf("refused %d, challenged %d in a Tor Space; want none", s.Refused, s.Challenged)
	}
}

// The form's draft is replayed against the recorded views, unsaved.
func TestTheShieldPageTriesTheTypedDraft(t *testing.T) {
	a, _ := shieldSitesApp(t)
	a.analytics = analytics.New(dbpkg.DB)
	if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_sessions(id, visitor_id, country, domain_id) VALUES('s1','v1','IN','')`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_pageviews(id, session_id, url_path, event_type, created_at) VALUES(?, 's1', '/wp-admin', 1, CURRENT_TIMESTAMP)`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_pageviews(id, session_id, url_path, event_type, created_at) VALUES('d', 's1', '/article', 1, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"sh_rules": {"block path^=/wp-"}}
	r := httptest.NewRequest(http.MethodPost, "/os/api/shield/simulate", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(context.WithValue(r.Context(), ctxUserKey, &users.User{ID: "op", Role: users.RoleAdmin}))
	w := httptest.NewRecorder()
	a.handleOSShieldSimulate(w, r)
	body := w.Body.String()
	for _, want := range []string{"Against 4 reader views in the last 7 days", "would have refused <strong>3</strong> (75.0%)",
		"line 1: block path^=/wp- &middot; 3 views", "<code>/wp-admin</code> from IN"} {
		if !strings.Contains(body, want) {
			t.Errorf("the report has no %q:\n%s", want, body)
		}
	}
	if rules, _ := a.vayuShield.OperatorRules(); len(rules) != 0 {
		t.Error("trying a draft saved it")
	}
	// A view whose visit was never recorded has no country; the fence's
	// sharp edge, posted with the draft, refuses it.
	if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_pageviews(id, session_id, url_path, event_type, created_at) VALUES('e', 'gone', '/article', 1, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	form = url.Values{"sh_allow_countries": {"IN"}, "sh_refuse_unknown": {"on"}}
	r = httptest.NewRequest(http.MethodPost, "/os/api/shield/simulate", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	a.handleOSShieldSimulate(w, r)
	if !strings.Contains(w.Body.String(), "would have refused <strong>1</strong>") {
		t.Errorf("the posted sharp edge was not tried:\n%s", w.Body.String())
	}
	if !strings.Contains(a.shieldPolicyBand(context.Background(), false), `hx-post="/os/api/shield/simulate" hx-include="closest form"`) {
		t.Error("the Security page does not offer the replay")
	}
}

// With the fence's sharp edge on, a view whose country is unknown is refused.
func TestADraftsFenceRefusesTheUnknownWhenAsked(t *testing.T) {
	views := []analytics.PathCountryViews{{Path: "/a", Country: "", Views: 4}, {Path: "/a", Country: "IN", Views: 6}}
	if s := simulateShield(views, shieldDraft{Allow: []string{"IN"}}, false, 7); s.Refused != 0 {
		t.Fatalf("refused %d without the sharp edge; want 0", s.Refused)
	}
	if s := simulateShield(views, shieldDraft{Allow: []string{"IN"}, RefuseUnknown: true}, false, 7); s.Refused != 4 {
		t.Errorf("refused %d with it; want the 4 of unknown country", s.Refused)
	}
}

// A site's draft is tried against that site's readers alone, after the
// install's saved lists and rules, as the shield runs them there; a body
// naming another site is refused.
func TestASitesDraftIsTriedAfterTheInstallsOnItsOwnReaders(t *testing.T) {
	a, d := shieldSitesApp(t)
	a.analytics = analytics.New(dbpkg.DB)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := dbpkg.DB.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO analytics_sessions(id, visitor_id, country, domain_id) VALUES('in','v1','IN','c1'),('zz','v2','ZZ','c1'),('pr','v3','IN','')`)
	for i, v := range []struct{ sess, path, site string }{
		{"in", "/a", "c1"}, {"in", "/a", "c1"}, {"in", "/b", "c1"}, {"in", "/b", "c1"}, {"in", "/b", "c1"}, {"zz", "/c", "c1"},
		{"pr", "/b", ""}, {"pr", "/b", ""},
	} {
		exec(`INSERT INTO analytics_pageviews(id, session_id, url_path, event_type, domain_id, created_at) VALUES(?,?,?,1,?,CURRENT_TIMESTAMP)`,
			"v"+strconv.Itoa(i), v.sess, v.path, v.site)
	}
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{
		settings.KeyShieldRules: "block path=/a", settings.KeyShieldDenyCountries: "ZZ",
	}); err != nil {
		t.Fatal(err)
	}
	a.vayuShield.ApplySettings(a.shieldSettings(ctx))
	post := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/os/d/c1/api/shield/simulate", strings.NewReader(body))
		rc := chi.NewRouteContext()
		rc.URLParams.Add("id", d.ID)
		c := context.WithValue(r.Context(), chi.RouteCtxKey, rc)
		c = context.WithValue(c, ctxScopedDomainKey, d)
		c = context.WithValue(c, ctxUserKey, &users.User{ID: "op", Role: users.RoleAdmin})
		w := httptest.NewRecorder()
		a.handleOSScopedShieldSimulate(w, r.WithContext(c))
		return w
	}
	w := post(`{"domain_id":"c1","settings":{"` + settings.KeyShieldSiteRules + `":"allow path=/a\nchallenge path=/b"}}`)
	var out struct{ Report string }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != http.StatusOK {
		t.Fatalf("answered %d %s", w.Code, w.Body.String())
	}
	for _, want := range []string{"Against 6 reader views", "refused <strong>3</strong>", "challenged <strong>3</strong>",
		"the install&#39;s line 1: block path=/a &middot; 2 views", "this site&#39;s line 2: challenge path=/b &middot; 3 views",
		"the country lists &middot; 1 view<"} {
		if !strings.Contains(out.Report, want) {
			t.Errorf("the report has no %q:\n%s", want, out.Report)
		}
	}
	if w := post(`{"domain_id":"someone-else","settings":{}}`); w.Code != http.StatusBadRequest {
		t.Errorf("a body naming another site answered %d, want 400", w.Code)
	}
	if !strings.Contains(string(a.scopedShieldForm(ctx, d)), "data-shield-try") || !strings.Contains(scopedShieldScript("n"), "/api/shield/simulate") {
		t.Error("the site's Security page does not offer the replay")
	}
}
