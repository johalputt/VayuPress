// SPDX-License-Identifier: Apache-2.0

package vayushield

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johalputt/vayupress/internal/vayushield/challenge"
)

// ruleShield is a two-site shield (a.test → "sa") whose ladder never acts on
// its own at these thresholds, so whatever a test sees is a rule's doing.
func ruleShield(rules string, mod func(*Config)) *Manager {
	cfg := Config{
		Enabled: true,
		Signer:  challenge.NewSigner([]byte("s")),
		SiteFn: func(r *http.Request) string {
			if r.Host == "a.test" {
				return "sa"
			}
			return ""
		},
	}
	if mod != nil {
		mod(&cfg)
	}
	m := New(cfg)
	m.ApplySettings(Settings{Enabled: true, PoWThreshold: 0.99, JSThreshold: 0.995, BlockThreshold: 1, Rules: rules})
	return m
}

// ask sends a browser GET and returns the status and the shield's header.
func ask(m *Manager, host, path string, hdr ...string) (int, string) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = host
	req.RemoteAddr = "198.51.100.20:40000"
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0")
	req.Header.Set("Accept", "text/html")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	return rec.Code, rec.Header().Get("X-VayuShield")
}

// A block rule refuses the request it names, and only that request: the same
// visitor's next page is served, because a rule installs nothing against its
// source.
func TestABlockRuleRefusesTheRequestAndNotTheVisitor(t *testing.T) {
	m := ruleShield("block path^=/wp-", nil)
	// With the jail on, so that a rule which jailed would be seen to.
	m.ApplySettings(Settings{Enabled: true, PoWThreshold: 0.99, JSThreshold: 0.995, BlockThreshold: 1,
		AutoBlock: true, AutoBlockJailMinutes: 10, Rules: "block path^=/wp-"})
	if code, _ := ask(m, "b.test", "/article"); code != http.StatusOK {
		t.Fatalf("a page no rule names answered %d", code)
	}
	if code, h := ask(m, "b.test", "/wp-login.php"); code != http.StatusForbidden || h != "block" {
		t.Fatalf("the named path answered %d %q; want 403 block", code, h)
	}
	if code, _ := ask(m, "b.test", "/article"); code != http.StatusOK {
		t.Errorf("after one refused request the visitor's next page answered %d; a rule must not jail", code)
	}
	if n, _ := m.opRules.Load().Rules()[0].Hits(); n != 1 {
		t.Errorf("the rule counted %d hits, want 1", n)
	}
}

// A test rule is counted and does nothing.
func TestATestRuleIsCountedAndDoesNotAct(t *testing.T) {
	m := ruleShield("test block path^=/wp-", nil)
	if code, _ := ask(m, "b.test", "/wp-login.php"); code != http.StatusOK {
		t.Errorf("a test rule acted: %d", code)
	}
	if n, _ := m.opRules.Load().Rules()[0].Hits(); n != 1 {
		t.Errorf("the test rule counted %d hits, want 1", n)
	}
}

// A challenge rule asks for a proof of work where the ladder would have served.
func TestAChallengeRuleChallenges(t *testing.T) {
	m := ruleShield("challenge path^=/search", nil)
	if _, h := ask(m, "b.test", "/search"); h != "challenge" {
		t.Errorf("the named path was answered %q; want a challenge", h)
	}
	if _, h := ask(m, "b.test", "/article"); h == "challenge" {
		t.Error("a path no rule names was challenged")
	}
}

// An allow rule serves what the ladder would have refused.
func TestAnAllowRuleOutranksTheLadder(t *testing.T) {
	m := ruleShield("allow header:X-Health=ok", nil)
	m.ApplySettings(Settings{Enabled: true, PoWThreshold: 0.01, JSThreshold: 0.02, BlockThreshold: 0.03, Rules: "allow header:X-Health=ok"})
	script := []string{"User-Agent", "curl/8.5.0", "Accept", "*/*"}
	if code, _ := ask(m, "b.test", "/article", script...); code == http.StatusOK {
		t.Fatal("setup: the ladder served a script at a block threshold of 0.03, so this proves nothing")
	}
	if code, _ := ask(m, "b.test", "/article", append(script, "X-Health", "ok")...); code != http.StatusOK {
		t.Errorf("the allowed request answered %d", code)
	}
}

// A site's rules act on that site alone, after the install's: the install's
// first match decides, so a site can add a rule and never override one.
func TestASitesRulesAreItsOwnAndComeAfterTheInstalls(t *testing.T) {
	m := ruleShield("allow path=/open", nil)
	if bad := m.SetSiteSettings(map[string]SiteSettings{"sa": {Rules: "block path=/private\nblock path=/open"}}); len(bad) != 0 {
		t.Fatalf("issues: %v", bad)
	}
	if code, _ := ask(m, "a.test", "/private"); code != http.StatusForbidden {
		t.Errorf("site a's rule did not refuse on site a: %d", code)
	}
	if code, _ := ask(m, "b.test", "/private"); code != http.StatusOK {
		t.Errorf("site a's rule refused on site b: %d", code)
	}
	if code, _ := ask(m, "a.test", "/open"); code != http.StatusOK {
		t.Errorf("site a's rule overrode the install's allow: %d", code)
	}
	if got := m.SiteOperatorRules("sa"); len(got) != 2 {
		t.Errorf("site a has %d rules on record, want 2", len(got))
	}
}

// A site's unreadable line is named for that site, beside the other issues.
func TestASitesBadRuleIsNamed(t *testing.T) {
	m := ruleShield("", nil)
	bad := m.SetSiteSettings(map[string]SiteSettings{"sa": {Rules: "block path=/x\nrefuse path=/y"}})
	if len(bad["sa"]) != 1 {
		t.Fatalf("issues %v; want the one bad line named for site a", bad)
	}
	if len(m.SiteOperatorRules("sa")) != 1 {
		t.Error("the readable line did not stand")
	}
}

// Observe mode counts a block rule as what it would have done and serves.
func TestObserveModeCountsARuleBlock(t *testing.T) {
	m := ruleShield("", nil)
	m.ApplySettings(Settings{Enabled: true, PoWThreshold: 0.99, JSThreshold: 0.995, BlockThreshold: 1,
		Rules: "block path^=/wp-", ObserveOnly: true})
	if code, _ := ask(m, "b.test", "/wp-login.php"); code != http.StatusOK {
		t.Errorf("observe mode enforced a rule: %d", code)
	}
	if got := m.WouldHave()[GateRule]; got != 1 {
		t.Errorf("would-have count for the rule gate is %d, want 1", got)
	}
}

// In a Tor Space every visitor resolves to the server's own country, so a
// country rule must match nobody there rather than everybody.
func TestACountryRuleMatchesNobodyInATorSpace(t *testing.T) {
	cn := func(c *Config) { c.CountryFn = func(*http.Request, string) string { return "CN" } }
	if code, _ := ask(ruleShield("block country=CN", cn), "b.test", "/article"); code != http.StatusForbidden {
		t.Fatalf("setup: the country rule did not refuse on the clearnet (%d)", code)
	}
	tor := func(c *Config) { cn(c); c.OnionMode = true }
	if code, _ := ask(ruleShield("block country=CN", tor), "b.test", "/article"); code != http.StatusOK {
		t.Errorf("a country rule refused in a Tor Space: %d", code)
	}
}
