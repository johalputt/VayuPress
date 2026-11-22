// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/vayushield/waf"
)

func wafApp(t *testing.T, mode waf.Mode) *App {
	t.Helper()
	a, _ := shieldSitesApp(t)
	a.waf = waf.New()
	if err := a.waf.Configure(mode, 1); err != nil {
		t.Fatal(err)
	}
	return a
}

func throughWAF(a *App, r *http.Request) int {
	h := a.wafMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Code
}

func wafGet(target string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) Firefox/121.0")
	r.Header.Set("Accept", "text/html")
	r.RemoteAddr = "203.0.113.40:5000"
	return r
}

var sqli = "?q=" + url.QueryEscape("1' UNION SELECT username, password FROM users--")

// Set to refuse, an injection on a public page is answered 403; the same
// request to the console or with an API key is not read; recording only,
// it is served.
func TestTheFirewallRefusesOnThePublicSiteOnly(t *testing.T) {
	a := wafApp(t, waf.ModeBlock)
	if code := throughWAF(a, wafGet("/search"+sqli)); code != http.StatusForbidden {
		t.Errorf("an injection on a public page was answered %d in refuse mode", code)
	}
	if code := throughWAF(a, wafGet("/os/posts"+sqli)); code != http.StatusOK {
		t.Errorf("the console was read by the firewall (%d)", code)
	}
	keyed := wafGet("/api/v1/articles" + sqli)
	keyed.Header.Set("X-API-Key", "k")
	if code := throughWAF(a, keyed); code != http.StatusOK {
		t.Errorf("an API-key request was read by the firewall (%d)", code)
	}
	if code := throughWAF(wafApp(t, waf.ModeDetect), wafGet("/search"+sqli)); code != http.StatusOK {
		t.Errorf("recording only, an injection was answered %d", code)
	}
}

// The Shield form saves the mode and paranoia level, a value it does not
// offer is stored as the safe one, and the firewall is rebuilt to match.
func TestTheShieldFormSavesTheFirewall(t *testing.T) {
	a := wafApp(t, waf.ModeOff)
	ctx := context.Background()
	saveShieldForm(t, a, url.Values{"sh_enabled": {"on"}, "sh_waf": {"detect"}, "sh_waf_pl": {"2"}})
	get := func(k string) string { return a.siteSettings.Get(ctx, settings.ForPrimary(), k) }
	if get(settings.KeyShieldWAF) != "detect" || get(settings.KeyShieldWAFParanoia) != "2" || a.waf.Mode() != waf.ModeDetect || a.waf.Paranoia() != 2 {
		t.Fatalf("saved %q at %q; the firewall is %q at %d", get(settings.KeyShieldWAF), get(settings.KeyShieldWAFParanoia), a.waf.Mode(), a.waf.Paranoia())
	}
	saveShieldForm(t, a, url.Values{"sh_enabled": {"on"}, "sh_waf": {"everything"}, "sh_waf_pl": {"9"}})
	if get(settings.KeyShieldWAF) != "off" || get(settings.KeyShieldWAFParanoia) != "1" {
		t.Fatalf("values the form does not offer were saved as %q and %q", get(settings.KeyShieldWAF), get(settings.KeyShieldWAFParanoia))
	}
	if band := a.wafBand(); !strings.Contains(band, `id="sh_waf"`) || !strings.Contains(band, `id="sh_waf_pl"`) {
		t.Fatal("the settings form offers no firewall fields")
	}
}
