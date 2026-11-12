// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAConnectionIsToAnActiveMailboxAndItsHoldersAlone — made only for a
// mailbox here, with only the scopes known; found by its key while the
// mailbox is active; listed and ended by its holder alone; gone with the
// mailbox.
func TestAConnectionIsToAnActiveMailboxAndItsHoldersAlone(t *testing.T) {
	e, s := calEngine(t)
	ctx := context.Background()
	if err := e.Connect("nobody@example.com", "k0", "Tool", []string{ScopeRead}); err == nil {
		t.Error("a connection to a mailbox that is not here")
	}
	if err := e.Connect(calBox, "k0", "Tool", []string{"send", "admin"}); err == nil {
		t.Error("a connection granted nothing this package knows")
	}
	if err := e.Connect(calBox, "k1", "Tool", []string{ScopeRead, "send", ScopeRead}); err != nil {
		t.Fatal(err)
	}
	c, ok := e.ConnectionOf("k1")
	if !ok || c.Mailbox != calBox || strings.Join(c.Scopes, ",") != ScopeRead || c.Can(ScopeDraft) {
		t.Fatalf("the connection: %+v %v", c, ok)
	}
	if _, ok := e.ConnectionOf("k2"); ok {
		t.Error("a key with no connection found one")
	}
	if err := s.SetActive(ctx, calBox, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.ConnectionOf("k1"); ok {
		t.Error("a suspended mailbox's connection was found")
	}
	if err := e.Connect(calBox, "k9", "Tool", []string{ScopeRead}); err == nil {
		t.Error("a suspended mailbox was connected")
	}
	if err := s.SetActive(ctx, calBox, true); err != nil {
		t.Fatal(err)
	}
	ankush := ReadAsOwner("ankush")
	if _, err := e.Connections(ReadAsOperator("ankush", "boss@example.com")); !errors.Is(err, ErrConnectionHolderOnly) {
		t.Errorf("an administrator listed them: %v", err)
	}
	if err := s.Create(ctx, "dana@example.com", "h", "Dana", "mailbox"); err != nil {
		t.Fatal(err)
	}
	if err := e.Disconnect(ReadAsOwner("dana"), "k1"); err == nil {
		t.Error("another holder ended it")
	}
	if err := e.Disconnect(ReadAsOperator("ankush", "boss@example.com"), "k1"); !errors.Is(err, ErrConnectionHolderOnly) {
		t.Errorf("an administrator ended it: %v", err)
	}
	if cs, _ := e.Connections(ankush); len(cs) != 1 {
		t.Fatalf("%d connections", len(cs))
	}
	if err := e.Disconnect(ankush, "k1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.ConnectionOf("k1"); ok {
		t.Error("found after it was ended")
	}
	// Gone with the mailbox, with its record.
	if err := e.Connect(calBox, "k3", "Tool", []string{ScopeRead}); err != nil {
		t.Fatal(err)
	}
	c, _ = e.ConnectionOf("k3")
	e.RecordToolCall(c, "search_mail", "searched")
	if err := s.Delete(ctx, calBox); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.db.QueryRow(`SELECT (SELECT COUNT(1) FROM vayumail_connections)+(SELECT COUNT(1) FROM vayumail_tool_calls)`).Scan(&n)
	if n != 0 {
		t.Errorf("%d rows left after the mailbox was deleted", n)
	}
}

// TestEveryToolCallIsRecordedForItsHolder — newest first, the latest kept,
// and the connection's last use.
func TestEveryToolCallIsRecordedForItsHolder(t *testing.T) {
	e, s := calEngine(t)
	if err := e.Connect(calBox, "k1", "Helper", []string{ScopeRead}); err != nil {
		t.Fatal(err)
	}
	c, _ := e.ConnectionOf("k1")
	if !c.LastUsed.IsZero() {
		t.Fatalf("used before a call: %v", c.LastUsed)
	}
	for i := 0; i < maxToolCalls+3; i++ {
		e.RecordToolCall(c, "search_mail", "call "+strings.Repeat("x", i%3))
	}
	e.RecordToolCall(c, "read_message", "the last")
	calls, err := e.ToolCalls(ReadAsOwner("ankush"), 1000)
	if err != nil || len(calls) != maxToolCalls || calls[0].What != "the last" || calls[0].Client != "Helper" {
		t.Fatalf("the record: %d %+v %v", len(calls), calls[0], err)
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(1) FROM vayumail_tool_calls`).Scan(&n)
	if n != maxToolCalls {
		t.Errorf("%d kept", n)
	}
	if c, _ = e.ConnectionOf("k1"); time.Since(c.LastUsed) > time.Minute {
		t.Errorf("last used %v", c.LastUsed)
	}
	if _, err := e.ToolCalls(ReadAsOperator("ankush", "boss@example.com"), 10); !errors.Is(err, ErrConnectionHolderOnly) {
		t.Errorf("an administrator read the record: %v", err)
	}
}

// TestAToolsDraftWaitsInDraftsMarkedAndThreaded — the tool's name in its
// own header, the reply threaded, nothing a tool writes becomes a header.
func TestAToolsDraftWaitsInDraftsMarkedAndThreaded(t *testing.T) {
	e, _ := calEngine(t)
	dir := t.TempDir()
	e.cfg.StorageDir = dir
	e.maildir = NewMaildir(filepath.Join(dir, "maildir"))
	ankush := ReadAsOwner("ankush")
	id, err := e.SaveToolDraft(ankush, "Helper\r\nBcc: x@evil.example", ToolDraft{To: []string{"Dana <dana@example.org>"}, Subject: "Re: plans\r\nBcc: y@evil.example",
		Body: "Thursday works.", InReplyTo: "<abc@example.org>", References: []string{"<root@example.org>"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := e.ReadFolderMessage(ankush, "Drafts", id)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{"From: ankush@example.com\r\n", "To: \"Dana\" <dana@example.org>\r\n", "In-Reply-To: <abc@example.org>\r\n",
		"References: <root@example.org> <abc@example.org>\r\n", "X-Drafted-By: Helper  Bcc: x@evil.example\r\n", "\r\n\r\nThursday works."} {
		if !strings.Contains(got, want) {
			t.Errorf("the draft has no %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\r\nBcc:") {
		t.Errorf("a tool's text became a header:\n%s", got)
	}
	if _, err := e.SaveToolDraft(ankush, "Helper", ToolDraft{To: []string{"dana@example.org\r\nBcc: z@evil.example"}}); !errors.Is(err, ErrDraftAddress) {
		t.Errorf("an address with a header in it: %v", err)
	}
	if _, err := e.SaveToolDraft(ReadAsOperator("ankush", "boss@example.com"), "Helper", ToolDraft{To: []string{"d@example.org"}}); !errors.Is(err, ErrConnectionHolderOnly) {
		t.Errorf("an administrator drafted: %v", err)
	}
}

// TestEventOfCountsGuestsInEveryOccurrence — a guest only in one changed
// occurrence counts.
func TestEventOfCountsGuestsInEveryOccurrence(t *testing.T) {
	e, s := calEngine(t)
	putEvent(t, s, "a.ics", "a@x", "BEGIN:VEVENT", "UID:a@x", "DTSTAMP:20261101T000000Z", "DTSTART:20261120T090000Z", "DTEND:20261120T100000Z",
		"RRULE:FREQ=WEEKLY;COUNT=3", "SUMMARY:Standup", "END:VEVENT",
		"BEGIN:VEVENT", "UID:a@x", "DTSTAMP:20261101T000000Z", "RECURRENCE-ID:20261127T090000Z", "DTSTART:20261127T100000Z", "DTEND:20261127T110000Z",
		"SUMMARY:Standup", "ATTENDEE:mailto:dana@example.org", "END:VEVENT")
	putEvent(t, s, "b.ics", "b@x", "BEGIN:VEVENT", "UID:b@x", "DTSTAMP:20261101T000000Z", "DTSTART:20261121T090000Z", "DTEND:20261121T100000Z", "SUMMARY:Alone", "END:VEVENT")
	ankush := ReadAsOwner("ankush")
	if ev, guests, err := e.EventOf(ankush, "a@x"); err != nil || guests != 1 || ev.Rule != "FREQ=WEEKLY;COUNT=3" {
		t.Errorf("the series: %+v %d %v", ev, guests, err)
	}
	if ev, guests, err := e.EventOf(ankush, "b@x"); err != nil || guests != 0 || ev.Summary != "Alone" {
		t.Errorf("an event alone: %+v %d %v", ev, guests, err)
	}
	if _, _, err := e.EventOf(ReadAsOperator("ankush", "boss@example.com"), "b@x"); err == nil {
		t.Error("an administrator read it")
	}
}
