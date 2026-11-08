// SPDX-License-Identifier: Apache-2.0

package mail

// reminders.go — a calendar's reminders.
//
// A reminder is an alarm on the event (VALARM), as phones keep them. One to
// show (ACTION:DISPLAY) is the phone's and the desktop program's to show:
// the server does not send it again. One by mail (ACTION:EMAIL) is delivered
// here, into the holder's own Inbox at its time, where the console's new-mail
// notice and the phone's mail alert both raise it.
//
// The worker looks every half minute. A reminder is sent when its time has
// come within the last ten minutes, once (vayumail_reminders_sent): a short
// restart catches up, and a server that was down for a day does not wake up
// to a burst of reminders for things already over.

import (
	"bytes"
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

const (
	// reminderLate is how late a reminder may still be sent.
	reminderLate = 10 * time.Minute
	// reminderLead is the longest a reminder may come before its event.
	reminderLead = 7 * 24 * time.Hour
)

func (s *AccountStore) ensureReminderTable() error {
	if s.db == nil {
		return sql.ErrConnDone
	}
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_reminders_sent(
		mailbox TEXT NOT NULL,
		uid TEXT NOT NULL,
		instance INTEGER NOT NULL,
		trig TEXT NOT NULL,
		sent_at DATETIME NOT NULL,
		PRIMARY KEY(mailbox, uid, instance, trig));`)
	return err
}

// reminderLeadOf is a relative TRIGGER as a lead before the start: "-PT10M"
// is ten minutes, "PT0S" the start itself. ok is false for one counted from
// the end or at a fixed time, which a reminder by mail is not.
func reminderLeadOf(p *ical.Prop) (time.Duration, bool) {
	if p == nil || strings.EqualFold(p.Params.Get(ical.ParamRelated), "END") || p.ValueType() == ical.ValueDateTime {
		return 0, false
	}
	d, err := p.Duration()
	if err != nil || d > 0 || -d > reminderLead {
		return 0, false
	}
	return -d, true
}

// sendReminders delivers every mail reminder whose time came in the last
// reminderLate before now.
func (e *Engine) sendReminders(now time.Time) {
	if e.accounts == nil || e.maildir == nil || e.accounts.ensureReminderTable() != nil {
		return
	}
	ctx := context.Background()
	rows, err := e.accounts.db.QueryContext(ctx, `SELECT mailbox,data FROM vayumail_dav WHERE kind=? AND data LIKE '%ACTION:EMAIL%'`, davEvent)
	if err != nil {
		return
	}
	type object struct {
		mbox string
		data []byte
	}
	var objs []object
	for rows.Next() {
		var o object
		if rows.Scan(&o.mbox, &o.data) == nil {
			objs = append(objs, o)
		}
	}
	if rows.Err() != nil || rows.Close() != nil {
		return
	}
	for _, o := range objs {
		cal, err := ical.NewDecoder(bytes.NewReader(o.data)).Decode()
		if err != nil {
			continue
		}
		master, _ := parts(cal)
		if master == nil {
			continue
		}
		var leads []time.Duration
		for _, al := range master.Children {
			if al.Name != ical.CompAlarm || !strings.EqualFold(textOf(al, ical.PropAction), "EMAIL") {
				continue
			}
			if lead, ok := reminderLeadOf(al.Props.Get(ical.PropTrigger)); ok {
				leads = append(leads, lead)
			}
		}
		if len(leads) == 0 {
			continue
		}
		c := newCalReader(cal, time.UTC)
		c.self = o.mbox
		budget := maxEventSteps
		for _, ev := range c.occurrences(cal, now.Add(-reminderLate), now.Add(reminderLead), &budget) {
			for _, lead := range leads {
				at := ev.Start.Add(-lead)
				if at.After(now) || !at.After(now.Add(-reminderLate)) {
					continue
				}
				e.remind(ctx, o.mbox, ev, lead)
			}
		}
	}
}

// remind delivers one reminder, once.
func (e *Engine) remind(ctx context.Context, mbox string, ev CalEvent, lead time.Duration) {
	inst := ev.Start.Unix()
	trig := strconv.FormatInt(int64(lead/time.Second), 10)
	res, err := e.accounts.db.ExecContext(ctx, `INSERT OR IGNORE INTO vayumail_reminders_sent(mailbox,uid,instance,trig,sent_at) VALUES(?,?,?,?,?)`,
		mbox, ev.UID, inst, trig, time.Now().UTC())
	if err != nil {
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return // sent already
	}
	local, dom := splitAddress(mbox)
	loc := time.UTC
	if l := zoneNamed(ev.Zone); l != nil {
		loc = l
	}
	when := ev.Start.In(loc).Format("Monday 2 January, 15:04")
	if ev.Zone != "" {
		when += " (" + ev.Zone + ")"
	}
	if ev.AllDay {
		when = ev.Start.Format("Monday 2 January") + ", all day"
	}
	title := strings.ReplaceAll(nonEmptyText(ev.Summary, "An event"), "\n", " ")
	body := title + "\n" + when + "\n"
	if ev.Location != "" {
		body += "Where: " + ev.Location + "\n"
	}
	var raw bytes.Buffer
	if err := writeHeaders(&raw, []HeaderField{
		{Key: "From", Value: "Calendar <" + mbox + ">"},
		{Key: "To", Value: mbox},
		{Key: "Subject", Value: "Reminder: " + title},
		{Key: "Date", Value: time.Now().UTC().Format(time.RFC1123Z)},
		{Key: "Message-ID", Value: e.messageID(dom)},
		{Key: "MIME-Version", Value: "1.0"},
		{Key: "Content-Type", Value: "text/plain; charset=utf-8"},
		{Key: "Content-Transfer-Encoding", Value: "8bit"},
	}); err != nil {
		return
	}
	raw.WriteString(normalizeCRLF(body))
	_, _ = e.maildir.Deliver(dom, local, raw.Bytes())
}
