// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_ux_phase2_test.go — regression pins for the VayuMail/VayuTalk UX pass,
// Phase 2 (docs/UX-AUDIT-2026-08-VAYUMAIL-VAYUTALK.md). These guard the scale and
// "does the product tell the truth about what it is showing" behaviours.

import (
	"strings"
	"testing"
)

// TestTheFolderViewIsWindowed pins the cap: an unbounded render made a large
// mailbox slow to open and re-render, and made every 90s poll re-emit thousands
// of rows. Older mail must stay one explicit click away, with the count shown.
func TestTheFolderViewIsWindowed(t *testing.T) {
	src := withoutComments(readFileString(t, "vayuos.go"))
	if !strings.Contains(src, "const inboxPageSize") {
		t.Error("the folder view needs a bounded window")
	}
	if !strings.Contains(src, "msgs = msgs[:limit]") {
		t.Error("the window must actually be applied to the message list")
	}
	if !strings.Contains(src, "Load older") {
		t.Error("a windowed list must offer the rest in one click — silently truncating is worse than rendering all of it")
	}
	if !strings.Contains(src, "Showing the newest ") {
		t.Error("the view must say how much is out of view")
	}
	// The window has to survive the poll and a fragment re-request, or the list
	// silently collapses back to page one under the reader.
	if !strings.Contains(src, `r.URL.Query().Get("limit")`) {
		t.Error("the fragment must accept the requested window")
	}
	if !strings.Contains(src, `r.PostFormValue("limit")`) {
		t.Error("a row/bulk action must re-render the window the operator was looking at")
	}
}

// TestSearchAsksForTwoCharactersAndSaysWhenItTruncated — a one-character query
// scanned the engine's whole bounded window, and a capped scan reported its
// result count as if it were a total.
func TestSearchAsksForTwoCharactersAndSaysWhenItTruncated(t *testing.T) {
	src := withoutComments(readFileString(t, "vayuos.go"))
	if !strings.Contains(src, "search starts at two characters") {
		t.Error("search must ask for a second character instead of scanning everything for one")
	}
	if !strings.Contains(src, "capped") || !strings.Contains(src, "hit its limit") {
		t.Error("a capped scan must say so — otherwise the count reads as a total")
	}
	if !strings.Contains(src, "delay:600ms") {
		t.Error("the debounce is what stops a fast typist from launching a scan per keystroke")
	}
}

// TestAnEmptyInboxPointsAtTheNextStep — first run used to be one muted sentence.
func TestAnEmptyInboxPointsAtTheNextStep(t *testing.T) {
	src := withoutComments(readFileString(t, "vayuos.go"))
	if !strings.Contains(src, "Your inbox is empty") {
		t.Error("an empty inbox needs a first-run state, not a muted sentence")
	}
	if !strings.Contains(src, "Write your first email") {
		t.Error("the empty state must offer the next action")
	}
	// The classes it uses have to exist (the console-contract test would otherwise
	// fail the build, so this documents the intent next to the markup).
	for _, cls := range []string{"empty-icon", "empty-title", "empty-sub"} {
		if !strings.Contains(adminOSCSS(t), ".vp-os ."+cls+" ") && !strings.Contains(adminOSCSS(t), ".vp-os ."+cls+"{") {
			t.Errorf(".%s is rendered but has no rule in admin-os.css", cls)
		}
	}
}

// TestTheDisplayedMailboxSizeIsCachedButTheQuotaGateIsNot is the correctness half
// of the accounts-page speed-up: a stale size must never be what decides whether
// a send is refused.
func TestTheDisplayedMailboxSizeIsCachedButTheQuotaGateIsNot(t *testing.T) {
	engine := readFileString(t, "../../internal/vayuos/mail/engine.go")
	if !strings.Contains(engine, "usageTTL") {
		t.Error("the display path needs its short cache, or the accounts page walks every mailbox on every render")
	}
	if !strings.Contains(engine, "e.mailboxUsage(email, false) >= q") {
		t.Error("MailboxOverQuota must measure fresh — a cached size could let a send slip past a quota just reached")
	}
	if !strings.Contains(engine, "return e.mailboxUsage(email, true)") {
		t.Error("MailboxUsage (display) should take the cached path")
	}
}

// TestFolderListingDoesNotRereadEveryMessage pins the header cache: listing a
// folder parsed every message file on every call.
func TestFolderListingDoesNotRereadEveryMessage(t *testing.T) {
	md := readFileString(t, "../../internal/vayuos/mail/maildir.go")
	if !strings.Contains(md, "hdrCache") || !strings.Contains(md, "func (m *Maildir) headersFor(") {
		t.Error("the maildir needs a parsed-header cache")
	}
	// The cache must be validated by the file's identity, not just its path.
	for _, want := range []string{"h.size == size", "h.modTime.Equal(mod)"} {
		if !strings.Contains(md, want) {
			t.Errorf("cache validation is missing %q — a rewritten message would keep its old subject", want)
		}
	}
	for _, f := range []string{"folders.go", "inbound.go"} {
		src := readFileString(t, "../../internal/vayuos/mail/"+f)
		if !strings.Contains(src, "m.headersFor(") {
			t.Errorf("%s must use the cached headers", f)
		}
		if strings.Contains(src, "os.ReadFile(filepath.Join(dir") {
			t.Errorf("%s still reads every message body while listing", f)
		}
	}
}
