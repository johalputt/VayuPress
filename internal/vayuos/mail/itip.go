// SPDX-License-Identifier: Apache-2.0

package mail

// itip.go — invitations: an event's guests are sent it, its changes and its
// cancellation as RFC 5546 (iTIP) carried by mail (RFC 6047, iMIP), from the
// holder's own address and signed like all their mail; a guest's answer
// arriving in the mailbox is recorded on the event.
//
// Only the event's organizer sends. An event someone else invited the holder
// to keeps its guests as its organizer set them, and a change the holder
// makes to it stays in their own calendar.
//
// A guest's answer is taken from the guest: the message's From must be the
// attendee the answer is for, and mail the junk filter or a blocked sender
// sent elsewhere never reaches this (inbound.go calls it after both). From
// can be forged where DMARC is not enforced; what a forged answer changes is
// one guest's going, maybe or not going on one event, which the holder sees,
// and nothing is sent because of it.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	netmail "net/mail"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

// maxGuests bounds one event's guests: an invitation is mail from the
// holder's address, and an event is not a mailing list.
const maxGuests = 100

// ErrNotOrganizer is a change to the guests of an event the holder was
// invited to.
var ErrNotOrganizer = errors.New("vayumail: this event's guests are its organizer's to change")

// ErrInvitationsNotSent is an event saved whose invitations did not all go.
var ErrInvitationsNotSent = errors.New("vayumail: saved, but not every guest could be sent it")

func mailtoOf(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > 7 && strings.EqualFold(v[:7], "mailto:") {
		v = v[7:]
	}
	return normEmail(v)
}

// guestsOf is a VEVENT's attendees' addresses.
func guestsOf(comp *ical.Component) []string {
	if comp == nil {
		return nil
	}
	var out []string
	for _, p := range comp.Props.Values(ical.PropAttendee) {
		if a := mailtoOf(p.Value); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// organizedBy reports whether mbox may send an event's invitations: it is
// the organizer, or no one is.
func organizedBy(comp *ical.Component, mbox string) bool {
	p := comp.Props.Get(ical.PropOrganizer)
	return p == nil || mailtoOf(p.Value) == normEmail(mbox)
}

// cleanGuests checks the editor's guest list: addresses, each once, not the
// holder, at most maxGuests.
func cleanGuests(guests []string, mbox string) ([]string, error) {
	out := []string{}
	for _, g := range guests {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		a, err := netmail.ParseAddress(g)
		if err != nil || !strings.Contains(a.Address, "@") {
			return nil, fmt.Errorf("%q is not an address", g)
		}
		addr := normEmail(a.Address)
		if addr == normEmail(mbox) || slices.Contains(out, addr) {
			continue
		}
		out = append(out, addr)
	}
	if len(out) > maxGuests {
		return nil, fmt.Errorf("an event has at most %d guests", maxGuests)
	}
	return out, nil
}

// setGuests writes an event's organizer and guests, keeping each guest's
// answer so far; a guest added is asked for one.
func setGuests(comp *ical.Component, mbox string, guests []string) {
	was := map[string]ical.Prop{}
	for _, p := range comp.Props.Values(ical.PropAttendee) {
		was[mailtoOf(p.Value)] = p
	}
	comp.Props.Del(ical.PropAttendee)
	if len(guests) == 0 {
		comp.Props.Del(ical.PropOrganizer)
		return
	}
	org := ical.NewProp(ical.PropOrganizer)
	org.Value = "mailto:" + normEmail(mbox)
	comp.Props.Set(org)
	for _, g := range guests {
		if p, ok := was[g]; ok {
			comp.Props.Add(&p)
			continue
		}
		p := ical.NewProp(ical.PropAttendee)
		p.Value = "mailto:" + g
		p.Params.Set(ical.ParamRole, "REQ-PARTICIPANT")
		p.Params.Set(ical.ParamParticipationStatus, "NEEDS-ACTION")
		p.Params.Set(ical.ParamRSVP, "TRUE")
		comp.Props.Add(p)
	}
}

func utcOffsetText(sec int) string {
	sign := "+"
	if sec < 0 {
		sign, sec = "-", -sec
	}
	return fmt.Sprintf("%s%02d%02d", sign, sec/3600, sec%3600/60)
}

// vtimezone describes a zone for the guests' programs, for the year an event
// starts in: one standard offset, or the two changes of that year as
// yearly rules ("the last Sunday of March"). A program that knows the zone
// by its name uses its own data; one that does not, uses this.
func vtimezone(loc *time.Location, year int) *ical.Component {
	tz := ical.NewComponent(ical.CompTimezone)
	tz.Props.SetText(ical.PropTimezoneID, loc.String())
	part := func(kind string, at time.Time, from, to int, name string, rule string) *ical.Component {
		c := ical.NewComponent(kind)
		start := ical.NewProp(ical.PropDateTimeStart)
		start.Value = at.In(time.FixedZone("", from)).Format("20060102T150405")
		c.Props.Set(start)
		f, t := ical.NewProp(ical.PropTimezoneOffsetFrom), ical.NewProp(ical.PropTimezoneOffsetTo)
		f.Value, t.Value = utcOffsetText(from), utcOffsetText(to)
		c.Props.Set(f)
		c.Props.Set(t)
		if name != "" {
			c.Props.SetText(ical.PropTimezoneName, name)
		}
		if rule != "" {
			r := ical.NewProp(ical.PropRecurrenceRule)
			r.SetValueType(ical.ValueRecurrence)
			r.Value = rule
			c.Props.Set(r)
		}
		return c
	}
	start := time.Date(year, 1, 1, 0, 0, 0, 0, loc)
	name, off := start.Zone()
	var changes []time.Time
	for d := start; d.Year() == year; d = d.Add(24 * time.Hour) {
		if _, o := d.Add(24 * time.Hour).Zone(); o != off {
			lo, hi := d, d.Add(24*time.Hour)
			for hi.Sub(lo) > time.Minute {
				mid := lo.Add(hi.Sub(lo) / 2)
				if _, o := mid.Zone(); o == off {
					lo = mid
				} else {
					hi = mid
				}
			}
			changes = append(changes, hi.Truncate(time.Minute))
			_, off = hi.Zone()
		}
	}
	if len(changes) == 0 {
		tz.Children = append(tz.Children, part(ical.CompTimezoneStandard, time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), off, off, name, ""))
		return tz
	}
	_, jan := start.Zone()
	_, jul := time.Date(year, 7, 1, 0, 0, 0, 0, loc).Zone()
	summer := max(jan, jul)
	for _, at := range changes {
		_, from := at.Add(-time.Minute).Zone()
		nm, to := at.Zone()
		kind := ical.CompTimezoneStandard
		if to == summer && jan != jul {
			kind = ical.CompTimezoneDaylight
		}
		local := at.In(time.FixedZone("", from))
		n := (local.Day()-1)/7 + 1
		if local.AddDate(0, 0, 7).Month() != local.Month() {
			n = -1
		}
		day := strings.ToUpper(local.Weekday().String()[:2])
		rule := fmt.Sprintf("FREQ=YEARLY;BYMONTH=%d;BYDAY=%d%s", int(local.Month()), n, day)
		tz.Children = append(tz.Children, part(kind, at, from, to, nm, rule))
	}
	return tz
}

// itipObject is an event as an invitation carries it: the method, the
// zones its times name, and its VEVENTs without the holder's own alarms.
func itipObject(cal *ical.Calendar, method string) ([]byte, error) {
	out := ical.NewCalendar()
	out.Props.SetText(ical.PropProductID, calProdID)
	out.Props.SetText(ical.PropVersion, "2.0")
	out.Props.SetText(ical.PropMethod, method)
	zones := map[string]bool{}
	var events []*ical.Component
	for _, ch := range cal.Children {
		if ch.Name != ical.CompEvent {
			continue
		}
		ev := &ical.Component{Name: ch.Name, Props: ical.Props{}}
		for k, v := range ch.Props {
			ev.Props[k] = slices.Clone(v)
		}
		for _, sub := range ch.Children {
			if sub.Name != ical.CompAlarm {
				ev.Children = append(ev.Children, sub)
			}
		}
		if method == "CANCEL" {
			ev.Props.SetText(ical.PropStatus, "CANCELLED")
		}
		for _, name := range []string{ical.PropDateTimeStart, ical.PropDateTimeEnd, ical.PropRecurrenceID} {
			if p := ev.Props.Get(name); p != nil {
				if tz := p.Params.Get(ical.ParamTimezoneID); tz != "" {
					zones[tz] = true
				}
			}
		}
		events = append(events, ev)
	}
	if len(events) == 0 {
		return nil, errNoSuchEvent
	}
	year := time.Now().Year()
	if p := events[0].Props.Get(ical.PropDateTimeStart); p != nil && len(p.Value) >= 4 {
		if y, err := strconv.Atoi(p.Value[:4]); err == nil {
			year = y
		}
	}
	for tz := range zones {
		if loc := zoneNamed(tz); loc != nil {
			out.Children = append(out.Children, vtimezone(loc, year))
		}
	}
	out.Children = append(out.Children, events...)
	return encodeCal(out)
}

// inviteWords is an invitation's subject and text: what, when and where.
func inviteWords(cal *ical.Calendar, kind, organizer string) (subject, text string) {
	master, _ := parts(cal)
	if master == nil {
		for _, ch := range cal.Children {
			if ch.Name == ical.CompEvent {
				master = ch
				break
			}
		}
	}
	ev, _, _, _ := newCalReader(cal, time.UTC).event(master)
	when := ev.Start.Format("Mon 2 Jan 2006")
	long := ev.Start.Format("Monday 2 January 2006")
	if ev.AllDay {
		long += ", all day"
	} else {
		when += " " + ev.Start.Format("15:04")
		long += ", " + ev.Start.Format("15:04") + "–" + ev.End.Format("15:04")
		if ev.Zone != "" {
			when += " (" + ev.Zone + ")"
			long += " (" + ev.Zone + ")"
		}
	}
	title := oneLine(nonEmptyText(ev.Summary, "An event"))
	subject = kind + ": " + title + " @ " + oneLine(when)
	text = organizer + " "
	switch kind {
	case "Cancelled":
		text += "has cancelled this event.\n\n"
	case "Updated invitation":
		text += "has changed this event.\n\n"
	default:
		text += "invites you to this event.\n\n"
	}
	text += title + "\n" + long + "\n"
	if ev.Location != "" {
		text += "Where: " + ev.Location + "\n"
	}
	if ev.Rule != "" {
		text += "It repeats.\n"
	}
	if kind != "Cancelled" {
		text += "\nYour calendar program shows Accept and Decline on this message.\n"
	}
	return subject, text
}

// oneLine is an event's title as a mail subject takes it: every line break
// and control character a space. writeHeaders refuses a header with a line
// break, so a title from an app, or from a stranger's invitation, that
// carried one would stop the invitation, the answer or the reminder going.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
}

func nonEmptyText(s, otherwise string) string {
	if strings.TrimSpace(s) == "" {
		return otherwise
	}
	return s
}

// sendInvitations tells an event's guests what a write did: those added are
// invited, those kept are sent the change, those removed (or all, when the
// event was deleted) are sent its cancellation. before and after are the
// stored object either side of the write, nil when there was none.
func (e *Engine) sendInvitations(ctx context.Context, mbox string, before, after []byte) error {
	decode := func(b []byte) *ical.Calendar {
		if b == nil {
			return nil
		}
		cal, err := ical.NewDecoder(bytes.NewReader(b)).Decode()
		if err != nil {
			return nil
		}
		return cal
	}
	bc, ac := decode(before), decode(after)
	var bm, am *ical.Component
	if bc != nil {
		bm, _ = parts(bc)
	}
	if ac != nil {
		am, _ = parts(ac)
	}
	for _, m := range []*ical.Component{bm, am} {
		if m != nil && !organizedBy(m, mbox) {
			return nil
		}
	}
	was, now := guestsOf(bm), guestsOf(am)
	var added, kept, removed []string
	for _, g := range now {
		if slices.Contains(was, g) {
			kept = append(kept, g)
		} else {
			added = append(added, g)
		}
	}
	for _, g := range was {
		if !slices.Contains(now, g) {
			removed = append(removed, g)
		}
	}
	var errs []error
	send := func(cal *ical.Calendar, method, kind string, to []string) {
		if len(to) == 0 || cal == nil {
			return
		}
		data, err := itipObject(cal, method)
		if err != nil {
			errs = append(errs, err)
			return
		}
		subject, text := inviteWords(cal, kind, mbox)
		if _, err := e.sendMail(ctx, mbox, to, subject, "", text, "", false, &calendarPart{method: method, data: data}); err != nil {
			errs = append(errs, err)
		}
	}
	send(ac, "REQUEST", "Invitation", added)
	send(ac, "REQUEST", "Updated invitation", kept)
	gone := bc
	if gone == nil {
		gone = ac
	}
	send(gone, "CANCEL", "Cancelled", removed)
	if len(errs) > 0 {
		return fmt.Errorf("%w: %w", ErrInvitationsNotSent, errors.Join(errs...))
	}
	return nil
}

// calendarPartOf is a message's iCalendar part and the method its
// Content-Type names: the first text/calendar (or .ics) part, at most a
// megabyte, within the depth every other walk of a message keeps to.
func calendarPartOf(raw []byte) (method string, data []byte) {
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return "", nil
	}
	var walk func(ctype, cte string, body io.Reader, depth int) bool
	walk = func(ctype, cte string, body io.Reader, depth int) bool {
		if depth > maxMIMEDepth {
			return false
		}
		mt, params, err := mime.ParseMediaType(ctype)
		if err != nil {
			return false
		}
		if strings.HasPrefix(mt, "multipart/") {
			mr := multipart.NewReader(body, params["boundary"])
			for {
				p, err := mr.NextPart()
				if err != nil {
					return false
				}
				if walk(p.Header.Get("Content-Type"), p.Header.Get("Content-Transfer-Encoding"), p, depth+1) {
					return true
				}
			}
		}
		if mt != "text/calendar" && mt != "application/ics" {
			return false
		}
		b := decodeBody(cte, io.LimitReader(body, maxDAVObject))
		method, data = strings.ToUpper(params["method"]), []byte(b)
		return true
	}
	walk(msg.Header.Get("Content-Type"), msg.Header.Get("Content-Transfer-Encoding"), msg.Body, 0)
	return method, data
}

// applyCalendarReply records a guest's answer to one of mbox's invitations,
// carried by an arriving message, and reports whether it was one: an
// iTIP REPLY from the guest it answers for, about an event mbox organizes.
func (e *Engine) applyCalendarReply(mbox string, raw []byte) bool {
	if e.accounts == nil {
		return false
	}
	_, data := calendarPartOf(raw)
	if data == nil {
		return false
	}
	reply, err := ical.NewDecoder(bytes.NewReader(data)).Decode()
	if err != nil || !strings.EqualFold(textOf(reply.Component, ical.PropMethod), "REPLY") {
		return false
	}
	from, err := netmail.ParseAddress(decodeMIMEWord(headerValue(raw, "From")))
	if err != nil {
		return false
	}
	var answer *ical.Component
	for _, ch := range reply.Children {
		if ch.Name == ical.CompEvent {
			answer = ch
			break
		}
	}
	if answer == nil {
		return false
	}
	attendees := answer.Props.Values(ical.PropAttendee)
	if len(attendees) != 1 || mailtoOf(attendees[0].Value) != normEmail(from.Address) {
		return false
	}
	guest := mailtoOf(attendees[0].Value)
	status := strings.ToUpper(attendees[0].Params.Get(ical.ParamParticipationStatus))
	if status != "ACCEPTED" && status != "DECLINED" && status != "TENTATIVE" {
		return false
	}
	ctx := context.Background()
	o, err := e.accounts.eventByUID(ctx, normEmail(mbox), textOf(answer, ical.PropUID))
	if err != nil {
		return false
	}
	cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
	if err != nil {
		return false
	}
	master, overrides := parts(cal)
	if master == nil || !organizedBy(master, mbox) || master.Props.Get(ical.PropOrganizer) == nil {
		return false
	}
	target := master
	if rid := answer.Props.Get(ical.PropRecurrenceID); rid != nil {
		c := newCalReader(cal, time.UTC)
		want, _, err := c.time(rid)
		if err != nil {
			return false
		}
		target = nil
		for _, ov := range overrides {
			if t, _, err := c.time(ov.Props.Get(ical.PropRecurrenceID)); err == nil && t.Equal(want) {
				target = ov
			}
		}
		if target == nil {
			// An answer for one occurrence nobody has changed: kept on the
			// series, which is what most programs do with it.
			target = master
		}
	}
	found := false
	props := target.Props[ical.PropAttendee]
	for i := range props {
		if mailtoOf(props[i].Value) == guest {
			props[i].Params.Set(ical.ParamParticipationStatus, status)
			props[i].Params.Del(ical.ParamRSVP)
			found = true
		}
	}
	if !found {
		return false
	}
	out, err := encodeCal(cal)
	if err != nil {
		return false
	}
	_, err = e.accounts.davPut(ctx, normEmail(mbox), davEvent, o.Name, textOf(master, ical.PropUID), out, davCondition{ifMatch: o.ETag})
	return err == nil
}

// ── Invitations the holder receives ─────────────────────────────────────────

// Invitation is an invitation a message carries, as the reader shows it.
// Nothing of it is in the holder's calendar until they answer it: anyone
// can send one, and a calendar that took them in unasked would be a way to
// put anything in front of the holder.
type Invitation struct {
	Method    string // REQUEST, or CANCEL for an event called off
	Event     CalEvent
	Organizer CalAttendee
	// Answer is the holder's answer so far, from their calendar: ACCEPTED,
	// TENTATIVE or DECLINED, "" when they have not answered.
	Answer string
	// Stored: the event is in the holder's calendar.
	Stored bool
}

// invitationOf reads the iTIP object a message carries: its method, the
// calendar and its event, when it is a REQUEST or a CANCEL from someone
// other than mbox about one event.
func invitationOf(raw []byte, mbox string) (string, *ical.Calendar, *ical.Component, bool) {
	_, data := calendarPartOf(raw)
	if data == nil {
		return "", nil, nil, false
	}
	cal, err := ical.NewDecoder(bytes.NewReader(data)).Decode()
	if err != nil {
		return "", nil, nil, false
	}
	method := strings.ToUpper(textOf(cal.Component, ical.PropMethod))
	if method != "REQUEST" && method != "CANCEL" {
		return "", nil, nil, false
	}
	var first *ical.Component
	for _, ch := range cal.Children {
		if ch.Name == ical.CompEvent {
			first = ch
			break
		}
	}
	if first == nil || textOf(first, ical.PropUID) == "" {
		return "", nil, nil, false
	}
	org := first.Props.Get(ical.PropOrganizer)
	if org == nil || mailtoOf(org.Value) == normEmail(mbox) {
		return "", nil, nil, false
	}
	return method, cal, first, true
}

// partStatOfSelf is mbox's own answer on an event, "" when it has none.
func partStatOfSelf(comp *ical.Component, mbox string) string {
	for _, p := range comp.Props.Values(ical.PropAttendee) {
		if mailtoOf(p.Value) == normEmail(mbox) {
			return strings.ToUpper(p.Params.Get(ical.ParamParticipationStatus))
		}
	}
	return ""
}

// InvitationIn is the invitation a message carries for rd's holder, read in
// their zone; ok is false when it carries none for them.
func (e *Engine) InvitationIn(rd Reader, raw []byte, home *time.Location) (Invitation, bool) {
	mbox, err := e.calendarOf(rd)
	if err != nil {
		return Invitation{}, false
	}
	method, cal, first, ok := invitationOf(raw, mbox)
	if !ok {
		return Invitation{}, false
	}
	ev, _, _, err := newCalReader(cal, home).event(first)
	if err != nil {
		return Invitation{}, false
	}
	inv := Invitation{Method: method, Event: ev, Organizer: ev.Organizer}
	if o, err := e.accounts.eventByUID(context.Background(), mbox, ev.UID); err == nil {
		inv.Stored = true
		if stored, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode(); err == nil {
			if m, _ := parts(stored); m != nil {
				inv.Answer = partStatOfSelf(m, mbox)
			}
		}
	}
	return inv, true
}

// ErrNotAnInvitation is an answer to a message that carries no invitation
// for the holder.
var ErrNotAnInvitation = errors.New("vayumail: this message carries no invitation for you")

// AnswerInvitation answers the invitation in rd's message folder/id:
// ACCEPTED, TENTATIVE or DECLINED. The event goes into the holder's calendar
// with their answer on it (a later version of it than the one stored
// replaces it; an earlier one changes only the answer), and the organizer is
// sent the answer from the holder's address.
func (e *Engine) AnswerInvitation(rd Reader, folder, id, answer string) error {
	mbox, err := e.calendarOf(rd)
	if err != nil {
		return err
	}
	answer = strings.ToUpper(answer)
	if answer != "ACCEPTED" && answer != "TENTATIVE" && answer != "DECLINED" {
		return errors.New("vayumail: an answer is accept, maybe or decline")
	}
	raw, err := e.ReadFolderMessage(rd, folder, id)
	if err != nil {
		return err
	}
	method, cal, first, ok := invitationOf(raw, mbox)
	if !ok || method != "REQUEST" {
		return ErrNotAnInvitation
	}
	ctx := context.Background()
	uid := textOf(first, ical.PropUID)
	seqOf := func(c *ical.Component) int {
		if p := c.Props.Get(ical.PropSequence); p != nil {
			n, _ := p.Int()
			return n
		}
		return 0
	}
	setSelf := func(c *ical.Component) {
		props := c.Props[ical.PropAttendee]
		for i := range props {
			if mailtoOf(props[i].Value) == mbox {
				props[i].Params.Set(ical.ParamParticipationStatus, answer)
				props[i].Params.Del(ical.ParamRSVP)
				return
			}
		}
		p := ical.NewProp(ical.PropAttendee)
		p.Value = "mailto:" + mbox
		p.Params.Set(ical.ParamParticipationStatus, answer)
		c.Props.Add(p)
	}
	// The calendar's copy: the invitation without its method.
	keep := ical.NewCalendar()
	keep.Props.SetText(ical.PropProductID, calProdID)
	keep.Props.SetText(ical.PropVersion, "2.0")
	for _, ch := range cal.Children {
		if ch.Name == ical.CompEvent || ch.Name == ical.CompTimezone {
			keep.Children = append(keep.Children, ch)
		}
	}
	name, cond := "", davCondition{ifNoneMatch: "*"}
	if o, err := e.accounts.eventByUID(ctx, mbox, uid); err == nil {
		name, cond = o.Name, davCondition{ifMatch: o.ETag}
		if stored, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode(); err == nil {
			if m, _ := parts(stored); m != nil && seqOf(m) > seqOf(first) {
				keep = stored // a later version is already here
			}
		}
	} else {
		id, err := randomHex(16)
		if err != nil {
			return err
		}
		name = id + ".ics"
	}
	for _, ch := range keep.Children {
		if ch.Name == ical.CompEvent {
			setSelf(ch)
		}
	}
	data, err := encodeCal(keep)
	if err != nil {
		return err
	}
	if _, err := e.accounts.davPut(ctx, mbox, davEvent, name, uid, data, cond); err != nil {
		return err
	}
	// The answer: the event named, and the holder alone, with their answer.
	reply := ical.NewComponent(ical.CompEvent)
	for _, n := range []string{ical.PropUID, ical.PropSequence, ical.PropDateTimeStart, ical.PropDateTimeEnd, ical.PropRecurrenceID, ical.PropSummary, ical.PropOrganizer} {
		if p := first.Props.Get(n); p != nil {
			reply.Props.Set(p)
		}
	}
	reply.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC().Truncate(time.Second))
	me := ical.NewProp(ical.PropAttendee)
	me.Value = "mailto:" + mbox
	me.Params.Set(ical.ParamParticipationStatus, answer)
	reply.Props.Add(me)
	out := ical.NewCalendar()
	out.Children = []*ical.Component{reply}
	obj, err := itipObject(out, "REPLY")
	if err != nil {
		return err
	}
	word := map[string]string{"ACCEPTED": "Accepted", "TENTATIVE": "Maybe", "DECLINED": "Declined"}[answer]
	title := oneLine(nonEmptyText(textOf(first, ical.PropSummary), "your event"))
	text := mbox + " " + map[string]string{"ACCEPTED": "is going to", "TENTATIVE": "may go to", "DECLINED": "is not going to"}[answer] + " " + title + ".\n"
	_, err = e.sendMail(ctx, mbox, []string{mailtoOf(first.Props.Get(ical.PropOrganizer).Value)}, word+": "+title, "", text, "", false, &calendarPart{method: "REPLY", data: obj})
	return err
}

// RemoveCancelled takes out of rd's calendar the event (or the occurrence)
// the cancellation in folder/id calls off, when it came from the event's
// own organizer.
func (e *Engine) RemoveCancelled(rd Reader, folder, id string) error {
	mbox, err := e.calendarOf(rd)
	if err != nil {
		return err
	}
	raw, err := e.ReadFolderMessage(rd, folder, id)
	if err != nil {
		return err
	}
	method, cal, first, ok := invitationOf(raw, mbox)
	if !ok || method != "CANCEL" {
		return ErrNotAnInvitation
	}
	from, err := netmail.ParseAddress(decodeMIMEWord(headerValue(raw, "From")))
	organizer := mailtoOf(first.Props.Get(ical.PropOrganizer).Value)
	if err != nil || normEmail(from.Address) != organizer {
		return errors.New("vayumail: this cancellation is not from the event's organizer")
	}
	ctx := context.Background()
	o, err := e.accounts.eventByUID(ctx, mbox, textOf(first, ical.PropUID))
	if err != nil {
		return err
	}
	stored, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
	if err != nil {
		return err
	}
	master, _ := parts(stored)
	if master == nil || mailtoOf(textOf(master, ical.PropOrganizer)) != organizer {
		return errors.New("vayumail: this cancellation is not from the event's organizer")
	}
	rid := first.Props.Get(ical.PropRecurrenceID)
	if rid == nil {
		return e.accounts.davDelete(ctx, mbox, davEvent, o.Name, davCondition{ifMatch: o.ETag})
	}
	// One occurrence called off: an exception date on the series.
	c := newCalReader(cal, time.UTC)
	at, _, err := c.time(rid)
	if err != nil {
		return err
	}
	ex := ical.NewProp(ical.PropExceptionDates)
	newCalReader(stored, time.UTC).setInstant(ex, master, at)
	master.Props.Add(ex)
	data, err := encodeCal(stored)
	if err != nil {
		return err
	}
	_, err = e.accounts.davPut(ctx, mbox, davEvent, o.Name, textOf(master, ical.PropUID), data, davCondition{ifMatch: o.ETag})
	return err
}
