// SPDX-License-Identifier: Apache-2.0

package main

// startup_cost_test.go — ADR-0155 P4.
//
// The number this file produces is used to decide whether to update a live site
// now or later, so the ways it can lie are the ways it can hurt: a fabricated
// sample, a flattering single figure presented as a range, or a duration
// described as an outage when the socket is queueing (or the reverse, which is
// far worse — telling someone a restart is free when every visitor gets a 502).

import (
	"strings"
	"testing"
)

// A corrupt or hostile stored value must not become a measurement. This is
// operator-facing evidence, and inventing a sample is worse than having none.
func TestACorruptRingProducesNoFabricatedSamples(t *testing.T) {
	for _, raw := range []string{
		"", ",,,", "abc", "-1", "-1,-2", "not,a,number",
		"99999999999", // longer than a day: certainly corrupt
	} {
		if got := parseStartupRing(raw); len(got) != 0 {
			t.Errorf("%q produced samples %v; a value that cannot be trusted must yield none", raw, got)
		}
	}
	// And a dreadful-but-real start must SURVIVE the filter — it is the exact
	// thing this measurement exists to surface. A bound that discarded it would
	// hide the worst installs and flatter every report.
	if got := parseStartupRing("600000"); len(got) != 1 || got[0] != 600000 {
		t.Fatalf("a ten-minute start was filtered out; that is the case this exists for, got %v", got)
	}
}

// Mixed valid and invalid keeps only the valid, in order.
func TestAPartlyCorruptRingKeepsWhatIsReadable(t *testing.T) {
	got := parseStartupRing("800, bad ,1200,,-5,1500")
	want := []int{800, 1200, 1500}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// THE test. The same duration means opposite things depending on whether the
// socket queues, and saying the comfortable one when it does not is telling an
// operator a restart is free while every visitor gets an error page.
func TestTheCostSaysErrorsWhenNothingQueuesAndWaitingWhenSomethingDoes(t *testing.T) {
	base := startupCost{Samples: 5, FastMS: 900, SlowMS: 4200, LatestMS: 1100}

	refusing := base
	refusing.Queued = false
	rd := refusing.Describe()
	if !strings.Contains(rd, "502") {
		t.Errorf("with nothing queueing, the cost does not say visitors get errors: %q", rd)
	}

	queueing := base
	queueing.Queued = true
	qd := queueing.Describe()
	if strings.Contains(qd, "502") {
		t.Errorf("with the socket queueing, the cost still claims visitors get 502s: %q", qd)
	}
	if !strings.Contains(qd, "QUEUED") && !strings.Contains(qd, "waits") {
		t.Errorf("the queueing case never says connections wait: %q", qd)
	}
	if rd == qd {
		t.Fatal("both cases read identically, so the socket makes no difference to what the " +
			"operator is told")
	}
}

// A single sample must not be dressed up as a range. One measurement and ten are
// different claims, and the flattering reading is the one an operator acts on.
func TestASingleSampleIsNotPresentedAsARange(t *testing.T) {
	one := startupCost{Samples: 1, FastMS: 1000, SlowMS: 1000, LatestMS: 1000}
	d := one.Describe()
	if strings.Contains(d, "–") {
		t.Errorf("a single sample rendered as a range: %q", d)
	}
	if !strings.Contains(d, "single recorded start") {
		t.Errorf("the copy does not say it is one sample: %q", d)
	}

	many := startupCost{Samples: 6, FastMS: 900, SlowMS: 5000, LatestMS: 1200}
	if dm := many.Describe(); !strings.Contains(dm, "–") {
		t.Errorf("a real spread was flattened to one figure: %q", dm)
	}
}

// No samples means no claim.
func TestNoSamplesClaimsNothing(t *testing.T) {
	d := startupCost{}.Describe()
	if !strings.Contains(d, "not known") {
		t.Errorf("an install with no measurement still made a claim: %q", d)
	}
	for _, forbidden := range []string{"502", "instant", "no downtime"} {
		if strings.Contains(d, forbidden) {
			t.Errorf("an unmeasured install claims %q: %s", forbidden, d)
		}
	}
}

// Durations are rendered the way somebody thinking about downtime reads them.
func TestDurationsRenderAsDowntime(t *testing.T) {
	for _, tc := range []struct {
		ms   int
		want string
	}{
		{0, "0ms"}, {450, "450ms"}, {999, "999ms"},
		{1000, "1.0s"}, {1450, "1.4s"}, {59999, "60.0s"},
		{60000, "60s"}, {600000, "600s"},
	} {
		if got := fmtMillis(tc.ms); got != tc.want {
			t.Errorf("fmtMillis(%d) = %q, want %q", tc.ms, got, tc.want)
		}
	}
}
