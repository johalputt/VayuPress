// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/mode"
	vastore "github.com/johalputt/vayupress/internal/vayuanalytics/store"
)

// Telemetry is held in read-only mode, entered when the disk is failing,
// and written in every other: in quarantine, a security isolation, the
// shield's record of what is happening is what the operator needs.
func TestTelemetryIsHeldInReadOnlyAlone(t *testing.T) {
	t.Cleanup(mode.Global.Reset)
	for m, want := range map[mode.Mode]bool{
		mode.ModeNormal: false, mode.ModeDegraded: false, mode.ModeReadOnly: true,
		mode.ModeRecovery: false, mode.ModeMaintenance: false, mode.ModeQuarantined: false,
	} {
		mode.Global.ForceTransition(m, "test")
		if got := telemetryHeld(); got != want {
			t.Errorf("%s: held %v, want %v", m, got, want)
		}
	}
}

// The shutdown writes what every queue holds: views and collected events
// are flushed, and the engagement writer, its channel closed, is waited for.
func TestTheShutdownWritesWhatTheQueuesHold(t *testing.T) {
	openMigratedDB(t)
	a := &App{analytics: analytics.New(dbpkg.DB), vaEngagement: vastore.New(dbpkg.DB)}
	done := make(chan struct{})
	a.vaEngagement.StartIngest(done)
	a.analytics.RecordAsync("", "/last", "")
	a.analytics.CollectAsync(analytics.CollectRequest{URL: "/last", Hostname: "site.test", EventType: 1}, "198.51.100.9", "Mozilla/5.0 Firefox/131.0", "")
	a.vaEngagement.QueueEnter(vastore.EnterInput{SessionHash: "h", PagePath: "/last", ClientType: "human"})
	close(done)
	a.flushTelemetry(5 * time.Second)

	for q, want := range map[string]int{
		`SELECT COALESCE(SUM(views),0) FROM analytics_daily WHERE path='/last'`: 1,
		`SELECT COUNT(*) FROM analytics_pageviews WHERE url_path='/last'`:       1,
		`SELECT COUNT(*) FROM vayuanalytics_sessions WHERE page_path='/last'`:   1,
	} {
		var n int
		if err := dbpkg.DB.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Errorf("%s = %d after the shutdown's flush, want %d", q, n, want)
		}
	}
}

// The flush stands between the shutdown channel closing (which starts the
// writers' last drain) and the database closing (after which nothing can be
// written).
func TestTheShutdownFlushesBetweenTheChannelAndTheDatabase(t *testing.T) {
	src := readSourceFile(t, "main.go")
	ch, fl, db := strings.Index(src, "close(queue.DoneCh)"), strings.Index(src, "a.flushTelemetry("), strings.Index(src, "dbpkg.DB.Close()")
	if ch < 0 || fl < 0 || db < 0 || !(ch < fl && fl < db) {
		t.Errorf("shutdown order: channel closed at %d, telemetry flushed at %d, database closed at %d", ch, fl, db)
	}
}

// Each queue's row says, by one sign each, whether it is held, refused and
// being tried again, has lost or dropped something, or writes.
func TestEachTelemetryRowSaysItsState(t *testing.T) {
	for want, q := range map[string]telemetryQueue{
		"Held: read-only mode": {held: true, lastErr: "x"},
		"Refused, tried again": {lastErr: "database is locked"},
		"Some not written":     {lost: 1},
		"Written":              {},
	} {
		q.name, q.unit = "Q", "events"
		if row := telemetryRows([]telemetryQueue{q})[0]; !strings.Contains(string(row.Control), want) {
			t.Errorf("%+v: control %s, want %q", q, row.Control, want)
		}
	}
	dropped := telemetryRows([]telemetryQueue{{name: "Q", unit: "events", dropped: 1}})[0]
	if !strings.Contains(string(dropped.Control), "Some not written") ||
		!strings.Contains(dropped.Hint, "1 dropped because the queue was full, 0 lost because the database refused them") {
		t.Errorf("a drop: %+v", dropped)
	}
}
