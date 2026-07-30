// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"strings"
	"testing"
)

// TestServerRequestsAndPageviewsAreNotCalledTheSameThing guards a claim, not a
// calculation.
//
// The page header renders the server-side counter (every page request, crawlers
// included) and a stat card renders the browser beacon (visitors who ran
// JavaScript). Both are correct. Labelled with the same noun they read as a
// contradiction: an operator saw "31643 views · last 24 hours" immediately above
// a Pageviews card reading 1327 — same word, same period, 24x apart.
//
// This is the same defect class as a posture panel overstating what is
// enforcing: the number is right and the sentence around it is not.
func TestServerRequestsAndPageviewsAreNotCalledTheSameThing(t *testing.T) {
	src, err := os.ReadFile("admin_os_intel.go")
	if err != nil {
		t.Skipf("source not readable here: %v", err)
	}
	s := string(src)

	// The header and the traffic chip both render sum.TotalViews — the server-side
	// counter. Neither may call it "views", which is the stat card's word.
	for _, bad := range []string{
		"`+strconv.FormatInt(sum.TotalViews, 10)+` views",
		"strconv.FormatInt(sum.TotalViews, 10) + ` views",
	} {
		if strings.Contains(s, bad) {
			t.Errorf("the server-side total is labelled %q — the Pageviews card uses that noun "+
				"for a different population, and side by side they read as a contradiction", "views")
		}
	}

	// It must be labelled as requests, in both places it appears.
	if n := strings.Count(s, "page requests") + strings.Count(s, "` requests"); n < 2 {
		t.Errorf("the server-side total is rendered in two places (page header and the "+
			"Traffic-over-time chip); only %d carry a request-flavoured label", n)
	}

	// And the difference must be explained where it is shown, not only in a
	// comment. A label without the reason still leaves the operator guessing.
	if !strings.Contains(s, "osRequestsVsPageviewsHint") {
		t.Error("no hint is attached to the server-side total; the operator is left to work " +
			"out why two numbers on one page disagree")
	}
	// Attached at both renderings — either can be the one an operator reads first.
	// Counted as title-attribute uses, so defining the constant does not by itself
	// satisfy the check (the mistake that made an earlier source-scanning test in
	// this repo incapable of failing).
	if c := strings.Count(s, `title="`) - strings.Count(s, `title="`+`Server`); c < 2 {
		t.Errorf("the hint is attached as a title in %d place(s); both renderings of the "+
			"server-side total need it", c)
	}
	if c := strings.Count(s, "osRequestsVsPageviewsHint"); c < 3 {
		t.Errorf("osRequestsVsPageviewsHint appears %d times (want the definition plus both "+
			"render sites); a hint defined and not attached explains nothing", c)
	}

	// The hint has to actually say which is which — a vague note is not an
	// explanation.
	for _, want := range []string{"crawlers", "Pageviews", "beacon"} {
		if !strings.Contains(osRequestsVsPageviewsHint, want) {
			t.Errorf("the hint never mentions %q, so it does not tell the operator what the "+
				"two numbers are: %q", want, osRequestsVsPageviewsHint)
		}
	}
}
