// SPDX-License-Identifier: Apache-2.0

package mail

// calendar.go — the holder's calendar as the console shows and edits it.
//
// It reads and writes the iCalendar objects CalDAV keeps (dav.go), so an
// event made on a phone is in the console and one made in the console is on
// the phone. Nothing is converted into a second model: an edit changes the
// properties the editor has fields for and keeps every other one as the app
// wrote it.
//
// Repeating events are expanded as RFC 5545 has it: RRULE, RDATE and EXDATE
// in the event's own time zone, so "every Tuesday 09:00 Europe/London" stays
// at 09:00 London across a clock change; DTSTART is always the first
// instance; an occurrence edited on its own is a VEVENT with a RECURRENCE-ID
// that replaces the one it names. Parsing and rule expansion are go-ical and
// rrule-go (both MIT) behind these types; the rest is here because it is
// where calendars go wrong, and each part has its own test.
//
// A calendar is its holder's alone. An administrator can suspend or delete a
// mailbox and never opens its calendar, so calendarOf refuses an operator
// Reader whether or not the mailbox is private.

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	// The zone database is in the binary: a server without /usr/share/zoneinfo
	// (a minimal container) would otherwise put every event in UTC.
	_ "time/tzdata"

	"github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"
)

// CalAttendee is one guest of an event.
type CalAttendee struct {
	Email, Name string
	Status      string // NEEDS-ACTION, ACCEPTED, TENTATIVE or DECLINED
}

// CalEvent is one occurrence of an event as the calendar shows it.
type CalEvent struct {
	UID                            string
	Summary, Location, Description string
	Start, End                     time.Time
	AllDay                         bool
	// Zone is the event's own time zone, "" for an all-day or floating event.
	Zone string
	// Rule is the RRULE as written, "" for an event that does not repeat.
	Rule string
	// Instance is the original start of this occurrence of a repeating
	// event (its RECURRENCE-ID), zero for one that does not repeat.
	Instance  time.Time
	Organizer CalAttendee
	Attendees []CalAttendee
	Sequence  int
}

// Repeats reports whether the occurrence belongs to a repeating series.
func (ev CalEvent) Repeats() bool { return !ev.Instance.IsZero() }

// ErrCalendarHolderOnly is a calendar opened by anyone but its holder.
var ErrCalendarHolderOnly = errors.New("vayumail: a calendar is opened only by its holder")

var errNoSuchEvent = errors.New("vayumail: there is no such event")

const (
	// maxEventSteps and maxCalendarSteps bound the occurrences one view
	// walks, for one event and for all of them. A rule is walked from its
	// first instance, and CalDAV takes whatever an app writes: an
	// every-minute rule from 1970 would otherwise be millions of steps on
	// each page. Per event, so one such rule cannot starve the holder's
	// other events; 50,000 is a daily event for 136 years. Past the bound a
	// series shows what it reached.
	maxEventSteps    = 50000
	maxCalendarSteps = 1000000
	// maxCalendarSpan is the widest window one view reads.
	maxCalendarSpan = 400 * 24 * time.Hour
	calProdID       = "-//VayuPress//VayuMail//EN"
)

// calendarOf is the mailbox whose calendar rd opens: its holder's own, or
// the product's on its behalf (reminders, invitations), never an
// administrator's.
func (e *Engine) calendarOf(rd Reader) (string, error) {
	if rd.kind != readerOwner && rd.kind != readerSystem {
		return "", ErrCalendarHolderOnly
	}
	if e.accounts == nil {
		return "", errors.New("vayumail: no account store")
	}
	dom, local := e.mailboxKey(rd.Key())
	return normEmail(local + "@" + dom), nil
}

// windowsZones maps the zone names a desktop mail program writes as TZID to
// the zone database's, for the zones most calendars use. From CLDR's
// windowsZones (Unicode licence), territory 001.
var windowsZones = map[string]string{
	"UTC": "UTC", "GMT Standard Time": "Europe/London", "W. Europe Standard Time": "Europe/Berlin",
	"Romance Standard Time": "Europe/Paris", "Central Europe Standard Time": "Europe/Budapest",
	"Central European Standard Time": "Europe/Warsaw", "E. Europe Standard Time": "Europe/Chisinau",
	"FLE Standard Time": "Europe/Kiev", "GTB Standard Time": "Europe/Bucharest",
	"Russian Standard Time": "Europe/Moscow", "Turkey Standard Time": "Europe/Istanbul",
	"Eastern Standard Time": "America/New_York", "Central Standard Time": "America/Chicago",
	"Mountain Standard Time": "America/Denver", "US Mountain Standard Time": "America/Phoenix",
	"Pacific Standard Time": "America/Los_Angeles", "Alaskan Standard Time": "America/Anchorage",
	"Hawaiian Standard Time": "Pacific/Honolulu", "Atlantic Standard Time": "America/Halifax",
	"E. South America Standard Time": "America/Sao_Paulo", "Argentina Standard Time": "America/Buenos_Aires",
	"SA Pacific Standard Time": "America/Bogota", "Central Standard Time (Mexico)": "America/Mexico_City",
	"India Standard Time": "Asia/Kolkata", "China Standard Time": "Asia/Shanghai",
	"Tokyo Standard Time": "Asia/Tokyo", "Korea Standard Time": "Asia/Seoul",
	"Singapore Standard Time": "Asia/Singapore", "SE Asia Standard Time": "Asia/Bangkok",
	"Arabian Standard Time": "Asia/Dubai", "Arab Standard Time": "Asia/Riyadh",
	"Israel Standard Time": "Asia/Jerusalem", "Pakistan Standard Time": "Asia/Karachi",
	"Bangladesh Standard Time": "Asia/Dhaka", "Nepal Standard Time": "Asia/Kathmandu",
	"AUS Eastern Standard Time": "Australia/Sydney", "E. Australia Standard Time": "Australia/Brisbane",
	"Cen. Australia Standard Time": "Australia/Adelaide", "W. Australia Standard Time": "Australia/Perth",
	"New Zealand Standard Time": "Pacific/Auckland", "South Africa Standard Time": "Africa/Johannesburg",
	"Egypt Standard Time": "Africa/Cairo", "W. Central Africa Standard Time": "Africa/Lagos",
	"E. Africa Standard Time": "Africa/Nairobi",
}

// zoneNamed is the zone database's zone for a TZID, or nil. "Local" is
// refused: it is the server's own zone, which no event means.
func zoneNamed(tzid string) *time.Location {
	tzid = strings.Trim(strings.TrimSpace(tzid), `"`)
	if tzid == "" || tzid == "Local" {
		return nil
	}
	if loc, err := time.LoadLocation(tzid); err == nil {
		return loc
	}
	// Some programs prefix the name with their own path:
	// /vendor.example/20050126_1/America/Argentina/Buenos_Aires.
	parts := strings.Split(tzid, "/")
	for i := len(parts) - 2; i > 0; i-- {
		if loc, err := time.LoadLocation(strings.Join(parts[i:], "/")); err == nil {
			return loc
		}
	}
	if name, ok := windowsZones[tzid]; ok {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	return nil
}

// calReader reads one stored object's events in the holder's zone.
type calReader struct {
	home  *time.Location
	vtz   map[string]*ical.Component
	zones map[string]*time.Location
}

func newCalReader(cal *ical.Calendar, home *time.Location) *calReader {
	c := &calReader{home: home, vtz: map[string]*ical.Component{}, zones: map[string]*time.Location{}}
	for _, ch := range cal.Children {
		if ch.Name == ical.CompTimezone {
			if id, err := ch.Props.Text(ical.PropTimezoneID); err == nil {
				c.vtz[id] = ch
			}
		}
	}
	return c
}

// zone is a TZID's zone: the zone database's when the name is one it knows,
// else the standard offset the object's own VTIMEZONE gives (a zone with no
// known name keeps its offset and loses its clock changes), else the
// holder's.
func (c *calReader) zone(tzid string) *time.Location {
	if loc, ok := c.zones[tzid]; ok {
		return loc
	}
	loc := zoneNamed(tzid)
	if loc == nil {
		if tz := c.vtz[tzid]; tz != nil {
			for _, part := range tz.Children {
				if part.Name != ical.CompTimezoneStandard {
					continue
				}
				if p := part.Props.Get(ical.PropTimezoneOffsetTo); p != nil {
					if off, ok := utcOffset(p.Value); ok {
						loc = time.FixedZone(tzid, off)
					}
				}
				break
			}
		}
	}
	if loc == nil {
		loc = c.home
	}
	c.zones[tzid] = loc
	return loc
}

// utcOffset reads an iCalendar UTC offset, +HHMM or -HHMM(SS).
func utcOffset(v string) (int, bool) {
	v = strings.TrimSpace(v)
	if len(v) < 5 || (v[0] != '+' && v[0] != '-') {
		return 0, false
	}
	h, err1 := strconv.Atoi(v[1:3])
	m, err2 := strconv.Atoi(v[3:5])
	if err1 != nil || err2 != nil {
		return 0, false
	}
	off := h*3600 + m*60
	if v[0] == '-' {
		off = -off
	}
	return off, true
}

// times reads a date-time property's values: a date (all day), a UTC time,
// a time in its TZID's zone, or a floating time, read in the holder's zone.
// EXDATE and RDATE carry lists; an RDATE period counts from its start.
func (c *calReader) times(p *ical.Prop) (ts []time.Time, allDay bool, err error) {
	loc := c.home
	if tz := p.Params.Get(ical.ParamTimezoneID); tz != "" {
		loc = c.zone(tz)
	}
	for _, v := range strings.Split(p.Value, ",") {
		v = strings.TrimSpace(v)
		if i := strings.IndexByte(v, '/'); i >= 0 {
			v = v[:i]
		}
		var t time.Time
		switch {
		case p.ValueType() == ical.ValueDate || len(v) == len("20060102"):
			t, err = time.ParseInLocation("20060102", v, c.home)
			allDay = true
		case strings.HasSuffix(v, "Z"):
			t, err = time.Parse("20060102T150405Z", v)
		default:
			t, err = time.ParseInLocation("20060102T150405", v, loc)
		}
		if err != nil {
			return nil, false, err
		}
		ts = append(ts, t)
	}
	if len(ts) == 0 {
		return nil, false, errors.New("vayumail: a date property with no value")
	}
	return ts, allDay, nil
}

func (c *calReader) time(p *ical.Prop) (time.Time, bool, error) {
	if p == nil {
		return time.Time{}, false, errors.New("vayumail: an event with no start")
	}
	ts, allDay, err := c.times(p)
	if err != nil {
		return time.Time{}, false, err
	}
	return ts[0], allDay, nil
}

// calPerson reads an ORGANIZER or ATTENDEE.
func calPerson(p ical.Prop) CalAttendee {
	addr := strings.TrimSpace(p.Value)
	if len(addr) > 7 && strings.EqualFold(addr[:7], "mailto:") {
		addr = addr[7:]
	}
	return CalAttendee{Email: normEmail(addr), Name: p.Params.Get(ical.ParamCommonName), Status: strings.ToUpper(p.Params.Get(ical.ParamParticipationStatus))}
}

// event reads one VEVENT. span is how long each occurrence lasts; days is
// that span in calendar days for an all-day event, which is counted in days
// so a clock change inside it does not move its end.
func (c *calReader) event(comp *ical.Component) (ev CalEvent, span time.Duration, days int, err error) {
	text := func(name string) string { s, _ := comp.Props.Text(name); return s }
	ev = CalEvent{UID: text(ical.PropUID), Summary: text(ical.PropSummary), Location: text(ical.PropLocation), Description: text(ical.PropDescription)}
	startProp := comp.Props.Get(ical.PropDateTimeStart)
	if ev.Start, ev.AllDay, err = c.time(startProp); err != nil {
		return ev, 0, 0, err
	}
	switch {
	case comp.Props.Get(ical.PropDateTimeEnd) != nil:
		if ev.End, _, err = c.time(comp.Props.Get(ical.PropDateTimeEnd)); err != nil {
			return ev, 0, 0, err
		}
	case comp.Props.Get(ical.PropDuration) != nil:
		d, derr := comp.Props.Get(ical.PropDuration).Duration()
		if derr != nil {
			return ev, 0, 0, derr
		}
		ev.End = ev.Start.Add(d)
	case ev.AllDay:
		ev.End = ev.Start.AddDate(0, 0, 1)
	default:
		ev.End = ev.Start
	}
	if ev.End.Before(ev.Start) {
		ev.End = ev.Start
	}
	if !ev.AllDay && startProp.Params.Get(ical.ParamTimezoneID) != "" {
		ev.Zone = ev.Start.Location().String()
	} else if !ev.AllDay && strings.HasSuffix(startProp.Value, "Z") {
		ev.Zone = "UTC"
	}
	if p := comp.Props.Get(ical.PropRecurrenceRule); p != nil {
		ev.Rule = p.Value
	}
	if p := comp.Props.Get(ical.PropOrganizer); p != nil {
		ev.Organizer = calPerson(*p)
	}
	for _, p := range comp.Props.Values(ical.PropAttendee) {
		ev.Attendees = append(ev.Attendees, calPerson(p))
	}
	if p := comp.Props.Get(ical.PropSequence); p != nil {
		ev.Sequence, _ = p.Int()
	}
	if ev.AllDay {
		days = int(ev.End.Sub(ev.Start).Round(24*time.Hour) / (24 * time.Hour))
	}
	return ev, ev.End.Sub(ev.Start), days, nil
}

func cancelled(comp *ical.Component) bool {
	s, _ := comp.Props.Text(ical.PropStatus)
	return strings.EqualFold(s, "CANCELLED")
}

// overlaps reports whether an occurrence is in [from, to). One with no
// length is in the window when it starts in it.
func overlaps(start, end, from, to time.Time) bool {
	if !start.Before(to) {
		return false
	}
	if end.Equal(start) {
		return !start.Before(from)
	}
	return end.After(from)
}

// occurrences is the stored object's events in [from, to), spending at
// most *budget steps of expansion.
func (c *calReader) occurrences(cal *ical.Calendar, from, to time.Time, budget *int) []CalEvent {
	var master *ical.Component
	overrides := map[int64]*ical.Component{}
	for _, ch := range cal.Children {
		if ch.Name != ical.CompEvent {
			continue
		}
		if rid := ch.Props.Get(ical.PropRecurrenceID); rid != nil {
			if t, _, err := c.time(rid); err == nil {
				overrides[t.Unix()] = ch
			}
			continue
		}
		if master == nil {
			master = ch
		}
	}
	var out []CalEvent
	// An occurrence edited on its own stands for the one it names, wherever
	// it has been moved to.
	for unix, ov := range overrides {
		ev, _, _, err := c.event(ov)
		if err != nil || cancelled(ov) || !overlaps(ev.Start, ev.End, from, to) {
			continue
		}
		ev.Instance = time.Unix(unix, 0).In(ev.Start.Location())
		if master != nil {
			ev.Rule = textOf(master, ical.PropRecurrenceRule)
		}
		out = append(out, ev)
	}
	if master == nil || cancelled(master) {
		return out
	}
	base, span, days, err := c.event(master)
	if err != nil {
		return out
	}
	end := func(t time.Time) time.Time {
		if base.AllDay {
			return t.AddDate(0, 0, days)
		}
		return t.Add(span)
	}
	set, err := c.recurrence(master, base)
	if err != nil || set == nil {
		*budget--
		if overrides[base.Start.Unix()] == nil && overlaps(base.Start, base.End, from, to) {
			out = append(out, base)
		}
		return out
	}
	next := set.Iterator()
	for steps := 0; *budget > 0 && steps < maxEventSteps; steps++ {
		*budget--
		t, ok := next()
		if !ok || !t.Before(to) {
			break
		}
		if overrides[t.Unix()] != nil {
			continue
		}
		occ := base
		occ.Instance, occ.Start, occ.End = t, t, end(t)
		if overlaps(occ.Start, occ.End, from, to) {
			out = append(out, occ)
		}
	}
	return out
}

func textOf(comp *ical.Component, name string) string {
	if p := comp.Props.Get(name); p != nil {
		return p.Value
	}
	return ""
}

// recurrence is a repeating event's set of starts, nil for one that does not
// repeat. The rule runs in the start's own zone, which is what keeps a
// local time across clock changes. An RRULE that cannot be read leaves the
// event as its first instance rather than hiding it.
func (c *calReader) recurrence(master *ical.Component, base CalEvent) (*rrule.Set, error) {
	rp := master.Props.Get(ical.PropRecurrenceRule)
	rdates := master.Props.Values(ical.PropRecurrenceDates)
	if rp == nil && len(rdates) == 0 {
		return nil, nil
	}
	set := &rrule.Set{}
	if rp != nil {
		opt, err := rrule.StrToROptionInLocation(rp.Value, base.Start.Location())
		if err != nil {
			return nil, err
		}
		opt.Dtstart = base.Start
		rule, err := rrule.NewRRule(*opt)
		if err != nil {
			return nil, err
		}
		set.RRule(rule)
		// DTSTART is the first instance even when the rule would not make it
		// (RFC 5545 §3.8.5.3); the set drops the duplicate when it would.
		if first, ok := rule.Iterator()(); !ok || !first.Equal(base.Start) {
			set.RDate(base.Start)
		}
	} else {
		set.RDate(base.Start)
	}
	for _, p := range rdates {
		ts, _, err := c.times(&p)
		if err != nil {
			continue
		}
		for _, t := range ts {
			set.RDate(t)
		}
	}
	for _, p := range master.Props.Values(ical.PropExceptionDates) {
		ts, _, err := c.times(&p)
		if err != nil {
			continue
		}
		for _, t := range ts {
			set.ExDate(t)
		}
	}
	return set, nil
}

// CalendarBetween is every occurrence in the holder's calendar that falls in
// [from, to), earliest first, with all-day events before timed ones on the
// same start. home is the holder's zone, in which dates and floating times
// are read. An entry the console cannot read is left out of the view and
// kept as its app wrote it.
func (e *Engine) CalendarBetween(rd Reader, from, to time.Time, home *time.Location) ([]CalEvent, error) {
	mbox, err := e.calendarOf(rd)
	if err != nil {
		return nil, err
	}
	if !to.After(from) || to.Sub(from) > maxCalendarSpan {
		return nil, errors.New("vayumail: a calendar view is at most 400 days")
	}
	if home == nil {
		home = time.UTC
	}
	objs, err := e.accounts.davList(context.Background(), mbox, davEvent)
	if err != nil {
		return nil, err
	}
	budget := maxCalendarSteps
	var out []CalEvent
	for _, o := range objs {
		cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
		if err != nil {
			continue
		}
		out = append(out, newCalReader(cal, home).occurrences(cal, from, to, &budget)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		if out[i].AllDay != out[j].AllDay {
			return out[i].AllDay
		}
		return out[i].Summary < out[j].Summary
	})
	return out, nil
}

// ── Writing ───────────────────────────────────────────────────────────────────

// EventInput is what the editor saves.
type EventInput struct {
	UID                            string // "" for a new event
	Summary, Location, Description string
	// Start and End are in Zone; for an all-day event, midnight of the first
	// day and of the day after the last.
	Start, End time.Time
	AllDay     bool
	// Rule is an RRULE value, "" for an event that does not repeat.
	Rule string
	// Guests are the addresses invited: nil keeps the event's guests as
	// they are, an empty list has none (itip.go sends them the event).
	Guests []string
}

// The parts of a repeating event an edit or a delete is for.
const (
	ScopeAll       = "all"
	ScopeOne       = "one"
	ScopeFollowing = "following"
)

func (in *EventInput) check() error {
	in.Summary = strings.TrimSpace(in.Summary)
	if in.Summary == "" || len(in.Summary) > 300 {
		return errors.New("an event needs a title, at most 300 characters")
	}
	if len(in.Location) > 500 || len(in.Description) > 20000 {
		return errors.New("a location is at most 500 characters, and notes at most 20,000")
	}
	if in.Start.IsZero() || in.End.Before(in.Start) {
		return errors.New("an event ends after it starts")
	}
	if in.AllDay && !in.End.After(in.Start) {
		return errors.New("an all-day event lasts at least a day")
	}
	in.Rule = strings.TrimPrefix(strings.TrimSpace(in.Rule), "RRULE:")
	if in.Rule != "" {
		opt, err := rrule.StrToROption(in.Rule)
		if err != nil {
			return errors.New("the repeat cannot be read")
		}
		// The editor offers days and longer; a rule finer than that, from a
		// form, is not a calendar entry.
		if opt.Freq == rrule.HOURLY || opt.Freq == rrule.MINUTELY || opt.Freq == rrule.SECONDLY {
			return errors.New("an event repeats at most daily")
		}
	}
	return nil
}

// eventByUID is the stored object holding uid.
func (s *AccountStore) eventByUID(ctx context.Context, mailbox, uid string) (davObject, error) {
	var o davObject
	err := s.db.QueryRowContext(ctx, `SELECT name,etag,data,modified FROM vayumail_dav WHERE mailbox=? AND kind=? AND uid=?`,
		mailbox, davEvent, uid).Scan(&o.Name, &o.ETag, &o.Data, &o.Modified)
	if errors.Is(err, sql.ErrNoRows) {
		return o, errNoSuchEvent
	}
	return o, err
}

// setWhen writes an event's start and end, in its zone or as dates.
func setWhen(comp *ical.Component, in EventInput) {
	comp.Props.Del(ical.PropDuration)
	start, end := ical.NewProp(ical.PropDateTimeStart), ical.NewProp(ical.PropDateTimeEnd)
	if in.AllDay {
		start.SetDate(in.Start)
		end.SetDate(in.End)
	} else {
		start.SetDateTime(in.Start)
		end.SetDateTime(in.End)
	}
	comp.Props.Set(start)
	comp.Props.Set(end)
}

// setInstant writes t in the form the series' DTSTART has (a date, UTC, a
// time in its TZID's zone, or a floating time), as a RECURRENCE-ID or an
// EXDATE must be to name an occurrence.
func (c *calReader) setInstant(p *ical.Prop, master *ical.Component, t time.Time) {
	ds := master.Props.Get(ical.PropDateTimeStart)
	switch {
	case ds != nil && (ds.ValueType() == ical.ValueDate || len(ds.Value) == len("20060102")):
		p.SetDate(t.In(c.home))
	case ds != nil && strings.HasSuffix(ds.Value, "Z"):
		p.SetDateTime(t.UTC())
	case ds != nil && ds.Params.Get(ical.ParamTimezoneID) != "":
		tz := ds.Params.Get(ical.ParamTimezoneID)
		p.Params.Set(ical.ParamTimezoneID, tz)
		p.SetValueType(ical.ValueDateTime)
		p.Value = t.In(c.zone(tz)).Format("20060102T150405")
	default:
		p.SetValueType(ical.ValueDateTime)
		p.Value = t.In(c.home).Format("20060102T150405")
	}
}

func setText(comp *ical.Component, name, v string) {
	if strings.TrimSpace(v) == "" {
		comp.Props.Del(name)
		return
	}
	comp.Props.SetText(name, v)
}

// fill writes the editor's fields onto a VEVENT, keeping every property the
// editor has no field for. An edit raises SEQUENCE, which is 0 when absent
// (RFC 5545 §3.8.7.4): it is how a guest's calendar tells a change from a
// copy it already has.
func fill(comp *ical.Component, in EventInput, now time.Time, edit bool) {
	comp.Props.SetText(ical.PropSummary, in.Summary)
	setText(comp, ical.PropLocation, in.Location)
	setText(comp, ical.PropDescription, in.Description)
	setWhen(comp, in)
	comp.Props.SetDateTime(ical.PropDateTimeStamp, now)
	comp.Props.SetDateTime(ical.PropLastModified, now)
	seq := 0
	if p := comp.Props.Get(ical.PropSequence); p != nil {
		seq, _ = p.Int()
	}
	if edit {
		seq++
	}
	sp := ical.NewProp(ical.PropSequence)
	sp.Value = strconv.Itoa(seq)
	comp.Props.Set(sp)
}

func encodeCal(cal *ical.Calendar) ([]byte, error) {
	var b bytes.Buffer
	if err := ical.NewEncoder(&b).Encode(cal); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// parts is a stored object's series and the occurrences edited on their own.
func parts(cal *ical.Calendar) (master *ical.Component, overrides []*ical.Component) {
	for _, ch := range cal.Children {
		if ch.Name != ical.CompEvent {
			continue
		}
		if ch.Props.Get(ical.PropRecurrenceID) != nil {
			overrides = append(overrides, ch)
		} else if master == nil {
			master = ch
		}
	}
	return master, overrides
}

// SaveEvent keeps an event from the editor and returns its UID. A new event
// (no UID) is made; for a repeating one, scope says whether the edit is the
// whole series (ScopeAll), the occurrence that started at instance alone
// (ScopeOne), or that occurrence and every one after it (ScopeFollowing,
// which ends the series before it and starts a new one, as RFC 5545 has
// it). The write fails rather than overwrite a change an app made since the
// event was read.
func (e *Engine) SaveEvent(rd Reader, in EventInput, scope string, instance time.Time) (string, error) {
	mbox, err := e.calendarOf(rd)
	if err != nil {
		return "", err
	}
	if err := in.check(); err != nil {
		return "", err
	}
	if in.Guests != nil {
		if in.Guests, err = cleanGuests(in.Guests, mbox); err != nil {
			return "", err
		}
	}
	ctx, now := context.Background(), time.Now().UTC().Truncate(time.Second)
	if in.UID == "" {
		return e.newEvent(ctx, mbox, in, now)
	}
	o, err := e.accounts.eventByUID(ctx, mbox, in.UID)
	if err != nil {
		return "", err
	}
	cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
	if err != nil {
		return "", err
	}
	master, overrides := parts(cal)
	if master == nil {
		return "", errNoSuchEvent
	}
	repeating := master.Props.Get(ical.PropRecurrenceRule) != nil || master.Props.Get(ical.PropRecurrenceDates) != nil
	if !repeating || instance.IsZero() {
		scope = ScopeAll
	}
	switch scope {
	case ScopeOne:
		var ov *ical.Component
		for _, c := range overrides {
			if t, _, err := newCalReader(cal, in.Start.Location()).time(c.Props.Get(ical.PropRecurrenceID)); err == nil && t.Equal(instance) {
				ov = c
			}
		}
		if ov == nil {
			ov = ical.NewComponent(ical.CompEvent)
			ov.Props.SetText(ical.PropUID, in.UID)
			// The occurrence has the series' organizer and guests, each
			// with their answer so far.
			for _, name := range []string{ical.PropOrganizer, ical.PropAttendee} {
				for _, p := range master.Props.Values(name) {
					ov.Props.Add(&p)
				}
			}
			rid := ical.NewProp(ical.PropRecurrenceID)
			newCalReader(cal, in.Start.Location()).setInstant(rid, master, instance)
			ov.Props.Set(rid)
			cal.Children = append(cal.Children, ov)
		}
		in.Rule = ""
		fill(ov, in, now, true)
	case ScopeFollowing:
		first, _, err := newCalReader(cal, in.Start.Location()).time(master.Props.Get(ical.PropDateTimeStart))
		if err == nil && !instance.After(first) {
			return e.SaveEvent(rd, in, ScopeAll, time.Time{})
		}
		if err := endSeriesBefore(master, instance); err != nil {
			return "", err
		}
		data, err := encodeCal(cal)
		if err != nil {
			return "", err
		}
		if _, err := e.accounts.davPut(ctx, mbox, davEvent, o.Name, in.UID, data, davCondition{ifMatch: o.ETag}); err != nil {
			return "", err
		}
		sent := e.sendInvitations(ctx, mbox, o.Data, data)
		if in.Guests == nil {
			in.Guests = guestsOf(master)
		}
		in.UID = ""
		uid, err := e.newEvent(ctx, mbox, in, now)
		if err == nil {
			err = sent
		}
		return uid, err
	default:
		if in.Guests != nil && !slices.Equal(in.Guests, guestsOf(master)) {
			if !organizedBy(master, mbox) {
				return "", ErrNotOrganizer
			}
			setGuests(master, mbox, in.Guests)
		}
		fill(master, in, now, true)
		if in.Rule == "" {
			master.Props.Del(ical.PropRecurrenceRule)
		} else {
			rp := ical.NewProp(ical.PropRecurrenceRule)
			rp.SetValueType(ical.ValueRecurrence)
			rp.Value = in.Rule
			master.Props.Set(rp)
		}
	}
	data, err := encodeCal(cal)
	if err != nil {
		return "", err
	}
	if _, err := e.accounts.davPut(ctx, mbox, davEvent, o.Name, in.UID, data, davCondition{ifMatch: o.ETag}); err != nil {
		return "", err
	}
	return in.UID, e.sendInvitations(ctx, mbox, o.Data, data)
}

// endSeriesBefore ends a series' rule just before the occurrence at t: UNTIL
// in UTC (RFC 5545 asks for UTC when DTSTART has a zone), and no COUNT.
func endSeriesBefore(master *ical.Component, t time.Time) error {
	rp := master.Props.Get(ical.PropRecurrenceRule)
	if rp == nil {
		return nil
	}
	var kept []string
	for _, part := range strings.Split(rp.Value, ";") {
		key := strings.ToUpper(strings.SplitN(part, "=", 2)[0])
		if key != "UNTIL" && key != "COUNT" && part != "" {
			kept = append(kept, part)
		}
	}
	until := t.Add(-time.Second).UTC().Format("20060102T150405Z")
	if ds := master.Props.Get(ical.PropDateTimeStart); ds != nil && len(ds.Value) == len("20060102") {
		until = t.AddDate(0, 0, -1).Format("20060102")
	}
	rp.Value = strings.Join(append(kept, "UNTIL="+until), ";")
	return nil
}

func (e *Engine) newEvent(ctx context.Context, mbox string, in EventInput, now time.Time) (string, error) {
	id, err := randomHex(16)
	if err != nil {
		return "", err
	}
	_, dom := splitAddress(mbox)
	uid := id + "@" + dom
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropProductID, calProdID)
	cal.Props.SetText(ical.PropVersion, "2.0")
	ev := ical.NewComponent(ical.CompEvent)
	ev.Props.SetText(ical.PropUID, uid)
	ev.Props.SetDateTime(ical.PropCreated, now)
	fill(ev, in, now, false)
	if len(in.Guests) > 0 {
		setGuests(ev, mbox, in.Guests)
	}
	if in.Rule != "" {
		rp := ical.NewProp(ical.PropRecurrenceRule)
		rp.SetValueType(ical.ValueRecurrence)
		rp.Value = in.Rule
		ev.Props.Set(rp)
	}
	cal.Children = append(cal.Children, ev)
	data, err := encodeCal(cal)
	if err != nil {
		return "", err
	}
	if _, err := e.accounts.davPut(ctx, mbox, davEvent, id+".ics", uid, data, davCondition{ifNoneMatch: "*"}); err != nil {
		return "", err
	}
	return uid, e.sendInvitations(ctx, mbox, nil, data)
}

// DeleteEvent removes an event: the whole series (ScopeAll), the occurrence
// that started at instance (ScopeOne, an EXDATE), or that occurrence and
// every one after it (ScopeFollowing).
func (e *Engine) DeleteEvent(rd Reader, uid, scope string, instance time.Time) error {
	mbox, err := e.calendarOf(rd)
	if err != nil {
		return err
	}
	ctx := context.Background()
	o, err := e.accounts.eventByUID(ctx, mbox, uid)
	if err != nil {
		return err
	}
	cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
	if err != nil {
		return err
	}
	master, _ := parts(cal)
	if master == nil || instance.IsZero() || (master.Props.Get(ical.PropRecurrenceRule) == nil && master.Props.Get(ical.PropRecurrenceDates) == nil) {
		scope = ScopeAll
	}
	if scope == ScopeFollowing {
		if first, _, err := newCalReader(cal, instance.Location()).time(master.Props.Get(ical.PropDateTimeStart)); err == nil && !instance.After(first) {
			scope = ScopeAll
		}
	}
	switch scope {
	case ScopeOne:
		kept := cal.Children[:0]
		c := newCalReader(cal, instance.Location())
		for _, ch := range cal.Children {
			if rid := ch.Props.Get(ical.PropRecurrenceID); rid != nil {
				if t, _, err := c.time(rid); err == nil && t.Equal(instance) {
					continue
				}
			}
			kept = append(kept, ch)
		}
		cal.Children = kept
		ex := ical.NewProp(ical.PropExceptionDates)
		c.setInstant(ex, master, instance)
		master.Props.Add(ex)
	case ScopeFollowing:
		if err := endSeriesBefore(master, instance); err != nil {
			return err
		}
	default:
		if err := e.accounts.davDelete(ctx, mbox, davEvent, o.Name, davCondition{ifMatch: o.ETag}); err != nil {
			return err
		}
		return e.sendInvitations(ctx, mbox, o.Data, nil)
	}
	data, err := encodeCal(cal)
	if err != nil {
		return err
	}
	if _, err = e.accounts.davPut(ctx, mbox, davEvent, o.Name, uid, data, davCondition{ifMatch: o.ETag}); err != nil {
		return err
	}
	return e.sendInvitations(ctx, mbox, o.Data, data)
}
