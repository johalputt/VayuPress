// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const groupOwner = "ankush@johal.in"

func groupsOf(t *testing.T, s *AccountStore, owner string) []ContactGroup {
	t.Helper()
	g, err := s.ContactGroups(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func contactNames(t *testing.T, s *AccountStore, owner string) map[string]string {
	t.Helper()
	list, err := s.ListContacts(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, c := range list {
		out[c.Email] = c.Name
	}
	return out
}

// A group's members are kept once each, without the mailbox's own address,
// and become contacts without losing a name one already had; saving under
// the same name in another case replaces the group.
func TestAContactGroupIsSavedAndReplaced(t *testing.T) {
	s, ctx := newContactStore(t), context.Background()
	if err := s.AddContact(ctx, groupOwner, "b@x.com", "Bea"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveContactGroup(ctx, groupOwner, "Team", []string{"A@x.com", "b@x.com", " a@x.com ", "", groupOwner}); err != nil {
		t.Fatal(err)
	}
	if got := groupsOf(t, s, groupOwner); !reflect.DeepEqual(got, []ContactGroup{{Name: "Team", Members: []string{"a@x.com", "b@x.com"}}}) {
		t.Fatalf("groups %+v", got)
	}
	if got := contactNames(t, s, groupOwner); !reflect.DeepEqual(got, map[string]string{"a@x.com": "", "b@x.com": "Bea"}) {
		t.Fatalf("contacts %v", got)
	}
	if err := s.SaveContactGroup(ctx, groupOwner, "team", []string{"c@x.com"}); err != nil {
		t.Fatal(err)
	}
	if got := groupsOf(t, s, groupOwner); !reflect.DeepEqual(got, []ContactGroup{{Name: "team", Members: []string{"c@x.com"}}}) {
		t.Fatalf("after replacing: %+v", got)
	}
	if got := contactNames(t, s, groupOwner); len(got) != 3 {
		t.Fatalf("a member left out of the group stopped being a contact: %v", got)
	}
}

// One seed per refusal, with the edge each allows.
func TestWhatAContactGroupCannotBe(t *testing.T) {
	s, ctx := newContactStore(t), context.Background()
	one := []string{"a@x.com"}
	for name, c := range map[string]struct {
		name    string
		members []string
		want    string
	}{
		"a character":       {"a@b", one, "a group's name"},
		"too long":          {strings.Repeat("é", 41), one, "a group's name"},
		"a space at an end": {"Team ", one, "a group's name"},
		"not an address":    {"Team", []string{"bob"}, `"bob" is not an email address`},
		"no one":            {"Team", []string{" ", ""}, "at least one address"},
		"only its own":      {"Team", []string{groupOwner}, "at least one address"},
	} {
		if err := s.SaveContactGroup(ctx, groupOwner, c.name, c.members); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	if err := s.SaveContactGroup(ctx, groupOwner, strings.Repeat("é", 40), one); err != nil {
		t.Errorf("a 40-character name: %v", err)
	}
	var many []string
	for i := 0; i < maxContactGroupMembers; i++ {
		many = append(many, "p"+strconv.Itoa(i)+"@x.com")
	}
	if err := s.SaveContactGroup(ctx, groupOwner, "Big", many); err != nil {
		t.Errorf("500 people: %v", err)
	}
	if err := s.SaveContactGroup(ctx, groupOwner, "Big", append(many, "one@more.com")); err == nil || !strings.Contains(err.Error(), "at most 500 people") {
		t.Errorf("501 people: %v", err)
	}
}

// At the cap a new group is refused, and one already kept can still be
// edited.
func TestTheContactGroupCapStillAllowsAnEdit(t *testing.T) {
	s, ctx := newContactStore(t), context.Background()
	for i := 0; i < maxContactGroups; i++ {
		if err := s.SaveContactGroup(ctx, groupOwner, "g"+strconv.Itoa(i), []string{"a@x.com"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveContactGroup(ctx, groupOwner, "one more", []string{"a@x.com"}); err == nil || !strings.Contains(err.Error(), "delete one to make another") {
		t.Fatalf("past the cap: %v", err)
	}
	if err := s.SaveContactGroup(ctx, groupOwner, "G7", []string{"b@x.com"}); err != nil {
		t.Fatalf("editing at the cap: %v", err)
	}
}

// Deleting a group keeps its people as contacts; deleting a contact takes it
// out of its groups, and a group left with no one is gone.
func TestDeletingAGroupOrAContact(t *testing.T) {
	s, ctx := newContactStore(t), context.Background()
	_ = s.SaveContactGroup(ctx, groupOwner, "Pair", []string{"a@x.com", "b@x.com"})
	_ = s.SaveContactGroup(ctx, groupOwner, "Solo", []string{"c@x.com"})
	if err := s.DeleteContactGroup(ctx, groupOwner, "pair"); err != nil {
		t.Fatal(err)
	}
	if got := groupsOf(t, s, groupOwner); len(got) != 1 || got[0].Name != "Solo" || len(contactNames(t, s, groupOwner)) != 3 {
		t.Fatalf("after deleting Pair: %+v, contacts %v", got, contactNames(t, s, groupOwner))
	}
	if err := s.DeleteContact(ctx, groupOwner, "C@x.com"); err != nil {
		t.Fatal(err)
	}
	if got := groupsOf(t, s, groupOwner); len(got) != 0 {
		t.Fatalf("a removed contact stayed in its group: %+v", got)
	}
}

// A group is its mailbox's: another neither lists nor deletes it, and
// deleting the account removes it.
func TestContactGroupsAreTheMailboxsOwn(t *testing.T) {
	s, ctx := newContactStore(t), context.Background()
	if err := s.Create(ctx, groupOwner, "x", "A", RoleMailbox); err != nil {
		t.Fatal(err)
	}
	_ = s.SaveContactGroup(ctx, groupOwner, "Team", []string{"a@x.com"})
	if got := groupsOf(t, s, "other@johal.in"); len(got) != 0 {
		t.Fatalf("another mailbox lists %+v", got)
	}
	if err := s.DeleteContactGroup(ctx, "other@johal.in", "Team"); err != nil || len(groupsOf(t, s, groupOwner)) != 1 {
		t.Fatalf("another mailbox deleted the group: %v", err)
	}
	if err := s.Delete(ctx, groupOwner); err != nil {
		t.Fatal(err)
	}
	if got := groupsOf(t, s, groupOwner); len(got) != 0 {
		t.Fatalf("a deleted account's groups outlived it: %+v", got)
	}
}
