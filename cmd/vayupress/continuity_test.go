// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"strings"
	"testing"
)

// docs/CONTINUITY.md tells a client that if the studio disappears, the mail
// they have received is already on their own machine — because the mailbox
// holder can mint an app password for themselves and point a mail client at
// IMAP.
//
// That advice rests on two properties of this code. Neither is obvious, both
// are one edit away from reversing, and if either goes the document becomes
// advice that fails on the single day it is needed — which is worse than no
// document, because the client stopped keeping their own copy on the strength
// of it.
//
// So they are pinned here, next to the code, rather than trusted to a paragraph
// in a file nobody runs.

// The holder must keep the ability to mint their own credential AFTER handover.
// The severance exists to stop the OPERATOR minting one; if it caught the owner
// too, a handed-over mailbox would be one nobody could attach a mail client to,
// and handover would quietly destroy the client's escape route.
func TestTheHolderCanStillMintTheirOwnCredentialAfterHandover(t *testing.T) {
	src := readSourceFile(t, "vayuos_mail.go")
	body := goFuncBody(src, "canManageAppPassword")
	if body == "" {
		t.Fatal("canManageAppPassword not found")
	}
	owner := strings.Index(body, "isOwner")
	severance := strings.Index(body, "IsHandedOver(")
	if owner < 0 || severance < 0 {
		t.Fatalf("expected both an owner check and a handover severance, got owner=%d severance=%d",
			owner, severance)
	}
	if owner > severance {
		t.Error("the handover severance is evaluated BEFORE the owner check, so a handed-over " +
			"mailbox's own holder can no longer create an app password for it. docs/CONTINUITY.md " +
			"§3.1 tells clients this is their route to a local copy of their mail; with this " +
			"ordering there is no route, and they find out when the studio is gone")
	}
	// And the owner branch must actually RETURN, not merely be consulted.
	if !strings.Contains(body[owner:severance], "return true") {
		t.Error("the owner check does not short-circuit, so ownership no longer settles the question")
	}
}

// A mailbox holder signs in as a mail-only session. If that session cannot
// reach the Connect tab, they cannot mint the app password the plan depends on.
func TestAMailOnlySessionCanReachTheConnectTab(t *testing.T) {
	if !mailOnlyPathAllowed("/os/vayumail/accounts/apppassword") {
		t.Error("a mail-confined session cannot reach the app-password endpoint, so a mailbox " +
			"holder cannot create the credential docs/CONTINUITY.md §3.1 tells them to create")
	}
	// The narrowing that surrounds this must still hold — the point is that the
	// holder reaches their own mailbox surface, not the operator's.
	for _, denied := range []string{
		"/os/api/vayuos/health",
		"/os/api/vayuos/security/check",
		"/os/domains",
	} {
		if mailOnlyPathAllowed(denied) {
			t.Errorf("%s is reachable from a mail-only session; the continuity path must not be "+
				"bought by widening what a confined mailbox login can see", denied)
		}
	}
}

// The document makes claims about what is NOT covered. Those are the sentences
// that make the rest credible, so they have to survive editing.
func TestTheContinuityPlanKeepsItsLimits(t *testing.T) {
	b, err := os.ReadFile("../../docs/CONTINUITY.md")
	if err != nil {
		t.Fatalf("docs/CONTINUITY.md is missing — ADR-0152 open decision 7 requires it: %v", err)
	}
	doc := string(b)
	for _, want := range []string{
		"no self-service mail export",
		"It is procedural, not enforced",
		"has not been rehearsed",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("the plan no longer states %q. A continuity document that lists only its "+
				"strengths is one a client is right to discount", want)
		}
	}
	// The single highest-value instruction must not get softened into a suggestion.
	if !strings.Contains(doc, "Register the domain in the client's own name") {
		t.Error("the plan no longer leads with putting the domain in the client's name, which is " +
			"the only asset in it that cannot be reconstructed")
	}
}
