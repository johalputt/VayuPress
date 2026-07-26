// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/render"
)

// The install-health check exists because "installable" is a conjunction of a
// dozen requirements and the install flow tells you nothing about which one
// failed — you find out weeks later, when the icon is gone after a reboot.
//
// A diagnostic that reports a false pass is worse than none, so these tests are
// mostly about the checks being able to FAIL.

func healthChecks(t *testing.T) []pwaCheck {
	t.Helper()
	config.Cfg.Domain = "example.com"
	render.SetActiveSettings(render.SiteSettings{Name: "Acme"})
	t.Cleanup(func() { render.SetActiveSettings(render.SiteSettings{}) })
	return (&App{}).pwaHealthChecks(httptest.NewRequest("GET", "/os/website", nil))
}

func findPWACheck(t *testing.T, checks []pwaCheck, namePart string) pwaCheck {
	t.Helper()
	for _, c := range checks {
		if strings.Contains(c.Name, namePart) {
			return c
		}
	}
	t.Fatalf("no check matching %q", namePart)
	return pwaCheck{}
}

// TestInstallHealthPassesOnThisBuild is the baseline: everything this instance
// controls must currently be right.
func TestInstallHealthPassesOnThisBuild(t *testing.T) {
	for _, c := range healthChecks(t) {
		if !c.OK {
			t.Errorf("check %q fails on the shipped build: %s", c.Name, c.Detail)
		}
	}
}

// TestInstallHealthCoversEveryRequirement guards against the checklist quietly
// losing an item — a missing check reads as a pass.
func TestInstallHealthCoversEveryRequirement(t *testing.T) {
	checks := healthChecks(t)
	for _, want := range []string{
		"Manifest is served", "required field", "display is standalone",
		"icon matches its declared size", "192px and 512px", "maskable",
		"served as JavaScript", "declares its scope",
		"register the worker", "link the manifest", "iOS install tags",
		"Bot protection",
	} {
		findPWACheck(t, checks, want) // fails the test if absent
	}
}

// TestIconSizeCheckCatchesAMismatch is the important one. The bug that shipped
// before v3.15.31 was a manifest declaring sizes its files did not have, and it is
// invisible: the icons load, they are just the wrong size. Reading the real PNG
// header is what makes that detectable.
func TestIconSizeCheckCatchesAMismatch(t *testing.T) {
	// The real 192 icon, checked against a claim of 512.
	b, _ := webAppIconBytes("/static/icons/webapp-192.png")
	w, h, ok := pngSize(b)
	if !ok {
		t.Fatal("the embedded 192 icon is not a readable PNG")
	}
	if w != 192 || h != 192 {
		t.Fatalf("embedded icon is %dx%d, want 192x192", w, h)
	}
	// pngSize must reject non-PNG bytes rather than returning a plausible number.
	if _, _, ok := pngSize([]byte("not a png at all, really")); ok {
		t.Error("pngSize accepted non-PNG bytes")
	}
	if _, _, ok := pngSize([]byte{0x89, 'P', 'N', 'G'}); ok {
		t.Error("pngSize accepted a truncated header")
	}
}

// TestHealthChipDoesNotOverclaim pins the wording. Server-side checks prove what
// the ORIGIN serves; the failure this exists to catch is an edge in front of it
// intercepting the manifest, which no server-side check can see. Saying
// "Installable" on the strength of them would be the diagnostic lying.
func TestHealthChipDoesNotOverclaim(t *testing.T) {
	config.Cfg.Domain = "example.com"
	render.SetActiveSettings(render.SiteSettings{Name: "Acme"})
	t.Cleanup(func() { render.SetActiveSettings(render.SiteSettings{}) })
	card := (&App{}).pwaHealthCardHTML(httptest.NewRequest("GET", "/os/website", nil), "n0nce")

	if strings.Contains(card, ">Installable<") {
		t.Error(`the chip must not claim "Installable" from server-side checks alone`)
	}
	if !strings.Contains(card, "Origin OK") {
		t.Error("expected the chip to say the origin is OK, not that installation works")
	}
	// The card must always be expanded: collapsed-when-green is how the
	// browser-side failure goes unnoticed.
	if !strings.Contains(card, `<details class="mon-acc" open>`) {
		t.Error("the install-health card must be expanded, or its browser half is never seen")
	}
	if !strings.Contains(card, "admin-os-pwa.js") {
		t.Error("the card must load the browser-side probe")
	}
}

// TestBrowserProbeDoesNotReportAFalseFailure covers the flaw found by running it:
// the probe runs from the CONSOLE, which registers /os/sw.js at scope /os/. The
// public worker at scope / is registered by public pages, so its absence there
// means "the public site has not been opened in this browser", not "broken".
func TestBrowserProbeDoesNotReportAFalseFailure(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "static", "js", "admin-os-pwa.js"))
	if err != nil {
		t.Fatalf("read admin-os-pwa.js: %v", err)
	}
	js := string(b)
	if !strings.Contains(js, "getRegistrations()") {
		t.Error("the probe must list ALL registrations — asking only for scope / reports a false failure in the console")
	}
	if strings.Contains(js, `getRegistration('/')`) {
		t.Error("getRegistration('/') from the console always misses; it made the diagnostic lie")
	}
	// The manifest check must inspect the body, not just the status: a CDN
	// challenge is a 200 with an HTML body.
	if !strings.Contains(js, "indexOf('json')") {
		t.Error("the manifest probe must verify the content type, or a challenge page passes as success")
	}
	// Nothing may be built with innerHTML: these rows carry server and network
	// strings.
	if strings.Contains(withoutComments(js), "innerHTML") {
		t.Error("the probe must build rows with textContent")
	}
}
