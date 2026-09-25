// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// The mailbox DIRECTORY — every mailbox on the install, one card per mail
// domain — is what an administrator without a mailbox of their own gets from
// /os/vayumail/inbox with no ?user=, and what any administrator gets from
// ?all=1 ("All mailboxes" in the Mail sidebar). One with a mailbox opens it.
//
// The authority consolidation replaced a `if !isAdmin { … }` guard with an
// unconditional `if user == "" { refuse }`. For an administrator, `user == ""`
// is not a refusal condition — it is the directory condition, and it was the
// only way to reach the directory. So the refusal was hoisted in front of the
// feature, and the administrator opening their own Mailbox tab was told to "ask
// an administrator to assign you an email address".
//
// The failure is specific and worth naming: the guard is CORRECT for the reader
// it was written for and wrong for the one that shares the branch. A test that
// only exercised the non-admin path would pass against the broken build, and
// every one of them did.

// mailboxDirectoryRequested must be the decision, so it can be exercised here
// rather than inferred from a rendered page.
func TestTheMailboxTabWithNoMailboxNamedIsTheDirectoryForAnAdministrator(t *testing.T) {
	cases := []struct {
		name      string
		isAdmin   bool
		own       string
		requested string
		all       bool
		want      bool
	}{
		{"an administrator with no mailbox of their own wants every mailbox", true, "", "", false, true},
		{"an administrator with a mailbox opens it (render 04)", true, "ankush", "", false, false},
		{"an administrator asking for all mailboxes gets them", true, "ankush", "", true, true},
		{"an administrator who named a mailbox wants that one", true, "ankush", "someone", true, false},
		{"a non-admin never gets the directory, named or not", false, "", "", true, false},
		{"a non-admin naming a mailbox still never gets the directory", false, "me", "victim", true, false},
		{"whitespace is not a mailbox name", true, "", "   ", false, true},
	}
	for _, c := range cases {
		if got := mailboxDirectoryRequested(c.isAdmin, c.own, c.requested, c.all); got != c.want {
			t.Errorf("%s: mailboxDirectoryRequested(%v, %q, %q, %v) = %v, want %v",
				c.name, c.isAdmin, c.own, c.requested, c.all, got, c.want)
		}
	}
}

// The structural half, and the one that actually pins the regression: inside the
// handler, the refusal must not be reachable before the directory. Ordering is
// the defect — both pieces of code were present the whole time.
func TestTheMailboxDirectoryIsNotFencedOffByTheRefusal(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "vayuos.go"), "handleVayuOSInbox")
	if body == "" {
		t.Fatal("handleVayuOSInbox not found — this test no longer checks anything")
	}
	dir := strings.Index(body, "vayuMailboxTabs")
	refuse := strings.Index(body, "No mailbox has been assigned")
	if dir < 0 {
		t.Fatal("the mailbox handler no longer renders the directory at all")
	}
	if refuse < 0 {
		return // nothing refuses; nothing to order
	}
	if refuse < dir {
		t.Fatal("the mailbox handler refuses before it can render the directory. An " +
			"administrator opening the Mailbox tab — which carries no ?user= — is told no " +
			"mailbox is assigned to them and to go ask an administrator, and the directory " +
			"of every mailbox on the install is unreachable")
	}
	if !strings.Contains(body, "mailboxDirectoryRequested") {
		t.Error("the handler no longer asks whether this is a directory request, so the " +
			"decision is back to being implied by a condition that means two things")
	}
}

// A refusal must not sit behind an identical one. Two consecutive `user == ""`
// guards is what the mechanical rewrite left behind: the second is unreachable,
// and its message is the accurate one, so the sentence a person actually sees is
// the one nobody chose.
func TestNoMailHandlerRefusesTwiceForTheSameReason(t *testing.T) {
	src := readSourceFile(t, "vayuos.go")
	for _, fn := range []string{
		"handleVayuOSInbox",
		"handleVayuOSInboxFragment",
		"handleVayuOSInboxAction",
		"handleVayuOSMessagePaneAction",
	} {
		body := goFuncBody(src, fn)
		if body == "" {
			continue
		}
		if n := strings.Count(body, `if user == ""`); n > 1 {
			t.Errorf("%s guards on an empty mailbox %d times; everything after the first "+
				"is dead, including the message that describes the situation correctly", fn, n)
		}
	}
}

// The refusal must describe what happened. "You can only manage your own
// mailbox" is a permission verdict, and no permission was denied — the caller
// named no mailbox and has none assigned. A false reason sends the reader to fix
// the wrong thing.
func TestTheMailRefusalDoesNotClaimAPermissionDenialThatDidNotHappen(t *testing.T) {
	src := readSourceFile(t, "vayuos.go")
	for _, fn := range []string{"handleVayuOSInboxAction", "handleVayuOSMessagePaneAction"} {
		body := goFuncBody(src, fn)
		if body == "" {
			continue
		}
		if strings.Contains(strings.ToLower(body), "you can only manage your own mailbox") {
			t.Errorf("%s refuses an empty mailbox by claiming a permission denial. Nothing "+
				"was forbidden: no mailbox was named and none is assigned", fn)
		}
	}
}
