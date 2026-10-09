// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// The cards other services export, one seed per way they are written.
func TestVCardsAreRead(t *testing.T) {
	for name, c := range map[string]struct {
		in   string
		want []VCard
	}{
		"3.0, CRLF": {"BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Priya Raman\r\nEMAIL;TYPE=INTERNET:priya@h.test\r\nEND:VCARD\r\n",
			[]VCard{{Name: "Priya Raman", Emails: []string{"priya@h.test"}}}},
		"folded": {"BEGIN:VCARD\nFN:Priya\n  Raman\nEMAIL:p@h.test\nEND:VCARD\n",
			[]VCard{{Name: "Priya Raman", Emails: []string{"p@h.test"}}}},
		"escapes": {"BEGIN:VCARD\nFN:Raman\\, Priya\\; ops\\\\x\nEMAIL:p@h.test\nEND:VCARD\n",
			[]VCard{{Name: `Raman, Priya; ops\x`, Emails: []string{"p@h.test"}}}},
		"N without FN": {"BEGIN:VCARD\nN:Raman;Priya;;;\nEMAIL:p@h.test\nEND:VCARD\n",
			[]VCard{{Name: "Priya Raman", Emails: []string{"p@h.test"}}}},
		"two addresses, a group prefix": {"BEGIN:VCARD\nFN:P\nitem1.EMAIL;type=INTERNET:a@h.test\nEMAIL;PREF=1:b@h.test\nEND:VCARD\n",
			[]VCard{{Name: "P", Emails: []string{"a@h.test", "b@h.test"}}}},
		"groups": {"BEGIN:VCARD\nFN:P\nEMAIL:p@h.test\nCATEGORIES:Team,Ops\\, night, \nEND:VCARD\n",
			[]VCard{{Name: "P", Emails: []string{"p@h.test"}, Groups: []string{"Team", "Ops, night"}}}},
		"2.1 quoted-printable, a soft break": {"BEGIN:VCARD\nVERSION:2.1\nFN;CHARSET=UTF-8;ENCODING=QUOTED-PRINTABLE:Ren=C3=A9e =\nDurand\nEMAIL;INTERNET:r@h.test\nEND:VCARD\n",
			[]VCard{{Name: "Renée Durand", Emails: []string{"r@h.test"}}}},
		"Latin-1": {"BEGIN:VCARD\nFN:Ren\xe9e\nEMAIL:r@h.test\nEND:VCARD\n",
			[]VCard{{Name: "Renée", Emails: []string{"r@h.test"}}}},
		"a quoted parameter": {"BEGIN:VCARD\nFN:P\nEMAIL;X-LABEL=\"home:main\":p@h.test\nEND:VCARD\n",
			[]VCard{{Name: "P", Emails: []string{"p@h.test"}}}},
		"mailto": {"BEGIN:VCARD\nFN:P\nEMAIL:mailto:p@h.test\nEND:VCARD\n",
			[]VCard{{Name: "P", Emails: []string{"p@h.test"}}}},
		"no address, and lines around": {"junk\nFN:outside\nBEGIN:VCARD\nno colon here\nFN:P\nEND:VCARD\nFN:after\n",
			[]VCard{{Name: "P"}}},
	} {
		got, err := ParseVCards([]byte(c.in))
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v, %v; want %+v", name, got, err, c.want)
		}
	}
	if _, err := ParseVCards([]byte("FN:no card\n")); err == nil || !strings.Contains(err.Error(), "no vCard") {
		t.Errorf("a file with no card: %v", err)
	}
}

// Written as 3.0, escaped, each line folded at 75 bytes without splitting a
// character, and read back as written.
func TestVCardsAreWritten(t *testing.T) {
	// Odd-length so a fold at 75 bytes would land inside a character.
	long := "x" + strings.Repeat("é", 40)
	cards := []VCard{
		{Name: "Raman, Priya; ops", Emails: []string{"p@h.test"}, Groups: []string{"Team", "Ops, night"}},
		{Emails: []string{"bare@h.test"}},
		{Name: long, Emails: []string{"l@h.test"}},
	}
	var b bytes.Buffer
	if err := WriteVCards(&b, cards); err != nil {
		t.Fatal(err)
	}
	want := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Raman\\, Priya\\; ops\r\nN:;Raman\\, Priya\\; ops;;;\r\nEMAIL;TYPE=INTERNET:p@h.test\r\nCATEGORIES:Team,Ops\\, night\r\nEND:VCARD\r\n" +
		"BEGIN:VCARD\r\nVERSION:3.0\r\nFN:bare@h.test\r\nN:;bare@h.test;;;\r\nEMAIL;TYPE=INTERNET:bare@h.test\r\nEND:VCARD\r\n" +
		"BEGIN:VCARD\r\nVERSION:3.0\r\nFN:x" + strings.Repeat("é", 35) + "\r\n " + strings.Repeat("é", 5) + "\r\nN:;x" + strings.Repeat("é", 35) + "\r\n " + strings.Repeat("é", 5) + ";;;\r\nEMAIL;TYPE=INTERNET:l@h.test\r\nEND:VCARD\r\n"
	if b.String() != want {
		t.Fatalf("written:\n%q\nwant:\n%q", b.String(), want)
	}
	back, err := ParseVCards(b.Bytes())
	cards[1].Name = "bare@h.test"
	if err != nil || !reflect.DeepEqual(back, cards) {
		t.Fatalf("read back %+v, %v", back, err)
	}
}

// An import saves each address, keeps a name a card does not give, counts
// what it passes over, and names the groups it could not make.
func TestContactsAreImported(t *testing.T) {
	s, ctx := newContactStore(t), context.Background()
	_ = s.AddContact(ctx, groupOwner, "kept@h.test", "Kept Name")
	res, err := s.ImportContacts(ctx, groupOwner, []VCard{
		{Name: "", Emails: []string{"KEPT@h.test"}, Groups: []string{"Team"}},
		{Name: "Priya", Emails: []string{"p@h.test", "not-an-address", groupOwner}, Groups: []string{"Team", "bad@name"}},
		{Name: "Nobody"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res, ContactImport{Saved: 2, Skipped: 3, GroupsLeftOut: []string{"bad@name"}}) {
		t.Fatalf("result %+v", res)
	}
	if got := contactNames(t, s, groupOwner); !reflect.DeepEqual(got, map[string]string{"kept@h.test": "Kept Name", "p@h.test": "Priya"}) {
		t.Fatalf("contacts %v", got)
	}
	if got := groupsOf(t, s, groupOwner); !reflect.DeepEqual(got, []ContactGroup{{Name: "Team", Members: []string{"kept@h.test", "p@h.test"}}}) {
		t.Fatalf("groups %+v", got)
	}
	// A card's name replaces the one kept.
	if _, err := s.ImportContacts(ctx, groupOwner, []VCard{{Name: "New Name", Emails: []string{"kept@h.test"}}}); err != nil || contactNames(t, s, groupOwner)["kept@h.test"] != "New Name" {
		t.Fatalf("a named card did not rename: %v %v", err, contactNames(t, s, groupOwner))
	}
}

// The caps hold through an import: no group past the mailbox's 50, no group
// past 500 people, and no file past 5000 cards.
func TestAnImportKeepsTheCaps(t *testing.T) {
	s, ctx := newContactStore(t), context.Background()
	for i := 0; i < maxContactGroups-1; i++ {
		_ = s.SaveContactGroup(ctx, groupOwner, "g"+strconv.Itoa(i), []string{"a@x.com"})
	}
	var full []string
	for i := 0; i < maxContactGroupMembers-1; i++ {
		full = append(full, "p"+strconv.Itoa(i)+"@x.com")
	}
	if err := s.SaveContactGroup(ctx, groupOwner, "Full", full); err != nil {
		t.Fatal(err)
	}
	res, err := s.ImportContacts(ctx, groupOwner, []VCard{
		{Emails: []string{"one@x.com"}, Groups: []string{"full", "Fifty-one"}},
		{Emails: []string{"two@x.com"}, Groups: []string{"Full", "G3"}},
	})
	if err != nil || !reflect.DeepEqual(res.GroupsLeftOut, []string{"Fifty-one", "Full"}) {
		t.Fatalf("left out %+v, %v", res, err)
	}
	for _, g := range groupsOf(t, s, groupOwner) {
		if strings.EqualFold(g.Name, "full") && len(g.Members) != maxContactGroupMembers {
			t.Fatalf("Full holds %d", len(g.Members))
		}
		if g.Name == "g3" && len(g.Members) != 2 {
			t.Fatalf("an existing group under the cap was not joined: %+v", g)
		}
	}
	if _, err := s.ImportContacts(ctx, groupOwner, make([]VCard, maxImportCards+1)); err == nil || !strings.Contains(err.Error(), "at most 5000") {
		t.Fatalf("5001 cards: %v", err)
	}
	if _, err := s.ImportContacts(ctx, groupOwner, make([]VCard, maxImportCards)); err != nil {
		t.Fatalf("5000 cards: %v", err)
	}
}

// The export is the address book as it stands: a card an address, with its
// groups.
func TestContactsAreExported(t *testing.T) {
	s, ctx := newContactStore(t), context.Background()
	_ = s.AddContact(ctx, groupOwner, "b@x.com", "Bea")
	_ = s.SaveContactGroup(ctx, groupOwner, "Team", []string{"a@x.com", "b@x.com"})
	_ = s.SaveContactGroup(ctx, groupOwner, "Ops", []string{"b@x.com"})
	got, err := s.ContactsAsVCards(ctx, groupOwner)
	want := []VCard{{Emails: []string{"a@x.com"}, Groups: []string{"Team"}}, {Name: "Bea", Emails: []string{"b@x.com"}, Groups: []string{"Ops", "Team"}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v, %v", got, err)
	}
}
