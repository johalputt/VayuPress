// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/settings"
)

func saveShieldForm(t *testing.T, a *App, form url.Values) {
	t.Helper()
	prev := config.TrustCloudflareEnabled()
	t.Cleanup(func() { config.SetTrustCloudflare(prev) })
	r := httptest.NewRequest(http.MethodPost, "/os/api/shield/settings", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handleOSShieldSettings(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("save answered %d %s", w.Code, w.Body.String())
	}
}

// Observe-only saved on records when it began; saved again it keeps that
// start, so a second save does not buy it another day; saved off, it has
// none.
func TestObserveOnlyRecordsWhenItBegan(t *testing.T) {
	a, _ := shieldSitesApp(t)
	ctx := context.Background()
	get := func(k string) string { return a.siteSettings.Get(ctx, settings.ForPrimary(), k) }
	saveShieldForm(t, a, url.Values{"sh_enabled": {"on"}, "sh_observe": {"on"}})
	first := get(settings.KeyShieldObserveSince)
	began, err := strconv.ParseInt(first, 10, 64)
	if err != nil || time.Since(time.Unix(began, 0)) > time.Minute {
		t.Fatalf("observe-only saved on began at %q", first)
	}
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{settings.KeyShieldObserveSince: "1000"}); err != nil {
		t.Fatal(err)
	}
	saveShieldForm(t, a, url.Values{"sh_enabled": {"on"}, "sh_observe": {"on"}})
	if got := get(settings.KeyShieldObserveSince); got != "1000" {
		t.Errorf("a second save moved observe-only's start to %q", got)
	}
	saveShieldForm(t, a, url.Values{"sh_enabled": {"on"}})
	if got := get(settings.KeyShieldObserveSince); got != "" {
		t.Errorf("observe-only saved off still has a start: %q", got)
	}
}

// A day after it began, observe-only ends by itself: switched off and
// applied, so the gates enforce again. Before then it stays on, and a mode
// switched on before its start was recorded gets its day from now.
func TestObserveOnlyEndsByItselfAfterADay(t *testing.T) {
	a, _ := shieldSitesApp(t)
	ctx := context.Background()
	set := func(kv map[string]string) {
		t.Helper()
		if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), kv); err != nil {
			t.Fatal(err)
		}
	}
	ago := func(d time.Duration) string { return strconv.FormatInt(time.Now().Add(-d).Unix(), 10) }

	set(map[string]string{settings.KeyShieldObserve: "on", settings.KeyShieldObserveSince: ago(23 * time.Hour)})
	a.endObserveIfDue(ctx)
	if _, on := a.observeUntil(ctx); !on {
		t.Fatal("observe-only ended before its day was over")
	}

	set(map[string]string{settings.KeyShieldObserve: "on", settings.KeyShieldObserveSince: ago(25 * time.Hour)})
	a.vayuShield.ApplySettings(a.shieldSettings(ctx))
	a.endObserveIfDue(ctx)
	if got := a.siteSettings.Get(ctx, settings.ForPrimary(), settings.KeyShieldObserve); got != "off" {
		t.Fatalf("observe-only is %q a day after it began", got)
	}
	if a.shieldSettings(ctx).ObserveOnly {
		t.Fatal("the shield still only observes after observe-only ended")
	}

	set(map[string]string{settings.KeyShieldObserve: "on", settings.KeyShieldObserveSince: ""})
	until, on := a.observeUntil(ctx)
	if !on || until.Before(time.Now().Add(observeFor-time.Minute)) {
		t.Fatalf("a mode switched on before its start was recorded ends at %v", until)
	}
}

// While observe-only is on, an administrator sees it across the console in
// red, with when it ends and the way to Shield; nobody else is shown a strip
// about a setting they cannot change, and nothing shows when it is off.
func TestObserveOnlyIsShownAcrossTheConsole(t *testing.T) {
	until := time.Date(2026, 11, 23, 13, 20, 0, 0, time.UTC)
	strip := saObserveStrip("en", until, true)
	if !strings.Contains(strip, `class="sa-strip sa-strip--danger"`) || !strings.Contains(strip, "Shield is only watching.") || !strings.Contains(strip, `href="/os/shield"`) {
		t.Fatalf("the strip is %q", strip)
	}
	if saObserveStrip("en", until, false) != "" || saObserveStrip("en", time.Time{}, true) != "" {
		t.Fatal("the strip shows to a non-administrator, or with observe-only off")
	}
}
