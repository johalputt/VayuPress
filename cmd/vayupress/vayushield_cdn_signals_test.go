// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// These tests are written from a real incident. A Cloudflare-proxied site
// (example.test) showed "No proxy detected in front of this origin on your own
// request" in the hardening panel while its kernel firewall was rate-limiting
// Cloudflare's edge nodes. The detection was not buggy — the administrator
// reached the console through a hosts entry pointing at the origin, so their
// request genuinely did not come through the CDN. The panel answered "did THIS
// request come through a proxy?" while presenting itself as answering "is this
// SITE behind a proxy?".
//
// The distinction matters because the person reading that notice is the
// administrator, and administrators are exactly the population most likely to be
// bypassing their own edge.

// resetCDNObservation clears the package-level sighting so each case starts from
// a known state.
func resetCDNObservation(t *testing.T) {
	t.Helper()
	cdnSeenUnix.Store(0)
	cdnSeenVendor.Store("")
	t.Cleanup(func() { cdnSeenUnix.Store(0); cdnSeenVendor.Store("") })
}

func seedCDNObservation(vendor string, age time.Duration) {
	cdnSeenVendor.Store(vendor)
	cdnSeenUnix.Store(time.Now().Add(-age).Unix())
}

// TestAdvisoryNeverClaimsUnproxiedFromAbsence is the core regression. Absence of
// a header is not evidence the site is unproxied, and the wording must not
// pretend otherwise — that reassurance is what stops an operator allowlisting the
// ranges their kernel is dropping.
func TestAdvisoryNeverClaimsUnproxiedFromAbsence(t *testing.T) {
	resetCDNObservation(t)
	a := newShieldApp(t, "off")
	got := a.shieldCDNAdvisory(httptest.NewRequest(http.MethodGet, "/os/shield", nil))

	if strings.Contains(got, "No proxy detected") {
		t.Error("advisory still reports a positive finding of 'no proxy' from an absence of evidence")
	}
	if !strings.Contains(got, "absence of a signal") {
		t.Errorf("advisory does not frame the negative case as an absence of evidence:\n%s", got)
	}
	if strings.Contains(got, "fully effective") {
		t.Error("advisory claims limits are 'fully effective' with no positive evidence the origin is direct")
	}
}

// TestObservedTrafficBeatsTheOperatorsOwnRequest reproduces example.test exactly: the
// site IS proxied (visitor traffic proves it) while the admin's own request is
// direct. This is the case the original design got wrong.
func TestObservedTrafficBeatsTheOperatorsOwnRequest(t *testing.T) {
	resetCDNObservation(t)
	seedCDNObservation("Cloudflare", time.Minute)
	a := newShieldApp(t, "on")

	// The operator's request carries NO CDN header — a hosts entry to the origin.
	got := a.shieldCDNAdvisory(httptest.NewRequest(http.MethodGet, "/os/shield", nil))

	if !strings.Contains(got, "Cloudflare is proxying this site") {
		t.Errorf("advisory ignored the sighting from visitor traffic:\n%s", got)
	}
	if !strings.Contains(got, "Your own connection is not going through it") {
		t.Error("advisory does not tell the operator their own connection bypasses the proxy")
	}
	// Naming the likely cause is the difference between a confusing notice and one
	// that ends the investigation.
	if !strings.Contains(got, "hosts") {
		t.Errorf("advisory does not name the usual cause (a hosts entry):\n%s", got)
	}
	// And the Tier 2 warning MUST still appear — that is the advice that prevents
	// the kernel silently dropping edge connections.
	if !strings.Contains(got, "no switch fixes it") {
		t.Error("the Tier 2 kernel carve-out disappeared in the very case it exists for")
	}
}

// TestObservationExpires — a sighting must not outlive its usefulness, or turning
// a proxy off would leave the panel insisting one is still there.
func TestObservationExpires(t *testing.T) {
	resetCDNObservation(t)
	seedCDNObservation("Cloudflare", cdnObservationTTL+time.Hour)
	if got := lastCDNObservation(); got != "" {
		t.Errorf("a sighting older than the TTL is still reported (%q)", got)
	}
	seedCDNObservation("Cloudflare", cdnObservationTTL-time.Hour)
	if got := lastCDNObservation(); got != "Cloudflare" {
		t.Errorf("a sighting inside the TTL was discarded (got %q)", got)
	}
}

// TestDeclaredSettingIsHonouredWithoutAnySighting — a quiet site may never have
// served a proxied request the process has seen (the observation is in-memory and
// resets on restart). The operator's explicit switch must still drive the advice.
func TestDeclaredSettingIsHonouredWithoutAnySighting(t *testing.T) {
	resetCDNObservation(t)
	a := newShieldApp(t, "on")
	got := a.shieldCDNAdvisory(httptest.NewRequest(http.MethodGet, "/os/shield", nil))

	if strings.Contains(got, "No proxy") {
		t.Error("the operator's explicit 'behind a CDN' setting was overridden by an absence of headers")
	}
	if !strings.Contains(got, "marked this site") {
		t.Errorf("advisory does not acknowledge the operator's setting:\n%s", got)
	}
	if !strings.Contains(got, "no switch fixes it") {
		t.Error("Tier 2 guidance is hidden despite the operator declaring a proxy")
	}
}

// TestDirectRequestStillWinsWhenPresent — a header on the request in hand is the
// strongest signal and should be reported as such, naming the vendor.
func TestDirectRequestStillWinsWhenPresent(t *testing.T) {
	resetCDNObservation(t)
	a := newShieldApp(t, "on")
	req := httptest.NewRequest(http.MethodGet, "/os/shield", nil)
	req.Header.Set("CF-Ray", "abc-DEL")

	got := a.shieldCDNAdvisory(req)
	if !strings.Contains(got, "Cloudflare is proxying this origin") {
		t.Errorf("a CDN header on the request was not reported:\n%s", got)
	}
	if strings.Contains(got, "Your own connection is not going through it") {
		t.Error("advisory claims the operator bypasses the proxy while their request carries its header")
	}
}

// TestNoteCDNObservationRecordsAndThrottles — this runs in the request path on
// every request, so it has to be cheap. It also must not record a sighting from a
// request that carries no proxy header.
func TestNoteCDNObservationRecordsAndThrottles(t *testing.T) {
	resetCDNObservation(t)

	plain := httptest.NewRequest(http.MethodGet, "/", nil)
	noteCDNObservation(plain)
	if lastCDNObservation() != "" {
		t.Error("a request with no proxy header was recorded as a sighting")
	}

	proxied := httptest.NewRequest(http.MethodGet, "/", nil)
	proxied.Header.Set("CF-Connecting-IP", "203.0.113.9")
	noteCDNObservation(proxied)
	if got := lastCDNObservation(); got != "Cloudflare" {
		t.Fatalf("sighting not recorded (got %q)", got)
	}

	// Throttle: with a fresh sighting the header scan is skipped entirely, so a
	// different vendor arriving seconds later must not overwrite it.
	//
	// Assert on the VENDOR, not the timestamp. The timestamp has one-second
	// granularity, so two calls in the same second are indistinguishable and an
	// earlier version of this check passed with the throttle removed entirely.
	other := httptest.NewRequest(http.MethodGet, "/", nil)
	other.Header.Set("Fastly-Client-IP", "203.0.113.9")
	noteCDNObservation(other)
	if got := lastCDNObservation(); got != "Cloudflare" {
		t.Errorf("the throttle did not hold — vendor changed to %q; this runs on every request and must stay near-free", got)
	}
}

// TestNoteCDNObservationIsRaceFree — realIPMiddleware runs concurrently on every
// request, so the sighting must be safe under parallel writes. Run with -race.
func TestNoteCDNObservationIsRaceFree(t *testing.T) {
	resetCDNObservation(t)
	var done atomic.Int32
	for i := 0; i < 16; i++ {
		go func() {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("CF-Ray", "x")
			for j := 0; j < 50; j++ {
				noteCDNObservation(req)
				_ = lastCDNObservation()
			}
			done.Add(1)
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for done.Load() < 16 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if done.Load() != 16 {
		t.Fatal("goroutines did not finish")
	}
}
