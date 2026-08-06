// SPDX-License-Identifier: Apache-2.0

package main

// vayuveil_suite.go — metering the one expensive thing in the VayuVeil report.
//
// # The finding this closes
//
// The pre-release pass over the MCP surface asked what a caller holding a
// settings/read key could do with a loop. Two things, and neither was metered.
//
// `RunRedTeam` is not a cheap read: it globs and opens `/dev/fb*`,
// `/dev/input/event*`, `/dev/dri/card*` and `/dev/vcs*`, and reads another
// process's memory through `/proc/1/mem`. `Inventory` probes the host beside it.
// On a page that is human-paced — somebody reloads. Behind a tool call it is a
// loop, and it is exactly the shape this project's multi-node audit already
// found once and wrote down: an unmetered compute sink on the lane reserved for
// the admin plane.
//
// The second half is quieter and worse. The panel records capture findings to
// the audit log and the MCP handler did not, so a REAL capture discovered
// through the connector was found and discarded — a trail with a hole in it that
// nothing reveals. But recording per call is not the fix: every run writes a
// "techniques-not-attempted" row, the audit log is WORM, and a loop therefore
// fills a table nobody can ever clean up. Fix one and you create the other.
//
// # Why caching this is allowed when caching a control is not
//
// This subsystem's standing rule is that a control is read back from the kernel
// at report time and never remembered, because a remembered control is a
// configuration wearing evidence's clothes. That rule is not relaxed here.
// `VerifyProcessHardening`, `ReadSandbox` and `ReadHostPosture` stay uncached on
// every path — they are a prctl and a handful of /proc reads, and they are the
// rows permitted to be green.
//
// The capture suite is a different kind of thing: an EXPERIMENT, not a control
// read-back. Nothing goes green because of it. Caching an experiment is honest
// on one condition, which is enforced below and asserted in the tests: the
// result carries the time it was TAKEN, so a page or a payload can say how old
// it is rather than presenting a minute-old sweep as this instant.

import (
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/vayuveil"
)

// veilSuiteEvery is how often the capture suite may actually run.
//
// Short enough that an operator reloading the page after changing something sees
// the change, long enough that a loop cannot turn a tool call into device I/O.
// Anyone reloading faster than this is not learning anything new: the suite
// probes hardware that does not appear and disappear between keystrokes.
const veilSuiteEvery = 15 * time.Second

// veilSuiteCache runs the capture suite and channel inventory at most once per
// interval, for every caller.
type veilSuiteCache struct {
	mu sync.Mutex

	// every is the interval. Zero falls back to the default rather than to no
	// limit — see Get. A guard that switches itself off when misconfigured is
	// how a rate limit comes to not exist.
	every time.Duration
	now   func() time.Time
	run   func() (map[vayuveil.ChannelID]vayuveil.Observation, []vayuveil.AttackResult)

	ranAt time.Time
	obs   map[vayuveil.ChannelID]vayuveil.Observation
	red   []vayuveil.AttackResult
	valid bool
}

// veilSuite is the process-wide instance. One cache for the page and the tool
// alike: metering only the MCP path would leave the same sink open behind a
// refresh loop, and two intervals to keep in step is one more thing to get wrong.
var veilSuite = &veilSuiteCache{
	every: veilSuiteEvery,
	now:   func() time.Time { return time.Now().UTC() },
	run: func() (map[vayuveil.ChannelID]vayuveil.Observation, []vayuveil.AttackResult) {
		host := vayuveil.LiveHost()
		// The suite runs for real and is judged on the bytes it comes away with.
		// Reads are non-blocking, so a machine with a keyboard does not hang this
		// waiting for a keypress.
		return vayuveil.Inventory(host), vayuveil.RunRedTeam(host, vayuveil.LiveRead)
	},
}

// Get returns the inventory and the suite result, running them only if the
// cached pair is older than the interval.
//
// ranAt is when the returned result was TAKEN, which is not necessarily now.
// fresh reports whether this particular call did the running — it is what the
// caller uses to decide whether to write the audit trail, so that a hundred
// readers produce one entry rather than a hundred.
func (c *veilSuiteCache) Get() (obs map[vayuveil.ChannelID]vayuveil.Observation,
	red []vayuveil.AttackResult, ranAt time.Time, fresh bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	every := c.every
	if every <= 0 {
		every = veilSuiteEvery
	}
	now := c.now()
	if c.valid && now.Sub(c.ranAt) < every {
		return c.obs, c.red, c.ranAt, false
	}
	c.obs, c.red = c.run()
	c.ranAt, c.valid = now, true
	return c.obs, c.red, c.ranAt, true
}

// DescribeAge renders how old the returned suite result is.
//
// Rendered rather than left implicit, because the alternative is a page that
// presents a minute-old sweep as the present tense. The controls above it ARE
// read at report time and say so; this one is not, and says that.
func veilSuiteAge(ranAt time.Time, now time.Time) string {
	if ranAt.IsZero() {
		return "The capture suite has not run."
	}
	age := now.Sub(ranAt)
	if age < time.Second {
		return "Run just now."
	}
	return "Run " + age.Truncate(time.Second).String() + " ago, and re-run at most once every " +
		veilSuiteEvery.String() + " — it opens device nodes, so it is metered rather than " +
		"repeated per request. The control rows above are read from the kernel every time and " +
		"are not cached."
}
