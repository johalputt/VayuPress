// SPDX-License-Identifier: Apache-2.0

package vayushield

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johalputt/vayupress/internal/vayushield/policy"
)

// siteShield is a shield serving two sites, a.test and b.test, whose
// requests it tells apart by host.
func siteShield(install policy.Config) *Manager {
	m := New(Config{Enabled: true, SiteFn: func(r *http.Request) string {
		switch r.Host {
		case "a.test":
			return "sa"
		case "b.test":
			return "sb"
		}
		return ""
	}})
	m.ApplySettings(Settings{Enabled: true, Policy: install})
	return m
}

// served reports whether a GET of host from ip reached the site.
func served(m *Manager, host, ip string) bool {
	ok := false
	h := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ok = true }))
	req := httptest.NewRequest(http.MethodGet, "/article", nil)
	req.Host = host
	req.RemoteAddr = ip + ":40000"
	h.ServeHTTP(httptest.NewRecorder(), req)
	return ok
}

// A site's own refusal is that site's: the network it refuses still reads
// every other site.
func TestASitesOwnDenyRefusesOnThatSiteAlone(t *testing.T) {
	m := siteShield(policy.Config{})
	if bad := m.SetSiteSettings(map[string]SiteSettings{"sa": {Policy: policy.Config{DenyCIDRs: []string{"198.51.100.0/24"}}}}); len(bad) != 0 {
		t.Fatalf("issues: %v", bad)
	}
	if served(m, "a.test", "198.51.100.9") {
		t.Error("site a served a network it refuses")
	}
	if !served(m, "b.test", "198.51.100.9") {
		t.Error("site b refused a network only site a refuses")
	}
}

// A site adds to the install's lists and never takes from them: allowing a
// source on one site does not lift the install's refusal of it.
func TestASiteCannotLiftTheInstallsRefusal(t *testing.T) {
	m := siteShield(policy.Config{DenyCIDRs: []string{"203.0.113.0/24"}})
	m.SetSiteSettings(map[string]SiteSettings{"sa": {Policy: policy.Config{AllowCIDRs: []string{"203.0.113.5"}}}})
	if served(m, "a.test", "203.0.113.5") {
		t.Error("a site's allow list lifted the install's refusal")
	}
}

// The install's lists are merged into every site's when either changes: a
// refusal added to the install after a site saved its own still applies there.
func TestTheInstallsNewRefusalReachesASiteWithItsOwnLists(t *testing.T) {
	m := siteShield(policy.Config{})
	m.SetSiteSettings(map[string]SiteSettings{"sa": {Policy: policy.Config{DenyCountries: []string{"ZZ"}}}})
	m.ApplySettings(Settings{Enabled: true, Policy: policy.Config{DenyCIDRs: []string{"192.0.2.0/24"}}})
	if served(m, "a.test", "192.0.2.7") {
		t.Error("the install's new refusal did not reach a site that has lists of its own")
	}
}

// A site's thresholds replace the install's for its own requests only; one it
// leaves unset is the install's.
func TestASitesThresholdsAreItsOwn(t *testing.T) {
	m := siteShield(policy.Config{})
	m.SetSiteSettings(map[string]SiteSettings{"sa": {BlockThreshold: 0.5}})
	a := httptest.NewRequest(http.MethodGet, "/", nil)
	a.Host = "a.test"
	b := httptest.NewRequest(http.MethodGet, "/", nil)
	b.Host = "b.test"
	if pow, _, block := m.thresholdsFor(a, 0.4, 0.6, 0.8); block != 0.5 || pow != 0.4 {
		t.Errorf("site a: pow %v block %v; want its own block 0.5 and the install's pow 0.4", pow, block)
	}
	if _, _, block := m.thresholdsFor(b, 0.4, 0.6, 0.8); block != 0.8 {
		t.Errorf("site b took site a's block threshold: %v", block)
	}
	m.SetSiteSettings(map[string]SiteSettings{"sa": {BlockThreshold: 7}})
	if _, _, block := m.thresholdsFor(a, 0.4, 0.6, 0.8); block != 0.8 {
		t.Errorf("an out-of-range threshold was applied: %v", block)
	}
}
