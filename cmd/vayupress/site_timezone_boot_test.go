// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
)

// The console reads in the time zone set in Settings from the moment it
// starts, not from the next Settings save (pipeline 3ad: Home showed UTC on
// johal.in after an update restarted it).
func TestTheStoredTimeZoneIsAppliedAtBoot(t *testing.T) {
	newTestUserStore(t) // migrations, on a fresh database
	a := &App{siteSettings: settings.New(dbpkg.DB)}
	if err := a.siteSettings.SetMany(context.Background(), settings.ForPrimary(), map[string]string{settings.KeySiteTimezone: "Asia/Kolkata"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.SetSiteTimeZone("") })
	_ = config.SetSiteTimeZone("") // as a fresh process starts: UTC
	a.applyRenderSettings(context.Background())
	if got := config.SiteLocation().String(); got != "Asia/Kolkata" {
		t.Fatalf("after boot's settings step the console reads %s, want Asia/Kolkata", got)
	}
	if _, off := config.InSite(time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)).Zone(); off != 5*3600+1800 {
		t.Errorf("a time on Home is %ds from UTC, want IST's 19800", off)
	}

	// And main runs that step: boot once kept its own copy of the mapping,
	// without the time zone, which is how the console came up in UTC.
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^\s*a\.applyRenderSettings\(context\.Background\(\)\)`).Match(src) {
		t.Error("main does not apply the stored settings at boot")
	}
	if regexp.MustCompile(`render\.SetActiveSettings\(`).Match(src) {
		t.Error("main keeps its own copy of the settings mapping again")
	}
}
