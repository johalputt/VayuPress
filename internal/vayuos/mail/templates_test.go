// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func names(t *testing.T, e *Engine, rd Reader) []string {
	t.Helper()
	list, err := e.Templates(rd)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, x := range list {
		out = append(out, x.Name+"="+x.Subject+"|"+x.Body)
	}
	return out
}

// Saved by name, listed by name, and saving under a name in use replaces it.
func TestTemplatesAreSavedListedAndReplaced(t *testing.T) {
	e := newLoopbackEngine(t, loopbackBridge{})
	rd := ReadAsOwner("alice")
	for _, s := range [][3]string{{"Thanks", "Thank you", "Thanks for this."}, {"away  now", "", "I am away."}, {"  Thanks  ", "Thank you!", "Thanks, again."}, {"away now", "", "Back on Monday."}} {
		if err := e.SaveTemplate(rd, s[0], s[1], s[2]); err != nil {
			t.Fatalf("save %q: %v", s[0], err)
		}
	}
	if got := strings.Join(names(t, e, rd), ","); got != "away now=|Back on Monday.,Thanks=Thank you!|Thanks, again." {
		t.Fatalf("listed %s", got)
	}
}

// One seed per refusal.
func TestWhatATemplateCannotBe(t *testing.T) {
	e := newLoopbackEngine(t, loopbackBridge{})
	rd := ReadAsOwner("alice")
	for name, c := range map[string]struct{ name, subject, body, want string }{
		"no name":   {"   ", "s", "b", "needs a name"},
		"long name": {strings.Repeat("é", 61), "s", "b", "at most 60 characters"},
		"empty":     {"x", " ", "\n", "nothing to save"},
		"too big":   {"x", "s", strings.Repeat("b", 16<<10), "at most 16 KB"},
	} {
		if err := e.SaveTemplate(rd, c.name, c.subject, c.body); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	if err := e.SaveTemplate(rd, strings.Repeat("é", 60), "", "b"); err != nil {
		t.Errorf("a 60-character name: %v", err)
	}
}

// At the cap a new name is refused, and an existing one can still be edited.
func TestTheTemplateCapStillAllowsAnEdit(t *testing.T) {
	e := newLoopbackEngine(t, loopbackBridge{})
	rd := ReadAsOwner("alice")
	for i := 0; i < maxTemplatesPerMailbox; i++ {
		if err := e.SaveTemplate(rd, "t"+strconv.Itoa(i), "", "b"); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.SaveTemplate(rd, "one more", "", "b"); err == nil || !strings.Contains(err.Error(), "delete one") {
		t.Fatalf("past the cap: %v", err)
	}
	if err := e.SaveTemplate(rd, "t7", "", "edited"); err != nil {
		t.Fatalf("editing at the cap: %v", err)
	}
}

// A template is its mailbox's: another mailbox neither lists nor deletes it,
// a read-only mailbox saves none, and deleting the mailbox removes them.
func TestTemplatesAreTheMailboxsOwn(t *testing.T) {
	e := newLoopbackEngine(t, loopbackBridge{})
	ctx := context.Background()
	alice, bob := ReadAsOwner("alice"), ReadAsOwner("bob")
	if err := e.accounts.Create(ctx, "alice@example.com", "x", "A", RoleMailbox); err != nil {
		t.Fatal(err)
	}
	if err := e.SaveTemplate(alice, "mine", "", "b"); err != nil {
		t.Fatal(err)
	}
	if got := names(t, e, bob); len(got) != 0 {
		t.Fatalf("bob lists %v", got)
	}
	list, _ := e.Templates(alice)
	if err := e.DeleteTemplate(bob, list[0].ID); err != nil || len(names(t, e, alice)) != 1 {
		t.Fatalf("bob deleting alice's template: %v; alice has %v", err, names(t, e, alice))
	}
	if err := e.DeleteTemplate(alice, list[0].ID); err != nil || len(names(t, e, alice)) != 0 {
		t.Fatalf("alice deleting her own: %v; left %v", err, names(t, e, alice))
	}
	if err := e.accounts.Create(ctx, "rev@example.com", "x", "R", RoleReviewer); err != nil {
		t.Fatal(err)
	}
	if err := e.SaveTemplate(ReadAsOwner("rev"), "x", "", "b"); err == nil {
		t.Fatal("a read-only mailbox saved a template")
	}
	_ = e.SaveTemplate(alice, "kept?", "", "b")
	if err := e.accounts.Delete(ctx, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	if got := names(t, e, alice); len(got) != 0 {
		t.Fatalf("a deleted mailbox's templates outlived it: %v", got)
	}
}
