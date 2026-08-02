// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Handover shipped with no trigger.
//
// Every severance was built and tested — panel reads, the operator's own console
// password over IMAP, password reset, second-factor clearing, credential
// minting, forwarding — and `HandOver()` had zero callers outside its own
// definition. No button, no route, no CLI subcommand. So the state they all key
// off could never be set, none of them could ever fire, and the sentence shipped
// in the changelog and on the website described a capability no operator could
// reach.
//
// These tests are written from the position of someone checking whether the
// published promise is real.

// The trigger has to exist and be wired, or nothing above it matters.
func TestHandoverIsReachableFromThePanel(t *testing.T) {
	src := readSourceFile(t, "vayuos_mail_accounts.go")
	if !strings.Contains(src, `case "handover":`) {
		t.Fatal("the accounts action handler has no handover operation, so the state every " +
			"severance keys off can never be set and none of them can ever fire")
	}
	if !strings.Contains(src, "vayuCardHandover") {
		t.Error("nothing renders the handover control, so the operation exists and is unreachable")
	}
	body := goFuncBody(src, "handOverMailbox")
	if !strings.Contains(body, "HandOver(") {
		t.Error("the handover operation never calls HandOver")
	}
}

// A one-way action must not be one click away. The database refuses to clear
// handed_at, so a mis-click is permanent and the operator cannot undo it for a
// client who calls back an hour later.
func TestHandoverRequiresTheAddressRetypedOnTheServer(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "vayuos_mail_accounts.go"), "handOverMailbox")
	if !strings.Contains(body, `r.FormValue("confirm")`) {
		t.Fatal("handover takes no typed confirmation. hx-confirm is a client-side courtesy that a " +
			"double-submit or a replayed request walks straight through, and the result is permanent")
	}
	if !strings.Contains(body, "!= email") {
		t.Error("the confirmation is read but not compared to the mailbox, so any value passes")
	}
}

// The published claim is that break-glass writes a record AND notifies a contact
// outside the install. With no confirmed recovery address the notice can only be
// filed into the mailbox — readable and deletable by whoever just used the
// override. Shipping the promise minus the half that makes it observable is the
// defect class this project keeps finding, so it is refused, not warned about.
// This test EXERCISES the gate rather than reading for it. The first version
// asserted that RecoveryContact( appeared in the body and preceded HandOver( —
// both of which stay true when the guard is replaced by `if false`, so it passed
// against a build with the gate removed and proved nothing at all.
func TestHandoverIsRefusedWithoutARecoveryAddress(t *testing.T) {
	a := appWithMailAccounts(t)
	ctx := context.Background()
	const mbox = "dana@example.com"

	req := httptest.NewRequest(http.MethodPost, "/os/vayumail/accounts/action",
		strings.NewReader(url.Values{"confirm": {mbox}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if err := a.handOverMailbox(req, mbox); err == nil {
		t.Fatal("a mailbox with no confirmed recovery address was handed over. The published " +
			"claim is that the emergency override writes a record AND notifies a contact outside " +
			"the install; with no contact the notice can only be filed into the mailbox, where " +
			"whoever used the override can delete it")
	}
	if a.vayuMail.IsHandedOver(mbox) {
		t.Error("the refusal returned an error but the handover took effect anyway")
	}

	// With a confirmed contact it must actually succeed, or the gate is an outage
	// rather than a gate.
	if err := a.vayuMail.Accounts().SetRecoveryContactPending(ctx, mbox, "owner@elsewhere.test",
		func(string) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if err := a.vayuMail.Accounts().VerifyRecoveryContact(ctx, mbox); err != nil {
		t.Fatal(err)
	}
	req2 := httptest.NewRequest(http.MethodPost, "/os/vayumail/accounts/action",
		strings.NewReader(url.Values{"confirm": {mbox}}.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := a.handOverMailbox(req2, mbox); err != nil {
		t.Fatalf("handover refused a mailbox that meets every condition: %v", err)
	}
	if !a.vayuMail.IsHandedOver(mbox) {
		t.Error("handover reported success and the mailbox is not handed over")
	}
}

// The typed confirmation, exercised rather than read for — same reason as above.
func TestAWrongConfirmationDoesNotHandTheMailboxOver(t *testing.T) {
	a := appWithMailAccounts(t)
	ctx := context.Background()
	const mbox = "dana@example.com"
	if err := a.vayuMail.Accounts().SetRecoveryContactPending(ctx, mbox, "owner@elsewhere.test",
		func(string) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if err := a.vayuMail.Accounts().VerifyRecoveryContact(ctx, mbox); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "yes", "DANA@EXAMPLE.COM ", "dana"} {
		req := httptest.NewRequest(http.MethodPost, "/os/vayumail/accounts/action",
			strings.NewReader(url.Values{"confirm": {bad}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		err := a.handOverMailbox(req, mbox)
		// A trimmed, lowercased match is the SAME address and must be accepted;
		// anything else is not and must not be.
		want := strings.ToLower(strings.TrimSpace(bad)) == mbox
		if want && err != nil {
			t.Errorf("confirmation %q is the same address and was refused: %v", bad, err)
		}
		if !want && err == nil {
			t.Errorf("confirmation %q handed the mailbox over permanently", bad)
		}
		if a.vayuMail.IsHandedOver(mbox) != want {
			t.Errorf("confirmation %q left handed-over = %v", bad, a.vayuMail.IsHandedOver(mbox))
		}
		if want {
			return // it is one-way; nothing after this can be tested on this mailbox
		}
	}
}

// Repeating it would append a second "handover" line to the client's record for
// an event that did not happen. A record that reports things that did not happen
// is one nobody reads, and the record is the entire product here.
func TestHandingOverTwiceIsRefused(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "vayuos_mail_accounts.go"), "handOverMailbox")
	if !strings.Contains(body, "IsHandedOver(") {
		t.Error("handover does not check whether the mailbox is already handed over, so it can " +
			"write a duplicate entry for an event that did not occur")
	}
}

// The operator is told what THEY lose, before they commit — and the card must
// not overstate it. "Not encryption" has to survive on the one screen where an
// operator forms their understanding of what they are selling.
//
// This asserts on the RENDERED card, not on the source. The first version read
// the function body and compared string offsets, which measures the order the
// variables are DECLARED in — the button is built before the prose it is
// concatenated after — and that has nothing to do with what an operator sees.
// A test that can pass or fail on source layout is testing the wrong artefact.
func TestTheHandoverCardStatesTheLimitBeforeTheButton(t *testing.T) {
	a := appWithMailAccounts(t)
	ctx := context.Background()

	accs, err := a.vayuMail.Accounts().List(ctx)
	if err != nil || len(accs) == 0 {
		t.Fatalf("no account to render: %v", err)
	}
	ac := accs[0]

	// With no recovery address the control must not be offered at all.
	blocked := a.vayuCardHandover(ctx, ac)
	assertCSPSafe(t, "vayuCardHandover", blocked)
	if strings.Contains(blocked, "Hand over</button>") {
		t.Error("the handover button is offered on a mailbox with no recovery address, so the " +
			"break-glass notice would have nowhere to go but the mailbox itself")
	}
	if !strings.Contains(blocked, "recovery address") {
		t.Error("the card does not say why the control is missing, which reads as a broken page")
	}

	// With one confirmed, the control appears — and the bounding sentences must
	// come before it in the markup the operator actually reads.
	if err := a.vayuMail.Accounts().SetRecoveryContactPending(ctx, ac.Email, "owner@elsewhere.test", func(string) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if err := a.vayuMail.Accounts().VerifyRecoveryContact(ctx, ac.Email); err != nil {
		t.Fatal(err)
	}
	card := a.vayuCardHandover(ctx, ac)
	assertCSPSafe(t, "vayuCardHandover", card)

	btn := strings.Index(card, "Hand over</button>")
	if btn < 0 {
		t.Fatal("with a confirmed recovery address the handover control is still not offered")
	}
	for _, want := range []string{"not encryption", "One way", "app passwords keep working"} {
		at := strings.Index(card, want)
		if at < 0 {
			t.Errorf("the rendered card never says %q, so the operator commits to a one-way "+
				"action without the sentence that bounds it", want)
			continue
		}
		if at > btn {
			t.Errorf("%q appears AFTER the button, where an operator has already clicked", want)
		}
	}
	// The confirmation field has to be in the rendered card, not merely handled.
	if !strings.Contains(card, `name="confirm"`) {
		t.Error("there is no field for the operator to retype the address into")
	}
}
