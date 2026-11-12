// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/apikeys"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

var mailToolNames = []string{"search_mail", "read_message", "list_threads", "draft_reply", "create_draft", "file_message",
	"list_events", "find_free_time", "create_event", "update_event", "cancel_event"}

// mailMCP calls a tool as the key, on a's server.
func mailMCP(a *App, ki apikeys.KeyInfo, tool string, args map[string]any) (string, bool) {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	rec := httptest.NewRecorder()
	a.buildMCPServer().ServeHTTP(rec, keyCtxRequest(string(b), ki))
	var out struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Error != nil {
		return out.Error.Message, false
	}
	text := ""
	if len(out.Result.Content) > 0 {
		text = out.Result.Content[0].Text
	}
	return text, !out.Result.IsError
}

func mailToolsVisible(a *App, ki apikeys.KeyInfo) map[string]bool {
	rec := httptest.NewRecorder()
	a.buildMCPServer().ServeHTTP(rec, keyCtxRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, ki))
	var out struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	seen := map[string]bool{}
	for _, t := range out.Result.Tools {
		for _, m := range mailToolNames {
			if t.Name == m {
				seen[m] = true
			}
		}
	}
	return seen
}

func connectedKey(t *testing.T, a *App, id string, scopes ...string) apikeys.KeyInfo {
	t.Helper()
	if err := a.vayuMail.Connect("dana@example.com", id, "Helper", scopes); err != nil {
		t.Fatal(err)
	}
	return apikeys.KeyInfo{ID: id, Label: "Helper (mailbox)", Scope: apikeys.ScopeExternal, Perms: apikeys.NewPermissions()}
}

// TestOnlyAHoldersConnectionSeesTheMailTools — a superuser key, a key with
// every site grant and no connection: none; a connection to read: the
// reading tools; one granted all three: all of them.
func TestOnlyAHoldersConnectionSeesTheMailTools(t *testing.T) {
	a := calendarApp(t)
	if got := mailToolsVisible(a, apikeys.SuperuserKeyInfo("root", "root", apikeys.ScopeExternal)); len(got) != 0 {
		t.Errorf("a superuser key sees %v", got)
	}
	perms := apikeys.NewPermissions()
	for _, s := range apikeys.AllSections {
		perms.Grant(s, apikeys.ActionAll)
	}
	if got := mailToolsVisible(a, apikeys.KeyInfo{ID: "every", Perms: perms}); len(got) != 0 {
		t.Errorf("a key granted every section sees %v", got)
	}
	reader := connectedKey(t, a, "kr", vmail.ScopeRead)
	got := mailToolsVisible(a, reader)
	for _, n := range []string{"search_mail", "read_message", "list_threads", "list_events", "find_free_time"} {
		if !got[n] {
			t.Errorf("a reading connection does not see %s", n)
		}
	}
	for _, n := range []string{"draft_reply", "create_draft", "file_message", "create_event", "update_event", "cancel_event"} {
		if got[n] {
			t.Errorf("a reading connection sees %s", n)
		}
		if _, ok := mailMCP(a, reader, n, map[string]any{}); ok {
			t.Errorf("a reading connection called %s", n)
		}
	}
	all := connectedKey(t, a, "ka", vmail.ScopeRead, vmail.ScopeDraft, vmail.ScopeCalendar)
	if got := mailToolsVisible(a, all); len(got) != len(mailToolNames) {
		t.Errorf("a full connection sees %d of %d", len(got), len(mailToolNames))
	}
	// Disconnected: nothing, at once.
	if err := a.vayuMail.Disconnect(vmail.ReadAsOwner("dana"), "ka"); err != nil {
		t.Fatal(err)
	}
	if got := mailToolsVisible(a, all); len(got) != 0 {
		t.Errorf("a disconnected key sees %v", got)
	}
}

// TestTheMailToolsReadDraftAndFileButNeverSend — a message found, read,
// answered as a draft to its sender, filed; nothing goes to Trash or out;
// each call in the holder's record.
func TestTheMailToolsReadDraftAndFileButNeverSend(t *testing.T) {
	a := calendarApp(t)
	raw := "From: Eli <eli@example.org>\r\nTo: dana@example.com\r\nSubject: Lunch on Thursday\r\nMessage-Id: <lunch@example.org>\r\nDate: Thu, 12 Nov 2026 10:00:00 +0000\r\n\r\nShall we meet at noon?\r\n"
	if _, err := a.vayuMail.DeliverInbound("eli@example.org", "dana@example.com", []byte(raw)); err != nil {
		t.Fatal(err)
	}
	ki := connectedKey(t, a, "k1", vmail.ScopeRead, vmail.ScopeDraft)
	found, ok := mailMCP(a, ki, "search_mail", map[string]any{"query": "lunch"})
	var hits []mailListed
	if !ok || json.Unmarshal([]byte(found), &hits) != nil || len(hits) != 1 {
		t.Fatalf("search: %v %s", ok, found)
	}
	msg, ok := mailMCP(a, ki, "read_message", map[string]any{"folder": hits[0].Folder, "id": hits[0].ID})
	if !ok || !strings.Contains(msg, "Shall we meet at noon?") {
		t.Fatalf("read: %v %s", ok, msg)
	}
	threads, ok := mailMCP(a, ki, "list_threads", map[string]any{})
	if !ok || !strings.Contains(threads, `"subject":"Lunch on Thursday"`) {
		t.Fatalf("threads: %v %s", ok, threads)
	}
	reply, ok := mailMCP(a, ki, "draft_reply", map[string]any{"folder": hits[0].Folder, "id": hits[0].ID, "body": "Noon is good."})
	if !ok || !strings.Contains(reply, `"to":"eli@example.org"`) || !strings.Contains(reply, `"subject":"Re: Lunch on Thursday"`) {
		t.Fatalf("reply: %v %s", ok, reply)
	}
	// To everyone it went to, but never back to the holder.
	if all, ok := mailMCP(a, ki, "draft_reply", map[string]any{"folder": hits[0].Folder, "id": hits[0].ID, "body": "Noon.", "reply_all": true}); !ok || strings.Contains(all, "dana@example.com") {
		t.Errorf("reply to all: %v %s", ok, all)
	}
	dana := vmail.ReadAsOwner("dana")
	drafts, _ := a.vayuMail.ListFolder(dana, "Drafts")
	if len(drafts) != 2 {
		t.Fatalf("%d drafts", len(drafts))
	}
	var body []byte
	for _, d := range drafts {
		if b, _ := a.vayuMail.ReadFolderMessage(dana, "Drafts", d.ID); strings.Contains(string(b), "Noon is good.") {
			body = b
		}
	}
	if !strings.Contains(string(body), "In-Reply-To: <lunch@example.org>") || !strings.Contains(string(body), "X-Drafted-By: Helper") {
		t.Errorf("the draft:\n%s", body)
	}
	// Sending files a copy in Sent; nothing is there.
	if sent, _ := a.vayuMail.ListFolder(dana, "Sent"); len(sent) != 0 {
		t.Errorf("%d messages sent", len(sent))
	}
	if refused, ok := mailMCP(a, ki, "file_message", map[string]any{"folder": hits[0].Folder, "id": hits[0].ID, "move_to": "Trash"}); ok || !strings.Contains(refused, "Trash") {
		t.Errorf("a move to Trash: %v %s", ok, refused)
	}
	if _, ok := mailMCP(a, ki, "file_message", map[string]any{"folder": hits[0].Folder, "id": hits[0].ID, "move_to": "Archive"}); !ok {
		t.Error("filed to Archive: refused")
	}
	calls, _ := a.vayuMail.ToolCalls(dana, 50)
	var said []string
	for _, c := range calls {
		said = append(said, c.Tool+": "+c.What)
	}
	record := strings.Join(said, "\n")
	for _, want := range []string{`search_mail: searched for "lunch": 1 found`, "read_message: read: Lunch on Thursday", "draft_reply: drafted a reply to eli@example.org: Re: Lunch on Thursday",
		"file_message: refused: deleting is yours to do", "file_message: moved a message from Inbox to Archive"} {
		if !strings.Contains(record, want) {
			t.Errorf("the record has no %q:\n%s", want, record)
		}
	}
}

// TestTheCalendarToolsLeaveEventsWithGuestsAlone — made without guests,
// changed and cancelled while it has none; one with guests is refused,
// since a change mails them.
func TestTheCalendarToolsLeaveEventsWithGuestsAlone(t *testing.T) {
	a := calendarApp(t)
	ki := connectedKey(t, a, "k1", vmail.ScopeRead, vmail.ScopeCalendar)
	made, ok := mailMCP(a, ki, "create_event", map[string]any{"summary": "Focus", "start": "2026-11-20T09:00", "end": "2026-11-20T11:00", "zone": "Asia/Kolkata"})
	var ev struct{ UID string }
	if !ok || json.Unmarshal([]byte(made), &ev) != nil || ev.UID == "" {
		t.Fatalf("create: %v %s", ok, made)
	}
	listed, ok := mailMCP(a, ki, "list_events", map[string]any{"from": "2026-11-20", "to": "2026-11-21", "zone": "Asia/Kolkata"})
	if !ok || !strings.Contains(listed, `"summary":"Focus"`) || !strings.Contains(listed, "2026-11-20T09:00:00+05:30") {
		t.Fatalf("list: %v %s", ok, listed)
	}
	free, ok := mailMCP(a, ki, "find_free_time", map[string]any{"from": "2026-11-20T08:00", "to": "2026-11-20T12:00", "minutes": 30, "zone": "Asia/Kolkata"})
	if !ok || !strings.Contains(free, `"start":"2026-11-20T08:00:00+05:30","end":"2026-11-20T09:00:00+05:30"`) || !strings.Contains(free, `"start":"2026-11-20T11:00:00+05:30"`) {
		t.Fatalf("free: %v %s", ok, free)
	}
	if _, ok := mailMCP(a, ki, "update_event", map[string]any{"uid": ev.UID, "summary": "Deep focus", "start": "2026-11-20T10:00", "end": "2026-11-20T12:00", "zone": "Asia/Kolkata"}); !ok {
		t.Fatal("update refused")
	}
	// With a guest, from the calendar: the tools leave it.
	dana := vmail.ReadAsOwner("dana")
	loc, _ := time.LoadLocation("Asia/Kolkata")
	start := time.Date(2026, 11, 21, 9, 0, 0, 0, loc)
	uid, err := a.vayuMail.SaveEvent(dana, vmail.EventInput{Summary: "With Eli", Start: start, End: start.Add(time.Hour), Guests: []string{"eli@example.org"}}, vmail.ScopeAll, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"update_event", "cancel_event"} {
		if said, ok := mailMCP(a, ki, tool, map[string]any{"uid": uid, "summary": "x", "start": "2026-11-21T10:00", "end": "2026-11-21T11:00"}); ok || !strings.Contains(said, "guests") {
			t.Errorf("%s on an event with a guest: %v %s", tool, ok, said)
		}
	}
	if _, ok := mailMCP(a, ki, "cancel_event", map[string]any{"uid": ev.UID}); !ok {
		t.Fatal("cancel refused")
	}
	if left, _ := a.vayuMail.CalendarBetween(dana, start.Add(-48*time.Hour), start.Add(48*time.Hour), loc); len(left) != 1 || left[0].Summary != "With Eli" {
		t.Errorf("left in the calendar: %+v", left)
	}
}
