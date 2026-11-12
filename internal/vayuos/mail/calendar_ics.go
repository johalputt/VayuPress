// SPDX-License-Identifier: Apache-2.0

package mail

// calendar_ics.go — a calendar in and out as one iCalendar file.
//
// Export is every event and task the holder keeps, as the apps wrote them,
// in one VCALENDAR: the file other calendar programs and services import.
// Import is the other way: a file is split by UID into the objects CalDAV
// keeps, each checked as a CalDAV PUT is checked and stored through the same
// davPut, so the sizes, the count and the allowance hold.
//
// An import adds and never replaces: an event whose UID the calendar already
// holds is left as it is, so importing the same file twice adds nothing and
// an event edited since is not undone. It sends nothing either: a METHOD
// (an invitation's REQUEST, a reply) is a message about an event, not the
// event, and is not kept; nobody on an imported event is invited by it.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"
)

// MaxCalendarImport is the largest file an import reads. A calendar kept for
// years exported by Google is a few megabytes; the store holds 100 MB of
// contacts and events in all.
const MaxCalendarImport = 20 << 20

// ErrICSTooLarge is an import file past MaxCalendarImport.
var ErrICSTooLarge = errors.New("vayumail: a calendar file is at most 20 MB")

// ErrICSUnreadable is an import file with no calendar in it.
var ErrICSUnreadable = errors.New("vayumail: the file is not an iCalendar (.ics) file")

// CalImport is what an import did.
type CalImport struct {
	// Added is the events and tasks kept; Already those the calendar held
	// (by UID) and left; Refused those that could not be kept (another kind
	// of entry, too large, unreadable).
	Added, Already, Refused int
	// Full: the calendar's room ran out, and the rest of the file was not
	// read.
	Full bool
	// Damaged: the file broke off or stopped making sense; what came
	// before is kept.
	Damaged bool
}

// CalendarExport writes the holder's calendar to w as one iCalendar file.
// Each time zone an event names goes once; an entry the console cannot read
// is left out, as it is left out of the calendar's view.
func (e *Engine) CalendarExport(rd Reader, name string, w io.Writer) error {
	mbox, err := e.calendarOf(rd)
	if err != nil {
		return err
	}
	objs, err := e.accounts.davList(context.Background(), mbox, davEvent)
	if err != nil {
		return err
	}
	// The head is written here so each entry can go out as it is read, not
	// the whole calendar held at once. The name is escaped by the library
	// (SetText); a control character, which SetText leaves as it is (a bare
	// CR among them, a line break to some readers), is a space first.
	title := ical.NewProp("X-WR-CALNAME")
	title.SetText(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, name))
	if _, err := io.WriteString(w, "BEGIN:VCALENDAR\r\nPRODID:"+calProdID+"\r\nVERSION:2.0\r\nX-WR-CALNAME:"+title.Value+"\r\n"); err != nil {
		return err
	}
	enc := ical.NewEncoder(w)
	zones := map[string]bool{}
	for _, o := range objs {
		cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
		if err != nil {
			continue
		}
		for _, ch := range cal.Children {
			if ch.Name == ical.CompTimezone {
				id, _ := ch.Props.Text(ical.PropTimezoneID)
				if zones[id] {
					continue
				}
				zones[id] = true
			}
			// A Calendar whose component is the entry encodes that entry
			// alone, with the encoder's own checks.
			if err := enc.Encode(&ical.Calendar{Component: ch}); err != nil {
				return err
			}
		}
	}
	_, err = io.WriteString(w, "END:VCALENDAR\r\n")
	return err
}

// CalendarImport keeps the events and tasks of an iCalendar file in the
// holder's calendar.
func (e *Engine) CalendarImport(rd Reader, r io.Reader, now time.Time) (CalImport, error) {
	var res CalImport
	mbox, err := e.calendarOf(rd)
	if err != nil {
		return res, err
	}
	data, err := io.ReadAll(io.LimitReader(r, MaxCalendarImport+1))
	if err != nil {
		return res, err
	}
	if len(data) > MaxCalendarImport {
		return res, ErrICSTooLarge
	}
	dec := ical.NewDecoder(bytes.NewReader(data))
	read := 0
	for {
		cal, err := dec.Decode()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// What was read before the damage is kept; the rest is not
			// guessed at.
			res.Damaged = true
			break
		}
		read++
		if full := e.importCalendar(mbox, cal, now, &res); full {
			res.Full = true
			break
		}
	}
	if read == 0 {
		return res, ErrICSUnreadable
	}
	// The decoder reads a calendar cut off by the end of the file as the
	// end: one begun and not read is damage too.
	if !res.Full && len(calendarBegins.FindAllIndex(data, -1)) > read {
		res.Damaged = true
	}
	return res, nil
}

var calendarBegins = regexp.MustCompile(`(?im)^BEGIN:VCALENDAR\s*$`)

// importCalendar keeps one VCALENDAR's entries, one object per UID with the
// time zones it names. It reports whether the calendar's room ran out.
func (e *Engine) importCalendar(mbox string, cal *ical.Calendar, now time.Time, res *CalImport) bool {
	ctx := context.Background()
	zones := map[string]*ical.Component{}
	groups := map[string][]*ical.Component{}
	var order []string
	for _, ch := range cal.Children {
		switch ch.Name {
		case ical.CompTimezone:
			if id, _ := ch.Props.Text(ical.PropTimezoneID); id != "" {
				zones[id] = ch
			}
			continue
		case ical.CompEvent, ical.CompToDo:
		default:
			res.Refused++
			continue
		}
		uid, _ := ch.Props.Text(ical.PropUID)
		if uid == "" {
			// A file from a program that writes no UID: each entry is its
			// own, as it was there.
			id, err := randomHex(16)
			if err != nil {
				res.Refused++
				continue
			}
			_, dom := splitAddress(mbox)
			uid = id + "@" + dom
			ch.Props.SetText(ical.PropUID, uid)
		}
		if ch.Props.Get(ical.PropDateTimeStamp) == nil {
			ch.Props.SetDateTime(ical.PropDateTimeStamp, now.UTC())
		}
		if _, ok := groups[uid]; !ok {
			order = append(order, uid)
		}
		groups[uid] = append(groups[uid], ch)
	}
	for _, uid := range order {
		obj := ical.NewCalendar()
		obj.Props.SetText(ical.PropProductID, calProdID)
		obj.Props.SetText(ical.PropVersion, "2.0")
		named := map[string]bool{}
		for _, ch := range groups[uid] {
			zoneIDs(ch, named)
		}
		for id := range named {
			if z, ok := zones[id]; ok {
				obj.Children = append(obj.Children, z)
			}
		}
		obj.Children = append(obj.Children, groups[uid]...)
		if _, _, err := caldav.ValidateCalendarObject(obj); err != nil {
			res.Refused++
			continue
		}
		data, err := encodeCal(obj)
		if err != nil {
			res.Refused++
			continue
		}
		name, err := randomHex(16)
		if err != nil {
			res.Refused++
			continue
		}
		_, err = e.accounts.davPut(ctx, mbox, davEvent, name+".ics", uid, data, davCondition{ifNoneMatch: "*"})
		switch {
		case err == nil:
			res.Added++
		case errors.Is(err, errDAVFull):
			return true
		case errors.Is(err, errDAVUIDTaken):
			// The calendar holds this UID: left as it is.
			res.Already++
		default:
			res.Refused++
		}
	}
	return false
}

// zoneIDs adds the TZIDs comp and its alarms name to into.
func zoneIDs(comp *ical.Component, into map[string]bool) {
	for _, props := range comp.Props {
		for _, p := range props {
			if id := p.Params.Get(ical.ParamTimezoneID); id != "" {
				into[id] = true
			}
		}
	}
	for _, ch := range comp.Children {
		zoneIDs(ch, into)
	}
}
