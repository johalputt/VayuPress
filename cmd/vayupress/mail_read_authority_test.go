// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// "May this caller open this mailbox?" used to be answered separately at every
// entry point: one helper, seven inline `if !a.isAdminRequest(r)` copies, and at
// least one handler that asked nobody. That shape has a specific failure mode —
// fix one, miss another, suite stays green, operator read path survives — and it
// makes a promise about handover impossible to state truthfully, because nobody
// can enumerate the ways in.

// The zero value must refuse. A Reader nobody constructed is a caller who never
// said who was asking.
func TestAnUnsetReaderCannotRead(t *testing.T) {
	var rd vmail.Reader
	e := &vmail.Engine{}
	if _, err := e.ListFolder(rd, "Inbox"); err != vmail.ErrNoReadAuthority {
		t.Errorf("ListFolder with a zero Reader returned %v, want ErrNoReadAuthority", err)
	}
	if _, err := e.ReadFolderMessage(rd, "Inbox", "1"); err != vmail.ErrNoReadAuthority {
		t.Errorf("ReadFolderMessage with a zero Reader returned %v, want ErrNoReadAuthority", err)
	}
	if _, err := e.Search(rd, "q", 10); err != vmail.ErrNoReadAuthority {
		t.Errorf("Search with a zero Reader returned %v, want ErrNoReadAuthority", err)
	}
	if err := e.MoveMessage(rd, "1", "Inbox", "Trash"); err != vmail.ErrNoReadAuthority {
		t.Errorf("MoveMessage with a zero Reader returned %v, want ErrNoReadAuthority", err)
	}
	if err := e.DeleteMessage(rd, "Inbox", "1"); err != vmail.ErrNoReadAuthority {
		t.Errorf("DeleteMessage with a zero Reader returned %v, want ErrNoReadAuthority", err)
	}
	// The three constructed forms carry authority and must NOT be refused here —
	// a gate that refuses everything is not a gate, it is an outage.
	for name, ok := range map[string]vmail.Reader{
		"owner":    vmail.ReadAsOwner("bob"),
		"operator": vmail.ReadAsOperator("bob", "admin@x.test"),
		"system":   vmail.ReadAsSystem("bob", "quota"),
	} {
		if _, err := e.ListFolder(ok, "Inbox"); err == vmail.ErrNoReadAuthority {
			t.Errorf("a %s Reader was refused for lack of authority", name)
		}
	}
}

// Only an operator read can ever be refused by a handover, so the kind has to be
// distinguishable — and an admin reading their OWN mailbox is an owner read, not
// an operator one. Recording that as operator access would fill the
// client-visible log with entries about the operator's own inbox, which trains
// people to ignore the log, and the log is the whole product here.
func TestOperatorAuthorityIsDistinguishable(t *testing.T) {
	if !vmail.ReadAsOperator("victim", "admin@x.test").IsOperator() {
		t.Error("an operator read does not report itself as one, so a handover can never refuse it")
	}
	for name, rd := range map[string]vmail.Reader{
		"owner":  vmail.ReadAsOwner("bob"),
		"system": vmail.ReadAsSystem("bob", "quota"),
	} {
		if rd.IsOperator() {
			t.Errorf("a %s read reports as operator access; the record would fill with noise", name)
		}
	}
	if got := vmail.ReadAsOperator("victim", "admin@x.test").Actor(); got != "admin@x.test" {
		t.Errorf("Actor() = %q — an operator read that cannot name who made it cannot be recorded", got)
	}
}

// The cmd layer must mint operator authority in exactly ONE place. A second
// place is a second thing to remember when the handover refusal lands.
func TestOperatorAuthorityIsMintedInOnePlaceOnly(t *testing.T) {
	var offenders []string
	for _, f := range []string{
		"vayuos.go", "vayuos_mail.go", "vayuos_mail_contacts.go",
		"vayuos_mail_notify.go", "handlers_team.go",
	} {
		src := readSourceFile(t, f)
		n := strings.Count(src, "ReadAsOperator(")
		if f == "handlers_team.go" {
			if n == 0 {
				t.Error("handlers_team.go no longer mints operator authority; mailReader is the " +
					"one place it is supposed to happen")
			}
			continue
		}
		if n > 0 {
			offenders = append(offenders, f)
		}
	}
	if len(offenders) > 0 {
		t.Errorf("operator authority is minted outside mailReader in: %s. Every such site is "+
			"another place to change when handover refuses operator reads, and another to miss",
			strings.Join(offenders, ", "))
	}
}

// The helper it replaced must be gone, not merely unused. A dead
// admin-or-own helper is the obvious thing for a future change to reach for.
func TestTheOldScopedHelperIsGone(t *testing.T) {
	for _, f := range []string{"handlers_team.go", "vayuos.go", "vayuos_mail.go"} {
		if strings.Contains(readSourceFile(t, f), "func (a *App) scopedMailUser") {
			t.Errorf("%s still defines scopedMailUser — it answers the same question as "+
				"mailReader, in a form that returns a bare string and carries no authority", f)
		}
	}
}

// The decision itself, exercised directly rather than asserted about.
//
// The first version of this file tested the type and the call-site count but not
// the RULE, so removing "an admin reading their own inbox is an owner read" left
// the suite green. A test that passes against the broken version proves nothing.
func TestWhoMayOpenWhichMailbox(t *testing.T) {
	const own, actor = "alice@studio.test", "alice@studio.test"

	cases := []struct {
		name       string
		isAdmin    bool
		own, want  string
		requested  string
		isOperator bool
	}{
		{"a non-admin gets their own mailbox and nothing else",
			false, own, own, "victim@client.test", false},
		{"a non-admin with no mailbox gets no key",
			false, "", "", "victim@client.test", false},
		{"an admin asking for someone else's mailbox reads as an operator",
			true, own, "victim@client.test", "victim@client.test", true},
		{"an admin asking for nothing gets their own, as an owner",
			true, own, own, "", false},
		{"an admin naming their OWN mailbox is still an owner read",
			true, own, own, own, false},
		{"whitespace around a requested mailbox is trimmed, not treated as a name",
			true, own, own, "  " + own + "  ", false},
	}
	for _, c := range cases {
		rd := mailReaderFor(c.isAdmin, c.own, c.requested, actor)
		if rd.Key() != c.want {
			t.Errorf("%s: key = %q, want %q", c.name, rd.Key(), c.want)
		}
		if rd.IsOperator() != c.isOperator {
			t.Errorf("%s: IsOperator = %v, want %v", c.name, rd.IsOperator(), c.isOperator)
		}
	}
}
