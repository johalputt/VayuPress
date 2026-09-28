// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// TestValidIndexNowKey checks the IndexNow key format rule (8–128 chars of
// a–z, A–Z, 0–9 or hyphen) used to catch a misconfigured key before submitting.
func TestValidIndexNowKey(t *testing.T) {
	good := []string{"abcd1234", "0123456789abcdef0123456789abcdef", "a-b-c-d-1234", strings.Repeat("a", 128)}
	for _, k := range good {
		if !validIndexNowKey(k) {
			t.Errorf("expected %q to be a valid IndexNow key", k)
		}
	}
	bad := []string{"", "short", "abcd123", strings.Repeat("a", 129), "has space", "has_underscore", "emoji✓key", "with\nnewline"}
	for _, k := range bad {
		if validIndexNowKey(k) {
			t.Errorf("expected %q to be rejected as an IndexNow key", k)
		}
	}
}

// TestIndexNowStatusHint maps the overloaded IndexNow status codes to advice.
// 200 and 202 deliberately differ: the protocol defines 202 as "received — key
// validation pending", and a 202 whose key never validates is dropped silently.
func TestIndexNowStatusHint(t *testing.T) {
	cases := map[int]string{200: "submitted", 202: "pending", 400: "invalid", 403: "key", 422: "host", 429: "rate"}
	for code, want := range cases {
		if got := indexNowStatusHint(code); !strings.Contains(strings.ToLower(got), want) {
			t.Errorf("hint for %d = %q, want it to mention %q", code, got, want)
		}
	}
}

// TestOSIndexNowStates covers the four states a post's IndexNow line can
// show, each as a dot and a word: sent, failed, never sent, and not public.
func TestOSIndexNowStates(t *testing.T) {
	for _, c := range []struct {
		name  string
		st    dbpkg.IndexNowStatus
		ok    bool
		draft bool
		dot   string
		word  string
	}{
		{"sent", dbpkg.IndexNowStatus{State: dbpkg.IndexNowSubmitted, HTTPCode: 200, SubmittedAt: time.Unix(1700000000, 0).UTC()}, true, false, "sa-dot--ok", "Sent 14 Nov"},
		{"failed", dbpkg.IndexNowStatus{State: dbpkg.IndexNowFailed, Detail: "endpoint returned HTTP 429"}, true, false, "sa-dot--danger", "Failed"},
		{"never sent", dbpkg.IndexNowStatus{}, false, false, "sa-dot--neutral", "Not sent"},
		{"a draft", dbpkg.IndexNowStatus{}, false, true, "sa-dot--neutral", "Not public yet"},
	} {
		got := osIndexNowState("hello-world", c.st, c.ok, c.draft)
		if !strings.Contains(got, `id="post-indexnow-hello-world"`) || !strings.Contains(got, c.dot) || !strings.Contains(got, c.word) {
			t.Errorf("%s: want %s and %q with the stable id:\n%s", c.name, c.dot, c.word, got)
		}
	}
}

// TestOSIndexNowButton verifies the manual re-ping control: hidden for drafts,
// "Send to IndexNow" for a published post that was never sent, and "Send again"
// once it has been submitted — always POSTing to the fragment endpoint.
func TestOSIndexNowButton(t *testing.T) {
	if got := osIndexNowButton("hello-world", dbpkg.IndexNowStatus{}, false, true); got != "" {
		t.Errorf("a draft must have no IndexNow button, got %q", got)
	}

	unsent := osIndexNowButton("hello-world", dbpkg.IndexNowStatus{}, false, false)
	for _, want := range []string{"Send to IndexNow", `hx-post="/os/api/posts/hello-world/indexnow-fragment"`, `hx-swap="outerHTML"`} {
		if !strings.Contains(unsent, want) {
			t.Errorf("button missing %q:\n%s", want, unsent)
		}
	}

	resend := osIndexNowButton("hello-world", dbpkg.IndexNowStatus{State: dbpkg.IndexNowSubmitted}, true, false)
	if !strings.Contains(resend, "Send again") {
		t.Errorf("an already-sent post should offer Send again:\n%s", resend)
	}
}

// TestOSIndexNowStateOOB checks the out-of-band variant the fragment endpoint
// returns so HTMX swaps just that post's state.
func TestOSIndexNowStateOOB(t *testing.T) {
	oob := osIndexNowStateOOB("hello-world", dbpkg.IndexNowStatus{State: dbpkg.IndexNowSubmitted}, true, false)
	if !strings.Contains(oob, `hx-swap-oob="true"`) || !strings.Contains(oob, `id="post-indexnow-hello-world"`) {
		t.Errorf("OOB state must carry the swap attr and the stable id:\n%s", oob)
	}
}

func TestNewIndexNowKeyIsValid(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		k := newIndexNowKey()
		if !validIndexNowKey(k) {
			t.Fatalf("generated key %q fails IndexNow format rules", k)
		}
		if len(k) != 32 {
			t.Fatalf("key %q length = %d, want 32 hex chars", k, len(k))
		}
		if seen[k] {
			t.Fatalf("generated a duplicate key %q", k)
		}
		seen[k] = true
	}
}
