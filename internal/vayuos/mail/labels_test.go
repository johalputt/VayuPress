// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// A label is put on and taken off by Message-ID, kept in the spelling it
// was first given, and listed by name and by message.
func TestLabelsAreSetAndListed(t *testing.T) {
	e := newLoopbackEngine(t, loopbackBridge{})
	rd := ReadAsOwner("alice")
	for _, c := range [][2]string{{"<A@x>", "Work"}, {"<b@x>", "work"}, {"<b@x>", "To do"}, {"a@x", " Work "}} {
		if err := e.SetLabel(rd, c[0], c[1], true); err != nil {
			t.Fatalf("label %v: %v", c, err)
		}
	}
	if got, _ := e.Labels(rd); !reflect.DeepEqual(got, []string{"To do", "Work"}) {
		t.Fatalf("labels %v", got)
	}
	if got, _ := e.LabelsByMessage(rd); !reflect.DeepEqual(got, map[string][]string{"a@x": {"Work"}, "b@x": {"To do", "Work"}}) {
		t.Fatalf("by message %v", got)
	}
	if err := e.SetLabel(rd, "<b@x>", "WORK", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.LabelsByMessage(rd); !reflect.DeepEqual(got, map[string][]string{"a@x": {"Work"}, "b@x": {"To do"}}) {
		t.Fatalf("after taking one off: %v", got)
	}
}

// One seed per refusal, with the edge each allows.
func TestWhatALabelCannotBe(t *testing.T) {
	e := newLoopbackEngine(t, loopbackBridge{})
	rd := ReadAsOwner("alice")
	for name, c := range map[string]struct{ id, label, want string }{
		"no message id": {" <> ", "Work", "no Message-ID"},
		"a character":   {"m@x", "a:b", "a label is letters"},
		"too long":      {"m@x", strings.Repeat("é", 31), "a label is letters"},
		"empty":         {"m@x", "  ", "a label is letters"},
	} {
		if err := e.SetLabel(rd, c.id, c.label, true); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	if err := e.SetLabel(rd, "m@x", strings.Repeat("é", 30), true); err != nil {
		t.Errorf("a 30-character label: %v", err)
	}
}

// At the cap a new label is refused, and one already in use can still be
// put on another message.
func TestTheLabelCapStillAllowsOneInUse(t *testing.T) {
	e := newLoopbackEngine(t, loopbackBridge{})
	rd := ReadAsOwner("alice")
	for i := 0; i < maxLabelsPerMailbox; i++ {
		if err := e.SetLabel(rd, "m@x", "l"+strconv.Itoa(i), true); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.SetLabel(rd, "m@x", "one more", true); err == nil || !strings.Contains(err.Error(), "keeps 100 labels") {
		t.Fatalf("past the cap: %v", err)
	}
	if err := e.SetLabel(rd, "n@x", "L7", true); err != nil {
		t.Fatalf("a label in use, at the cap: %v", err)
	}
}

// A label is its mailbox's: another neither sees nor removes it, a read-only
// mailbox labels nothing, and deleting the mailbox removes its labels.
func TestLabelsAreTheMailboxsOwn(t *testing.T) {
	e := newLoopbackEngine(t, loopbackBridge{})
	ctx := context.Background()
	alice, bob := ReadAsOwner("alice"), ReadAsOwner("bob")
	if err := e.accounts.Create(ctx, "alice@example.com", "x", "A", RoleMailbox); err != nil {
		t.Fatal(err)
	}
	_ = e.SetLabel(alice, "m@x", "Work", true)
	if got, _ := e.Labels(bob); len(got) != 0 {
		t.Fatalf("bob sees %v", got)
	}
	_ = e.SetLabel(bob, "m@x", "Work", false)
	if got, _ := e.Labels(alice); len(got) != 1 {
		t.Fatalf("bob took alice's label off: %v", got)
	}
	if err := e.accounts.Create(ctx, "rev@example.com", "x", "R", RoleReviewer); err != nil {
		t.Fatal(err)
	}
	if err := e.SetLabel(ReadAsOwner("rev"), "m@x", "Work", true); err == nil {
		t.Fatal("a read-only mailbox labelled a message")
	}
	if err := e.accounts.Delete(ctx, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Labels(alice); len(got) != 0 {
		t.Fatalf("a deleted mailbox's labels outlived it: %v", got)
	}
}

// label: finds the messages that carry the label in any folder, as they
// move, and nothing else.
func TestALabelIsSearched(t *testing.T) {
	e := newLoopbackEngine(t, loopbackBridge{})
	rd := ReadAsOwner("bob")
	var ids []string
	for _, n := range []string{"one", "two"} {
		id, err := e.DeliverInbound("x@far.test", "bob@example.com", []byte("Message-ID: <"+n+"@far.test>\r\nFrom: x@far.test\r\nSubject: "+n+"\r\n\r\nbody"))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := e.SetLabel(rd, "<one@far.test>", "Work", true); err != nil {
		t.Fatal(err)
	}
	if err := e.MoveMessage(rd, ids[0], "Inbox", "Archive"); err != nil {
		t.Fatal(err)
	}
	got, err := e.Search(rd, ParseSearchQuery("label:work"), 50)
	if err != nil || len(got) != 1 || got[0].Subject != "one" || got[0].Folder != "Archive" {
		t.Fatalf("label:work found %+v, %v", got, err)
	}
	if got, _ := e.Search(rd, ParseSearchQuery("label:none"), 50); len(got) != 0 {
		t.Fatalf("an unused label found %+v", got)
	}
}
