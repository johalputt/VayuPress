// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/johalputt/vayupress/internal/mode"
)

// A node's status and band are written into class attributes. Today every one
// is a constant, but the node list also carries a figure computed from the
// request (the search count), and a status that ever came from data would be
// markup injection. Whatever arrives, only a suffix the stylesheet defines goes
// out. One seed per rule.
func TestTopologyClassesAreOnlyTheOnesTheStylesheetDefines(t *testing.T) {
	for in, want := range map[string]string{
		"ok": "ok", "warn": "warn", "err": "err",
		`ok" onmouseover="x`: "ok", // anything else is the neutral tone
		"":                   "ok",
	} {
		if got := topoTone(in, mode.ModeNormal); got != want {
			t.Errorf("status %q drew class suffix %q, want %q", in, got, want)
		}
	}
	if got := topoTone("mode-read_only", mode.ModeReadOnly); got != "warn" {
		t.Errorf("the mode node in read-only drew %q, want warn", got)
	}
	for in, want := range map[string]string{
		"write": "write", "read": "read", "govern": "govern", "observe": "observe",
		`read"><script>`: "write",
	} {
		if got := topoBand(in); got != want {
			t.Errorf("band %q drew class suffix %q, want %q", in, got, want)
		}
	}
}
