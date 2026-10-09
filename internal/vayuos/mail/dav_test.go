// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
	"github.com/emersion/go-webdav/carddav"
)

const (
	davPass  = "app-password"
	davBook  = "/dav/card/" + groupOwner + "/books/contacts/"
	davCal   = "/dav/cal/" + groupOwner + "/calendars/calendar/"
	davVCard = "text/vcard"
)

type davFixture struct {
	t   *testing.T
	s   *AccountStore
	srv *httptest.Server
	hc  webdav.HTTPClient
}

// newDAV serves the CardDAV and CalDAV of one mailbox, groupOwner, whose
// password is davPass, over a store in a file as the server keeps it.
func newDAV(t *testing.T) *davFixture {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "dav.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := NewAccountStore(db)
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{accounts: s}
	srv := httptest.NewServer(e.DAVHandler(func(user, password string) (string, bool) {
		return groupOwner, user == groupOwner && password == davPass
	}))
	t.Cleanup(srv.Close)
	return &davFixture{t: t, s: s, srv: srv, hc: webdav.HTTPClientWithBasicAuth(srv.Client(), groupOwner, davPass)}
}

// req sends one request signed in as the mailbox, with header pairs, and
// answers the status, the headers and the body.
func (f *davFixture) req(method, p, body string, header ...string) (int, http.Header, string) {
	f.t.Helper()
	r, err := http.NewRequest(method, f.srv.URL+p, strings.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	r.SetBasicAuth(groupOwner, davPass)
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	resp, err := f.srv.Client().Do(r)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(b)
}

// putCard writes a card as a phone does and answers the status and ETag.
func (f *davFixture) putCard(name, card string, header ...string) (int, string) {
	f.t.Helper()
	code, h, _ := f.req(http.MethodPut, davBook+name, card, append([]string{"Content-Type", davVCard}, header...)...)
	return code, h.Get("ETag")
}

func vcardText(uid, fn string, lines ...string) string {
	return "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:" + uid + "\r\nFN:" + fn + "\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VCARD\r\n"
}

func (f *davFixture) cards() map[string]*carddav.AddressObject {
	f.t.Helper()
	c, err := carddav.NewClient(f.hc, f.srv.URL)
	if err != nil {
		f.t.Fatal(err)
	}
	list, err := c.QueryAddressBook(context.Background(), davBook, &carddav.AddressBookQuery{DataRequest: carddav.AddressDataRequest{AllProp: true}})
	if err != nil {
		f.t.Fatal(err)
	}
	out := map[string]*carddav.AddressObject{}
	for i := range list {
		out[strings.TrimPrefix(list[i].Path, davBook)] = &list[i]
	}
	return out
}

// An app finds the address book and the calendar from the server's name.
func TestAnAppFindsTheAddressBookAndCalendar(t *testing.T) {
	f := newDAV(t)
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for wk, want := range map[string]string{"/.well-known/carddav": "/dav/card/", "/.well-known/caldav": "/dav/cal/"} {
		resp, err := noFollow.Get(f.srv.URL + wk)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != want {
			t.Errorf("%s: %d to %q", wk, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	ctx := context.Background()
	cc, _ := carddav.NewClient(f.hc, f.srv.URL+"/dav/card/")
	principal, err := cc.FindCurrentUserPrincipal(ctx)
	if err != nil || principal != "/dav/card/"+groupOwner+"/" {
		t.Fatalf("card principal %q, %v", principal, err)
	}
	home, err := cc.FindAddressBookHomeSet(ctx, principal)
	if err != nil {
		t.Fatal(err)
	}
	books, err := cc.FindAddressBooks(ctx, home)
	if err != nil || len(books) != 1 || books[0].Path != davBook || books[0].Name != "Contacts" || books[0].MaxResourceSize != maxDAVObject {
		t.Fatalf("books %+v, %v", books, err)
	}
	kc, _ := caldav.NewClient(f.hc, f.srv.URL+"/dav/cal/")
	principal, err = kc.FindCurrentUserPrincipal(ctx)
	if err != nil || principal != "/dav/cal/"+groupOwner+"/" {
		t.Fatalf("calendar principal %q, %v", principal, err)
	}
	home, err = kc.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		t.Fatal(err)
	}
	cals, err := kc.FindCalendars(ctx, home)
	if err != nil || len(cals) != 1 || cals[0].Path != davCal || !reflect.DeepEqual(cals[0].SupportedComponentSet, []string{"VEVENT", "VTODO"}) {
		t.Fatalf("calendars %+v, %v", cals, err)
	}
}

// The console's contacts are the cards an app sees, and a change on either
// side reaches the other.
func TestTheAddressBookIsTheConsolesContacts(t *testing.T) {
	f := newDAV(t)
	ctx := context.Background()
	if err := f.s.AddContact(ctx, groupOwner, "bea@x.test", "Bea"); err != nil {
		t.Fatal(err)
	}
	cards := f.cards()
	if len(cards) != 1 {
		t.Fatalf("cards %v", cards)
	}
	var made string
	for name, c := range cards {
		made = name
		if upper := strings.ToUpper(strings.TrimSuffix(name, ".vcf")) + ".vcf"; true {
			if code, _, _ := f.req(http.MethodGet, davBook+strings.Replace(upper, "VP-", "vp-", 1), ""); code != http.StatusNotFound {
				t.Fatalf("another spelling of a made card's name reached it: %d", code)
			}
		}
		if c.Card.PreferredValue("FN") != "Bea" || c.Card.Value("EMAIL") != "bea@x.test" {
			t.Fatalf("the made card: %v", c.Card)
		}
	}

	// A phone adds a number to the console's contact.
	code, _ := f.putCard(made, vcardText(strings.TrimSuffix(made, ".vcf"), "Bea", "EMAIL:bea@x.test", "TEL:+44 20 7946 0000"), "If-Match", `"`+cards[made].ETag+`"`)
	if code != http.StatusCreated {
		t.Fatalf("writing over a made card: %d", code)
	}
	if c := f.cards(); len(c) != 1 || c[made].Card.Value("TEL") != "+44 20 7946 0000" {
		t.Fatalf("after the phone's edit: %v", c)
	}

	// A phone's own card brings its addresses in under its name.
	if code, _ := f.putCard("u1.vcf", vcardText("u1", "Priya Raman", "N:Raman;Priya;;;", "EMAIL:p@h.test", "item1.EMAIL;TYPE=WORK:mailto:Priya@Work.test", "TEL:+91 1", "EMAIL:"+groupOwner, "EMAIL:not-an-address")); code != http.StatusCreated {
		t.Fatalf("a new card: %d", code)
	}
	if got := contactNames(t, f.s, groupOwner); !reflect.DeepEqual(got, map[string]string{"bea@x.test": "Bea", "p@h.test": "Priya Raman", "priya@work.test": "Priya Raman"}) {
		t.Fatalf("contacts %v", got)
	}

	// The console renames one address: the card, and so its other address,
	// take the name; what the console has no field for stays.
	if err := f.s.AddContact(ctx, groupOwner, "p@h.test", "Priya R"); err != nil {
		t.Fatal(err)
	}
	u1 := f.cards()["u1.vcf"].Card
	if u1.PreferredValue("FN") != "Priya R" || u1.Name().GivenName != "Priya R" || u1.Name().FamilyName != "" || u1.Value("TEL") != "+91 1" {
		t.Fatalf("renamed card %v", u1)
	}
	if got := contactNames(t, f.s, groupOwner)["priya@work.test"]; got != "Priya R" {
		t.Fatalf("the card's other address is named %q", got)
	}
	// An import names a card the same way, and a card it gives no name
	// leaves the card's name as it is.
	if _, err := f.s.ImportContacts(ctx, groupOwner, []VCard{{Name: "Priya Raman", Emails: []string{"p@h.test"}}, {Emails: []string{"priya@work.test"}}}); err != nil {
		t.Fatal(err)
	}
	if fn := f.cards()["u1.vcf"].Card.PreferredValue("FN"); fn != "Priya Raman" {
		t.Fatalf("after an import the card is named %q", fn)
	}
	// A card held at another name hides the made card the contact had
	// before it: the address is reached once.
	if _, err := f.s.davGet(ctx, groupOwner, davCard, madeCardName("p@h.test", time.Now())); err == nil {
		t.Fatal("a made card is offered for an address a card holds")
	}

	// A card that gives no name leaves its addresses' names alone.
	f.putCard("nameless.vcf", "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:nameless\r\nEMAIL:bea@x.test\r\nEND:VCARD\r\n")
	if got := contactNames(t, f.s, groupOwner)["bea@x.test"]; got != "Bea" {
		t.Fatalf("a nameless card renamed its address to %q", got)
	}
	f.req(http.MethodDelete, davBook+"nameless.vcf", "")

	// The console deletes one address, then the other: the card loses the
	// first and goes with the last.
	if err := f.s.DeleteContact(ctx, groupOwner, "priya@work.test"); err != nil {
		t.Fatal(err)
	}
	if u1 := f.cards()["u1.vcf"]; u1 == nil || len(u1.Card["EMAIL"]) != 3 || u1.Card.Value("TEL") != "+91 1" {
		t.Fatalf("after one address went: %v", u1)
	}
	if err := f.s.DeleteContact(ctx, groupOwner, "p@h.test"); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.cards()["u1.vcf"]; ok {
		t.Fatal("a card left with no address of a contact outlived it")
	}
	// The phone deletes the console's contact, one card it wrote over and
	// one it never did.
	_ = f.s.AddContact(ctx, groupOwner, "cy@x.test", "Cy")
	for name := range f.cards() {
		if code, _, _ := f.req(http.MethodDelete, davBook+name, ""); code != http.StatusNoContent {
			t.Fatalf("delete %s: %d", name, code)
		}
	}
	if got := contactNames(t, f.s, groupOwner); len(got) != 0 {
		t.Fatalf("contacts after the phone deleted them: %v", got)
	}
}

// What a card no longer holds stops being a contact, and leaves its groups,
// unless another card still holds it.
func TestACardsAddressesFollowTheCard(t *testing.T) {
	f := newDAV(t)
	ctx := context.Background()
	f.putCard("a.vcf", vcardText("a", "Sam", "EMAIL:sam@x.test", "EMAIL:old@x.test"))
	f.putCard("b.vcf", vcardText("b", "Sam at work", "EMAIL:sam@x.test"))
	if err := f.s.SaveContactGroup(ctx, groupOwner, "Team", []string{"old@x.test", "sam@x.test"}); err != nil {
		t.Fatal(err)
	}
	f.putCard("a.vcf", vcardText("a", "Sam", "EMAIL:sam@x.test", "EMAIL:new@x.test"))
	if got := contactNames(t, f.s, groupOwner); !reflect.DeepEqual(got, map[string]string{"sam@x.test": "Sam", "new@x.test": "Sam"}) {
		t.Fatalf("contacts %v", got)
	}
	if g := groupsOf(t, f.s, groupOwner); len(g) != 1 || !reflect.DeepEqual(g[0].Members, []string{"sam@x.test"}) {
		t.Fatalf("groups %+v", g)
	}
	f.req(http.MethodDelete, davBook+"a.vcf", "")
	if got := contactNames(t, f.s, groupOwner); !reflect.DeepEqual(got, map[string]string{"sam@x.test": "Sam"}) {
		t.Fatalf("an address card b still holds went with card a: %v", got)
	}
	f.req(http.MethodDelete, davBook+"b.vcf", "")
	if got := contactNames(t, f.s, groupOwner); len(got) != 0 {
		t.Fatalf("contacts %v", got)
	}
	// A contact saved again after an app wrote another person over its made
	// card is offered under a new name, not hidden behind that card.
	_ = f.s.AddContact(ctx, groupOwner, "re@x.test", "Re")
	var made string
	for n := range f.cards() {
		made = n
	}
	f.putCard(made, vcardText("other", "Someone else", "EMAIL:else@x.test"))
	if c := f.cards(); len(c) != 1 {
		t.Fatalf("a made card is offered for an address a card holds: %v", c)
	}
	if _, ok := contactNames(t, f.s, groupOwner)["re@x.test"]; ok {
		t.Fatal("an address the card no longer holds stayed a contact")
	}
	_ = f.s.AddContact(ctx, groupOwner, "re@x.test", "Re")
	// Saved again within the same second, its card's name would be the one
	// the app took: it is not offered twice under one name.
	stem := strings.TrimSuffix(made, ".vcf")
	at, _ := strconv.ParseInt(stem[strings.LastIndex(stem, "-")+1:], 36, 64)
	if _, err := f.s.db.Exec(`UPDATE vayumail_contacts SET created_at=? WHERE email='re@x.test'`, time.Unix(at, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if c := f.cards(); len(c) != 1 || c[made].Card.Value("EMAIL") != "else@x.test" {
		t.Fatalf("a made card under a name an app holds: %v", c)
	}
	if _, err := f.s.db.Exec(`UPDATE vayumail_contacts SET created_at=? WHERE email='re@x.test'`, time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatal(err)
	}
	if c := f.cards(); len(c) != 2 || c[made].Card.Value("EMAIL") != "else@x.test" {
		t.Fatalf("the contact saved again: %v", c)
	}
}

// Only the mailbox signed in is reached, and only by its own sign-in.
func TestOnlyTheSignedInMailboxIsReached(t *testing.T) {
	f := newDAV(t)
	for name, c := range map[string]struct{ user, password string }{"no sign-in": {}, "a wrong password": {groupOwner, "nope"}} {
		r, _ := http.NewRequest("PROPFIND", f.srv.URL+"/dav/card/", nil)
		if c.user != "" {
			r.SetBasicAuth(c.user, c.password)
		}
		resp, err := f.srv.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Basic ") {
			t.Errorf("%s: %d %q", name, resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
		}
	}
	other := "/dav/card/someone@johal.in/books/contacts/"
	propfind := `<?xml version="1.0"?><D:propfind xmlns:D="DAV:"><D:prop><D:getetag/></D:prop></D:propfind>`
	if code, _, _ := f.req("PROPFIND", other, propfind, "Depth", "1", "Content-Type", "application/xml"); code != http.StatusNotFound {
		t.Errorf("another mailbox's book: %d", code)
	}
	// A REPORT names cards by href in its body, past the address check: the
	// store still finds only this mailbox's.
	f.putCard("mine.vcf", vcardText("mine", "M", "EMAIL:m@x.test"))
	body := `<?xml version="1.0"?><C:addressbook-multiget xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav"><D:prop><D:getetag/><C:address-data/></D:prop>` +
		`<D:href>` + other + `mine.vcf</D:href><D:href>` + davBook + `mine.vcf</D:href></C:addressbook-multiget>`
	code, _, out := f.req("REPORT", davBook, body, "Content-Type", "application/xml")
	if code != http.StatusMultiStatus || strings.Count(out, "EMAIL:m@x.test") != 1 || !strings.Contains(out, "404") {
		t.Errorf("multiget across mailboxes: %d\n%s", code, out)
	}
	// One address book and one calendar: none made, none deleted.
	for _, c := range []struct{ method, path string }{
		{"MKCOL", "/dav/card/" + groupOwner + "/books/other/"},
		{http.MethodDelete, davBook},
		{"MKCALENDAR", "/dav/cal/" + groupOwner + "/calendars/other/"},
		{http.MethodDelete, davCal},
	} {
		if code, _, _ := f.req(c.method, c.path, ""); code != http.StatusForbidden {
			t.Errorf("%s %s: %d", c.method, c.path, code)
		}
	}
	for name, entry := range map[string]string{"a space": "a%20b.vcf", "a leading dot": ".hidden.vcf", "513 characters": strings.Repeat("n", maxDAVName+1)} {
		if code, _ := f.putCard(entry, vcardText("x", "X", "EMAIL:x@x.test")); code != http.StatusNotFound {
			t.Errorf("a name with %s: %d", name, code)
		}
	}
	if code, _ := f.putCard(strings.Repeat("n", maxDAVName), vcardText("long", "L", "EMAIL:l@x.test")); code != http.StatusCreated {
		t.Errorf("a name of %d characters: %d", maxDAVName, code)
	}
	query := `<?xml version="1.0"?><C:addressbook-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav"><D:prop><D:getetag/></D:prop></C:addressbook-query>`
	if code, _, out := f.req("REPORT", other, query, "Content-Type", "application/xml"); code != http.StatusNotFound {
		t.Errorf("a query of another mailbox's book: %d\n%s", code, out)
	}
}

// A write that names what it expects to replace is refused when that is not
// what is there.
func TestWritesHoldToTheirConditions(t *testing.T) {
	f := newDAV(t)
	card := vcardText("c", "C", "EMAIL:c@x.test")
	code, tag := f.putCard("c.vcf", card, "If-None-Match", "*")
	if code != http.StatusCreated || tag == "" {
		t.Fatalf("a new card: %d %q", code, tag)
	}
	for name, hdr := range map[string][]string{
		"If-None-Match: * over a card":    {"If-None-Match", "*"},
		"If-None-Match: its tag":          {"If-None-Match", tag},
		"If-Match: another tag":           {"If-Match", `"0"`},
		"If-Match: a tag that is not one": {"If-Match", "unquoted"},
	} {
		if code, _ := f.putCard("c.vcf", card, hdr...); code != http.StatusPreconditionFailed {
			t.Errorf("%s: %d", name, code)
		}
	}
	if code, _ := f.putCard("none.vcf", vcardText("n", "N", "EMAIL:n@x.test"), "If-Match", "*"); code != http.StatusPreconditionFailed {
		t.Errorf("If-Match: * where there is nothing: %d", code)
	}
	if code, _, _ := f.req(http.MethodDelete, davBook+"c.vcf", "", "If-Match", `"0"`); code != http.StatusPreconditionFailed {
		t.Errorf("a delete of a changed card: %d", code)
	}
	code, tag2 := f.putCard("c.vcf", vcardText("c", "C2", "EMAIL:c@x.test"), "If-Match", tag)
	if code != http.StatusCreated || tag2 == tag {
		t.Fatalf("a write over its own tag: %d %q", code, tag2)
	}
	if code, _, _ := f.req(http.MethodDelete, davBook+"c.vcf", "", "If-Match", tag2); code != http.StatusNoContent {
		t.Fatalf("a delete over its own tag: %d", code)
	}
	if code, _, _ := f.req(http.MethodDelete, davBook+"c.vcf", ""); code != http.StatusNotFound {
		t.Fatalf("a delete of nothing: %d", code)
	}
}

// What a mailbox keeps is bounded: an entry's size, its UID, how many
// addresses a card brings in, how many entries, and how much in all.
func TestWhatAMailboxKeepsIsBounded(t *testing.T) {
	f := newDAV(t)
	f.putCard("a.vcf", vcardText("same", "A", "EMAIL:a@x.test"))
	code, _, body := f.req(http.MethodPut, davBook+"b.vcf", vcardText("same", "B", "EMAIL:b@x.test"), "Content-Type", davVCard)
	if code != http.StatusConflict || !strings.Contains(body, "no-uid-conflict") {
		t.Errorf("a second card with one UID: %d %s", code, body)
	}
	photo := "PHOTO;ENCODING=b;TYPE=JPEG:" + strings.Repeat("A", maxDAVObject)
	code, _, body = f.req(http.MethodPut, davBook+"big.vcf", vcardText("big", "Big", "EMAIL:big@x.test", photo), "Content-Type", davVCard)
	if code != http.StatusConflict || !strings.Contains(body, "max-resource-size") {
		t.Errorf("a card past a megabyte: %d %s", code, body)
	}
	if code, _ := f.putCard("huge.vcf", vcardText("huge", "Huge", strings.Repeat("A", maxDAVRequest))); code != http.StatusBadRequest {
		t.Errorf("a request past its bound: %d", code)
	}
	var many []string
	for i := 0; i < maxCardEmails+10; i++ {
		many = append(many, "EMAIL:p"+strconv.Itoa(i)+"@list.test")
	}
	f.putCard("many.vcf", vcardText("many", "Many", many...))
	if n := len(contactNames(t, f.s, groupOwner)); n != 1+maxCardEmails {
		t.Errorf("a card of %d addresses made %d contacts", len(many), n)
	}
	for _, name := range []string{"big.vcf", "huge.vcf"} {
		if code, _, _ := f.req(http.MethodGet, davBook+name, ""); code != http.StatusNotFound {
			t.Errorf("%s was kept: %d", name, code)
		}
	}

	// Entries: the cap counts per kind, and an entry written over is not a
	// new one.
	tx, _ := f.s.db.Begin()
	for i := 2; i < maxDAVObjects; i++ {
		_, _ = tx.Exec(`INSERT INTO vayumail_dav(mailbox,kind,name,uid,etag,data,modified) VALUES(?,?,?,?,?,?,?)`, groupOwner, davCard, "f"+strconv.Itoa(i), "f"+strconv.Itoa(i), "t", []byte("x"), time.Now())
	}
	_ = tx.Commit()
	if code, _ := f.putCard("one-more.vcf", vcardText("om", "OM", "EMAIL:om@x.test")); code != http.StatusInsufficientStorage {
		t.Errorf("entry %d: %d", maxDAVObjects+1, code)
	}
	if code, _ := f.putCard("a.vcf", vcardText("same", "A2", "EMAIL:a@x.test")); code != http.StatusCreated {
		t.Errorf("a card written over at the cap: %d", code)
	}
	if code, _, _ := f.req(http.MethodPut, davCal+"e.ics", eventText("e", "VEVENT", ""), "Content-Type", "text/calendar"); code != http.StatusCreated {
		t.Errorf("an event while cards are at their cap: %d", code)
	}
	// Bytes: events and cards together.
	if _, err := f.s.db.Exec(`UPDATE vayumail_dav SET data=zeroblob(?) WHERE mailbox=? AND kind=? AND name='e.ics'`, maxDAVBytes-2*maxDAVObjects, groupOwner, davEvent); err != nil {
		t.Fatal(err)
	}
	if code, _ := f.putCard("a.vcf", vcardText("same", "A3", "EMAIL:a@x.test", "NOTE:"+strings.Repeat("n", maxDAVObjects))); code != http.StatusInsufficientStorage {
		t.Errorf("past %d bytes in all: %d", maxDAVBytes, code)
	}
}

func eventText(uid, comp, extra string) string {
	uidLine := ""
	if uid != "" {
		uidLine = "UID:" + uid + "\r\n"
	}
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\n" + extra +
		"BEGIN:" + comp + "\r\n" + uidLine + "DTSTAMP:20261001T090000Z\r\nDTSTART:20261010T100000Z\r\nDTEND:20261010T110000Z\r\nSUMMARY:Standup\r\nEND:" + comp + "\r\nEND:VCALENDAR\r\n"
}

// The calendar keeps events and tasks as written, found by date, one per
// UID; anything else is refused with the reason CalDAV names.
func TestTheCalendarKeepsEventsAndTasks(t *testing.T) {
	f := newDAV(t)
	put := func(name, body string) (int, string) {
		code, _, out := f.req(http.MethodPut, davCal+name, body, "Content-Type", "text/calendar")
		return code, out
	}
	for name, body := range map[string]string{"standup.ics": eventText("ev1", "VEVENT", ""), "task.ics": eventText("td1", "VTODO", "")} {
		if code, out := put(name, body); code != http.StatusCreated {
			t.Fatalf("%s: %d %s", name, code, out)
		}
	}
	for name, c := range map[string]struct{ body, reason string }{
		"a journal":           {eventText("j1", "VJOURNAL", ""), "supported-calendar-component"},
		"no UID":              {eventText("", "VEVENT", ""), "valid-calendar-object-resource"},
		"a METHOD":            {eventText("m1", "VEVENT", "METHOD:REQUEST\r\n"), "valid-calendar-object-resource"},
		"another entry's UID": {eventText("ev1", "VEVENT", ""), "no-uid-conflict"},
	} {
		if code, out := put("x.ics", c.body); code != http.StatusConflict || !strings.Contains(out, c.reason) {
			t.Errorf("%s: %d %s", name, code, out)
		}
	}
	// A card may share a UID with an event: the rule is per kind.
	if code, _ := f.putCard("ev1.vcf", vcardText("ev1", "E", "EMAIL:e@x.test")); code != http.StatusCreated {
		t.Errorf("a card with an event's UID: %d", code)
	}
	kc, _ := caldav.NewClient(f.hc, f.srv.URL)
	find := func(start, end time.Time) []string {
		got, err := kc.QueryCalendar(context.Background(), davCal, &caldav.CalendarQuery{
			CompRequest: caldav.CalendarCompRequest{Name: "VCALENDAR", AllProps: true, AllComps: true},
			CompFilter:  caldav.CompFilter{Name: "VCALENDAR", Comps: []caldav.CompFilter{{Name: "VEVENT", Start: start, End: end}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, o := range got {
			names = append(names, strings.TrimPrefix(o.Path, davCal))
		}
		sort.Strings(names)
		return names
	}
	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	if got := find(day, day.AddDate(0, 0, 1)); !reflect.DeepEqual(got, []string{"standup.ics"}) {
		t.Errorf("events on the day: %v", got)
	}
	if got := find(day.AddDate(0, 1, 0), day.AddDate(0, 2, 0)); len(got) != 0 {
		t.Errorf("events a month on: %v", got)
	}
	o, err := kc.GetCalendarObject(context.Background(), davCal+"standup.ics")
	if err != nil || o.Data.Children[0].Props.Get("SUMMARY").Value != "Standup" {
		t.Fatalf("read back %+v, %v", o, err)
	}
	if got := contactNames(t, f.s, groupOwner); !reflect.DeepEqual(got, map[string]string{"e@x.test": "E"}) {
		t.Errorf("events touched the contacts: %v", got)
	}
}

// Deleting the mailbox deletes what its apps kept.
func TestCardsAndEventsGoWithTheirMailbox(t *testing.T) {
	f := newDAV(t)
	ctx := context.Background()
	if err := f.s.Create(ctx, groupOwner, "x", "A", RoleMailbox); err != nil {
		t.Fatal(err)
	}
	f.putCard("a.vcf", vcardText("a", "A", "EMAIL:a@x.test"))
	f.req(http.MethodPut, davCal+"e.ics", eventText("e", "VEVENT", ""), "Content-Type", "text/calendar")
	if err := f.s.Delete(ctx, groupOwner); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"vayumail_dav", "vayumail_dav_emails"} {
		var n int
		_ = f.s.db.QueryRow(`SELECT COUNT(1) FROM ` + table).Scan(&n)
		if n != 0 {
			t.Errorf("%s keeps %d rows", table, n)
		}
	}
}
