// SPDX-License-Identifier: Apache-2.0

package main

// mcp_mail.go — VayuMCP's tools for a holder's own mailbox and calendar
// (internal/vayuos/mail/connections.go). A tool is listed and runs only for
// a key minted when the holder connected the tool and granted its scope;
// no other key, an operator's superuser key included, sees one. The tools
// read, draft and file; none sends: a reply or a new message waits in
// Drafts for the holder, and an event a tool makes or changes has no
// guests, since a change to an event with guests mails them. Each call is
// in the holder's record.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	netmail "net/mail"
	"sort"
	"strings"
	"time"

	"github.com/microcosm-cc/bluemonday"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/mcp"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// mailToolMost bounds what one call lists.
const mailToolMost = 50

// mailToolDays bounds the days a calendar call reads.
const mailToolDays = 92

// mailConnection is the connection the request's key was minted for, and
// the holder's Reader for it.
func (a *App) mailConnection(ctx context.Context) (vmail.Connection, vmail.Reader, bool) {
	ki, ok := auth.KeyInfoFromContext(ctx)
	if !ok || !a.mailRunning() || a.vayuMail.Accounts() == nil {
		return vmail.Connection{}, vmail.Reader{}, false
	}
	c, ok := a.vayuMail.ConnectionOf(ki.ID)
	if !ok {
		return vmail.Connection{}, vmail.Reader{}, false
	}
	return c, vmail.ReadAsOwner(mailKeyOf(c.Mailbox, a.vayuMail.Config().Domain)), true
}

// mailTool is a tool over the holder's mailbox, for connections granted
// scope. run answers the result and a line for the holder's record.
func (a *App) mailTool(name, desc, scope string, schema map[string]any, run func(c vmail.Connection, rd vmail.Reader, args json.RawMessage) (string, string, error)) mcp.Tool {
	return mcp.Tool{
		Name:        name,
		Description: desc,
		InputSchema: schema,
		Visible: func(ctx context.Context) bool {
			c, _, ok := a.mailConnection(ctx)
			return ok && c.Can(scope)
		},
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			// The server calls a tool only when Visible says so for this
			// request, so the scope is already the connection's.
			c, rd, ok := a.mailConnection(ctx)
			if !ok {
				return "", errors.New("this connection is not granted that")
			}
			out, what, err := run(c, rd, args)
			if err != nil {
				a.vayuMail.RecordToolCall(c, name, "refused: "+driveRefusal(err))
				return "", errors.New(driveRefusal(err))
			}
			a.vayuMail.RecordToolCall(c, name, what)
			return out, nil
		},
	}
}

// mailArgs reads a call's arguments, refusing what does not fit.
func mailArgs(raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return errors.New("the arguments do not fit this tool")
	}
	return nil
}

type mailListed struct {
	ID      string    `json:"id"`
	Folder  string    `json:"folder"`
	From    string    `json:"from"`
	To      string    `json:"to"`
	Subject string    `json:"subject"`
	Date    time.Time `json:"date"`
	Unread  bool      `json:"unread"`
	Preview string    `json:"preview,omitempty"`
}

func listedMessage(m vmail.StoredMessage, folder string) mailListed {
	return mailListed{ID: m.ID, Folder: folder, From: m.From, To: m.To, Subject: m.Subject, Date: m.Date, Unread: !m.Seen, Preview: m.Preview}
}

// sealedText reports a body still sealed: the holder's key is not open.
func sealedText(raw []byte) bool {
	s := string(raw)
	return strings.Contains(s, "-----BEGIN PGP MESSAGE-----") || strings.Contains(strings.ToLower(s), "multipart/encrypted")
}

// eventTime reads a time given to a calendar tool: RFC 3339, or a date and
// time in zone ("2026-11-20T15:00"), or a date alone.
func eventTime(s string, zone *time.Location) (time.Time, bool, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.In(zone), false, nil
	}
	for _, f := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(f, s, zone); err == nil {
			return t, false, nil
		}
	}
	if t, err := time.ParseInLocation("2006-01-02", s, zone); err == nil {
		return t, true, nil
	}
	return time.Time{}, false, fmt.Errorf("%q is not a time (2026-11-20T15:00, with a zone, or RFC 3339)", s)
}

func toolZone(name string) (*time.Location, error) {
	if strings.TrimSpace(name) == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("%q is not a time zone (an IANA name such as Asia/Kolkata)", name)
	}
	return loc, nil
}

type toolEvent struct {
	UID         string    `json:"uid"`
	Summary     string    `json:"summary"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	AllDay      bool      `json:"all_day,omitempty"`
	Location    string    `json:"location,omitempty"`
	Description string    `json:"description,omitempty"`
	Repeats     string    `json:"repeats,omitempty"`
	Guests      int       `json:"guests,omitempty"`
}

// window reads a calendar call's from and to, at most mailToolDays apart.
func window(from, to string, zone *time.Location) (time.Time, time.Time, error) {
	f, _, err := eventTime(from, zone)
	if err != nil {
		return f, f, err
	}
	t, _, err := eventTime(to, zone)
	if err != nil {
		return f, t, err
	}
	if !t.After(f) || t.Sub(f) > mailToolDays*24*time.Hour {
		return f, t, fmt.Errorf("to is after from, and at most %d days after it", mailToolDays)
	}
	return f, t, nil
}

// registerMailTools adds the holder's mailbox tools.
func (a *App) registerMailTools(srv *mcp.Server) {
	srv.Register(a.mailTool("search_mail", "Search your mailbox. query takes words and from:, to:, subject:, in:<folder>, label:, after:YYYY-MM-DD, before:YYYY-MM-DD, has:attachment, is:unread. Newest first.", vmail.ScopeRead,
		objSchema([]string{"query"}, map[string]any{"query": strProp("What to look for."), "limit": intProp("At most this many (1 to 50, 20 if left out).")}),
		func(_ vmail.Connection, rd vmail.Reader, raw json.RawMessage) (string, string, error) {
			var in struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := mailArgs(raw, &in); err != nil {
				return "", "", err
			}
			q := vmail.ParseSearchQuery(in.Query)
			if q.Empty() {
				return "", "", errors.New("say what to look for")
			}
			limit := in.Limit
			if limit <= 0 {
				limit = 20
			}
			res, err := a.vayuMail.Search(rd, q, min(limit, mailToolMost))
			if err != nil {
				return "", "", err
			}
			out := make([]mailListed, 0, len(res))
			for _, m := range res {
				out = append(out, listedMessage(m.StoredMessage, m.Folder))
			}
			return jsonStr(out), fmt.Sprintf("searched for %q: %d found", in.Query, len(out)), nil
		}))

	srv.Register(a.mailTool("read_message", "Read one message: its headers, its text, and the names of its attachments. folder and id come from search_mail or list_threads.", vmail.ScopeRead,
		objSchema([]string{"folder", "id"}, map[string]any{"folder": strProp("The folder, e.g. INBOX."), "id": strProp("The message's id.")}),
		func(_ vmail.Connection, rd vmail.Reader, raw json.RawMessage) (string, string, error) {
			var in struct{ Folder, ID string }
			if err := mailArgs(raw, &in); err != nil {
				return "", "", err
			}
			body, err := a.vayuMail.ReadFolderMessage(rd, in.Folder, in.ID)
			if err != nil {
				return "", "", errors.New("there is no such message")
			}
			if sealedText(body) {
				return "", "", errors.New("this message is sealed to your key; a tool reads it only while your mailbox is unlocked")
			}
			pm := vmail.ParseMessage(body)
			text := pm.Text
			if strings.TrimSpace(text) == "" && pm.HTML != "" {
				text = htmlTagRe.ReplaceAllString(bluemonday.StrictPolicy().Sanitize(pm.HTML), "")
			}
			names := make([]string, 0, len(pm.Attachments))
			for _, at := range pm.Attachments {
				names = append(names, at.Filename)
			}
			return jsonStr(map[string]any{"from": pm.From, "to": pm.To, "cc": pm.Cc, "subject": pm.Subject, "date": pm.Date, "text": text, "attachments": names}),
				"read: " + pm.Subject, nil
		}))

	srv.Register(a.mailTool("list_threads", "List the conversations in a folder, newest first: each with its subject, who took part, how many messages and the latest one's id.", vmail.ScopeRead,
		objSchema(nil, map[string]any{"folder": strProp("The folder, INBOX if left out."), "limit": intProp("At most this many conversations (1 to 50, 20 if left out).")}),
		func(_ vmail.Connection, rd vmail.Reader, raw json.RawMessage) (string, string, error) {
			var in struct {
				Folder string `json:"folder"`
				Limit  int    `json:"limit"`
			}
			if err := mailArgs(raw, &in); err != nil {
				return "", "", err
			}
			if in.Folder == "" {
				in.Folder = "INBOX"
			}
			msgs, err := a.vayuMail.ListFolder(rd, in.Folder)
			if err != nil {
				return "", "", errors.New("there is no such folder")
			}
			limit := in.Limit
			if limit <= 0 {
				limit = 20
			}
			threads := mailThreads(msgs, in.Folder, min(limit, mailToolMost))
			return jsonStr(threads), fmt.Sprintf("listed %d conversations in %s", len(threads), in.Folder), nil
		}))

	srv.Register(a.mailTool("draft_reply", "Write a reply to a message as a draft in Drafts. It is not sent: you open it, change what you like, and send it.", vmail.ScopeDraft,
		objSchema([]string{"folder", "id", "body"}, map[string]any{"folder": strProp("The message's folder."), "id": strProp("The message's id."), "body": strProp("The reply's text."),
			"reply_all": map[string]any{"type": "boolean", "description": "Also to everyone else it was sent to."}}),
		func(c vmail.Connection, rd vmail.Reader, raw json.RawMessage) (string, string, error) {
			var in struct {
				Folder, ID, Body string
				ReplyAll         bool `json:"reply_all"`
			}
			if err := mailArgs(raw, &in); err != nil {
				return "", "", err
			}
			body, err := a.vayuMail.ReadFolderMessage(rd, in.Folder, in.ID)
			if err != nil {
				return "", "", errors.New("there is no such message")
			}
			d, err := replyDraft(body, c.Mailbox, in.Body, in.ReplyAll)
			if err != nil {
				return "", "", err
			}
			id, err := a.vayuMail.SaveToolDraft(rd, c.Client, d)
			if err != nil {
				return "", "", err
			}
			return jsonStr(map[string]string{"draft": id, "folder": "Drafts", "to": strings.Join(d.To, ", "), "cc": strings.Join(d.Cc, ", "), "subject": d.Subject}),
				"drafted a reply to " + strings.Join(d.To, ", ") + ": " + d.Subject, nil
		}))

	srv.Register(a.mailTool("create_draft", "Write a new message as a draft in Drafts. It is not sent: you open it and send it.", vmail.ScopeDraft,
		objSchema([]string{"to", "subject", "body"}, map[string]any{"to": arrProp("Addresses to send it to."), "cc": arrProp("Addresses to copy."), "subject": strProp("Its subject."), "body": strProp("Its text.")}),
		func(c vmail.Connection, rd vmail.Reader, raw json.RawMessage) (string, string, error) {
			var in struct {
				To, Cc        []string
				Subject, Body string
			}
			if err := mailArgs(raw, &in); err != nil {
				return "", "", err
			}
			if len(in.To) == 0 {
				return "", "", errors.New("a draft is to someone")
			}
			id, err := a.vayuMail.SaveToolDraft(rd, c.Client, vmail.ToolDraft{To: in.To, Cc: in.Cc, Subject: in.Subject, Body: in.Body})
			if err != nil {
				return "", "", err
			}
			return jsonStr(map[string]string{"draft": id, "folder": "Drafts"}), "drafted to " + strings.Join(in.To, ", ") + ": " + in.Subject, nil
		}))

	srv.Register(a.mailTool("file_message", "File a message: move it to another folder, or put a label on it or take one off. Nothing is deleted: Trash is not a folder this moves to.", vmail.ScopeDraft,
		objSchema([]string{"folder", "id"}, map[string]any{"folder": strProp("The message's folder."), "id": strProp("The message's id."),
			"move_to": strProp("A folder to move it to."), "label": strProp("A label to put on it."), "unlabel": strProp("A label to take off it.")}),
		func(_ vmail.Connection, rd vmail.Reader, raw json.RawMessage) (string, string, error) {
			var in struct {
				Folder, ID     string
				MoveTo         string `json:"move_to"`
				Label, Unlabel string
			}
			if err := mailArgs(raw, &in); err != nil {
				return "", "", err
			}
			switch {
			case in.MoveTo != "":
				if strings.EqualFold(strings.TrimSpace(in.MoveTo), "Trash") {
					return "", "", errors.New("deleting is yours to do: a tool moves nothing to Trash")
				}
				if err := a.vayuMail.MoveMessage(rd, in.ID, in.Folder, in.MoveTo); err != nil {
					return "", "", errors.New("it was not moved: " + driveRefusal(err))
				}
				return `{"moved":true}`, "moved a message from " + in.Folder + " to " + in.MoveTo, nil
			case in.Label != "" || in.Unlabel != "":
				label, on := in.Label, true
				if label == "" {
					label, on = in.Unlabel, false
				}
				if err := a.vayuMail.SetLabel(rd, in.ID, label, on); err != nil {
					return "", "", err
				}
				what := "labelled a message " + label
				if !on {
					what = "took the label " + label + " off a message"
				}
				return `{"done":true}`, what, nil
			}
			return "", "", errors.New("say move_to, label or unlabel")
		}))

	srv.Register(a.mailTool("list_events", "List your events between two times, in a time zone (UTC if left out). At most 92 days.", vmail.ScopeRead,
		objSchema([]string{"from", "to"}, map[string]any{"from": strProp("The first time, e.g. 2026-11-20 or 2026-11-20T09:00."), "to": strProp("The time after the last."), "zone": strProp("An IANA time zone, e.g. Asia/Kolkata.")}),
		func(_ vmail.Connection, rd vmail.Reader, raw json.RawMessage) (string, string, error) {
			var in struct{ From, To, Zone string }
			if err := mailArgs(raw, &in); err != nil {
				return "", "", err
			}
			zone, err := toolZone(in.Zone)
			if err != nil {
				return "", "", err
			}
			from, to, err := window(in.From, in.To, zone)
			if err != nil {
				return "", "", err
			}
			evs, err := a.vayuMail.CalendarBetween(rd, from, to, zone)
			if err != nil {
				return "", "", err
			}
			out := make([]toolEvent, 0, len(evs))
			for _, e := range evs {
				out = append(out, toolEvent{UID: e.UID, Summary: e.Summary, Start: e.Start, End: e.End, AllDay: e.AllDay, Location: e.Location, Description: e.Description, Repeats: e.Rule, Guests: len(e.Attendees)})
			}
			return jsonStr(out), fmt.Sprintf("listed %d events", len(out)), nil
		}))

	srv.Register(a.mailTool("find_free_time", "Find times you have nothing in your calendar, at least minutes long, between two times.", vmail.ScopeRead,
		objSchema([]string{"from", "to", "minutes"}, map[string]any{"from": strProp("The first time."), "to": strProp("The time after the last."), "minutes": intProp("How long a time must be free (5 to 1440)."), "zone": strProp("An IANA time zone.")}),
		func(_ vmail.Connection, rd vmail.Reader, raw json.RawMessage) (string, string, error) {
			var in struct {
				From, To, Zone string
				Minutes        int
			}
			if err := mailArgs(raw, &in); err != nil {
				return "", "", err
			}
			if in.Minutes < 5 || in.Minutes > 1440 {
				return "", "", errors.New("minutes is 5 to 1440")
			}
			zone, err := toolZone(in.Zone)
			if err != nil {
				return "", "", err
			}
			from, to, err := window(in.From, in.To, zone)
			if err != nil {
				return "", "", err
			}
			evs, err := a.vayuMail.CalendarBetween(rd, from, to, zone)
			if err != nil {
				return "", "", err
			}
			free := freeTimes(evs, from, to, time.Duration(in.Minutes)*time.Minute)
			return jsonStr(free), fmt.Sprintf("found %d free times", len(free)), nil
		}))

	eventSchema := map[string]any{"summary": strProp("What it is."), "start": strProp("When it starts, e.g. 2026-11-20T15:00, or a date for all day."),
		"end": strProp("When it ends; for all day, the day after the last."), "zone": strProp("An IANA time zone, UTC if left out."),
		"location": strProp("Where."), "description": strProp("Notes.")}
	srv.Register(a.mailTool("create_event", "Put an event in your calendar. It has no guests: invitations are yours to send, from the calendar.", vmail.ScopeCalendar,
		objSchema([]string{"summary", "start", "end"}, eventSchema),
		func(_ vmail.Connection, rd vmail.Reader, raw json.RawMessage) (string, string, error) {
			in, err := eventIn(raw)
			if err != nil {
				return "", "", err
			}
			uid, err := a.vayuMail.SaveEvent(rd, in, vmail.ScopeAll, time.Time{})
			if err != nil {
				return "", "", err
			}
			return jsonStr(map[string]string{"uid": uid}), "made the event " + in.Summary + " on " + in.Start.Format("2 January 2006 15:04 MST"), nil
		}))

	updateSchema := map[string]any{"uid": strProp("The event's uid, from list_events.")}
	for k, v := range eventSchema {
		updateSchema[k] = v
	}
	srv.Register(a.mailTool("update_event", "Change an event of yours that has no guests (one with guests is changed in the calendar, since a change mails them). A repeating event is changed as a whole, and keeps repeating.", vmail.ScopeCalendar,
		objSchema([]string{"uid", "summary", "start", "end"}, updateSchema),
		func(_ vmail.Connection, rd vmail.Reader, raw json.RawMessage) (string, string, error) {
			var id struct{ UID string }
			if err := mailArgs(raw, &id); err != nil {
				return "", "", err
			}
			was, guests, err := a.vayuMail.EventOf(rd, id.UID)
			if err != nil {
				return "", "", errors.New("there is no such event")
			}
			if guests > 0 {
				return "", "", errors.New("this event has guests; change it in the calendar, which tells them")
			}
			in, err := eventIn(raw)
			if err != nil {
				return "", "", err
			}
			in.UID, in.Rule = id.UID, was.Rule
			if _, err := a.vayuMail.SaveEvent(rd, in, vmail.ScopeAll, time.Time{}); err != nil {
				return "", "", err
			}
			return `{"changed":true}`, "changed the event " + in.Summary, nil
		}))

	srv.Register(a.mailTool("cancel_event", "Take an event of yours that has no guests out of your calendar (a repeating one, every time). One with guests is cancelled in the calendar, since that mails them.", vmail.ScopeCalendar,
		objSchema([]string{"uid"}, map[string]any{"uid": strProp("The event's uid, from list_events.")}),
		func(_ vmail.Connection, rd vmail.Reader, raw json.RawMessage) (string, string, error) {
			var id struct{ UID string }
			if err := mailArgs(raw, &id); err != nil {
				return "", "", err
			}
			was, guests, err := a.vayuMail.EventOf(rd, id.UID)
			if err != nil {
				return "", "", errors.New("there is no such event")
			}
			if guests > 0 {
				return "", "", errors.New("this event has guests; cancel it in the calendar, which tells them")
			}
			if err := a.vayuMail.DeleteEvent(rd, id.UID, vmail.ScopeAll, time.Time{}); err != nil {
				return "", "", err
			}
			return `{"cancelled":true}`, "cancelled the event " + was.Summary, nil
		}))
}

// eventIn reads an event a tool gives.
func eventIn(raw json.RawMessage) (vmail.EventInput, error) {
	var in struct{ Summary, Start, End, Zone, Location, Description string }
	if err := mailArgs(raw, &in); err != nil {
		return vmail.EventInput{}, err
	}
	zone, err := toolZone(in.Zone)
	if err != nil {
		return vmail.EventInput{}, err
	}
	start, allDay, err := eventTime(in.Start, zone)
	if err != nil {
		return vmail.EventInput{}, err
	}
	end, endDay, err := eventTime(in.End, zone)
	if err != nil {
		return vmail.EventInput{}, err
	}
	if allDay != endDay {
		return vmail.EventInput{}, errors.New("start and end are both dates, for all day, or both times")
	}
	return vmail.EventInput{Summary: in.Summary, Location: in.Location, Description: in.Description, Start: start, End: end, AllDay: allDay}, nil
}

// replyDraft is the draft answering raw from mbox: to its Reply-To or
// From, with everyone else it went to for reply_all, but never mbox.
func replyDraft(raw []byte, mbox, body string, all bool) (vmail.ToolDraft, error) {
	msg, err := netmail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		return vmail.ToolDraft{}, errors.New("that message cannot be answered")
	}
	h := msg.Header
	pm := vmail.ParseMessage(raw)
	seen := map[string]bool{strings.ToLower(mbox): true}
	pick := func(field string) []string {
		var out []string
		list, _ := h.AddressList(field)
		for _, a := range list {
			if k := strings.ToLower(a.Address); !seen[k] {
				seen[k] = true
				out = append(out, a.Address)
			}
		}
		return out
	}
	to := pick("Reply-To")
	if len(to) == 0 {
		to = pick("From")
	}
	var cc []string
	if all {
		cc = append(pick("To"), pick("Cc")...)
	}
	if len(to) == 0 {
		return vmail.ToolDraft{}, errors.New("that message has no one to answer")
	}
	subject := pm.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	return vmail.ToolDraft{To: to, Cc: cc, Subject: subject, Body: body,
		InReplyTo: h.Get("Message-Id"), References: strings.Fields(h.Get("References"))}, nil
}

// mailThread is one conversation as list_threads gives it.
type mailThread struct {
	Subject  string    `json:"subject"`
	People   []string  `json:"people"`
	Messages int       `json:"messages"`
	Unread   int       `json:"unread"`
	Latest   time.Time `json:"latest"`
	LatestID string    `json:"latest_id"`
	Folder   string    `json:"folder"`
}

// mailThreads groups a folder's messages into conversations by their
// first reference, or their own id, or their subject without Re:/Fwd:.
func mailThreads(msgs []vmail.StoredMessage, folder string, most int) []mailThread {
	byKey := map[string]*mailThread{}
	var order []*mailThread
	for _, m := range msgs {
		key := "s:" + strings.ToLower(threadSubject(m.Subject))
		switch {
		case len(m.References) > 0:
			key = "m:" + m.References[0]
		case m.InReplyTo != "":
			key = "m:" + m.InReplyTo
		case m.MessageID != "":
			if _, ok := byKey["m:"+m.MessageID]; ok {
				key = "m:" + m.MessageID
			}
		}
		t := byKey[key]
		if t == nil {
			t = &mailThread{Subject: threadSubject(m.Subject), Folder: folder}
			byKey[key] = t
			if m.MessageID != "" {
				byKey["m:"+m.MessageID] = t
			}
			order = append(order, t)
		}
		t.Messages++
		if !m.Seen {
			t.Unread++
		}
		if m.Date.After(t.Latest) || t.LatestID == "" {
			t.Latest, t.LatestID = m.Date, m.ID
		}
		if who := m.From; who != "" && !contains(t.People, who) && len(t.People) < 10 {
			t.People = append(t.People, who)
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].Latest.After(order[j].Latest) })
	out := make([]mailThread, 0, min(len(order), most))
	for _, t := range order {
		if len(out) == most {
			break
		}
		out = append(out, *t)
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// threadSubject is a subject without the Re: and Fwd: in front of it.
func threadSubject(s string) string {
	for {
		t := strings.TrimSpace(s)
		l := strings.ToLower(t)
		switch {
		case strings.HasPrefix(l, "re:"):
			s = t[3:]
		case strings.HasPrefix(l, "fwd:"):
			s = t[4:]
		case strings.HasPrefix(l, "fw:"):
			s = t[3:]
		default:
			return t
		}
	}
}

// freeTime is one gap find_free_time gives.
type freeTime struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// freeTimes is the gaps between busy events in [from, to) at least d long,
// at most mailToolMost. An event marked free does not count.
func freeTimes(evs []vmail.CalEvent, from, to time.Time, d time.Duration) []freeTime {
	type span struct{ s, e time.Time }
	var busy []span
	for _, e := range evs {
		if !e.Free {
			busy = append(busy, span{e.Start, e.End})
		}
	}
	sort.Slice(busy, func(i, j int) bool { return busy[i].s.Before(busy[j].s) })
	var out []freeTime
	at := from
	for _, b := range append(busy, span{to, to}) {
		if b.s.After(to) {
			b.s = to
		}
		if b.s.Sub(at) >= d && len(out) < mailToolMost {
			out = append(out, freeTime{Start: at, End: b.s})
		}
		if b.e.After(at) {
			at = b.e
		}
	}
	return out
}
