// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// The Website page says what the domain shows in one line, for every mode
// the save accepts, naming the design only where the domain shows it.
func TestWebsiteSaysWhatTheDomainShows(t *testing.T) {
	for mode, want := range map[string]string{
		"":                 "example.com shows the blog",
		"blog":             "example.com shows the blog",
		"business":         "example.com shows the website, in Bistro",
		"business_subpath": "example.com shows the website, the blog at /blog",
		"custom":           "example.com shows the site you uploaded",
	} {
		if got := websiteServes("example.com", mode, "Bistro"); got != want {
			t.Errorf("%q: %q, want %q", mode, got, want)
		}
	}
}

// monAcc frames the accordions the pages not yet converted still use.
func TestMonAccIsBalanced(t *testing.T) {
	// monAcc must produce a balanced details/summary frame.
	out := monAcc(saIcon("globe"), "T", "S", monChip(true, "on", "off"), true, `<div class="x"></div>`)
	if strings.Count(out, "<details") != 1 || strings.Count(out, "</details>") != 1 {
		t.Error("monAcc must emit exactly one details element")
	}
	if !strings.Contains(out, `class="mon-acc__body"`) {
		t.Error("monAcc must wrap the body")
	}
}
