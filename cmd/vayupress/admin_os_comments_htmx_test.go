// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// TestOSCommentControls verifies the HTMX moderation controls render correctly
// and stay CSP-safe: the state is a dot and a word under a per-id id for its
// row and for the inspector, and the action buttons offer every status EXCEPT
// the current one, each posting to the fragment endpoint and swapping the
// inspector's action row.
func TestOSCommentControls(t *testing.T) {
	for status, want := range map[string]string{"approved": "sa-dot--ok", "pending": "sa-dot--warn", "spam": "sa-dot--danger", "rejected": "sa-dot--neutral"} {
		st := osCommentState("c1", status, "c", false)
		assertCSPSafe(t, "comment state", st)
		if !strings.Contains(st, `id="cstate-c1"`) || !strings.Contains(st, want) || strings.Contains(st, "status-pill") {
			t.Errorf("%s: want a dot and a word under id cstate-c1, got:\n%s", status, st)
		}
		if strings.Contains(st, "hx-swap-oob") {
			t.Error("a non-oob state must not carry hx-swap-oob")
		}
	}
	if oob := osCommentState("c1", "pending", "i", true); !strings.Contains(oob, `id="istate-c1" hx-swap-oob="true"`) {
		t.Errorf("oob inspector state wrong:\n%s", oob)
	}

	// Actions for a pending comment: Approve + Reject + Spam, all HTMX, targeting
	// the inspector's action row; none is the current status.
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
