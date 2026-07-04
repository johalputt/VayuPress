package main

import (
	"strings"
	"testing"
)

// TestOSCommentControls verifies the HTMX moderation controls render correctly
// and stay CSP-safe: the pill carries a per-id id + live data-status (so the
// client filter re-categorises a moderated row), and the action buttons offer
// every status EXCEPT the current one, each posting to the fragment endpoint and
// swapping the row's action cell.
func TestOSCommentControls(t *testing.T) {
	// Pill: id, data-status, and status-specific class; CSP-safe; OOB when asked.
	pill := osCommentPill("c1", "approved", false)
	assertCSPSafe(t, "comment pill", pill)
	for _, want := range []string{`id="cpill-c1"`, `data-status="approved"`, "status-pill--live", "● approved"} {
		if !strings.Contains(pill, want) {
			t.Errorf("pill missing %q in:\n%s", want, pill)
		}
	}
	if strings.Contains(pill, "hx-swap-oob") {
		t.Error("non-oob pill must not carry hx-swap-oob")
	}
	if oob := osCommentPill("c1", "pending", true); !strings.Contains(oob, `hx-swap-oob="true"`) || !strings.Contains(oob, "status-pill--draft") {
		t.Errorf("oob pending pill wrong:\n%s", oob)
	}

	// Actions for a pending comment: Approve + Reject + Spam, all HTMX, targeting
	// this row's action cell; none is the current status.
	act := osCommentActions("c1", "pending")
	assertCSPSafe(t, "comment actions", act)
	for _, want := range []string{
		`hx-post="/os/api/comments/c1/status-fragment"`,
		`hx-target="#cact-c1"`,
		`hx-vals='{"status":"approved"}'`,
		`hx-vals='{"status":"rejected"}'`,
		`hx-vals='{"status":"spam"}'`,
		`hx-disabled-elt="this"`,
		">Approve</button>", ">Reject</button>", ">Spam</button>",
	} {
		if !strings.Contains(act, want) {
			t.Errorf("pending actions missing %q in:\n%s", want, act)
		}
	}

	// An approved comment must NOT offer Approve again (idempotent UI).
	appr := osCommentActions("c1", "approved")
	if strings.Contains(appr, ">Approve</button>") {
		t.Errorf("approved comment must not offer Approve:\n%s", appr)
	}
	if !strings.Contains(appr, ">Reject</button>") || !strings.Contains(appr, ">Spam</button>") {
		t.Errorf("approved comment should still offer Reject/Spam:\n%s", appr)
	}
}
