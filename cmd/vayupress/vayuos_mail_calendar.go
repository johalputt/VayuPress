// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_calendar.go — the holder's Calendar, a place beside Mail.
//
// It is the calendar phones and desktop apps already keep over CalDAV
// (internal/vayuos/mail/calendar.go reads and writes it): a week, a month or
// a day, laid out in the holder's own zone, an event's details and its
// editor in a sheet. It opens only for the session's own mailbox: an
// administrator who opens someone's mail does not get their calendar, and
// the engine refuses an operator's read whatever this page does.
//
// The holder's zone is the browser's, kept in a cookie by
// admin-os-calendar.js; until it is known, the install's display zone. A
// zone is a place, not a person's setting the server could guess.

import (
	"errors"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	htmpl "html/template"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/ui"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
	"github.com/teambition/rrule-go"
)

const calTZCookie = "vm_tz"

// calZones are offered in the editor's zone field; any name the zone
// database knows is taken.
var calZones = []string{
	"UTC", "Europe/London", "Europe/Dublin", "Europe/Lisbon", "Europe/Paris", "Europe/Berlin", "Europe/Madrid",
	"Europe/Rome", "Europe/Amsterdam", "Europe/Stockholm", "Europe/Warsaw", "Europe/Athens", "Europe/Istanbul",
	"Europe/Moscow", "Africa/Cairo", "Africa/Lagos", "Africa/Nairobi", "Africa/Johannesburg", "Asia/Dubai",
	"Asia/Karachi", "Asia/Kolkata", "Asia/Kathmandu", "Asia/Dhaka", "Asia/Bangkok", "Asia/Jakarta",
	"Asia/Singapore", "Asia/Shanghai", "Asia/Hong_Kong", "Asia/Tokyo", "Asia/Seoul", "Australia/Perth",
	"Australia/Adelaide", "Australia/Sydney", "Pacific/Auckland", "Pacific/Honolulu", "America/Anchorage",
	"America/Los_Angeles", "America/Denver", "America/Phoenix", "America/Chicago", "America/Mexico_City",
	"America/New_York", "America/Toronto", "America/Halifax", "America/Bogota", "America/Lima",
	"America/Sao_Paulo", "America/Buenos_Aires",
}

// zoneRenamed are zones browsers still report by a name the zone database
// has since replaced (their time-zone library keeps the old one): Chrome
// says Asia/Calcutta for Kolkata. The page shows the place's name now.
var zoneRenamed = map[string]string{
	"Asia/Calcutta": "Asia/Kolkata", "Asia/Katmandu": "Asia/Kathmandu", "Asia/Saigon": "Asia/Ho_Chi_Minh",
	"Asia/Rangoon": "Asia/Yangon", "Europe/Kiev": "Europe/Kyiv",
}

// calZone is a zone name the database knows, never the server's own.
func calZone(name string) *time.Location {
	name = strings.TrimSpace(name)
	if now, ok := zoneRenamed[name]; ok {
		name = now
	}
	if name == "" || name == "Local" || len(name) > 64 {
		return nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil
	}
	return loc
}

// calendarHome is the zone the holder's calendar is laid out in.
func (a *App) calendarHome(r *http.Request) *time.Location {
	if c, err := r.Cookie(calTZCookie); err == nil {
		if v, err := url.QueryUnescape(c.Value); err == nil {
			if loc := calZone(v); loc != nil {
				return loc
			}
		}
	}
	return config.SiteLocation()
}

// calendarOwner is the Reader for the session's own mailbox's calendar.
func (a *App) calendarOwner(r *http.Request) (vmail.Reader, bool) {
	if !a.mailRunning() {
		return vmail.Reader{}, false
	}
	_, own := a.ownMailbox(r)
	if own == "" {
		return vmail.Reader{}, false
	}
	rd := a.mailReader(r, mailKeyOf(own, a.vayuMail.Config().Domain))
	return rd, rd.Key() != ""
}

// calView is the stretch of days a page shows.
type calView struct {
	Kind       string // day | week | month
	Day        time.Time
	From, To   time.Time
	Title      string
	Prev, Next time.Time
}

func calDate(t time.Time) string { return t.Format("2006-01-02") }

func midnight(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// mondayOf is the Monday on or before t.
func mondayOf(t time.Time) time.Time {
	return midnight(t).AddDate(0, 0, -((int(t.Weekday()) + 6) % 7))
}

func calViewFor(kind, d string, home *time.Location, now time.Time) calView {
	day := midnight(now.In(home))
	if t, err := time.ParseInLocation("2006-01-02", d, home); err == nil && t.Year() > 1900 && t.Year() < 3000 {
		day = t
	}
	// Each case names its view as a literal: the request's own string, even
	// once it has matched, is what a page would then be written from.
	v := calView{Day: day}
	switch kind {
	case "day":
		v.Kind = "day"
		v.From, v.To = day, day.AddDate(0, 0, 1)
		v.Title = day.Format("Monday 2 January 2006")
		v.Prev, v.Next = day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)
	case "month":
		v.Kind = "month"
		first := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, home)
		v.From = mondayOf(first)
		v.To = v.From.AddDate(0, 0, 42)
		v.Title = first.Format("January 2006")
		v.Prev, v.Next = first.AddDate(0, -1, 0), first.AddDate(0, 1, 0)
	default:
		v.Kind = "week"
		v.From = mondayOf(day)
		v.To = v.From.AddDate(0, 0, 7)
		last := v.To.AddDate(0, 0, -1)
		switch {
		case v.From.Year() != last.Year():
			v.Title = v.From.Format("2 January 2006") + " – " + last.Format("2 January 2006")
		case v.From.Month() != last.Month():
			v.Title = v.From.Format("2 January") + " – " + last.Format("2 January 2006")
		default:
			v.Title = v.From.Format("2") + " – " + last.Format("2 January 2006")
		}
		v.Prev, v.Next = v.From.AddDate(0, 0, -7), v.From.AddDate(0, 0, 7)
	}
	return v
}

func calHref(kind string, d time.Time) string {
	return "/os/vayumail/calendar?v=" + kind + "&amp;d=" + calDate(d)
}

// calEventRef is how a page names one occurrence: its UID, its start as shown
// and its instance in a series.
func calEventRef(ev vmail.CalEvent) string {
	q := "uid=" + url.QueryEscape(ev.UID) + "&s=" + strconv.FormatInt(ev.Start.Unix(), 10)
	if ev.Repeats() {
		q += "&i=" + strconv.FormatInt(ev.Instance.Unix(), 10)
	}
	return html.EscapeString(q)
}

func calOpen(ev vmail.CalEvent) string {
	return ` hx-get="/os/vayumail/calendar/event?` + calEventRef(ev) + `" hx-target="#cal-sheet-body" hx-swap="innerHTML"`
}

// spansDays is an event the grid shows in the all-day row: all day, or a
// day or longer.
func spansDays(ev vmail.CalEvent) bool { return ev.AllDay || ev.End.Sub(ev.Start) >= 24*time.Hour }

func onDay(ev vmail.CalEvent, day time.Time) bool {
	end := day.AddDate(0, 0, 1)
	if ev.End.Equal(ev.Start) {
		return !ev.Start.Before(day) && ev.Start.Before(end)
	}
	return ev.Start.Before(end) && ev.End.After(day)
}

func clock(t time.Time) string { return t.Format("15:04") }

// calSegment is the part of a timed event inside one day's column.
type calSegment struct {
	ev          vmail.CalEvent
	top, mins   int // minutes from midnight, and long
	lane, lanes int
}

// daySegments lays out a day's timed events in lanes: events that overlap
// share the column's width, each in the first lane free when it starts.
func daySegments(evs []vmail.CalEvent, day time.Time) []calSegment {
	end := day.AddDate(0, 0, 1)
	var segs []calSegment
	for _, ev := range evs {
		if spansDays(ev) || !onDay(ev, day) {
			continue
		}
		s, e := ev.Start, ev.End
		if s.Before(day) {
			s = day
		}
		if e.After(end) {
			e = end
		}
		segs = append(segs, calSegment{ev: ev, top: int(s.Sub(day).Minutes()), mins: int(e.Sub(s).Minutes())})
	}
	sort.SliceStable(segs, func(i, j int) bool { return segs[i].top < segs[j].top })
	// The shortest a block is drawn is half an hour, so lanes are counted
	// with that length too: two blocks that would be drawn over each other
	// share the width.
	drawn := func(s calSegment) int { return s.top + max(s.mins, 30) }
	for i := 0; i < len(segs); {
		j, reach := i, drawn(segs[i])
		for j < len(segs) && segs[j].top < reach {
			reach = max(reach, drawn(segs[j]))
			j++
		}
		var ends []int
		for k := i; k < j; k++ {
			lane := -1
			for l, e := range ends {
				if e <= segs[k].top {
					lane = l
					break
				}
			}
			if lane < 0 {
				lane = len(ends)
				ends = append(ends, 0)
			}
			ends[lane] = drawn(segs[k])
			segs[k].lane = lane
		}
		for k := i; k < j; k++ {
			segs[k].lanes = len(ends)
		}
		i = j
	}
	return segs
}

func calChip(ev vmail.CalEvent, home *time.Location) string {
	label := esc(nonEmpty(ev.Summary, "(no title)"))
	if !spansDays(ev) {
		label = `<span class="cal-chip__t">` + clock(ev.Start.In(home)) + `</span> ` + label
	}
	return `<button type="button" class="cal-chip"` + calOpen(ev) + `>` + label + `</button>`
}

func nonEmpty(s, otherwise string) string {
	if strings.TrimSpace(s) == "" {
		return otherwise
	}
	return s
}

// calGrid is the week or the day: day heads, the all-day row, and the hours
// with each day's timed events, placed by admin-os-calendar.js from the
// minutes they carry (the console's CSP takes no style attributes).
func calGrid(v calView, evs []vmail.CalEvent, home *time.Location, today time.Time) string {
	days := 7
	if v.Kind == "day" {
		days = 1
	}
	var b strings.Builder
	b.WriteString(`<div class="cal-grid cal-grid--` + strconv.Itoa(days) + `">`)
	b.WriteString(`<div class="cal-grid__row cal-grid__head"><span class="cal-grid__gutter"></span>`)
	for i := 0; i < days; i++ {
		d := v.From.AddDate(0, 0, i)
		cls := "cal-dayhead"
		cur := ""
		if d.Equal(today) {
			cls += " is-today"
			cur = ` aria-current="date"`
		}
		b.WriteString(`<a class="` + cls + `" href="` + calHref("day", d) + `"` + cur + `><span class="cal-dayhead__w">` + d.Format("Mon") + `</span><span class="cal-dayhead__n">` + d.Format("2") + `</span></a>`)
	}
	b.WriteString(`</div><div class="cal-grid__row cal-grid__allday"><span class="cal-grid__gutter">all day</span>`)
	for i := 0; i < days; i++ {
		d := v.From.AddDate(0, 0, i)
		b.WriteString(`<div class="cal-grid__cell">`)
		for _, ev := range evs {
			if spansDays(ev) && onDay(ev, d) {
				b.WriteString(calChip(ev, home))
			}
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</div><div class="cal-grid__scroll" data-cal-scroll><div class="cal-grid__row cal-grid__body"><div class="cal-hours" aria-hidden="true">`)
	for h := 0; h < 24; h++ {
		b.WriteString(`<span>` + strconv.Itoa(100 + h)[1:] + `:00</span>`)
	}
	b.WriteString(`</div>`)
	for i := 0; i < days; i++ {
		d := v.From.AddDate(0, 0, i)
		cls := "cal-col"
		now := ""
		if d.Equal(today) {
			cls += " is-today"
			now = ` data-cal-today`
		}
		b.WriteString(`<div class="` + cls + `" data-day="` + calDate(d) + `"` + now + `>`)
		for _, s := range daySegments(evs, d) {
			when := clock(s.ev.Start.In(home))
			if s.ev.Repeats() {
				when += " · repeats"
			}
			b.WriteString(`<button type="button" class="cal-ev" data-top="` + strconv.Itoa(s.top) + `" data-mins="` + strconv.Itoa(s.mins) +
				`" data-lane="` + strconv.Itoa(s.lane) + `" data-lanes="` + strconv.Itoa(s.lanes) + `"` + calOpen(s.ev) + `>` +
				`<span class="cal-ev__t">` + esc(nonEmpty(s.ev.Summary, "(no title)")) + `</span><span class="cal-ev__w">` + when + `</span></button>`)
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</div></div></div>`)
	return b.String()
}

// calMonth is six weeks from the Monday on or before the 1st, three events
// a day and a count of the rest.
func calMonth(v calView, evs []vmail.CalEvent, home *time.Location, today time.Time) string {
	var b strings.Builder
	b.WriteString(`<div class="cal-month"><div class="cal-month__head" aria-hidden="true">`)
	for _, w := range []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"} {
		b.WriteString(`<span>` + w + `</span>`)
	}
	b.WriteString(`</div><div class="cal-month__grid">`)
	for i := 0; i < 42; i++ {
		d := v.From.AddDate(0, 0, i)
		cls := "cal-mday"
		cur := ""
		if d.Month() != v.Day.Month() {
			cls += " is-out"
		}
		if d.Equal(today) {
			cls += " is-today"
			cur = ` aria-current="date"`
		}
		b.WriteString(`<div class="` + cls + `"><a class="cal-mday__n" href="` + calHref("day", d) + `"` + cur + ` aria-label="` + d.Format("Monday 2 January") + `">` + d.Format("2") + `</a>`)
		n := 0
		for _, ev := range evs {
			if !onDay(ev, d) {
				continue
			}
			if n < 3 {
				b.WriteString(calChip(ev, home))
			}
			n++
		}
		if n > 3 {
			b.WriteString(`<a class="cal-mday__more" href="` + calHref("day", d) + `">` + strconv.Itoa(n-3) + ` more</a>`)
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</div></div>`)
	return b.String()
}

// calAgenda is the same days as a list, which is what a phone shows.
func calAgenda(v calView, evs []vmail.CalEvent, home *time.Location) string {
	var b strings.Builder
	b.WriteString(`<ol class="cal-agenda">`)
	shown := false
	for d := v.From; d.Before(v.To); d = d.AddDate(0, 0, 1) {
		var items []string
		for _, ev := range evs {
			if !onDay(ev, d) {
				continue
			}
			when := "All day"
			if !spansDays(ev) {
				when = clock(ev.Start.In(home)) + "–" + clock(ev.End.In(home))
			}
			items = append(items, `<li><button type="button" class="cal-agenda__ev"`+calOpen(ev)+`><span class="cal-agenda__when">`+when+`</span><span class="cal-agenda__t">`+esc(nonEmpty(ev.Summary, "(no title)"))+`</span></button></li>`)
		}
		if len(items) == 0 {
			continue
		}
		shown = true
		b.WriteString(`<li><h2 class="cal-agenda__date">` + d.Format("Mon 2 January") + `</h2><ul>` + strings.Join(items, "") + `</ul></li>`)
	}
	if !shown {
		b.WriteString(`<li class="cal-agenda__none">Nothing on these days.</li>`)
	}
	b.WriteString(`</ol>`)
	return b.String()
}

// calSide is the Calendar's sidebar: a new event, a month to move by, and
// the zone the page is laid out in.
func calSide(v calView, home *time.Location, today time.Time) string {
	var b strings.Builder
	b.WriteString(`<button type="button" class="btn btn--primary mx-compose-btn" hx-get="/os/vayumail/calendar/edit?d=` + calDate(v.Day) + `" hx-target="#cal-sheet-body" hx-swap="innerHTML">` + saIcon("plus") + `New event</button>`)
	first := time.Date(v.Day.Year(), v.Day.Month(), 1, 0, 0, 0, 0, home)
	b.WriteString(`<div class="cal-mini"><div class="cal-mini__head"><span>` + first.Format("January 2006") + `</span>` +
		`<a class="btn btn--ghost btn--sm" href="` + calHref(v.Kind, first.AddDate(0, -1, 0)) + `" aria-label="Previous month">` + saIcon("chev-l") + `</a>` +
		`<a class="btn btn--ghost btn--sm" href="` + calHref(v.Kind, first.AddDate(0, 1, 0)) + `" aria-label="Next month">` + saIcon("chev-r") + `</a></div><div class="cal-mini__grid">`)
	for _, w := range []string{"M", "T", "W", "T", "F", "S", "S"} {
		b.WriteString(`<span class="cal-mini__w" aria-hidden="true">` + w + `</span>`)
	}
	start := mondayOf(first)
	for i := 0; i < 42; i++ {
		d := start.AddDate(0, 0, i)
		if i >= 35 && d.Month() != first.Month() {
			break
		}
		cls := "cal-mini__d"
		if d.Month() != first.Month() {
			cls += " is-out"
		}
		if !d.Before(v.From) && d.Before(v.To) && v.Kind != "month" {
			cls += " is-shown"
		}
		cur := ""
		if d.Equal(today) {
			cls += " is-today"
			cur = ` aria-current="date"`
		}
		b.WriteString(`<a class="` + cls + `" href="` + calHref(v.Kind, d) + `"` + cur + `>` + d.Format("2") + `</a>`)
	}
	b.WriteString(`</div></div>`)
	b.WriteString(`<div class="sa-appside__group">Time zone</div><p class="cal-zone">` + saIcon("globe") + `<span>` + esc(home.String()) + `</span></p>`)
	b.WriteString(`<div class="sa-appside__group">On your phone and computer</div><a class="sa-appside__item" href="/os/vayumail/connect">` + saIcon("phone") + `<span class="sa-appside__label">Add this calendar</span></a>`)
	return b.String()
}

func (a *App) handleVayuOSCalendar(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() || a.vayuMail.Accounts() == nil {
		a.writeMailSetup(w, r, "Calendar")
		return
	}
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	csrfTokenFor(w, r)
	rd, ok := a.calendarOwner(r)
	if !ok {
		page := `<div data-page-kind="status"><div class="page-header"><h1>Calendar</h1></div><div class="empty-state">A calendar belongs to a mailbox, and this account has none. An administrator can give you an address under <strong>Members → Team &amp; roles</strong>.</div></div>`
		writeOSHTML(w, r, adminOSLayout(nonce, "Calendar", "vayuos", cfg, htmpl.HTML(page)))
		return
	}
	home := a.calendarHome(r)
	now := time.Now()
	today := midnight(now.In(home))
	v := calViewFor(r.URL.Query().Get("v"), r.URL.Query().Get("d"), home, now)
	evs, err := a.vayuMail.CalendarBetween(rd, v.From, v.To, home)
	side := a.mailSide(r, rd, "", "", nil)
	side.Place, side.PlaceSide = "calendar", calSide(v, home, today)
	cfg.MailSide = side

	var b strings.Builder
	b.WriteString(`<div class="cal" data-page-kind="app" data-cal data-zone="` + esc(home.String()) + `">`)
	seg := func(kind, label string) string {
		cls := "seg-btn"
		cur := ""
		if v.Kind == kind {
			cls += " is-active"
			cur = ` aria-current="page"`
		}
		return `<a class="` + cls + `" href="` + calHref(kind, v.Day) + `"` + cur + `>` + label + `</a>`
	}
	b.WriteString(`<div class="cal-head"><a class="btn btn--sm" href="` + calHref(v.Kind, today) + `">Today</a>` +
		`<a class="btn btn--ghost btn--sm" href="` + calHref(v.Kind, v.Prev) + `" aria-label="Earlier">` + saIcon("chev-l") + `</a>` +
		`<a class="btn btn--ghost btn--sm" href="` + calHref(v.Kind, v.Next) + `" aria-label="Later">` + saIcon("chev-r") + `</a>` +
		`<h1 class="cal-head__title">` + esc(v.Title) + `</h1>` +
		`<nav class="seg-filter" aria-label="Show">` + seg("day", "Day") + seg("week", "Week") + seg("month", "Month") + `</nav></div>`)
	if err != nil {
		b.WriteString(saCallout("danger", "The calendar could not be read: "+esc(err.Error())+"."))
	}
	if v.Kind == "month" {
		b.WriteString(calMonth(v, evs, home, today))
	} else {
		b.WriteString(calGrid(v, evs, home, today))
	}
	b.WriteString(calAgenda(v, evs, home))
	b.WriteString(`</div>`)
	b.WriteString(string(ui.Sheet("cal-sheet", "Event", ui.HTML(`<div id="cal-sheet-body"></div>`))))
	b.WriteString(`<datalist id="cal-zones">`)
	for _, z := range calZones {
		b.WriteString(`<option value="` + z + `">`)
	}
	b.WriteString(`</datalist>`)
	b.WriteString(`<script nonce="` + nonce + `" src="/os/static/js/admin-os-calendar.js?v=` + assetVer("js/admin-os-calendar.js") + `"></script>`)
	writeOSHTML(w, r, adminOSLayout(nonce, "Calendar", "vayuos", cfg, htmpl.HTML(b.String())))
}

// findOccurrence is the occurrence a page named by calEventRef.
func (a *App) findOccurrence(r *http.Request, rd vmail.Reader, home *time.Location) (vmail.CalEvent, bool) {
	q := r.URL.Query()
	uid := q.Get("uid")
	s, err := strconv.ParseInt(q.Get("s"), 10, 64)
	if uid == "" || err != nil {
		return vmail.CalEvent{}, false
	}
	start := time.Unix(s, 0)
	evs, err := a.vayuMail.CalendarBetween(rd, start, start.Add(time.Minute), home)
	if err != nil {
		return vmail.CalEvent{}, false
	}
	inst, _ := strconv.ParseInt(q.Get("i"), 10, 64)
	for _, ev := range evs {
		if ev.UID == uid && ev.Start.Unix() == s && (!ev.Repeats() || ev.Instance.Unix() == inst) {
			return ev, true
		}
	}
	return vmail.CalEvent{}, false
}

// calWhen is an occurrence's time in words, in the holder's zone and, when
// the event keeps another, in its own too.
func calWhen(ev vmail.CalEvent, home *time.Location) string {
	s, e := ev.Start.In(home), ev.End.In(home)
	if ev.AllDay {
		last := ev.End.AddDate(0, 0, -1)
		if !last.After(ev.Start) {
			return s.Format("Monday 2 January 2006") + " · all day"
		}
		return s.Format("Monday 2 January") + " – " + last.Format("Monday 2 January 2006")
	}
	out := s.Format("Monday 2 January 2006") + " · " + clock(s) + "–" + clock(e)
	if midnight(e) != midnight(s) {
		out = s.Format("Monday 2 January") + " " + clock(s) + " – " + e.Format("Monday 2 January 2006") + " " + clock(e)
	}
	if ev.Zone != "" && ev.Zone != home.String() {
		if loc := calZone(ev.Zone); loc != nil {
			out += " (" + clock(ev.Start.In(loc)) + "–" + clock(ev.End.In(loc)) + " " + ev.Zone + ")"
		}
	}
	return out
}

// calDayNames are rrule's weekdays by Day(), Monday first.
var calDayNames = [7]string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

func andList(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " and " + xs[len(xs)-1]
}

// repeatWords is a repeat as a sentence: "Every 2 weeks on Tuesday and
// Thursday, until 15 December 2026".
func repeatWords(rule string, start time.Time) string {
	opt, err := rrule.StrToROption(rule)
	if err != nil {
		return "Repeats"
	}
	units := map[rrule.Frequency][2]string{rrule.DAILY: {"day", "days"}, rrule.WEEKLY: {"week", "weeks"}, rrule.MONTHLY: {"month", "months"}, rrule.YEARLY: {"year", "years"}}
	u, ok := units[opt.Freq]
	if !ok {
		return "Repeats"
	}
	out := "Every " + u[0]
	if opt.Interval > 1 {
		out = "Every " + strconv.Itoa(opt.Interval) + " " + u[1]
	}
	switch opt.Freq {
	case rrule.WEEKLY:
		var days []string
		for _, d := range opt.Byweekday {
			days = append(days, calDayNames[d.Day()])
		}
		if len(days) == 0 {
			days = []string{start.Weekday().String()}
		}
		if len(days) == 5 && !strings.Contains(strings.Join(days, ""), "Saturday") && !strings.Contains(strings.Join(days, ""), "Sunday") && opt.Interval <= 1 {
			out = "Every weekday"
		} else {
			out += " on " + andList(days)
		}
	case rrule.MONTHLY:
		switch {
		case len(opt.Bymonthday) == 1 && opt.Bymonthday[0] > 0:
			out += " on day " + strconv.Itoa(opt.Bymonthday[0])
		case len(opt.Bymonthday) == 0 && len(opt.Byweekday) == 0:
			out += " on day " + strconv.Itoa(start.Day())
		}
	case rrule.YEARLY:
		if len(opt.Bymonth) == 0 && len(opt.Byweekday) == 0 {
			out += " on " + start.Format("2 January")
		}
	}
	switch {
	case !opt.Until.IsZero():
		out += ", until " + opt.Until.In(start.Location()).Format("2 January 2006")
	case opt.Count == 1:
		out += ", once"
	case opt.Count > 1:
		out += ", " + strconv.Itoa(opt.Count) + " times"
	}
	return out
}

func (a *App) handleVayuOSCalendarEvent(w http.ResponseWriter, r *http.Request) {
	rd, ok := a.calendarOwner(r)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "A calendar opens only for its holder.", "")
		return
	}
	home := a.calendarHome(r)
	ev, found := a.findOccurrence(r, rd, home)
	if !found {
		writeOSHTML(w, r, saCallout("danger", "This event is no longer in the calendar. It may have been changed on another device."))
		return
	}
	var b strings.Builder
	b.WriteString(`<div class="cal-detail" data-sheet-title="` + esc(nonEmpty(ev.Summary, "(no title)")) + `">`)
	b.WriteString(`<p class="cal-detail__row">` + saIcon("calendar") + `<span>` + esc(calWhen(ev, home)) + `</span></p>`)
	if ev.Rule != "" {
		b.WriteString(`<p class="cal-detail__row">` + saIcon("refresh") + `<span>` + esc(repeatWords(ev.Rule, ev.Start)) + `</span></p>`)
	}
	if ev.Location != "" {
		b.WriteString(`<p class="cal-detail__row">` + saIcon("where") + `<span>` + esc(ev.Location) + `</span></p>`)
	}
	if len(ev.Attendees) > 0 {
		b.WriteString(`<ul class="cal-detail__people">`)
		for _, p := range ev.Attendees {
			b.WriteString(`<li><span>` + esc(nonEmpty(p.Name, p.Email)) + `</span> <span class="muted">` + esc(partStat(p.Status)) + `</span></li>`)
		}
		b.WriteString(`</ul>`)
	}
	if ev.Description != "" {
		b.WriteString(`<p class="cal-detail__notes">` + esc(ev.Description) + `</p>`)
	}
	ref := calEventRef(ev)
	edit := func(scope, label string) string {
		return `<button type="button" class="btn btn--sm" hx-get="/os/vayumail/calendar/edit?` + ref + `&amp;scope=` + scope + `" hx-target="#cal-sheet-body" hx-swap="innerHTML">` + label + `</button>`
	}
	b.WriteString(`<div class="cal-detail__acts">`)
	if ev.Repeats() {
		b.WriteString(edit(vmail.ScopeAll, "Edit") + edit(vmail.ScopeOne, "This one only"))
	} else {
		b.WriteString(edit(vmail.ScopeAll, "Edit"))
	}
	b.WriteString(`</div><form class="cal-detail__delete" hx-post="/os/vayumail/calendar/action" hx-target="#cal-sheet-body" hx-swap="innerHTML">` +
		`<input type="hidden" name="op" value="delete"><input type="hidden" name="uid" value="` + esc(ev.UID) + `">` +
		`<input type="hidden" name="i" value="` + strconv.FormatInt(ev.Instance.Unix(), 10) + `"><input type="hidden" name="d" value="` + calDate(ev.Start.In(home)) + `">`)
	if ev.Repeats() {
		b.WriteString(`<fieldset class="cal-scope"><legend>Delete</legend>` + scopeRadios(vmail.ScopeOne) + `</fieldset>`)
	} else {
		b.WriteString(`<input type="hidden" name="scope" value="` + vmail.ScopeAll + `">`)
	}
	b.WriteString(`<button type="submit" class="btn btn--danger btn--sm">Delete</button></form></div>`)
	writeOSHTML(w, r, b.String())
}

func partStat(s string) string {
	switch s {
	case "ACCEPTED":
		return "going"
	case "DECLINED":
		return "not going"
	case "TENTATIVE":
		return "maybe"
	}
	return "no answer yet"
}

func scopeRadios(on string) string {
	var b strings.Builder
	for _, o := range [][2]string{{vmail.ScopeOne, "This event"}, {vmail.ScopeFollowing, "This and following events"}, {vmail.ScopeAll, "All events"}} {
		chk := ""
		if o[0] == on {
			chk = " checked"
		}
		b.WriteString(`<label class="cal-scope__opt"><input type="radio" name="scope" value="` + o[0] + `"` + chk + ` data-cal-scope> ` + o[1] + `</label>`)
	}
	return b.String()
}

// editableRule reports whether the editor's fields can say a rule: a
// frequency, an interval, weekdays for a weekly one, and an end. Anything
// more (the last Friday of the month) is kept as it is.
func editableRule(rule string) bool {
	if rule == "" {
		return true
	}
	opt, err := rrule.StrToROption(rule)
	if err != nil || len(opt.Bysetpos) > 0 || len(opt.Bymonthday) > 0 || len(opt.Bymonth) > 0 || len(opt.Byyearday) > 0 ||
		len(opt.Byweekno) > 0 || len(opt.Byhour) > 0 || len(opt.Byminute) > 0 || len(opt.Bysecond) > 0 {
		return false
	}
	switch opt.Freq {
	case rrule.DAILY, rrule.MONTHLY, rrule.YEARLY:
		return len(opt.Byweekday) == 0
	case rrule.WEEKLY:
		for _, d := range opt.Byweekday {
			if d.N() != 0 {
				return false
			}
		}
		return true
	}
	return false
}

func (a *App) handleVayuOSCalendarEdit(w http.ResponseWriter, r *http.Request) {
	rd, ok := a.calendarOwner(r)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "A calendar opens only for its holder.", "")
		return
	}
	home := a.calendarHome(r)
	var ev vmail.CalEvent
	scope := r.URL.Query().Get("scope")
	if r.URL.Query().Get("uid") != "" {
		found := false
		if ev, found = a.findOccurrence(r, rd, home); !found {
			writeOSHTML(w, r, saCallout("danger", "This event is no longer in the calendar. It may have been changed on another device."))
			return
		}
	} else {
		day, err := time.ParseInLocation("2006-01-02", r.URL.Query().Get("d"), home)
		if err != nil {
			day = midnight(time.Now().In(home))
		}
		start := day.Add(9 * time.Hour)
		if h, err := strconv.Atoi(r.URL.Query().Get("h")); err == nil && h >= 0 && h < 24 {
			start = time.Date(day.Year(), day.Month(), day.Day(), h, 0, 0, 0, home)
		} else if day.Equal(midnight(time.Now().In(home))) {
			start = time.Now().In(home).Truncate(time.Hour).Add(time.Hour)
		}
		ev = vmail.CalEvent{Start: start, End: start.Add(time.Hour)}
	}
	writeOSHTML(w, r, calEditor(ev, scope, home, ""))
}

// calEditor is the event's form, in the event's own zone.
func calEditor(ev vmail.CalEvent, scope string, home *time.Location, msg string) string {
	zone := home
	if loc := calZone(ev.Zone); loc != nil {
		zone = loc
	}
	s, e := ev.Start.In(zone), ev.End.In(zone)
	if ev.AllDay {
		s, e = ev.Start.In(home), ev.End.In(home).AddDate(0, 0, -1)
	}
	title := "New event"
	if ev.UID != "" {
		title = "Edit event"
	}
	var b strings.Builder
	b.WriteString(`<form class="cal-form" data-sheet-title="` + title + `" hx-post="/os/vayumail/calendar/action" hx-target="#cal-sheet-body" hx-swap="innerHTML" data-cal-form>`)
	b.WriteString(`<div id="cal-form-msg">` + msg + `</div>`)
	b.WriteString(`<input type="hidden" name="op" value="save"><input type="hidden" name="uid" value="` + esc(ev.UID) + `">`)
	if ev.Repeats() {
		b.WriteString(`<input type="hidden" name="i" value="` + strconv.FormatInt(ev.Instance.Unix(), 10) + `">`)
		if scope == "" {
			scope = vmail.ScopeAll
		}
		b.WriteString(`<fieldset class="cal-scope"><legend>Change</legend>` + scopeRadios(scope) + `</fieldset>`)
	}
	field := func(label, input string) string {
		return `<label class="field"><span class="field-label">` + label + `</span>` + input + `</label>`
	}
	b.WriteString(field("Title", `<input type="text" name="title" required maxlength="300" value="`+esc(ev.Summary)+`" autocomplete="off">`))
	allDay := ""
	if ev.AllDay {
		allDay = " checked"
	}
	b.WriteString(`<label class="cal-check"><input type="checkbox" name="allday" value="1"` + allDay + ` data-cal-allday> All day</label>`)
	b.WriteString(`<div class="cal-two">` +
		field("Starts", `<span class="cal-when"><input type="date" name="sd" required value="`+calDate(s)+`"><input type="time" name="st" value="`+clock(s)+`" data-cal-time></span>`) +
		field("Ends", `<span class="cal-when"><input type="date" name="ed" required value="`+calDate(e)+`"><input type="time" name="et" value="`+clock(e)+`" data-cal-time></span>`) + `</div>`)
	b.WriteString(field("Time zone", `<input type="text" name="zone" list="cal-zones" value="`+esc(zone.String())+`" autocomplete="off" spellcheck="false" data-cal-time>`))
	b.WriteString(calRepeatFields(ev))
	b.WriteString(field("Where", `<input type="text" name="location" maxlength="500" value="`+esc(ev.Location)+`">`))
	b.WriteString(field("Notes", `<textarea name="notes" rows="3" maxlength="20000">`+esc(ev.Description)+`</textarea>`))
	b.WriteString(`<div class="cal-form__acts"><button type="button" class="btn" data-sheet-close>Cancel</button><button type="submit" class="btn btn--primary">Save</button></div></form>`)
	return b.String()
}

// calRepeatFields is the repeat part of the editor: how often, on which days
// for a week, and when it ends.
func calRepeatFields(ev vmail.CalEvent) string {
	var opt *rrule.ROption
	if ev.Rule != "" {
		opt, _ = rrule.StrToROption(ev.Rule)
	}
	freq, interval, days, end := "", 1, map[int]bool{}, "never"
	until, count := "", "10"
	keep := ev.Rule != "" && !editableRule(ev.Rule)
	if opt != nil && !keep {
		freq = opt.Freq.String()
		if opt.Interval > 1 {
			interval = opt.Interval
		}
		for _, d := range opt.Byweekday {
			days[d.Day()] = true
		}
		switch {
		case !opt.Until.IsZero():
			end, until = "until", calDate(opt.Until.In(ev.Start.Location()))
		case opt.Count > 0:
			end, count = "count", strconv.Itoa(opt.Count)
		}
	}
	if len(days) == 0 && !ev.Start.IsZero() {
		days[(int(ev.Start.Weekday())+6)%7] = true
	}
	var b strings.Builder
	b.WriteString(`<fieldset class="cal-repeat" data-cal-repeat><legend>Repeat</legend><div class="cal-repeat__row"><select name="r_freq" aria-label="Repeat" data-cal-freq>`)
	opts := [][2]string{{"", "Does not repeat"}, {"DAILY", "Daily"}, {"WEEKLY", "Weekly"}, {"MONTHLY", "Monthly"}, {"YEARLY", "Yearly"}}
	if keep {
		opts = append([][2]string{{"keep", "As it is: " + repeatWords(ev.Rule, ev.Start)}}, opts...)
		freq = "keep"
	}
	for _, o := range opts {
		sel := ""
		if o[0] == freq {
			sel = " selected"
		}
		b.WriteString(`<option value="` + o[0] + `"` + sel + `>` + esc(o[1]) + `</option>`)
	}
	b.WriteString(`</select><label class="cal-repeat__every" data-cal-when-freq>every <input type="number" name="r_int" min="1" max="99" value="` + strconv.Itoa(interval) + `" aria-label="Every how many"></label></div>`)
	b.WriteString(`<div class="cal-repeat__days" data-cal-when-weekly role="group" aria-label="On">`)
	for i, d := range []string{"MO", "TU", "WE", "TH", "FR", "SA", "SU"} {
		chk := ""
		if days[i] {
			chk = " checked"
		}
		b.WriteString(`<label class="cal-day"><input type="checkbox" name="r_day" value="` + d + `"` + chk + `><span>` + d[:1] + `</span></label>`)
	}
	b.WriteString(`</div><div class="cal-repeat__row" data-cal-when-freq><select name="r_end" aria-label="Ends">`)
	for _, o := range [][2]string{{"never", "Never ends"}, {"until", "Ends on"}, {"count", "Ends after"}} {
		sel := ""
		if o[0] == end {
			sel = " selected"
		}
		b.WriteString(`<option value="` + o[0] + `"` + sel + `>` + o[1] + `</option>`)
	}
	b.WriteString(`</select><input type="date" name="r_until" value="` + until + `" aria-label="Last day"><label class="cal-repeat__every"><input type="number" name="r_count" min="1" max="999" value="` + count + `" aria-label="How many times"> times</label></div>`)
	if keep {
		b.WriteString(`<input type="hidden" name="r_rule" value="` + esc(ev.Rule) + `">`)
	}
	b.WriteString(`</fieldset>`)
	return b.String()
}

// ruleFromCalForm builds the RRULE the editor's repeat fields say.
func ruleFromCalForm(r *http.Request, start time.Time, allDay bool) (string, error) {
	freq := r.PostFormValue("r_freq")
	switch freq {
	case "":
		return "", nil
	case "keep":
		return r.PostFormValue("r_rule"), nil
	case "DAILY", "WEEKLY", "MONTHLY", "YEARLY":
	default:
		return "", errors.New("the repeat is not one the editor offers")
	}
	parts := []string{"FREQ=" + freq}
	if n, err := strconv.Atoi(r.PostFormValue("r_int")); err == nil && n > 1 {
		if n > 99 {
			return "", errors.New("an event repeats at most every 99 days, weeks, months or years")
		}
		parts = append(parts, "INTERVAL="+strconv.Itoa(n))
	}
	if freq == "WEEKLY" {
		var days []string
		for _, d := range r.PostForm["r_day"] {
			switch d {
			case "MO", "TU", "WE", "TH", "FR", "SA", "SU":
				days = append(days, d)
			}
		}
		if len(days) > 0 {
			parts = append(parts, "BYDAY="+strings.Join(days, ","))
		}
	}
	switch r.PostFormValue("r_end") {
	case "until":
		day, err := time.ParseInLocation("2006-01-02", r.PostFormValue("r_until"), start.Location())
		if err != nil || day.Before(midnight(start)) {
			return "", errors.New("a repeat ends on or after the first day")
		}
		if allDay {
			parts = append(parts, "UNTIL="+day.Format("20060102"))
		} else {
			parts = append(parts, "UNTIL="+day.Add(24*time.Hour-time.Second).UTC().Format("20060102T150405Z"))
		}
	case "count":
		n, err := strconv.Atoi(r.PostFormValue("r_count"))
		if err != nil || n < 1 || n > 999 {
			return "", errors.New("a repeat ends after 1 to 999 times")
		}
		parts = append(parts, "COUNT="+strconv.Itoa(n))
	}
	return strings.Join(parts, ";"), nil
}

// calInputFromForm reads the editor into what the engine saves.
func calInputFromForm(r *http.Request, home *time.Location) (vmail.EventInput, error) {
	in := vmail.EventInput{UID: r.PostFormValue("uid"), Summary: r.PostFormValue("title"), Location: strings.TrimSpace(r.PostFormValue("location")),
		Description: strings.TrimSpace(r.PostFormValue("notes")), AllDay: r.PostFormValue("allday") == "1"}
	zone := home
	if !in.AllDay {
		if zone = calZone(r.PostFormValue("zone")); zone == nil {
			return in, errors.New("the time zone is not one the calendar knows: pick one from the list")
		}
	}
	var err error
	if in.AllDay {
		if in.Start, err = time.ParseInLocation("2006-01-02", r.PostFormValue("sd"), home); err != nil {
			return in, errors.New("the start is not a date")
		}
		last, err := time.ParseInLocation("2006-01-02", r.PostFormValue("ed"), home)
		if err != nil {
			return in, errors.New("the end is not a date")
		}
		in.End = last.AddDate(0, 0, 1)
	} else {
		if in.Start, err = time.ParseInLocation("2006-01-02 15:04", r.PostFormValue("sd")+" "+r.PostFormValue("st"), zone); err != nil {
			return in, errors.New("the start is not a date and time")
		}
		if in.End, err = time.ParseInLocation("2006-01-02 15:04", r.PostFormValue("ed")+" "+r.PostFormValue("et"), zone); err != nil {
			return in, errors.New("the end is not a date and time")
		}
	}
	in.Rule, err = ruleFromCalForm(r, in.Start, in.AllDay)
	return in, err
}

func (a *App) handleVayuOSCalendarAction(w http.ResponseWriter, r *http.Request) {
	rd, ok := a.calendarOwner(r)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "A calendar opens only for its holder.", "")
		return
	}
	_ = r.ParseForm()
	home := a.calendarHome(r)
	inst := time.Time{}
	if i, err := strconv.ParseInt(r.PostFormValue("i"), 10, 64); err == nil && i > 0 {
		inst = time.Unix(i, 0).In(home)
	}
	scope := r.PostFormValue("scope")
	switch scope {
	case vmail.ScopeOne, vmail.ScopeFollowing, vmail.ScopeAll:
	default:
		scope = vmail.ScopeAll
	}
	switch r.PostFormValue("op") {
	case "save":
		in, err := calInputFromForm(r, home)
		var uid string
		if err == nil {
			uid, err = a.vayuMail.SaveEvent(rd, in, scope, inst)
		}
		if err != nil {
			ev := vmail.CalEvent{UID: in.UID, Summary: in.Summary, Location: in.Location, Description: in.Description, Start: in.Start, End: in.End, AllDay: in.AllDay, Rule: in.Rule, Instance: inst}
			if in.Start.IsZero() {
				ev.Start = time.Now().In(home)
				ev.End = ev.Start
			} else if !in.AllDay {
				ev.Zone = in.Start.Location().String()
			}
			writeOSHTML(w, r, calEditor(ev, scope, home, saCallout("danger", esc(calSaveRefusal(err))+".")))
			return
		}
		_ = uid
		w.Header().Set("HX-Redirect", "/os/vayumail/calendar?v=week&d="+calDate(in.Start.In(home)))
		writeOSHTML(w, r, "")
	case "delete":
		if err := a.vayuMail.DeleteEvent(rd, r.PostFormValue("uid"), scope, inst); err != nil {
			writeOSHTML(w, r, saCallout("danger", "The event was not deleted: "+esc(calSaveRefusal(err))+"."))
			return
		}
		day, err := time.ParseInLocation("2006-01-02", r.PostFormValue("d"), home)
		if err != nil {
			day = time.Now().In(home)
		}
		w.Header().Set("HX-Redirect", "/os/vayumail/calendar?v=week&d="+calDate(day))
		writeOSHTML(w, r, "")
	default:
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "unknown operation", "")
	}
}

// calSaveRefusal is a save's error in the words the editor shows: the
// engine's own sentence, without its package prefix.
func calSaveRefusal(err error) string {
	msg := strings.TrimPrefix(err.Error(), "vayumail: ")
	if strings.Contains(msg, "changed since it was read") {
		return "the event was changed on another device since you opened it: close this and open it again"
	}
	return msg
}
