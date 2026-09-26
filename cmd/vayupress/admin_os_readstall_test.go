// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
)

// Mid-incident the section says the read pool is full, since when, what it has
// cost, and that cached pages still serve.
func TestTheReadSectionNamesAStallThatIsHappeningNow(t *testing.T) {
	st := dbpkg.StallState{Watching: true, MaxOpen: 48, Stalled: true, Total: 1, Current: &dbpkg.StallEvent{
		Start: time.Date(2026, 9, 26, 0, 30, 5, 0, time.UTC), Duration: 95 * time.Second,
		Blocked: 40 * time.Minute, Waits: 812, Ongoing: true,
	}}
	out := readStallSection(st, 0)
	for _, want := range []string{"Every read connection is taken right now", "00:30:05", "1m 35s", "812", "40m 0s",
		"Pages with a cache file are unaffected", "stat-card--warn", "48 connections"} {
		if !strings.Contains(out, want) {
			t.Errorf("the read section does not report %q during a stall:\n%s", want, out)
		}
	}
}

// A stall that has ended stays findable with its snapshot, and the render
// ceiling's count is shown beside it.
func TestTheReadSectionKeepsHistoryAndTheRetryCount(t *testing.T) {
	st := dbpkg.StallState{Watching: true, MaxOpen: 48, Total: 1, Longest: 20 * time.Minute, Recent: []dbpkg.StallEvent{{
		Start: time.Date(2026, 9, 26, 0, 30, 5, 0, time.UTC), Duration: 20 * time.Minute, Waits: 5000,
		Blocked: 3 * time.Hour, Dump: "/var/lib/vayupress/stalls/readstall-20260926T003010Z.txt",
	}}}
	out := readStallSection(st, 4312)
	for _, want := range []string{"readstall-20260926T003010Z.txt", "worst 20m 0s", "4312", "Pages asked to retry"} {
		if !strings.Contains(out, want) {
			t.Errorf("the read section does not show %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "right now") {
		t.Error("an ended stall is reported as happening now")
	}
}

// A watch that never started is a fault, not a quiet install.
func TestTheReadSectionSaysWhenNothingIsWatched(t *testing.T) {
	if out := readStallSection(dbpkg.StallState{}, 0); !strings.Contains(out, "Not being watched") {
		t.Errorf("the read section does not say its watchdog is off:\n%s", out)
	}
	if out := readStallSection(dbpkg.StallState{Watching: true, MaxOpen: 24}, 0); strings.Contains(out, "Not being watched") {
		t.Error("a watched pool is reported as unwatched")
	}
}

// The Monitoring page carries the section, not only the function that draws it.
func TestTheMonitoringPageShowsTheReadPool(t *testing.T) {
	setupRelatedTestDB(t)
	a := &App{siteSettings: settings.New(dbpkg.DB)}
	req := httptest.NewRequest(http.MethodGet, "/os/monitoring", nil)
	req.Header.Set("X-API-Key", config.Cfg.APIKey)
	rec := httptest.NewRecorder()
	a.handleOSMonitoring(rec, req)
	for _, want := range []string{"Read connections", "Pages asked to retry"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("the Monitoring page does not show %q (status %d)", want, rec.Code)
		}
	}
}
