package main

import (
	"strings"
	"testing"
)

// TestOSPostStatusControls verifies the HTMX publish/unpublish controls render
// correctly and stay CSP-safe (no inline styles, no external hosts): the button
// posts the OPPOSITE status to the fragment endpoint, targets itself for an
// outerHTML swap, and the out-of-band pill is keyed by the row's per-slug id so
// HTMX updates only that cell.
func TestOSPostStatusControls(t *testing.T) {
	if got := osPostStatusPill("published"); !strings.Contains(got, "Published") {
		t.Errorf("published pill = %q", got)
	}
	if got := osPostStatusPill("draft"); !strings.Contains(got, "Draft") {
		t.Errorf("draft pill = %q", got)
	}

	// A published post offers "Unpublish", which posts status=draft.
	pub := osPostStatusButton("hello-world", "published")
	assertCSPSafe(t, "published button", pub)
	for _, want := range []string{
		`hx-post="/os/api/posts/hello-world/status-fragment"`,
		`hx-vals='{"status":"draft"}'`,
		`hx-target="this"`,
		`hx-swap="outerHTML"`,
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

	// The out-of-band pill carries the stable id and hx-swap-oob marker.
	oob := osPostStatusOOB("hello-world", "draft")
	assertCSPSafe(t, "oob pill", oob)
	for _, want := range []string{`id="post-status-hello-world"`, `hx-swap-oob="true"`, "Draft"} {
		if !strings.Contains(oob, want) {
			t.Errorf("oob pill missing %q in:\n%s", want, oob)
		}
	}
}
