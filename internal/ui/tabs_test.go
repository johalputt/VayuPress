// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"strings"
	"testing"
)

// The current tab is marked for a screen reader, the others are plain links,
// and every label and address is escaped once.
func TestTabsMarkTheCurrentPage(t *testing.T) {
	got := string(Tabs("Shield", Tab{Label: "Bot protection", Href: "/os/shield", Current: true}, Tab{Label: "A & B", Href: "/os/x?a=1&b=2"}))
	for _, want := range []string{`<nav class="tabs" aria-label="Shield">`,
		`<a class="tab" href="/os/shield" aria-current="page">Bot protection</a>`,
		`<a class="tab" href="/os/x?a=1&amp;b=2">A &amp; B</a>`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}
	if strings.Count(got, "aria-current") != 1 {
		t.Error("more than one tab is current")
	}
}
