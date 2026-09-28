// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/budget"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/mode"
)

func TestBudgetTone(t *testing.T) {
	// An unknown state is never read as a good one.
	for state, want := range map[string]string{"healthy": "ok", "at-risk": "warn", "exhausted": "danger", "": "danger"} {
		if got := budgetTone(state); got != want {
			t.Errorf("budgetTone(%q) = %q, want %q", state, got, want)
		}
	}
}

// The Monitoring page opens on one sentence. Each case trips exactly one rule
// and asserts that rule's own sentence, so a rule removed or reordered is seen
// on its own.
func TestTheMonitoringSentenceNamesTheMostUrgentThing(t *testing.T) {
	well := func() (*adminMetricsSnapshot, mode.Mode, dbpkg.StallState, dbpkg.StallState, analytics.CollectorState, []budget.Status) {
		return &adminMetricsSnapshot{HTTPP95: 42, StoragePct: 11}, mode.ModeNormal,
			dbpkg.StallState{Watching: true}, dbpkg.StallState{Watching: true},
			analytics.CollectorState{Running: true}, []budget.Status{{Name: "degradation-debt", State: "healthy"}}
	}
	held := &dbpkg.StallEvent{Duration: 42 * time.Second}
	cases := []struct {
		name  string
		trip  func(*adminMetricsSnapshot, *mode.Mode, *dbpkg.StallState, *dbpkg.StallState, *analytics.CollectorState, *[]budget.Status)
		tone  string
		state string
	}{
		{"well", func(*adminMetricsSnapshot, *mode.Mode, *dbpkg.StallState, *dbpkg.StallState, *analytics.CollectorState, *[]budget.Status) {
		},
			"ok", "Running normally: requests answer in 42 ms"},
		{"writer held", func(_ *adminMetricsSnapshot, _ *mode.Mode, ws, _ *dbpkg.StallState, _ *analytics.CollectorState, _ *[]budget.Status) {
			ws.Stalled, ws.Current = true, held
		}, "danger", "Writes are waiting: the write connection has been held for 42.0s"},
		{"read pool full", func(_ *adminMetricsSnapshot, _ *mode.Mode, _, rs *dbpkg.StallState, _ *analytics.CollectorState, _ *[]budget.Status) {
			rs.Stalled, rs.Current = true, held
		}, "danger", "Reads are waiting"},
		{"mode", func(_ *adminMetricsSnapshot, m *mode.Mode, _, _ *dbpkg.StallState, _ *analytics.CollectorState, _ *[]budget.Status) {
			*m = mode.ModeReadOnly
		}, "warn", "Running in read-only mode"},
		{"unwatched", func(_ *adminMetricsSnapshot, _ *mode.Mode, ws, _ *dbpkg.StallState, _ *analytics.CollectorState, _ *[]budget.Status) {
			ws.Watching = false
		}, "warn", "Stalls are not being watched"},
		{"views lost", func(_ *adminMetricsSnapshot, _ *mode.Mode, _, _ *dbpkg.StallState, rec *analytics.CollectorState, _ *[]budget.Status) {
			rec.Running = false
		}, "danger", "Page views are not being written"},
		{"failed jobs", func(s *adminMetricsSnapshot, _ *mode.Mode, _, _ *dbpkg.StallState, _ *analytics.CollectorState, _ *[]budget.Status) {
			s.FailedJobs = 3
		}, "warn", "3 background jobs failed"},
		{"storage", func(s *adminMetricsSnapshot, _ *mode.Mode, _, _ *dbpkg.StallState, _ *analytics.CollectorState, _ *[]budget.Status) {
			s.StoragePct = 93
		}, "warn", "Storage is 93% full"},
		{"budget", func(_ *adminMetricsSnapshot, _ *mode.Mode, _, _ *dbpkg.StallState, _ *analytics.CollectorState, b *[]budget.Status) {
			(*b)[0].State = "exhausted"
		}, "warn", "The degradation-debt budget is exhausted"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			snap, m, ws, rs, rec, b := well()
			c.trip(snap, &m, &ws, &rs, &rec, &b)
			tone, state := monitoringState(snap, m, ws, rs, rec, b)
			if tone != c.tone || !strings.HasPrefix(state, c.state) {
				t.Errorf("got (%s) %q, want (%s) %q…", tone, state, c.tone, c.state)
			}
		})
	}
}

func TestTheGovernanceSentence(t *testing.T) {
	for _, c := range []struct {
		name              string
		m                 mode.Mode
		atRisk, exhausted int
		tone, state       string
	}{
		{"well", mode.ModeNormal, 0, 0, "ok", "In normal mode, and every budget is healthy"},
		{"at risk", mode.ModeNormal, 2, 0, "warn", "In normal mode, with 2 budgets at risk"},
		{"exhausted", mode.ModeNormal, 1, 1, "danger", "In normal mode, with 1 budget exhausted"},
		{"mode", mode.ModeQuarantined, 0, 0, "danger", "In quarantined mode, and every budget is healthy"},
	} {
		if tone, state := governanceState(c.m, c.atRisk, c.exhausted); tone != c.tone || state != c.state {
			t.Errorf("%s: got (%s) %q, want (%s) %q", c.name, tone, state, c.tone, c.state)
		}
	}
}

func TestTheVayuFlowSentence(t *testing.T) {
	for _, c := range []struct {
		name              string
		wired             bool
		fail, warn, armed int
		tone, state       string
	}{
		{"unwired outranks all", false, 3, 2, 1, "danger", "The automation engine is not running"},
		{"fail", true, 1, 2, 1, "danger", "1 posture check to act on"},
		{"warn", true, 0, 2, 1, "warn", "2 posture checks to look at"},
		{"armed", true, 0, 0, 3, "ok", "3 flows armed live"},
		{"quiet", true, 0, 0, 0, "ok", "No flow is armed live"},
	} {
		if tone, state := vayuFlowState(c.wired, c.fail, c.warn, c.armed); tone != c.tone || !strings.HasPrefix(state, c.state) {
			t.Errorf("%s: got (%s) %q, want (%s) %q…", c.name, tone, state, c.tone, c.state)
		}
	}
}
