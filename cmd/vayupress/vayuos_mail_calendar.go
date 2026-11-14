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
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	htmpl "html/template"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/i18n"
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

// calendarAddress is the address of the mailbox a calendar Reader opens.
func (a *App) calendarAddress(rd vmail.Reader) string {
	return strings.ToLower(mailAddrOf(rd.Key(), a.vayuMail.Config().Domain))
}

// calendarOwner is the Reader for the session's own mailbox's calendar.
func (a *App) calendarOwner(r *http.Request) (vmail.Reader, bool) {
	return a.placeOwner(r, "/os/vayumail/calendar")
}

// driveOwner is the Reader for the session's own mailbox's Drive.
func (a *App) driveOwner(r *http.Request) (vmail.Reader, bool) {
	return a.placeOwner(r, "/os/vayumail/drive")
}

// placeOwner is the Reader for one of the places beside Mail, for a session
// that has its own mailbox and can reach that place (sessionReaches). Every
// page that offers the place asks here, so the offer and the route agree.
func (a *App) placeOwner(r *http.Request, place string) (vmail.Reader, bool) {
	if !a.mailRunning() || !sessionReaches(r, place) {
		return vmail.Reader{}, false
	}
	_, own := a.ownMailbox(r)
	if own == "" {
		return vmail.Reader{}, false
	}
	rd := a.mailReader(r, mailKeyOf(own, a.vayuMail.Config().Domain))
	return rd, rd.Key() != ""
}

// holderPlaces is which places beside Mail this session is offered.
type holderPlaces struct{ Calendar, Drive bool }

func (a *App) holderPlaces(r *http.Request) holderPlaces {
	_, cal := a.calendarOwner(r)
	_, drive := a.driveOwner(r)
	return holderPlaces{Calendar: cal, Drive: drive}
}

// calView is the stretch of days a page shows.
type calView struct {
	Lang       string // the language the page is written in
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

func calViewFor(lang, kind, d string, home *time.Location, now time.Time) calView {
	day := midnight(now.In(home))
	if t, err := time.ParseInLocation("2006-01-02", d, home); err == nil && t.Year() > 1900 && t.Year() < 3000 {
		day = t
	}
	// Each case names its view as a literal: the request's own string, even
	// once it has matched, is what a page would then be written from.
	v := calView{Lang: lang, Day: day}
	switch kind {
	case "day":
		v.Kind = "day"
		v.From, v.To = day, day.AddDate(0, 0, 1)
		v.Title = i18n.Date(lang, day, i18n.DayMonthYear)
		v.Prev, v.Next = day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)
	case "month":
		v.Kind = "month"
		first := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, home)
		v.From = mondayOf(first)
		v.To = v.From.AddDate(0, 0, 42)
		v.Title = i18n.Date(lang, first, i18n.MonthYear)
		v.Prev, v.Next = first.AddDate(0, -1, 0), first.AddDate(0, 1, 0)
	default:
		v.Kind = "week"
		v.From = mondayOf(day)
		v.To = v.From.AddDate(0, 0, 7)
		last := v.To.AddDate(0, 0, -1)
		to := i18n.Date(lang, last, i18n.DayMonthYearPlain)
		switch {
		case v.From.Year() != last.Year():
			v.Title = i18n.T(lang, "{from} – {to}", "from", i18n.Date(lang, v.From, i18n.DayMonthYearPlain), "to", to)
		case v.From.Month() != last.Month():
			v.Title = i18n.T(lang, "{from} – {to}", "from", i18n.Date(lang, v.From, i18n.DayMonthPlain), "to", to)
		default:
			v.Title = i18n.T(lang, "{day} – {date}", "day", v.From.Day(), "date", to)
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

// calTitle is an event's title, or a word for none.
func calTitle(lang string, ev vmail.CalEvent) string {
	return nonEmpty(ev.Summary, i18n.T(lang, "(no title)"))
}

// calHead is the short name of d's weekday, as the calendar's columns write it.
func calHead(lang string, d time.Time) string {
	return i18n.WeekdayHeads(lang)[(int(d.Weekday())+6)%7]
}

func calChip(lang string, ev vmail.CalEvent, home *time.Location) string {
	label := esc(calTitle(lang, ev))
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
		b.WriteString(`<a class="` + cls + `" href="` + calHref("day", d) + `"` + cur + `><span class="cal-dayhead__w">` + esc(calHead(v.Lang, d)) + `</span><span class="cal-dayhead__n">` + d.Format("2") + `</span></a>`)
	}
	b.WriteString(`</div><div class="cal-grid__row cal-grid__allday"><span class="cal-grid__gutter">` + esc(i18n.T(v.Lang, "all day")) + `</span>`)
	for i := 0; i < days; i++ {
		d := v.From.AddDate(0, 0, i)
		b.WriteString(`<div class="cal-grid__cell">`)
		for _, ev := range evs {
			if spansDays(ev) && onDay(ev, d) {
				b.WriteString(calChip(v.Lang, ev, home))
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
				when = i18n.T(v.Lang, "{time} · repeats", "time", when)
			}
			b.WriteString(`<button type="button" class="cal-ev" data-top="` + strconv.Itoa(s.top) + `" data-mins="` + strconv.Itoa(s.mins) +
				`" data-lane="` + strconv.Itoa(s.lane) + `" data-lanes="` + strconv.Itoa(s.lanes) + `"` + calOpen(s.ev) + `>` +
				`<span class="cal-ev__t">` + esc(calTitle(v.Lang, s.ev)) + `</span><span class="cal-ev__w">` + esc(when) + `</span></button>`)
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
	for _, w := range i18n.WeekdayHeads(v.Lang) {
		b.WriteString(`<span>` + esc(w) + `</span>`)
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
		b.WriteString(`<div class="` + cls + `"><a class="cal-mday__n" href="` + calHref("day", d) + `"` + cur + ` aria-label="` + esc(i18n.Date(v.Lang, d, i18n.DayMonth)) + `">` + d.Format("2") + `</a>`)
		n := 0
		for _, ev := range evs {
			if !onDay(ev, d) {
				continue
			}
			if n < 3 {
				b.WriteString(calChip(v.Lang, ev, home))
			}
			n++
		}
		if n > 3 {
			b.WriteString(`<a class="cal-mday__more" href="` + calHref("day", d) + `">` + esc(i18n.T(v.Lang, "{n} more", "n", n-3)) + `</a>`)
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
			when := i18n.T(v.Lang, "All day")
			if !spansDays(ev) {
				when = clock(ev.Start.In(home)) + "–" + clock(ev.End.In(home))
			}
			items = append(items, `<li><button type="button" class="cal-agenda__ev"`+calOpen(ev)+`><span class="cal-agenda__when">`+esc(when)+`</span><span class="cal-agenda__t">`+esc(calTitle(v.Lang, ev))+`</span></button></li>`)
		}
		if len(items) == 0 {
			continue
		}
		shown = true
		b.WriteString(`<li><h2 class="cal-agenda__date">` + esc(i18n.Date(v.Lang, d, i18n.DayMonth)) + `</h2><ul>` + strings.Join(items, "") + `</ul></li>`)
	}
	if !shown {
		b.WriteString(`<li class="cal-agenda__none">` + esc(i18n.T(v.Lang, "Nothing on these days.")) + `</li>`)
	}
	b.WriteString(`</ol>`)
	return b.String()
}

// calSide is the Calendar's sidebar: a new event, a month to move by, and
// the zone the page is laid out in.
func calSide(v calView, home *time.Location, today time.Time, pages []vmail.BookingPage, onPage int64) string {
	lang := v.Lang
	var b strings.Builder
	b.WriteString(`<button type="button" class="btn btn--primary mx-compose-btn" hx-get="/os/vayumail/calendar/edit?d=` + calDate(v.Day) + `" hx-target="#cal-sheet-body" hx-swap="innerHTML">` + saIcon("plus") + esc(i18n.T(lang, "New event")) + `</button>`)
	first := time.Date(v.Day.Year(), v.Day.Month(), 1, 0, 0, 0, 0, home)
	b.WriteString(`<div class="cal-mini"><div class="cal-mini__head"><span>` + esc(i18n.Date(lang, first, i18n.MonthYear)) + `</span>` +
		`<a class="btn btn--ghost btn--sm" href="` + calHref(v.Kind, first.AddDate(0, -1, 0)) + `" aria-label="` + esc(i18n.T(lang, "Previous month")) + `">` + saIcon("chev-l") + `</a>` +
		`<a class="btn btn--ghost btn--sm" href="` + calHref(v.Kind, first.AddDate(0, 1, 0)) + `" aria-label="` + esc(i18n.T(lang, "Next month")) + `">` + saIcon("chev-r") + `</a></div><div class="cal-mini__grid">`)
	for _, w := range i18n.WeekdayHeads(lang) {
		b.WriteString(`<span class="cal-mini__w" aria-hidden="true">` + esc(w) + `</span>`)
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
	b.WriteString(`<div class="sa-appside__group">` + esc(i18n.T(lang, "Booking pages")) + `</div>`)
	for _, p := range pages {
		cur := ""
		if p.ID == onPage {
			cur = ` aria-current="page"`
		}
		b.WriteString(`<a class="sa-appside__item" href="/os/vayumail/calendar/booking?id=` + strconv.FormatInt(p.ID, 10) + `"` + cur + `>` + saIcon("link") +
			`<span class="sa-appside__label">` + esc(p.Title) + `</span></a>`)
	}
	newCur := ""
	if onPage < 0 {
		newCur = ` aria-current="page"`
	}
	b.WriteString(`<a class="sa-appside__item" href="/os/vayumail/calendar/booking"` + newCur + `>` + saIcon("plus") + `<span class="sa-appside__label">` + esc(i18n.T(lang, "New booking page")) + `</span></a>`)
	b.WriteString(`<div class="sa-appside__group">` + esc(i18n.T(lang, "Time zone")) + `</div><p class="cal-zone">` + saIcon("globe") + `<span>` + esc(home.String()) + `</span></p>`)
	b.WriteString(`<div class="sa-appside__group">` + esc(i18n.T(lang, "On your phone and computer")) + `</div><a class="sa-appside__item" href="/os/vayumail/connect">` + saIcon("phone") + `<span class="sa-appside__label">` + esc(i18n.T(lang, "Add this calendar")) + `</span></a>`)
	b.WriteString(`<button type="button" class="sa-appside__item" hx-get="/os/vayumail/calendar/subs" hx-target="#cal-sheet-body" hx-swap="innerHTML">` + saIcon("refresh") +
		`<span class="sa-appside__label">` + esc(i18n.T(lang, "Subscribed calendars")) + `</span></button>`)
	b.WriteString(calTransfer(lang))
	return b.String()
}

// calSubsSheet is the holder's subscribed calendars, each with how its last
// read went, and the form that adds one: by its address, or signed in to
// another CalDAV server. The address is shown cut to its host (a private
// feed's address is its key), and a password is never shown back.
func calSubsSheet(lang string, subs []vmail.CalSubscription, said string) string {
	var b strings.Builder
	b.WriteString(`<div class="cal-detail" data-sheet-title="` + esc(i18n.T(lang, "Subscribed calendars")) + `">`)
	if said != "" {
		b.WriteString(`<p class="vm-keys-said" role="status">` + esc(said) + `</p>`)
	}
	act := func(op string, id int64, label, cls string) string {
		return `<form hx-post="/os/vayumail/calendar/subs" hx-target="#cal-sheet-body" hx-swap="innerHTML"><input type="hidden" name="op" value="` + op +
			`"><input type="hidden" name="id" value="` + strconv.FormatInt(id, 10) + `"><button type="submit" class="btn btn--sm` + cls + `">` + esc(label) + `</button></form>`
	}
	if len(subs) > 0 {
		b.WriteString(`<ul class="cal-detail__people">`)
		for _, c := range subs {
			host := c.URL
			if u, err := url.Parse(c.URL); err == nil {
				host = u.Host
			}
			var state string
			switch {
			case c.Error != "" && c.Busy:
				state = i18n.T(lang, "Last read failed: {reason}; makes you busy on booking pages", "reason", c.Error)
			case c.Error != "":
				state = i18n.T(lang, "Last read failed: {reason}; does not make you busy", "reason", c.Error)
			case c.Fetched.IsZero() && c.Busy:
				state = i18n.T(lang, "Not read yet; makes you busy on booking pages")
			case c.Fetched.IsZero():
				state = i18n.T(lang, "Not read yet; does not make you busy")
			case c.Busy:
				state = i18n.T(lang, "Read {when}; makes you busy on booking pages", "when", i18n.Date(lang, config.InSite(c.Fetched), i18n.DayMonthAt))
			default:
				state = i18n.T(lang, "Read {when}; does not make you busy", "when", i18n.Date(lang, config.InSite(c.Fetched), i18n.DayMonthAt))
			}
			if c.User != "" {
				host = i18n.T(lang, "{user} at {host}", "user", c.User, "host", host)
			}
			b.WriteString(`<li><p class="cal-detail__row">` + saIcon("calendar") + `<span><strong>` + esc(c.Name) + `</strong> · ` + esc(host) + `<br><span class="muted">` +
				esc(state) + `</span></span></p><div class="cal-detail__acts">` + act("refresh", c.ID, i18n.T(lang, "Read again"), "") + act("remove", c.ID, i18n.T(lang, "Unsubscribe"), " btn--danger") + `</div></li>`)
		}
		b.WriteString(`</ul>`)
	}
	b.WriteString(`<form class="cal-form" hx-post="/os/vayumail/calendar/subs" hx-target="#cal-sheet-body" hx-swap="innerHTML"><input type="hidden" name="op" value="add">` +
		`<div class="cal-two"><label class="field"><span class="field-label">` + esc(i18n.T(lang, "Name")) + `</span><input type="text" name="name" maxlength="80" placeholder="` + esc(i18n.T(lang, "Team calendar")) + `"></label>` +
		`<label class="field"><span class="field-label">` + esc(i18n.T(lang, "Address (.ics, https or webcal)")) + `</span><input type="url" name="url" required placeholder="https://…/basic.ics" autocomplete="off"></label></div>` +
		`<fieldset class="cal-repeat"><legend>` + esc(i18n.T(lang, "On another CalDAV server you sign in to (leave empty for an address anyone with it can read)")) + `</legend><div class="cal-two">` +
		`<label class="field"><span class="field-label">` + esc(i18n.T(lang, "User name there")) + `</span><input type="text" name="user" maxlength="200" autocomplete="off"></label>` +
		`<label class="field"><span class="field-label">` + esc(i18n.T(lang, "App password there")) + `</span><input type="password" name="password" maxlength="500" autocomplete="new-password"></label></div></fieldset>` +
		`<label class="cal-check"><input type="checkbox" name="busy" value="1" checked> ` + esc(i18n.T(lang, "Its events make me busy on my booking pages")) + `</label>` +
		`<p class="cal-note">` + esc(i18n.T(lang, "Its events are shown beside yours and read again every half hour; they are not changed here. Another calendar service's private address in iCalendar format works here, and is kept like a password.")) + `</p>` +
		`<div class="cal-form__acts"><button type="submit" class="btn btn--primary">` + esc(i18n.T(lang, "Subscribe")) + `</button></div></form></div>`)
	return b.String()
}

// handleVayuOSCalendarSubs shows the subscribed calendars, and on POST adds,
// reads again or removes one.
func (a *App) handleVayuOSCalendarSubs(w http.ResponseWriter, r *http.Request) {
	lang := i18n.Lang(r.Context())
	rd, ok := a.calendarOwner(r)
	if !ok || !a.mailRunning() {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(lang, "A calendar opens only for its holder."), "")
		return
	}
	said := ""
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
		var err error
		switch r.PostFormValue("op") {
		case "add":
			name, addr, busy := r.PostFormValue("name"), r.PostFormValue("url"), r.PostFormValue("busy") == "1"
			if user := strings.TrimSpace(r.PostFormValue("user")); user != "" {
				_, err = a.vayuMail.AddCalDAVSubscription(rd, name, addr, user, r.PostFormValue("password"), busy, time.Now())
			} else {
				_, err = a.vayuMail.AddCalendarSubscription(rd, name, addr, busy, time.Now())
			}
			said = i18n.T(lang, "Subscribed.")
		case "refresh":
			err = a.vayuMail.RefreshCalendarSubscription(rd, id, time.Now())
			said = i18n.T(lang, "Read again.")
		case "remove":
			err = a.vayuMail.RemoveCalendarSubscription(rd, id)
			said = i18n.T(lang, "Unsubscribed.")
		}
		if err != nil {
			said = i18n.T(lang, "Not done: {reason}.", "reason", strings.TrimPrefix(err.Error(), "vayumail: "))
		}
	}
	subs, err := a.vayuMail.CalendarSubscriptions(rd)
	if err != nil {
		writeOSFragment(w, saCallout("danger", esc(i18n.T(lang, "The subscriptions could not be read."))))
		return
	}
	writeOSFragment(w, calSubsSheet(lang, subs, said))
}

// calTransfer is the calendar out and in as one .ics file: the iCalendar
// file other calendar programs and services export and import.
func calTransfer(lang string) string {
	return `<a class="sa-appside__item" href="/os/vayumail/calendar/export" download>` + saIcon("download") + `<span class="sa-appside__label">` + esc(i18n.T(lang, "Export as .ics")) + `</span></a>` +
		`<form hx-post="/os/vayumail/calendar/import" hx-encoding="multipart/form-data" hx-trigger="change" hx-target="#cal-import-said" hx-swap="innerHTML">` +
		`<label class="sa-appside__item">` + saIcon("upload") + `<span class="sa-appside__label">` + esc(i18n.T(lang, "Import a .ics file")) + `</span>` +
		`<input class="vp-sr-only" type="file" name="file" accept=".ics,text/calendar"></label></form>` +
		`<p class="vm-keys-said" id="cal-import-said" role="status"></p>`
}

// handleVayuOSCalendarExport sends the holder's calendar as one .ics file.
func (a *App) handleVayuOSCalendarExport(w http.ResponseWriter, r *http.Request) {
	rd, ok := a.calendarOwner(r)
	if !ok || !a.mailRunning() {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(i18n.Lang(r.Context()), "A calendar opens only for its holder."), "")
		return
	}
	addr := a.calendarAddress(rd)
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+mailSafeFilename(addr+".ics")+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	if err := a.vayuMail.CalendarExport(rd, addr, w); err != nil {
		// Once the file has begun there is no other answer to give; before
		// it, the error is the answer.
		slog.Warn("vayumail: calendar export", "err", err)
	}
}

// handleVayuOSCalendarImport keeps a .ics file's events in the holder's
// calendar and says what it did.
func (a *App) handleVayuOSCalendarImport(w http.ResponseWriter, r *http.Request) {
	lang := i18n.Lang(r.Context())
	rd, ok := a.calendarOwner(r)
	if !ok || !a.mailRunning() {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(lang, "A calendar opens only for its holder."), "")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, vmail.MaxCalendarImport+(64<<10))
	mr, err := r.MultipartReader()
	if err != nil {
		writeOSFragment(w, esc(i18n.T(lang, "Not imported: no file was chosen.")))
		return
	}
	for {
		part, err := mr.NextPart()
		if err != nil {
			writeOSFragment(w, esc(i18n.T(lang, "Not imported: no file was chosen, or it is larger than 20 MB.")))
			return
		}
		if part.FormName() != "file" {
			continue
		}
		res, err := a.vayuMail.CalendarImport(rd, part, time.Now())
		writeOSFragment(w, calImportSaid(lang, res, err))
		return
	}
}

// calImportSaid is what an import did, in words, as markup: each sentence
// is escaped, and the one link is the page's own.
func calImportSaid(lang string, res vmail.CalImport, err error) string {
	switch {
	case errors.Is(err, vmail.ErrICSTooLarge):
		return esc(i18n.T(lang, "Not imported: a calendar file is at most 20 MB."))
	case errors.Is(err, vmail.ErrICSUnreadable):
		return esc(i18n.T(lang, "Not imported: the file is not a calendar (.ics) file."))
	case err != nil:
		return esc(i18n.T(lang, "Not imported: the file could not be read."))
	}
	said := esc(i18n.T(lang, "{n, plural, one {Imported # event.} other {Imported # events.}}", "n", res.Added))
	if res.Added > 0 {
		said = escLinked(i18n.T(lang, "{n, plural, one {Imported # event. {link}.} other {Imported # events. {link}.}}", "n", res.Added, "link", linkMark),
			"/os/vayumail/calendar", i18n.T(lang, "Show the calendar"))
	}
	switch {
	case res.Already > 0 && res.Added == 0:
		said = esc(i18n.T(lang, "Nothing new: {n} already in the calendar, left as they are.", "n", res.Already))
	case res.Already > 0:
		said += " " + esc(i18n.T(lang, "{n} already in the calendar, left as they are.", "n", res.Already))
	}
	if res.Refused > 0 {
		said += " " + esc(i18n.T(lang, "{n} could not be kept: another kind of entry, or larger than a megabyte.", "n", res.Refused))
	}
	if res.Full {
		said += " " + esc(i18n.T(lang, "The calendar is full, so the rest of the file was not read."))
	}
	if res.Damaged {
		said += " " + esc(i18n.T(lang, "The file broke off or stopped making sense part way; what came before is kept."))
	}
	return said
}

func (a *App) handleVayuOSCalendar(w http.ResponseWriter, r *http.Request) {
	lang := i18n.Lang(r.Context())
	title := i18n.T(lang, "Calendar")
	if !a.mailRunning() || a.vayuMail.Accounts() == nil {
		a.writeMailSetup(w, r, title)
		return
	}
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	csrfTokenFor(w, r)
	rd, ok := a.calendarOwner(r)
	if !ok {
		page := `<div data-page-kind="status"><div class="page-header"><h1>` + esc(title) + `</h1></div><div class="empty-state">` +
			escBold(i18n.T(lang, "A calendar belongs to a mailbox, and this account has none. An administrator can give you an address under {place}.", "place", htmlMark(0)), i18n.T(lang, "Members → Team & roles")) + `</div></div>`
		writeOSHTML(w, r, adminOSLayout(nonce, title, "vayuos", cfg, htmpl.HTML(page)))
		return
	}
	home := a.calendarHome(r)
	now := time.Now()
	today := midnight(now.In(home))
	v := calViewFor(lang, r.URL.Query().Get("v"), r.URL.Query().Get("d"), home, now)
	evs, err := a.vayuMail.CalendarBetween(rd, v.From, v.To, home)
	side := a.mailSide(r, rd, "", "", nil)
	pages, _ := a.vayuMail.Accounts().BookingPagesFor(r.Context(), a.calendarAddress(rd))
	side.Place, side.PlaceSide = "calendar", calSide(v, home, today, pages, 0)
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
		return `<a class="` + cls + `" href="` + calHref(kind, v.Day) + `"` + cur + `>` + esc(label) + `</a>`
	}
	b.WriteString(`<div class="cal-head"><a class="btn btn--sm" href="` + calHref(v.Kind, today) + `">` + esc(i18n.T(lang, "Today")) + `</a>` +
		`<a class="btn btn--ghost btn--sm" href="` + calHref(v.Kind, v.Prev) + `" aria-label="` + esc(i18n.T(lang, "Earlier")) + `">` + saIcon("chev-l") + `</a>` +
		`<a class="btn btn--ghost btn--sm" href="` + calHref(v.Kind, v.Next) + `" aria-label="` + esc(i18n.T(lang, "Later")) + `">` + saIcon("chev-r") + `</a>` +
		`<h1 class="cal-head__title">` + esc(v.Title) + `</h1>` +
		`<nav class="seg-filter" aria-label="` + esc(i18n.T(lang, "Show")) + `">` + seg("day", i18n.T(lang, "Day")) + seg("week", i18n.T(lang, "Week")) + seg("month", i18n.T(lang, "Month")) + `</nav></div>`)
	if err != nil {
		b.WriteString(saCallout("danger", esc(i18n.T(lang, "The calendar could not be read: {reason}.", "reason", err.Error()))))
	}
	if r.URL.Query().Get("note") == "unsent" {
		b.WriteString(saCallout("warn", esc(i18n.T(lang, "The event is saved, but not every guest could be sent it. Open it and save again to send it once more."))))
	}
	if v.Kind == "month" {
		b.WriteString(calMonth(v, evs, home, today))
	} else {
		b.WriteString(calGrid(v, evs, home, today))
	}
	b.WriteString(calAgenda(v, evs, home))
	b.WriteString(`</div>`)
	b.WriteString(string(ui.Sheet("cal-sheet", i18n.T(lang, "Event"), ui.HTML(`<div id="cal-sheet-body"></div>`))))
	b.WriteString(`<datalist id="cal-zones">`)
	for _, z := range calZones {
		b.WriteString(`<option value="` + z + `">`)
	}
	b.WriteString(`</datalist>`)
	b.WriteString(`<script nonce="` + nonce + `" src="/os/static/js/admin-os-calendar.js?v=` + assetVer("js/admin-os-calendar.js") + `"></script>`)
	writeOSHTML(w, r, adminOSLayout(nonce, title, "vayuos", cfg, htmpl.HTML(b.String())))
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
func calWhen(lang string, ev vmail.CalEvent, home *time.Location) string {
	s, e := ev.Start.In(home), ev.End.In(home)
	if ev.AllDay {
		last := ev.End.AddDate(0, 0, -1)
		if !last.After(ev.Start) {
			return i18n.T(lang, "{date} · all day", "date", i18n.Date(lang, s, i18n.DayMonthYear))
		}
		return i18n.T(lang, "{from} – {to}", "from", i18n.Date(lang, s, i18n.DayMonth), "to", i18n.Date(lang, last, i18n.DayMonthYear))
	}
	out := i18n.T(lang, "{date} · {from}–{to}", "date", i18n.Date(lang, s, i18n.DayMonthYear), "from", clock(s), "to", clock(e))
	if midnight(e) != midnight(s) {
		out = i18n.T(lang, "{from} – {to}", "from", i18n.Date(lang, s, i18n.DayMonthAt), "to", i18n.Date(lang, e, i18n.DayMonthYearAt))
	}
	if ev.Zone != "" && ev.Zone != home.String() {
		if loc := calZone(ev.Zone); loc != nil {
			out = i18n.T(lang, "{when} ({from}–{to} {zone})", "when", out, "from", clock(ev.Start.In(loc)), "to", clock(ev.End.In(loc)), "zone", ev.Zone)
		}
	}
	return out
}

// calAnd joins names as a sentence lists them: "Tuesday and Thursday".
func calAnd(lang string, xs []string) string {
	if len(xs) == 1 {
		return xs[0]
	}
	return i18n.T(lang, "{list} and {last}", "list", strings.Join(xs[:len(xs)-1], ", "), "last", xs[len(xs)-1])
}

// repeatWords is a repeat as a sentence: "Every 2 weeks on Tuesday and
// Thursday, until 15 December 2026". Each shape is a sentence of its own,
// so a language orders it as it says it.
func repeatWords(lang, rule string, start time.Time) string {
	opt, err := rrule.StrToROption(rule)
	if err != nil {
		return i18n.T(lang, "Repeats")
	}
	n := max(opt.Interval, 1)
	var out string
	switch opt.Freq {
	case rrule.DAILY:
		out = i18n.T(lang, "{n, plural, one {Every day} other {Every # days}}", "n", n)
	case rrule.WEEKLY:
		out = i18n.T(lang, "{n, plural, one {Every week} other {Every # weeks}}", "n", n)
	case rrule.MONTHLY:
		out = i18n.T(lang, "{n, plural, one {Every month} other {Every # months}}", "n", n)
	case rrule.YEARLY:
		out = i18n.T(lang, "{n, plural, one {Every year} other {Every # years}}", "n", n)
	default:
		return i18n.T(lang, "Repeats")
	}
	switch opt.Freq {
	case rrule.WEEKLY:
		// rrule counts Monday as 0; time.Weekday counts Sunday as 0.
		var days []time.Weekday
		for _, d := range opt.Byweekday {
			days = append(days, time.Weekday((d.Day()+1)%7))
		}
		if len(days) == 0 {
			days = []time.Weekday{start.Weekday()}
		}
		weekend := slices.Contains(days, time.Saturday) || slices.Contains(days, time.Sunday)
		if len(days) == 5 && !weekend && n == 1 {
			out = i18n.T(lang, "Every weekday")
		} else {
			names := make([]string, len(days))
			for i, d := range days {
				names[i] = i18n.WeekdayName(lang, d)
			}
			out = i18n.T(lang, "{every} on {days}", "every", out, "days", calAnd(lang, names))
		}
	case rrule.MONTHLY:
		switch {
		case len(opt.Bymonthday) == 1 && opt.Bymonthday[0] > 0:
			out = i18n.T(lang, "{every} on day {day}", "every", out, "day", opt.Bymonthday[0])
		case len(opt.Bymonthday) == 0 && len(opt.Byweekday) == 0:
			out = i18n.T(lang, "{every} on day {day}", "every", out, "day", start.Day())
		}
	case rrule.YEARLY:
		if len(opt.Bymonth) == 0 && len(opt.Byweekday) == 0 {
			out = i18n.T(lang, "{every} on {date}", "every", out, "date", i18n.Date(lang, start, i18n.DayMonthPlain))
		}
	}
	switch {
	case !opt.Until.IsZero():
		out = i18n.T(lang, "{rule}, until {date}", "rule", out, "date", i18n.Date(lang, opt.Until.In(start.Location()), i18n.DayMonthYearPlain))
	case opt.Count == 1:
		out = i18n.T(lang, "{rule}, once", "rule", out)
	case opt.Count > 1:
		out = i18n.T(lang, "{rule}, {n} times", "rule", out, "n", opt.Count)
	}
	return out
}

func (a *App) handleVayuOSCalendarEvent(w http.ResponseWriter, r *http.Request) {
	lang := i18n.Lang(r.Context())
	rd, ok := a.calendarOwner(r)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(lang, "A calendar opens only for its holder."), "")
		return
	}
	home := a.calendarHome(r)
	ev, found := a.findOccurrence(r, rd, home)
	if !found {
		writeOSHTML(w, r, saCallout("danger", esc(i18n.T(lang, "This event is no longer in the calendar. It may have been changed on another device."))))
		return
	}
	var b strings.Builder
	b.WriteString(`<div class="cal-detail" data-sheet-title="` + esc(calTitle(lang, ev)) + `">`)
	b.WriteString(`<p class="cal-detail__row">` + saIcon("calendar") + `<span>` + esc(calWhen(lang, ev, home)) + `</span></p>`)
	if ev.Rule != "" {
		b.WriteString(`<p class="cal-detail__row">` + saIcon("refresh") + `<span>` + esc(repeatWords(lang, ev.Rule, ev.Start)) + `</span></p>`)
	}
	if ev.Location != "" {
		b.WriteString(`<p class="cal-detail__row">` + saIcon("where") + `<span>` + esc(ev.Location) + `</span></p>`)
	}
	if ev.Remind != nil {
		words := leadWords(lang, ev.Remind.Before)
		if ev.Remind.ByMail {
			words = i18n.T(lang, "{lead}, and by mail", "lead", words)
		}
		b.WriteString(`<p class="cal-detail__row">` + saIcon("bell") + `<span>` + esc(words) + `</span></p>`)
	}
	if len(ev.Attendees) > 0 {
		b.WriteString(`<ul class="cal-detail__people">`)
		for _, p := range ev.Attendees {
			b.WriteString(`<li><span>` + esc(nonEmpty(p.Name, p.Email)) + `</span> <span class="muted">` + esc(partStat(lang, p.Status)) + `</span></li>`)
		}
		b.WriteString(`</ul>`)
	}
	if ev.Description != "" {
		b.WriteString(`<p class="cal-detail__notes">` + esc(ev.Description) + `</p>`)
	}
	if ev.Sub != "" {
		b.WriteString(`<p class="cal-detail__row">` + saIcon("refresh") + `<span>` + esc(i18n.T(lang, "From {calendar}, a subscribed calendar: shown here, changed only where it comes from.", "calendar", ev.Sub)) + `</span></p></div>`)
		writeOSHTML(w, r, b.String())
		return
	}
	ref := calEventRef(ev)
	edit := func(scope, label string) string {
		return `<button type="button" class="btn btn--sm" hx-get="/os/vayumail/calendar/edit?` + ref + `&amp;scope=` + scope + `" hx-target="#cal-sheet-body" hx-swap="innerHTML">` + esc(label) + `</button>`
	}
	b.WriteString(`<div class="cal-detail__acts">`)
	if ev.Repeats() {
		b.WriteString(edit(vmail.ScopeAll, i18n.T(lang, "Edit")) + edit(vmail.ScopeOne, i18n.T(lang, "This one only")))
	} else {
		b.WriteString(edit(vmail.ScopeAll, i18n.T(lang, "Edit")))
	}
	b.WriteString(`</div><form class="cal-detail__delete" hx-post="/os/vayumail/calendar/action" hx-target="#cal-sheet-body" hx-swap="innerHTML">` +
		`<input type="hidden" name="op" value="delete"><input type="hidden" name="uid" value="` + esc(ev.UID) + `">` +
		`<input type="hidden" name="i" value="` + strconv.FormatInt(ev.Instance.Unix(), 10) + `"><input type="hidden" name="d" value="` + calDate(ev.Start.In(home)) + `">`)
	if ev.Repeats() {
		b.WriteString(`<fieldset class="cal-scope"><legend>` + esc(i18n.T(lang, "Delete")) + `</legend>` + scopeRadios(lang, vmail.ScopeOne) + `</fieldset>`)
	} else {
		b.WriteString(`<input type="hidden" name="scope" value="` + vmail.ScopeAll + `">`)
	}
	b.WriteString(`<button type="submit" class="btn btn--danger btn--sm">` + esc(i18n.T(lang, "Delete")) + `</button></form></div>`)
	writeOSHTML(w, r, b.String())
}

func partStat(lang, s string) string {
	switch s {
	case "ACCEPTED":
		return i18n.T(lang, "going")
	case "DECLINED":
		return i18n.T(lang, "not going")
	case "TENTATIVE":
		return i18n.T(lang, "maybe")
	}
	return i18n.T(lang, "no answer yet")
}

func scopeRadios(lang, on string) string {
	var b strings.Builder
	for _, o := range [][2]string{{vmail.ScopeOne, i18n.T(lang, "This event")}, {vmail.ScopeFollowing, i18n.T(lang, "This and following events")}, {vmail.ScopeAll, i18n.T(lang, "All events")}} {
		chk := ""
		if o[0] == on {
			chk = " checked"
		}
		b.WriteString(`<label class="cal-scope__opt"><input type="radio" name="scope" value="` + o[0] + `"` + chk + ` data-cal-scope> ` + esc(o[1]) + `</label>`)
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
	lang := i18n.Lang(r.Context())
	rd, ok := a.calendarOwner(r)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(lang, "A calendar opens only for its holder."), "")
		return
	}
	home := a.calendarHome(r)
	var ev vmail.CalEvent
	scope := r.URL.Query().Get("scope")
	if r.URL.Query().Get("uid") != "" {
		found := false
		if ev, found = a.findOccurrence(r, rd, home); !found {
			writeOSHTML(w, r, saCallout("danger", esc(i18n.T(lang, "This event is no longer in the calendar. It may have been changed on another device."))))
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
	_, own := a.ownMailbox(r)
	writeOSHTML(w, r, calEditor(lang, ev, scope, home, own, ""))
}

// calEditor is the event's form, in the event's own zone. own is the
// holder's address: an event someone else organizes keeps its guests as
// they set them.
func calEditor(lang string, ev vmail.CalEvent, scope string, home *time.Location, own, msg string) string {
	zone := home
	if loc := calZone(ev.Zone); loc != nil {
		zone = loc
	}
	s, e := ev.Start.In(zone), ev.End.In(zone)
	if ev.AllDay {
		s, e = ev.Start.In(home), ev.End.In(home).AddDate(0, 0, -1)
	}
	title := i18n.T(lang, "New event")
	if ev.UID != "" {
		title = i18n.T(lang, "Edit event")
	}
	var b strings.Builder
	b.WriteString(`<form class="cal-form" data-sheet-title="` + esc(title) + `" hx-post="/os/vayumail/calendar/action" hx-target="#cal-sheet-body" hx-swap="innerHTML" data-cal-form>`)
	b.WriteString(`<div id="cal-form-msg">` + msg + `</div>`)
	b.WriteString(`<input type="hidden" name="op" value="save"><input type="hidden" name="uid" value="` + esc(ev.UID) + `">`)
	if ev.Repeats() {
		b.WriteString(`<input type="hidden" name="i" value="` + strconv.FormatInt(ev.Instance.Unix(), 10) + `">`)
		if scope == "" {
			scope = vmail.ScopeAll
		}
		b.WriteString(`<fieldset class="cal-scope"><legend>` + esc(i18n.T(lang, "Change")) + `</legend>` + scopeRadios(lang, scope) + `</fieldset>`)
	}
	field := func(label, input string) string {
		return `<label class="field"><span class="field-label">` + esc(label) + `</span>` + input + `</label>`
	}
	b.WriteString(field(i18n.T(lang, "Title"), `<input type="text" name="title" required maxlength="300" value="`+esc(ev.Summary)+`" autocomplete="off">`))
	allDay := ""
	if ev.AllDay {
		allDay = " checked"
	}
	b.WriteString(`<label class="cal-check"><input type="checkbox" name="allday" value="1"` + allDay + ` data-cal-allday> ` + esc(i18n.T(lang, "All day")) + `</label>`)
	b.WriteString(`<div class="cal-two">` +
		field(i18n.T(lang, "Starts"), `<span class="cal-when"><input type="date" name="sd" required value="`+calDate(s)+`"><input type="time" name="st" value="`+clock(s)+`" data-cal-time></span>`) +
		field(i18n.T(lang, "Ends"), `<span class="cal-when"><input type="date" name="ed" required value="`+calDate(e)+`"><input type="time" name="et" value="`+clock(e)+`" data-cal-time></span>`) + `</div>`)
	b.WriteString(field(i18n.T(lang, "Time zone"), `<input type="text" name="zone" list="cal-zones" value="`+esc(zone.String())+`" autocomplete="off" spellcheck="false" data-cal-time>`))
	b.WriteString(calRepeatFields(lang, ev))
	if ev.Organizer.Email != "" && !strings.EqualFold(ev.Organizer.Email, own) {
		b.WriteString(`<p class="cal-note">` + esc(i18n.T(lang, "Organized by {name}. Its guests are theirs; a change you make here stays in your own calendar.", "name", nonEmpty(ev.Organizer.Name, ev.Organizer.Email))) + `</p>`)
	} else {
		var guests []string
		for _, g := range ev.Attendees {
			guests = append(guests, g.Email)
		}
		b.WriteString(field(i18n.T(lang, "Guests"), `<input type="text" name="guests" value="`+esc(strings.Join(guests, ", "))+`" placeholder="`+esc(i18n.T(lang, "Addresses, separated by commas"))+`" autocomplete="off" spellcheck="false">`))
		b.WriteString(`<p class="cal-note">` + esc(i18n.T(lang, "Guests are sent the event, and each change to it, by mail from your address.")) + `</p>`)
	}
	b.WriteString(field(i18n.T(lang, "Where"), `<input type="text" name="location" maxlength="500" value="`+esc(ev.Location)+`">`))
	b.WriteString(calRemindFields(lang, ev))
	b.WriteString(field(i18n.T(lang, "Notes"), `<textarea name="notes" rows="3" maxlength="20000">`+esc(ev.Description)+`</textarea>`))
	b.WriteString(`<div class="cal-form__acts"><button type="button" class="btn" data-sheet-close>` + esc(i18n.T(lang, "Cancel")) + `</button><button type="submit" class="btn btn--primary">` + esc(i18n.T(lang, "Save")) + `</button></div></form>`)
	return b.String()
}

// calLeads are the reminders the editor offers, in seconds before.
var calLeads = []int{0, 300, 600, 1800, 3600, 86400, 604800}

// leadWords is a reminder's lead in words.
func leadWords(lang string, d time.Duration) string {
	switch d {
	case 0:
		return i18n.T(lang, "At the start")
	case 5 * time.Minute:
		return i18n.T(lang, "5 minutes before")
	case 10 * time.Minute:
		return i18n.T(lang, "10 minutes before")
	case 30 * time.Minute:
		return i18n.T(lang, "30 minutes before")
	case time.Hour:
		return i18n.T(lang, "1 hour before")
	case 24 * time.Hour:
		return i18n.T(lang, "1 day before")
	case 7 * 24 * time.Hour:
		return i18n.T(lang, "1 week before")
	}
	return i18n.T(lang, "{time} before", "time", d.String())
}

// calRemindFields is the reminder part of the editor: when, and whether by
// mail as well as on the holder's devices.
func calRemindFields(lang string, ev vmail.CalEvent) string {
	cur, mail := -1, false
	if ev.Remind != nil {
		cur, mail = int(ev.Remind.Before/time.Second), ev.Remind.ByMail
	}
	var b strings.Builder
	b.WriteString(`<input type="hidden" name="remind_set" value="1"><div class="cal-two"><label class="field"><span class="field-label">` + esc(i18n.T(lang, "Remind me")) + `</span><select name="remind">`)
	b.WriteString(`<option value=""` + map[bool]string{true: " selected"}[cur < 0] + `>` + esc(i18n.T(lang, "No reminder")) + `</option>`)
	listed := false
	for _, secs := range calLeads {
		sel := ""
		if secs == cur {
			sel, listed = " selected", true
		}
		b.WriteString(`<option value="` + strconv.Itoa(secs) + `"` + sel + `>` + esc(leadWords(lang, time.Duration(secs)*time.Second)) + `</option>`)
	}
	if cur >= 0 && !listed {
		b.WriteString(`<option value="` + strconv.Itoa(cur) + `" selected>` + esc(leadWords(lang, time.Duration(cur)*time.Second)) + `</option>`)
	}
	chk := ""
	if mail {
		chk = " checked"
	}
	b.WriteString(`</select></label><label class="cal-check cal-check--field"><input type="checkbox" name="remind_mail" value="1"` + chk + `> ` + esc(i18n.T(lang, "By mail too")) + `</label></div>`)
	return b.String()
}

// calRepeatFields is the repeat part of the editor: how often, on which days
// for a week, and when it ends.
func calRepeatFields(lang string, ev vmail.CalEvent) string {
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
	repeat := esc(i18n.T(lang, "Repeat"))
	b.WriteString(`<fieldset class="cal-repeat" data-cal-repeat><legend>` + repeat + `</legend><div class="cal-repeat__row"><select name="r_freq" aria-label="` + repeat + `" data-cal-freq>`)
	opts := [][2]string{{"", i18n.T(lang, "Does not repeat")}, {"DAILY", i18n.T(lang, "Daily")}, {"WEEKLY", i18n.T(lang, "Weekly")}, {"MONTHLY", i18n.T(lang, "Monthly")}, {"YEARLY", i18n.T(lang, "Yearly")}}
	if keep {
		opts = append([][2]string{{"keep", i18n.T(lang, "As it is: {rule}", "rule", repeatWords(lang, ev.Rule, ev.Start))}}, opts...)
		freq = "keep"
	}
	for _, o := range opts {
		sel := ""
		if o[0] == freq {
			sel = " selected"
		}
		b.WriteString(`<option value="` + o[0] + `"` + sel + `>` + esc(o[1]) + `</option>`)
	}
	b.WriteString(`</select><label class="cal-repeat__every" data-cal-when-freq>` + esc(i18n.T(lang, "every")) + ` <input type="number" name="r_int" min="1" max="99" value="` + strconv.Itoa(interval) + `" aria-label="` + esc(i18n.T(lang, "Every how many")) + `"></label></div>`)
	b.WriteString(`<div class="cal-repeat__days" data-cal-when-weekly role="group" aria-label="` + esc(i18n.T(lang, "On")) + `">`)
	heads := i18n.WeekdayHeads(lang)
	for i, d := range []string{"MO", "TU", "WE", "TH", "FR", "SA", "SU"} {
		chk := ""
		if days[i] {
			chk = " checked"
		}
		b.WriteString(`<label class="cal-day"><input type="checkbox" name="r_day" value="` + d + `"` + chk + `><span>` + esc(heads[i]) + `</span></label>`)
	}
	b.WriteString(`</div><div class="cal-repeat__row" data-cal-when-freq><select name="r_end" aria-label="` + esc(i18n.T(lang, "Ends")) + `">`)
	for _, o := range [][2]string{{"never", i18n.T(lang, "Never ends")}, {"until", i18n.T(lang, "Ends on")}, {"count", i18n.T(lang, "Ends after")}} {
		sel := ""
		if o[0] == end {
			sel = " selected"
		}
		b.WriteString(`<option value="` + o[0] + `"` + sel + `>` + esc(o[1]) + `</option>`)
	}
	b.WriteString(`</select><input type="date" name="r_until" value="` + until + `" aria-label="` + esc(i18n.T(lang, "Last day")) + `"><label class="cal-repeat__every"><input type="number" name="r_count" min="1" max="999" value="` + count + `" aria-label="` + esc(i18n.T(lang, "How many times")) + `"> ` + esc(i18n.T(lang, "times")) + `</label></div>`)
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
	if r.PostFormValue("remind_set") == "1" {
		in.SetRemind = true
		if v := r.PostFormValue("remind"); v != "" {
			secs, err := strconv.Atoi(v)
			if err != nil {
				return in, errors.New("the reminder is not one the editor offers")
			}
			in.Remind = &vmail.Reminder{Before: time.Duration(secs) * time.Second, ByMail: r.PostFormValue("remind_mail") == "1"}
		}
	}
	if _, ok := r.PostForm["guests"]; ok {
		in.Guests = []string{}
		for _, g := range strings.FieldsFunc(r.PostFormValue("guests"), func(c rune) bool { return c == ',' || c == ';' || c == '\n' }) {
			if g = strings.TrimSpace(g); g != "" {
				in.Guests = append(in.Guests, g)
			}
		}
	}
	in.Rule, err = ruleFromCalForm(r, in.Start, in.AllDay)
	return in, err
}

func (a *App) handleVayuOSCalendarAction(w http.ResponseWriter, r *http.Request) {
	lang := i18n.Lang(r.Context())
	rd, ok := a.calendarOwner(r)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(lang, "A calendar opens only for its holder."), "")
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
		back := "/os/vayumail/calendar?v=week&d=" + calDate(in.Start.In(home))
		if errors.Is(err, vmail.ErrInvitationsNotSent) {
			// Saved: the calendar says so, and that a guest was not told.
			w.Header().Set("HX-Redirect", back+"&note=unsent")
			writeOSHTML(w, r, "")
			return
		}
		if err != nil {
			ev := vmail.CalEvent{UID: in.UID, Summary: in.Summary, Location: in.Location, Description: in.Description, Start: in.Start, End: in.End, AllDay: in.AllDay, Rule: in.Rule, Instance: inst}
			for _, g := range in.Guests {
				ev.Attendees = append(ev.Attendees, vmail.CalAttendee{Email: g})
			}
			if in.Start.IsZero() {
				ev.Start = time.Now().In(home)
				ev.End = ev.Start
			} else if !in.AllDay {
				ev.Zone = in.Start.Location().String()
			}
			_, own := a.ownMailbox(r)
			writeOSHTML(w, r, calEditor(lang, ev, scope, home, own, saCallout("danger", esc(i18n.T(lang, "Not saved: {reason}.", "reason", calSaveRefusal(lang, err))))))
			return
		}
		_ = uid
		w.Header().Set("HX-Redirect", back)
		writeOSHTML(w, r, "")
	case "delete":
		if err := a.vayuMail.DeleteEvent(rd, r.PostFormValue("uid"), scope, inst); err != nil {
			writeOSHTML(w, r, saCallout("danger", esc(i18n.T(lang, "The event was not deleted: {reason}.", "reason", calSaveRefusal(lang, err)))))
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
func calSaveRefusal(lang string, err error) string {
	msg := strings.TrimPrefix(err.Error(), "vayumail: ")
	if strings.Contains(msg, "changed since it was read") {
		return i18n.T(lang, "the event was changed on another device since you opened it: close this and open it again")
	}
	return msg
}
