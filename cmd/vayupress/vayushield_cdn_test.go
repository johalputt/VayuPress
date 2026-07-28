// SPDX-License-Identifier: Apache-2.0

package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/settings"
)

// The hardening panel used to assert, unconditionally, that nothing was proxying
// the origin — and that per-IP limits were therefore "fully effective". On a
// CDN-fronted install that claim is false in the most damaging direction: it
// tells the operator the limits they just switched on are protecting visitors,
// while those limits are actually measuring a handful of edge addresses. The
// audience then looks like a few very busy clients, trips the rate limit, and
// gets challenged or dropped.
//
// These tests pin that the panel reports what is actually happening.

func newShieldApp(t *testing.T, behindCDN string) *App {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE site_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '', updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("settings schema: %v", err)
	}
	st := settings.New(db)
	if behindCDN != "" {
		if err := st.SetMany(t.Context(), map[string]string{settings.KeyShieldBehindCDN: behindCDN}); err != nil {
			t.Fatalf("set behind_cdn: %v", err)
		}
	}
	return &App{siteSettings: st}
}

func TestDetectCDNFromRealVendorHeaders(t *testing.T) {
	for _, tc := range []struct {
		header, value, wantVendor string
		wantCDN                   bool
	}{
		{"CF-Ray", "8a1b2c3d4e5f6789-DEL", "Cloudflare", true},
		{"CF-Connecting-IP", "203.0.113.9", "Cloudflare", true},
		{"X-Amz-Cf-Id", "abc123", "CloudFront", true},
		{"Fastly-Client-IP", "203.0.113.9", "Fastly", true},
		{"X-Azure-Ref", "ref", "Azure Front Door", true},
		{"CDN-Loop", "cloudflare", "a proxy", true},
		// The critical negative: a plain local nginx reverse proxy sets
		// X-Forwarded-For on EVERY request. Treating it as a CDN signal would
		// report a proxy in front of every ordinary install and make the advisory
		// worthless noise.
		{"X-Forwarded-For", "203.0.113.9", "", false},
		{"X-Real-IP", "203.0.113.9", "", false},
	} {
		req := httptest.NewRequest(http.MethodGet, "/os/shield", nil)
		if tc.header != "" {
			req.Header.Set(tc.header, tc.value)
		}
		gotCDN, gotVendor := shieldDetectCDN(req)
		if gotCDN != tc.wantCDN || gotVendor != tc.wantVendor {
			t.Errorf("%s: got (%v, %q), want (%v, %q)", tc.header, gotCDN, gotVendor, tc.wantCDN, tc.wantVendor)
		}
	}
	if got, _ := shieldDetectCDN(httptest.NewRequest(http.MethodGet, "/os/shield", nil)); got {
		t.Error("a bare request reported a CDN in front")
	}
	if got, _ := shieldDetectCDN(nil); got {
		t.Error("a nil request reported a CDN in front")
	}
}

// TestAdvisoryNeverClaimsDirectWhenProxied is the regression test for the bug.
func TestAdvisoryNeverClaimsDirectWhenProxied(t *testing.T) {
	a := newShieldApp(t, "off")
	req := httptest.NewRequest(http.MethodGet, "/os/shield", nil)
	req.Header.Set("CF-Ray", "8a1b2c3d4e5f6789-DEL")

	got := a.shieldCDNAdvisory(req)
	if strings.Contains(got, "No proxy detected") {
		t.Error("advisory claims no proxy while the request carries CF-Ray")
	}
	if !strings.Contains(got, "Cloudflare is proxying this origin") {
		t.Errorf("advisory does not name the detected proxy:\n%s", got)
	}
	// With the trust switch off, this is the state that breaks the site — it has
	// to be called out, not merely described.
	if !strings.Contains(got, "is switched off") {
		t.Errorf("advisory does not warn that the trust switch is off:\n%s", got)
	}
}

// TestAdvisoryAlwaysSeparatesTier2 — Tier 2 is the one no setting can fix. It
// runs in the kernel, before any HTTP header exists, so its per-IP limits key on
// edge addresses whether or not the operator flipped the trust switch. Folding it
// in with Tiers 1 and 3 is what leaves someone throttling their own CDN while
// believing they had configured their way out of it.
func TestAdvisoryAlwaysSeparatesTier2(t *testing.T) {
	for _, trust := range []string{"off", "on"} {
		a := newShieldApp(t, trust)
		req := httptest.NewRequest(http.MethodGet, "/os/shield", nil)
		req.Header.Set("CF-Connecting-IP", "203.0.113.9")

		got := a.shieldCDNAdvisory(req)
		if !strings.Contains(got, "no switch fixes it") {
			t.Errorf("behind_cdn=%s: advisory does not say Tier 2 is unfixable by a setting:\n%s", trust, got)
		}
		if !strings.Contains(got, "Allowlist") {
			t.Errorf("behind_cdn=%s: advisory does not give the Tier 2 remedy:\n%s", trust, got)
		}
	}
}

// TestAdvisoryConfirmsTier1WhenTrusted — with the switch on, the operator has
// actually fixed something and should be told so; a warning that never clears is
// a warning people learn to ignore.
func TestAdvisoryConfirmsTier1WhenTrusted(t *testing.T) {
	a := newShieldApp(t, "on")
	req := httptest.NewRequest(http.MethodGet, "/os/shield", nil)
	req.Header.Set("CF-Ray", "abc-DEL")

	got := a.shieldCDNAdvisory(req)
	if strings.Contains(got, "is switched off") {
		t.Error("advisory still warns the trust switch is off after it was turned on")
	}
	if !strings.Contains(got, "reading the real visitor address") {
		t.Errorf("advisory does not confirm Tier 1 is now per-visitor:\n%s", got)
	}
}

// TestAdvisoryReportsDirectWhenNotProxied — the honest converse. An unproxied
// origin must not be nagged about a CDN it does not have.
func TestAdvisoryReportsDirectWhenNotProxied(t *testing.T) {
	a := newShieldApp(t, "")
	got := a.shieldCDNAdvisory(httptest.NewRequest(http.MethodGet, "/os/shield", nil))
	if !strings.Contains(got, "No proxy detected") {
		t.Errorf("advisory does not report a direct origin:\n%s", got)
	}
	if strings.Contains(got, "no switch fixes it") {
		t.Errorf("advisory shows the Tier 2 proxy warning with no proxy present:\n%s", got)
	}
}

// TestHardeningBodyCSPSafe — the VayuOS shell forbids inline style attributes,
// and this section is re-rendered by HTMX every 10 seconds, so a CSP violation
// here would fire continuously rather than once.
func TestHardeningBodyCSPSafe(t *testing.T) {
	a := newShieldApp(t, "off")
	req := httptest.NewRequest(http.MethodGet, "/os/shield", nil)
	req.Header.Set("CF-Ray", "abc-DEL")
	body := a.shieldHardeningBody(req)
	if strings.Contains(body, `style="`) {
		t.Error("hardening body contains an inline style attribute")
	}
	if strings.Contains(body, "<script") {
		t.Error("hardening body contains an inline script")
	}
}
