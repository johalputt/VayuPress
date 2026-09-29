// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/analytics"
)

// The server's count of page requests and the beacon's pageviews are two
// populations: requests include crawlers and visitors without JavaScript.
// Labelled with the same noun they read as a contradiction; an operator saw
// "31643 views" above a Pageviews figure of 1327, same word, same period.
// Asserted on the figures the page draws, not on its source text.
func TestServerRequestsAndPageviewsAreNotCalledTheSameThing(t *testing.T) {
	figs := analyticsFigures(&analytics.Overview{UniqueVisitors: 400, TotalPageviews: 1327, TotalVisits: 500},
		&analytics.Overview{UniqueVisitors: 200, TotalPageviews: 1327, TotalVisits: 300}, 31643)
	byLabel := map[string]string{}
	for _, f := range figs {
		if _, dup := byLabel[f.Label]; dup {
			t.Fatalf("two figures are labelled %q", f.Label)
		}
		byLabel[f.Label] = f.Value + " | " + f.Note
	}
	if got := byLabel["Page requests"]; !strings.HasPrefix(got, "31643 | ") || !strings.Contains(got, "Crawlers") {
		t.Errorf("the server's count does not say it includes crawlers: %q", got)
	}
	if got := byLabel["Pageviews"]; !strings.HasPrefix(got, "1327 | The same as the period before") {
		t.Errorf("pageviews: %q", got)
	}
	if got := byLabel["Visitors"]; got != "400 | Up 100% on the period before" {
		t.Errorf("visitors: %q", got)
	}

	// A figure that would read 0 is left out, and a first period has no change.
	if figs := analyticsFigures(&analytics.Overview{}, nil, 0); len(figs) != 0 {
		t.Errorf("an install with no traffic shows %d figures", len(figs))
	}
	for _, f := range analyticsFigures(&analytics.Overview{UniqueVisitors: 3, TotalPageviews: 5}, nil, 9) {
		if strings.Contains(f.Note, "period before") {
			t.Errorf("%s claims a change with nothing to compare: %q", f.Label, f.Note)
		}
	}
}
