// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// TestOSPostPinControls verifies the HTMX pin/unpin button and the out-of-band
// pinned badge render correctly and stay CSP-safe: the button posts the OPPOSITE
// pinned state, targets itself for an outerHTML swap, and the badge is keyed by
// the row's per-slug id (present-but-empty when unpinned so the OOB target
// always exists).
func TestOSPostPinControls(t *testing.T) {
	// Unpinned post → "Pin" button that posts pinned=1.
	unp := osPostPinButton("hello-world", false)
	assertCSPSafe(t, "pin button", unp)
	for _, want := range []string{
		`hx-post="/os/api/posts/hello-world/pin-fragment"`,
		`hx-vals='{"pinned":"1"}'`,
		`hx-target="this"`,
		`hx-swap="outerHTML"`,
		`hx-disabled-elt="this"`,
		`>Pin</button>`,
	} {
		if !strings.Contains(unp, want) {
			t.Errorf("pin button missing %q in:\n%s", want, unp)
		}
	}
	// Pinned post → "Unpin" that posts pinned=0.
	pin := osPostPinButton("x", true)
	if !strings.Contains(pin, `hx-vals='{"pinned":"0"}'`) || !strings.Contains(pin, `>Unpin</button>`) {
		t.Errorf("unpin button wrong:\n%s", pin)
	}

	// Badge: empty when unpinned, chip when pinned; both keyed by id; OOB flag honoured.
	empty := osPostPinBadge("hello-world", false, false)
	assertCSPSafe(t, "empty badge", empty)
	if !strings.Contains(empty, `id="ppin-hello-world"`) || strings.Contains(empty, "Pinned") {
		t.Errorf("unpinned badge should be an empty keyed span:\n%s", empty)
	}
	full := osPostPinBadge("hello-world", true, true)
	for _, want := range []string{`id="ppin-hello-world"`, `hx-swap-oob="true"`, "📌 Pinned", "chip"} {
		if !strings.Contains(full, want) {
			t.Errorf("pinned OOB badge missing %q in:\n%s", want, full)
		}
	}
}
