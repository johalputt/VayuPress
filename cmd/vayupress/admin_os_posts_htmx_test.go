// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// TestOSPostStatusControls verifies the HTMX publish/unpublish controls render
// correctly and stay CSP-safe (no inline styles, no external hosts): the button
// posts the OPPOSITE status to the fragment endpoint, targets itself for an
// outerHTML swap, and the out-of-band state is keyed by per-slug ids so HTMX
// updates the row's state and the inspector's, and nothing else.
func TestOSPostStatusControls(t *testing.T) {
	if got := osPostState("published"); !strings.Contains(got, "sa-dot--ok") || !strings.Contains(got, "Published") {
		t.Errorf("published state = %q", got)
	}
	if got := osPostState("draft"); !strings.Contains(got, "sa-dot--neutral") || !strings.Contains(got, "Draft") {
		t.Errorf("draft state = %q", got)
	}

	// A published post offers "Unpublish", which posts status=draft.
	pub := osPostStatusButton("hello-world", "published")
	assertCSPSafe(t, "published button", pub)
	for _, want := range []string{
		`hx-post="/os/api/posts/hello-world/status-fragment"`,
		`hx-vals='{"status":"draft"}'`,
		`hx-target="this"`,
		`hx-swap="outerHTML"`,
		`hx-disabled-elt="this"`,
		`>Unpublish</button>`,
	} {
		if !strings.Contains(pub, want) {
			t.Errorf("published button missing %q in:\n%s", want, pub)
		}
	}

	// A draft offers "Publish", which posts status=published.
	dft := osPostStatusButton("x", "draft")
	assertCSPSafe(t, "draft button", dft)
	if !strings.Contains(dft, `hx-vals='{"status":"published"}'`) || !strings.Contains(dft, `>Publish</button>`) {
		t.Errorf("draft button wrong:\n%s", dft)
	}

	// The out-of-band state carries the stable ids and hx-swap-oob markers.
	oob := osPostStatusOOB("hello-world", "draft")
	assertCSPSafe(t, "oob state", oob)
	for _, want := range []string{`id="post-status-hello-world"`, `id="post-istate-hello-world"`, `hx-swap-oob="true"`, "Draft"} {
		if !strings.Contains(oob, want) {
			t.Errorf("oob state missing %q in:\n%s", want, oob)
		}
	}
}
