// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/settings"
)

// askShield sends one GET for path on host through a's shield and reports
// whether it was served.
func askShield(a *App, host, path string) bool {
	ok := false
	h := a.vayuShield.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ok = true }))
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host, req.RemoteAddr = host, "198.51.100.9:40000"
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0")
	h.ServeHTTP(httptest.NewRecorder(), req)
	return ok
}

// The install's rules, stored under their key, are what the shield applies,
// and the Security page lists each with what it has matched.
func TestTheInstallsSavedRulesApplyAndAreCounted(t *testing.T) {
	a, _ := shieldSitesApp(t)
	ctx := context.Background()
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{
		settings.KeyShieldRules: "block path^=/wp-\ntest block path=/probe\nrefuse path=/x",
	}); err != nil {
		t.Fatal(err)
	}
	a.vayuShield.ApplySettings(a.shieldSettings(ctx))
	if askShield(a, "example.test", "/wp-login.php") {
		t.Error("the install's saved block rule is not in force")
	}
	if !askShield(a, "example.test", "/probe") {
		t.Error("a test rule acted")
	}
	band := a.shieldPolicyBand(ctx, false)
	for _, want := range []string{`id="sh_rules"`, "Line 1 <code>block path^=/wp-</code> &middot; blocked 1 request, last",
		"Line 2 <code>test block path=/probe</code> &middot; would have blocked 1 request", "line 3: starts with &#34;refuse&#34;"} {
		if !strings.Contains(band, want) {
			t.Errorf("the Security page has no %q", want)
		}
	}
}

// A site's rules, saved on its Security page, are in force at once on that
// site alone; a line that cannot be read is named in the answer and on the
// page, and the rest stand.
func TestASitesSavedRulesApplyToThatSiteAlone(t *testing.T) {
	a, d := shieldSitesApp(t)
	body, _ := json.Marshal(map[string]any{"domain_id": "c1", "settings": map[string]string{
		settings.KeyShieldSiteRules: "block path=/private\nrefuse path=/x",
	}})
	w := saveSiteShield(a, d, string(body))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "line 2") {
		t.Fatalf("save answered %d %s; want ok, naming line 2", w.Code, w.Body.String())
	}
	if askShield(a, "one.example", "/private") {
		t.Error("the site's rule is not in force on the site")
	}
	if !askShield(a, "example.test", "/private") {
		t.Error("the site's rule applied to the primary")
	}
	form := string(a.scopedShieldForm(context.Background(), d))
	for _, want := range []string{"Line 1 <code>block path=/private</code> &middot; blocked 1 request", "line 2: starts with", "run after the install&#39;s"} {
		if !strings.Contains(form, want) && !strings.Contains(form, strings.ReplaceAll(want, "&#39;", "'")) {
			t.Errorf("the site's form has no %q", want)
		}
	}
}

// The connector is told what each rule did and never the rule: a line can
// carry a value the operator would not hand out.
func TestTheConnectorCountsRulesWithoutTheirText(t *testing.T) {
	a, _ := shieldSitesApp(t)
	ctx := context.Background()
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{
		settings.KeyShieldRules: "allow header:X-Health=s3cret-token",
	}); err != nil {
		t.Fatal(err)
	}
	a.vayuShield.ApplySettings(a.shieldSettings(ctx))
	out, _ := json.Marshal(shieldRuleCounts(a.vayuShield))
	if strings.Contains(string(out), "s3cret") || strings.Contains(string(out), "X-Health") {
		t.Errorf("the connector was handed the rule's text: %s", out)
	}
	if !strings.Contains(string(out), `"action":"allow"`) {
		t.Errorf("the connector was not told the rule exists: %s", out)
	}
}

// A rule keyed on a country depends on the same lookup as the country lists,
// so the posture report counts it when it asks whether any geography is set.
func TestACountryRuleCountsAsAGeoRule(t *testing.T) {
	a, _ := shieldSitesApp(t)
	ctx := context.Background()
	if a.shieldAuditInputs(nil).GeoRulesSet {
		t.Fatal("setup: geography reported set with nothing saved")
	}
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{
		settings.KeyShieldRules: "block path^=/x\nchallenge Country=CN",
	}); err != nil {
		t.Fatal(err)
	}
	if !a.shieldAuditInputs(nil).GeoRulesSet {
		t.Error("a country rule is not counted as geography, so the real-address warning cannot name it")
	}
}

// The install's form posts the rules field, and the save applies it.
func TestTheInstallsFormSavesItsRules(t *testing.T) {
	a, _ := shieldSitesApp(t)
	prev := config.TrustCloudflareEnabled()
	t.Cleanup(func() { config.SetTrustCloudflare(prev) })
	form := url.Values{"sh_enabled": {"on"}, "sh_rules": {"block path=/gone"}}
	r := httptest.NewRequest(http.MethodPost, "/os/api/shield/settings", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handleOSShieldSettings(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("save answered %d %s", w.Code, w.Body.String())
	}
	if rules, _ := a.vayuShield.OperatorRules(); len(rules) != 1 || rules[0].Text != "block path=/gone" {
		t.Errorf("the saved rule is not the shield's: %v", rules)
	}
}

// A limit's line on the page says what it refused, beside what it matched,
// and the connector carries the refusals too.
func TestALimitShowsWhatItRefused(t *testing.T) {
	a, _ := shieldSitesApp(t)
	ctx := context.Background()
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{
		settings.KeyShieldRules: "limit 1/min path=/search\ntest limit 1/min path=/notes",
	}); err != nil {
		t.Fatal(err)
	}
	a.vayuShield.ApplySettings(a.shieldSettings(ctx))
	for i := 0; i < 2; i++ {
		askShield(a, "example.test", "/search")
		askShield(a, "example.test", "/notes")
	}
	band := a.shieldPolicyBand(ctx, false)
	for _, want := range []string{"<code>limit 1/min path=/search</code> &middot; matched 2 requests, refused 1",
		"<code>test limit 1/min path=/notes</code> &middot; matched 2 requests, would have refused 1"} {
		if !strings.Contains(band, want) {
			t.Errorf("the Security page has no %q", want)
		}
	}
	out, _ := json.Marshal(shieldRuleCounts(a.vayuShield))
	if !strings.Contains(string(out), `"refused":1`) {
		t.Errorf("the connector was not told what the limit refused: %s", out)
	}
}
