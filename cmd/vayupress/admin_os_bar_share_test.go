// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestBarShareIsTakenAgainstTheRealPopulation is the regression guard for a
// percentage that overstated by two orders of magnitude on a live panel.
//
// The share used to be computed against the sum of the rows displayed. On a
// complete breakdown (countries, browsers) that is the population and the number
// was right. On a top-N list it is not: an operator's homepage showed "87%" of
// traffic because it held 242 of the 278 views in the ten rows rendered, while
// the window actually contained 31,643. The true share was 0.8%.
func TestBarShareIsTakenAgainstTheRealPopulation(t *testing.T) {
	// The exact numbers from the panel that reported this.
	top10 := []osChartBar{
		{Label: "/", Value: 242}, {Label: "/a", Value: 6}, {Label: "/b", Value: 5},
		{Label: "/c", Value: 4}, {Label: "/d", Value: 4}, {Label: "/e", Value: 4},
		{Label: "/f", Value: 4}, {Label: "/g", Value: 3}, {Label: "/h", Value: 3},
		{Label: "/i", Value: 3},
	}

	got := osBarList(top10, osShareOf(31643), "")
	if strings.Contains(got, ">87%<") {
		t.Error("the homepage still reports 87% of traffic; that is its share of the ten rows " +
			"shown, not of the 31,643 views in the window")
	}
	if !strings.Contains(got, ">0%<") {
		t.Errorf("242 of 31,643 should render as 0%%; got:\n%s", firstBar(got))
	}

	// A complete breakdown still takes its share of the rows, because there the
	// rows are the whole population.
	listed := osBarList([]osChartBar{{Label: "a", Value: 3}, {Label: "b", Value: 1}},
		osShareOfListed(), "")
	if !strings.Contains(listed, ">75%<") {
		t.Errorf("a complete breakdown should read 3/4 = 75%%; got:\n%s", firstBar(listed))
	}

	// Truncated with no honest denominator: no percentage at all.
	hidden := osBarList(top10, osShareHidden(), "")
	if strings.Contains(hidden, "vp-bar__pct") {
		t.Error("a truncated list with no known population must show no percentage — " +
			"no number is better than a wrong one")
	}

	// The zero value must never quietly reinstate the old behaviour.
	unset := osBarList(top10, osBarDenom{}, "")
	if strings.Contains(unset, "vp-bar__pct") {
		t.Error("an unset denominator printed a percentage; it must fail closed")
	}

	// A total smaller than the rows it contains must not print above 100%.
	clamped := osBarList([]osChartBar{{Label: "a", Value: 500}}, osShareOf(100), "")
	if !strings.Contains(clamped, ">100%<") || strings.Contains(clamped, ">500%<") {
		t.Errorf("share above 100%% was not clamped; got:\n%s", firstBar(clamped))
	}

	// Both ends. Found by attacking this function during the pre-release pass:
	// the first clamp guarded only the upper bound, because that was the end with
	// an obvious way to go wrong, and a negative count rendered "-5%".
	// Asserted on the percentage span specifically. A first version checked for
	// ">-" anywhere in the markup, which matched the rendered VALUE "-5" and so
	// failed against the fixed code too — a test that fails for the wrong reason
	// is no more use than one that passes for the wrong reason.
	neg := osBarList([]osChartBar{{Label: "a", Value: -5}}, osShareOf(100), "")
	if strings.Contains(neg, `vp-bar__pct">-`) {
		t.Errorf("a negative count rendered a negative share; got:\n%s", firstBar(neg))
	}

	// A non-positive denominator must suppress the percentage, never divide by it.
	for _, d := range []osBarDenom{osShareOf(0), osShareOf(-1)} {
		if out := osBarList([]osChartBar{{Label: "a", Value: 5}}, d, ""); strings.Contains(out, "vp-bar__pct") {
			t.Errorf("percentage printed against a non-positive denominator; got:\n%s", firstBar(out))
		}
	}
}

// TestEveryBarListDeclaresItsDenominator stops a new call site from being added
// with the zero value, which is the one spelling that silently shows nothing and
// looks like a deliberate choice.
func TestEveryBarListDeclaresItsDenominator(t *testing.T) {
	src, err := os.ReadFile("admin_os_intel.go")
	if err != nil {
		t.Skipf("source not readable here: %v", err)
	}
	calls := regexp.MustCompile(`osBarList\(`).FindAllStringIndex(string(src), -1)
	if len(calls) == 0 {
		t.Fatal("no osBarList call sites found; this test is no longer checking anything")
	}
	if strings.Contains(string(src), "osBarDenom{}") {
		t.Error("a call site constructs the zero denominator directly; use osShareOf, " +
			"osShareOfListed or osShareHidden so the choice is visible in review")
	}
	for _, s := range []string{"osShareOf(", "osShareOfListed()", "osShareHidden()"} {
		if !strings.Contains(string(src), s) {
			t.Errorf("no call site uses %s — the three cases exist because all three occur "+
				"on this page; losing one means a list is being described wrongly", s)
		}
	}
}

func firstBar(html string) string {
	if i := strings.Index(html, `<div class="vp-bar `); i >= 0 {
		if j := strings.Index(html[i+1:], `<div class="vp-bar `); j > 0 {
			return html[i : i+j]
		}
		return html[i:]
	}
	return html
}
