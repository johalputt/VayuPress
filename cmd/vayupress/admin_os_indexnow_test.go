package main

import (
	"strings"
	"testing"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// TestOSIndexNowBadgeStates covers the four states the per-post IndexNow badge
// can render: submitted, failed, never-sent, and draft (not public).
func TestOSIndexNowBadgeStates(t *testing.T) {
	sub := osIndexNowBadge("hello-world",
		dbpkg.IndexNowStatus{State: dbpkg.IndexNowSubmitted, HTTPCode: 200, SubmittedAt: time.Unix(1700000000, 0).UTC()}, true, false)
	if !strings.Contains(sub, `id="post-indexnow-hello-world"`) || !strings.Contains(sub, "✓ IndexNow") {
		t.Errorf("submitted badge wrong:\n%s", sub)
	}

	failed := osIndexNowBadge("hello-world", dbpkg.IndexNowStatus{State: dbpkg.IndexNowFailed, Detail: "endpoint returned HTTP 429"}, true, false)
	if !strings.Contains(failed, "IndexNow failed") {
		t.Errorf("failed badge wrong:\n%s", failed)
	}

	notSent := osIndexNowBadge("hello-world", dbpkg.IndexNowStatus{}, false, false)
	if !strings.Contains(notSent, "not sent") {
		t.Errorf("not-sent badge wrong:\n%s", notSent)
	}

	draft := osIndexNowBadge("hello-world", dbpkg.IndexNowStatus{}, false, true)
	if !strings.Contains(draft, "IndexNow: —") {
		t.Errorf("draft badge should show a neutral dash:\n%s", draft)
	}
}

// TestOSIndexNowButton verifies the manual re-ping control: hidden for drafts,
// "Ping IndexNow" for a published post that was never submitted, and "Re-ping"
// once it has been submitted — always POSTing to the fragment endpoint.
func TestOSIndexNowButton(t *testing.T) {
	if got := osIndexNowButton("hello-world", dbpkg.IndexNowStatus{}, false, true); got != "" {
		t.Errorf("a draft must have no IndexNow button, got %q", got)
	}

	unsent := osIndexNowButton("hello-world", dbpkg.IndexNowStatus{}, false, false)
	for _, want := range []string{"Ping IndexNow", `hx-post="/os/api/posts/hello-world/indexnow-fragment"`, `hx-swap="outerHTML"`} {
		if !strings.Contains(unsent, want) {
			t.Errorf("button missing %q:\n%s", want, unsent)
		}
	}

	resend := osIndexNowButton("hello-world", dbpkg.IndexNowStatus{State: dbpkg.IndexNowSubmitted}, true, false)
	if !strings.Contains(resend, "Re-ping") {
		t.Errorf("an already-submitted post should offer Re-ping:\n%s", resend)
	}
}

// TestOSIndexNowBadgeOOB checks the out-of-band variant the fragment endpoint
// returns so HTMX swaps just that row's badge.
func TestOSIndexNowBadgeOOB(t *testing.T) {
	oob := osIndexNowBadgeOOB("hello-world", dbpkg.IndexNowStatus{State: dbpkg.IndexNowSubmitted}, true, false)
	if !strings.Contains(oob, `hx-swap-oob="true"`) || !strings.Contains(oob, `id="post-indexnow-hello-world"`) {
		t.Errorf("OOB badge must carry the swap attr and the stable id:\n%s", oob)
	}
}
