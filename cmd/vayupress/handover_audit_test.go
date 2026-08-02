// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// AUDIT FINDING (pre-release adversarial pass over ADR-0152 Phase 5).
//
// ADR-0152 D4 puts this sentence in front of a client:
//
//	"our own admin password stops working on your account"
//
// It was not true. verifyCredentialScoped step 1 authenticates the request
// against userStore BY ADDRESS:
//
//	b.app.userStore.Authenticate(ctx, addr, password)
//
// So an operator who creates a CMS user whose email is the handed-over mailbox's
// address — which an administrator can do at any time — authenticates IMAP,
// POP3 and submission for that mailbox with a password they chose. It leaves no
// ledger entry, no notice and no break-glass mark, and it is quieter and cheaper
// than the loud path.
//
// The other four severances were built and this one was described as built. A
// claim that is 80% enforced is not 80% true; it is false, and it was about to
// ship in a sentence a client would rely on.
func TestTheOperatorsOwnPasswordStopsWorkingOnAHandedOverMailbox(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "vayuos.go"), "verifyCredentialScoped")
	if body == "" {
		t.Fatal("verifyCredentialScoped not found")
	}
	if !strings.Contains(body, "IsHandedOver") {
		t.Fatal("verifyCredentialScoped never consults the handover state. An operator can " +
			"create a CMS user carrying the handed-over mailbox's address and authenticate " +
			"IMAP with it — defeating handover with no record. ADR-0152 D4's sentence " +
			"\"our own admin password stops working on your account\" is false while this holds.")
	}
	// The refusal must sit on the CMS-user branch specifically. Refusing the
	// mailbox's OWN password would lock the client out of their own mail, which
	// is the opposite of what handover is for.
	i := strings.Index(body, "IsHandedOver")
	j := strings.Index(body, "userStore.Authenticate")
	if i < 0 || j < 0 || i > j {
		t.Error("the handover check does not precede the CMS-user credential branch, so that " +
			"branch can still authenticate a handed-over mailbox")
	}
}

// AUDIT FINDING 2 (same pass). ADR-0152 D4's sentence promises "a permanent
// entry into the access log YOU CAN SEE". The ledger was written, chained and
// verifiable — and never rendered anywhere a client could reach.
//
// A claim whose evidence lives only in a database the client has no access to is
// a claim they are asked to take on trust, which is precisely what the ledger
// exists to avoid. Half an enforced sentence is not half true.
func TestTheClientCanActuallySeeTheAccessLog(t *testing.T) {
	src := readSourceFile(t, "admin_os_mysite.go")
	if !strings.Contains(src, "Ledger(") {
		t.Fatal("nothing on the client's page reads the access ledger, so the client cannot " +
			"see the log ADR-0152 D4 promises them")
	}
	body := goFuncBody(src, "handleOSMySite")
	if !strings.Contains(body, "mySiteAccessCard") {
		t.Error("the access record is not rendered on the page the client actually visits")
	}
	// The card must not overstate what the record covers. Direct server access is
	// exactly what it does NOT capture, and the client is told so.
	card := goFuncBody(src, "mySiteAccessCard")
	if !strings.Contains(card, "not encrypted") {
		t.Error("the access card does not say the record excludes someone reading the files " +
			"directly on the server — which would let a client read it as complete coverage")
	}
}
