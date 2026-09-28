// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
)

// TestMessagesSurfaceRendersWithoutDB guards the contact inbox against a nil DB
// (worst-case startup): it must render the empty-state shell, not panic.
//
// "Without a DB" is set here, not assumed: the database is a package global,
// and a contact test that ran first left its message in it. Under -shuffle
// that made this test fail whenever the order put the two together.
func TestMessagesSurfaceRendersWithoutDB(t *testing.T) {
	prev := dbpkg.DB
	dbpkg.DB = nil
	t.Cleanup(func() { dbpkg.DB = prev })
	a := &App{}
	req := httptest.NewRequest("GET", "/os/messages", nil)
	rec := httptest.NewRecorder()

	a.handleOSMessages(rec, req) // must not panic

	if rec.Code != 200 {
		t.Fatalf("Messages status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "No messages yet") {
		t.Error("Messages surface should show the empty state without a DB")
	}
}

func seedMessage(t *testing.T, id, name string, read int, created time.Time) {
	t.Helper()
	if _, err := dbpkg.DB.Exec(`INSERT INTO contact_messages(id,name,email,message,page,is_read,created_at) VALUES(?,?,?,?,?,?,?)`,
		id, name, strings.ToLower(name)+"@readers.example", "A note from "+name, "/contact", read, created); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func messageRead(t *testing.T, id string) bool {
	t.Helper()
	var read int
	if err := dbpkg.DB.QueryRow(`SELECT is_read FROM contact_messages WHERE id=?`, id).Scan(&read); err != nil {
		t.Fatalf("read state of %s: %v", id, err)
	}
	return read != 0
}

// TestTheInboxIsAListAndOpeningAMessageReadsIt — the messages as rows, the
// newest shown first and left unread, a message opened by its address or by
// selecting it marked read, with the New count following it.
func TestTheInboxIsAListAndOpeningAMessageReadsIt(t *testing.T) {
	openMigratedDB(t)
	now := time.Now().UTC()
	seedMessage(t, "m-new", "Priya", 0, now.Add(-time.Hour))
	seedMessage(t, "m-old", "Owen", 0, now.Add(-2*time.Hour))
	seedMessage(t, "m-seen", "Sam", 1, now.Add(-3*time.Hour))
	a := &App{siteSettings: settings.New(dbpkg.DB)}
	page := func(target string) string {
		rec := httptest.NewRecorder()
		a.handleOSMessages(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec.Body.String()
	}
	row := func(id string) string {
		return `data-id="` + id + `" data-list-src="/os/messages/inspector/` + id + `" tabindex="0" aria-selected="`
	}

	body := page("/os/messages")
	if !strings.Contains(body, `data-page-kind="list"`) {
		t.Error("Messages does not say it is a list")
	}
	if n := strings.Count(body, "data-list-row"); n != 3 {
		t.Errorf("%d rows, want every message", n)
	}
	for _, want := range []string{row("m-new") + `true"`, `id="msg-unread">2</span>`, `href="/os/messages?unread=1"`, `id="istate-m-new"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if messageRead(t, "m-new") {
		t.Error("the message shown first on load was marked read; only opening one reads it")
	}

	if n := strings.Count(page("/os/messages?unread=1"), "data-list-row"); n != 2 {
		t.Errorf("the New view lists %d rows, want the two unread", n)
	}

	opened := page("/os/messages?open=m-old")
	if !strings.Contains(opened, row("m-old")+`true"`) || !strings.Contains(opened, row("m-new")+`false"`) {
		t.Error("?open= does not select the message it names")
	}
	if !messageRead(t, "m-old") {
		t.Error("a message opened by its address is still unread")
	}
	if !strings.Contains(opened, `id="msg-unread">1</span>`) {
		t.Error("the New count does not count the message just opened as read")
	}

	rec := httptest.NewRecorder()
	a.handleOSMessageInspector(rec, commentReq(http.MethodGet, "/os/messages/inspector/m-new", "m-new", ""))
	insp := rec.Body.String()
	if !messageRead(t, "m-new") {
		t.Error("selecting a message does not mark it read")
	}
	for _, want := range []string{`id="mstate-m-new" hx-swap-oob="true"`, `id="msg-unread" hx-swap-oob="true">0</span>`, "priya@readers.example"} {
		if !strings.Contains(insp, want) {
			t.Errorf("inspector: missing %q", want)
		}
	}

	rec = httptest.NewRecorder()
	a.handleOSMessageInspector(rec, commentReq(http.MethodGet, "/os/messages/inspector/m-seen", "m-seen", ""))
	if strings.Contains(rec.Body.String(), "hx-swap-oob") {
		t.Error("opening a message already read still rewrites its row and the count")
	}

	rec = httptest.NewRecorder()
	a.handleOSMessageInspector(rec, commentReq(http.MethodGet, "/os/messages/inspector/gone", "gone", ""))
	if rec.Code != http.StatusNotFound {
		t.Errorf("an unknown message: %d, want 404", rec.Code)
	}
}

// TestMessagesFilteredEmptyShowsToolbar proves that with an active search the
// page keeps the search box and says nothing matches, not the pristine
// "no messages yet" empty state.
func TestMessagesFilteredEmptyShowsToolbar(t *testing.T) {
	openMigratedDB(t)
	seedMessage(t, "m-1", "Priya", 0, time.Now().UTC())
	a := &App{siteSettings: settings.New(dbpkg.DB)}
	rec := httptest.NewRecorder()

	a.handleOSMessages(rec, httptest.NewRequest("GET", "/os/messages?q=alice", nil))

	body := rec.Body.String()
	if !strings.Contains(body, `name="q"`) {
		t.Error("filtered view should render the search box")
	}
	if !strings.Contains(body, "No message matches that") {
		t.Error("filtered view with no results should show the no-match state")
	}
	if strings.Contains(body, "No messages yet") {
		t.Error("filtered view must not show the pristine empty state")
	}
}

// TestAMessageLinkOpensTheInbox — a link to one message, from the bell or an
// email, opens the inbox with that message selected.
func TestAMessageLinkOpensTheInbox(t *testing.T) {
	a := &App{}
	rec := httptest.NewRecorder()

	a.handleOSMessageDetail(rec, commentReq(http.MethodGet, "/os/messages/a%20b", "a b", ""))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("detail status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/os/messages?open=a+b" {
		t.Errorf("Location = %q, want the inbox with the message open", loc)
	}
}

// TestMessagesCSVExportHeader proves the CSV export always emits the header row
// with the right content-type/disposition, even with no DB.
func TestMessagesCSVExportHeader(t *testing.T) {
	a := &App{}
	req := httptest.NewRequest("GET", "/os/api/messages/export.csv", nil)
	rec := httptest.NewRecorder()

	a.handleOSMessagesExportCSV(rec, req)

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("content-type = %q, want text/csv", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "contact-messages.csv") {
		t.Errorf("content-disposition = %q, want attachment filename", cd)
	}
	if !strings.Contains(rec.Body.String(), "created_at,name,email,page,country,region,city,read,message") {
		t.Errorf("CSV header row missing, got: %q", rec.Body.String())
	}
}
