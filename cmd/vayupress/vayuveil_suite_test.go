// SPDX-License-Identifier: Apache-2.0

package main

// vayuveil_suite_test.go — the pre-release adversarial pass over the MCP surface.
//
// ATTACK: hold a settings/read key and call vayuveil_posture in a loop.
//
// Two things happen per call, and both are the caller's to trigger and nobody's
// to meter. The capture suite runs for real — globbing /dev/fb*, /dev/input/
// event*, /dev/dri/card*, /dev/vcs* and reading /proc/1/mem — and the channel
// inventory probes the host alongside it. On the panel that is human-paced: a
// person reloads a page. Over a tool call it is a loop, and it is exactly the
// shape this project's multi-node audit already found once, described there as
// an unmetered compute sink on the lane reserved for the admin plane.
//
// The second half is worse and less obvious. The panel records capture findings
// to the audit log; the MCP path did not, so a REAL capture discovered through
// the connector was found and thrown away. Recording it naively is no better:
// every run writes a "techniques-not-attempted" row, the audit log is WORM, and
// a loop therefore fills a table nothing can clean up.
//
// One fix answers both. The expensive experiment is cached for a short interval
// and the trail is written only when it actually ran.

import (
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/vayuveil"
)

func testSuiteCache(t *testing.T, now *time.Time, runs *int) *veilSuiteCache {
	t.Helper()
	return &veilSuiteCache{
		every: 15 * time.Second,
		now:   func() time.Time { return *now },
		run: func() (map[vayuveil.ChannelID]vayuveil.Observation, []vayuveil.AttackResult) {
			*runs++
			return map[vayuveil.ChannelID]vayuveil.Observation{},
				[]vayuveil.AttackResult{{Technique: "t", Outcome: vayuveil.AttackNothingPresent}}
		},
	}
}

// THE test. A caller in a loop must not be able to make this host open its device
// nodes once per request.
func TestTheCaptureSuiteIsNotRunOncePerCall(t *testing.T) {
	base := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	now, runs := base, 0
	c := testSuiteCache(t, &now, &runs)

	for i := 0; i < 50; i++ {
		c.Get()
	}
	if runs != 1 {
		t.Fatalf("50 calls ran the capture suite %d times; it must run once per interval", runs)
	}

	// Past the interval it runs again — a cache that never refreshes is a report
	// frozen at boot, which is the opposite failure.
	now = base.Add(16 * time.Second)
	c.Get()
	if runs != 2 {
		t.Fatalf("the suite did not re-run after its interval elapsed (%d runs)", runs)
	}
}

// The trail is written only when the suite actually ran. The audit log is WORM,
// so a row written per call is a table nobody can ever clean up.
func TestTheAuditTrailIsWrittenOnlyWhenTheSuiteActuallyRan(t *testing.T) {
	base := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	now, runs := base, 0
	c := testSuiteCache(t, &now, &runs)

	fresh := 0
	for i := 0; i < 20; i++ {
		if _, _, _, isFresh := c.Get(); isFresh {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("%d of 20 calls reported themselves as a fresh run; the trail would get %d rows", fresh, fresh)
	}

	now = base.Add(time.Minute)
	if _, _, _, isFresh := c.Get(); !isFresh {
		t.Fatal("a genuine re-run did not report itself as fresh, so its findings would go unrecorded")
	}
}

// A cached answer must carry WHEN it was taken. This subsystem's standing rule is
// that a control is read back at report time and never remembered — caching the
// experiment is defensible only because it is an experiment rather than a
// control, and only if the report says how old it is. A stale result presented
// as "now" is the same defect as a remembered control.
func TestACachedSuiteResultSaysWhenItWasTaken(t *testing.T) {
	base := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	now, runs := base, 0
	c := testSuiteCache(t, &now, &runs)

	_, _, firstAt, _ := c.Get()
	if !firstAt.Equal(base) {
		t.Fatalf("the first run is stamped %v, want %v", firstAt, base)
	}
	now = base.Add(10 * time.Second)
	_, _, cachedAt, fresh := c.Get()
	if fresh {
		t.Fatal("the second call re-ran inside the interval")
	}
	if !cachedAt.Equal(base) {
		t.Fatalf("a cached result is stamped %v — it must carry the time it was TAKEN, not now", cachedAt)
	}
}

// The zero interval must not disable the cache. A misconfigured or unset every
// would turn the guard off silently, which is how a rate limit comes to not
// exist — and this one exists to stop a loop.
func TestAZeroIntervalStillMeters(t *testing.T) {
	base := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	now, runs := base, 0
	c := testSuiteCache(t, &now, &runs)
	c.every = 0

	for i := 0; i < 10; i++ {
		c.Get()
	}
	if runs != 1 {
		t.Fatalf("a zero interval ran the suite %d times; it must fall back to the default, not to no limit", runs)
	}
}
