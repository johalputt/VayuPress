// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_booking.go — the holder's booking pages, in the Calendar: one
// page per kind of meeting, its link, what it offers, and what was booked on
// it. The public page is book_public.go; the engine is
// internal/vayuos/mail/booking.go.

import (
	htmpl "html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/render"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// bookingOwner is a mailbox as a booking link names it: the local part on
// the primary domain, the whole address on another.
func bookingOwner(mbox, primary string) string {
	if at := strings.LastIndex(mbox, "@"); at >= 0 && strings.EqualFold(mbox[at+1:], primary) {
		return mbox[:at]
	}
	return mbox
}

// bookingLink is a page's public address.
func (a *App) bookingLink(r *http.Request, p vmail.BookingPage) string {
	return a.oauthBaseURL(r) + "/book/" + url.PathEscape(bookingOwner(p.Mailbox, a.vayuMail.Config().Domain)) + "/" + p.Slug
}

var bookingDayNames = [7]string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

// hoursText is a day's windows as the editor shows them: "10:00-13:00, 15:00-18:00".
func hoursText(ws [][2]int) string {
	var out []string
	hm := func(m int) string { return strconv.Itoa(100 + m/60)[1:] + ":" + strconv.Itoa(100 + m%60)[1:] }
	for _, w := range ws {
		out = append(out, hm(w[0])+"-"+hm(w[1]))
	}
	return strings.Join(out, ", ")
}

// parseHours reads a day's windows from the editor.
func parseHours(s string) ([][2]int, bool) {
	var out [][2]int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(strings.ReplaceAll(part, "–", "-"))
		if part == "" {
			continue
		}
		ends := strings.SplitN(part, "-", 2)
		if len(ends) != 2 {
			return nil, false
		}
		var mins [2]int
		for i, e := range ends {
			t, err := time.Parse("15:04", strings.TrimSpace(e))
			if err != nil {
				if strings.TrimSpace(e) != "24:00" {
					return nil, false
				}
				mins[i] = 24 * 60
				continue
			}
			mins[i] = t.Hour()*60 + t.Minute()
		}
		out = append(out, mins)
	}
	return out, true
}

var bookingLengths = []int{15, 30, 45, 60, 90}

// bookingEditor is a page's form, and its bookings below.
func (a *App) bookingEditor(r *http.Request, p vmail.BookingPage, bookings []vmail.Booking, csrf, msg string) string {
	var b strings.Builder
	title := "New booking page"
	if p.ID > 0 {
		title = p.Title
	}
	b.WriteString(`<div class="cal-booking" data-page-kind="settings"><div class="page-header"><h1>` + esc(title) + `</h1>` +
		`<p class="page-sub">A page anyone with the link can book a free time on. They see only free and busy, never what is in your calendar.</p></div>`)
	b.WriteString(msg)
	if p.ID > 0 {
		link := a.bookingLink(r, p)
		b.WriteString(`<p class="cal-booking__link">` + saIcon("link") + `<a href="` + esc(link) + `" target="_blank" rel="noopener">` + esc(link) + `</a></p>`)
	}
	b.WriteString(`<form class="cal-form" method="post" action="/os/vayumail/calendar/booking/action">`)
	b.WriteString(`<input type="hidden" name="csrf_token" value="` + esc(csrf) + `"><input type="hidden" name="op" value="save"><input type="hidden" name="id" value="` + strconv.FormatInt(p.ID, 10) + `">`)
	field := func(label, input string) string {
		return `<label class="field"><span class="field-label">` + label + `</span>` + input + `</label>`
	}
	b.WriteString(`<div class="cal-two">` + field("Name", `<input type="text" name="title" required maxlength="120" value="`+esc(p.Title)+`" placeholder="Intro call">`) +
		field("Name in the link", `<input type="text" name="slug" required maxlength="40" pattern="[a-z0-9][a-z0-9-]*" value="`+esc(p.Slug)+`" placeholder="intro">`) + `</div>`)
	b.WriteString(`<fieldset class="cal-repeat"><legend>Lengths offered</legend><div class="cal-repeat__row">`)
	for _, l := range bookingLengths {
		chk := ""
		for _, o := range p.Lengths {
			if o == l {
				chk = " checked"
			}
		}
		b.WriteString(`<label class="cal-check"><input type="checkbox" name="length" value="` + strconv.Itoa(l) + `"` + chk + `> ` + strconv.Itoa(l) + ` min</label>`)
	}
	b.WriteString(`</div></fieldset>`)
	b.WriteString(`<fieldset class="cal-repeat"><legend>Weekly hours, in ` + esc(p.Zone) + `</legend>`)
	for d, name := range bookingDayNames {
		b.WriteString(`<label class="cal-booking__day"><span>` + name + `</span><input type="text" name="hours` + strconv.Itoa(d) + `" value="` + esc(hoursText(p.Hours[d])) +
			`" placeholder="unavailable" aria-label="` + name + ` hours, as 10:00-13:00, 15:00-18:00"></label>`)
	}
	b.WriteString(`<input type="hidden" name="zone" value="` + esc(p.Zone) + `"></fieldset>`)
	num := func(name string, v, max int) string {
		return `<input type="number" name="` + name + `" min="0" max="` + strconv.Itoa(max) + `" value="` + strconv.Itoa(v) + `">`
	}
	b.WriteString(`<div class="cal-two">` + field("Minutes kept free before", num("before", p.Before, 240)) + field("Minutes kept free after", num("after", p.After, 240)) + `</div>`)
	b.WriteString(`<div class="cal-two">` + field("At least this many hours ahead", num("notice", p.Notice/60, 720)) + field("Up to this many days out", num("reach", p.Reach, 180)) + `</div>`)
	b.WriteString(`<div class="cal-two">` + field("Most bookings in a day (0 for no limit)", num("perday", p.PerDay, 100)) +
		field("A question for the guest", `<input type="text" name="question" maxlength="300" value="`+esc(p.Question)+`" placeholder="What would you like to talk about?">`) + `</div>`)
	remind := p.Remind
	if p.ID == 0 {
		remind = 1440
	}
	opts := ""
	for _, o := range []struct {
		v     int
		label string
	}{{0, "No reminder"}, {60, "An hour before"}, {1440, "A day before"}} {
		sel := ""
		if o.v == remind {
			sel = " selected"
		}
		opts += `<option value="` + strconv.Itoa(o.v) + `"` + sel + `>` + o.label + `</option>`
	}
	reps := ""
	for _, n := range []int{0, 2, 4, 6, 8, 12} {
		sel, label := "", "Single bookings only"
		if n == p.Repeats {
			sel = " selected"
		}
		if n > 0 {
			label = "Up to " + strconv.Itoa(n) + " in a series"
		}
		reps += `<option value="` + strconv.Itoa(n) + `"` + sel + `>` + label + `</option>`
	}
	b.WriteString(`<div class="cal-two">` + field("Remind the guest and you", `<select name="remind">`+opts+`</select>`) +
		field("A guest may book the same time weekly or every two weeks", `<select name="repeats">`+reps+`</select>`) + `</div>`)
	on := ""
	if p.Enabled || p.ID == 0 {
		on = " checked"
	}
	b.WriteString(`<label class="cal-check"><input type="checkbox" name="enabled" value="1"` + on + `> Taking bookings</label>`)
	b.WriteString(`<p class="cal-note">The guest confirms by a link sent to their address before the time is booked; the booking then comes into your calendar with the guest invited.</p>`)
	b.WriteString(`<div class="cal-form__acts">`)
	if p.ID > 0 {
		b.WriteString(`<button type="submit" class="btn btn--danger" name="op" value="delete" formnovalidate>Delete page</button>`)
	}
	b.WriteString(`<button type="submit" class="btn btn--primary">Save</button></div></form>`)
	if p.ID > 0 {
		b.WriteString(`<h2 class="cal-booking__h">Recent bookings</h2>`)
		if len(bookings) == 0 {
			b.WriteString(`<p class="cal-note">Nothing booked yet.</p>`)
		}
		loc := calZone(p.Zone)
		if loc == nil {
			loc = time.UTC
		}
		b.WriteString(`<ul class="cal-booking__list">`)
		for _, bk := range bookings {
			state := map[string]string{"held": "waiting for their confirmation", "confirmed": "confirmed", "cancelled": "cancelled", "checking": "being confirmed"}[bk.Status]
			b.WriteString(`<li><span class="cal-booking__who">` + esc(bk.Name) + ` <span class="muted">` + esc(bk.Email) + `</span></span>` +
				`<span class="muted">` + esc(bk.Start.In(loc).Format("Mon 2 Jan · 15:04")) + ` · ` + strconv.Itoa(int(bk.End.Sub(bk.Start).Minutes())) + ` min · ` + esc(state) + `</span></li>`)
		}
		b.WriteString(`</ul>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

func (a *App) handleVayuOSBookingPage(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() || a.vayuMail.Accounts() == nil {
		a.writeMailSetup(w, r, "Booking pages")
		return
	}
	rd, ok := a.calendarOwner(r)
	if !ok {
		page := `<div data-page-kind="status"><div class="page-header"><h1>Booking pages</h1></div><div class="empty-state">Booking pages belong to a mailbox, and this account has none. An administrator can give you an address under <strong>Members → Team &amp; roles</strong>.</div></div>`
		writeOSHTML(w, r, adminOSLayout(render.CSPNonce(r), "Booking pages", "vayuos", a.getOSSettings(r.Context()), htmpl.HTML(page)))
		return
	}
	mbox := a.calendarAddress(rd)
	home := a.calendarHome(r)
	p := vmail.BookingPage{Mailbox: mbox, Zone: home.String(), Lengths: []int{30}, Before: 10, After: 10, Notice: 4 * 60, Reach: 30, Enabled: true}
	for d := 0; d < 5; d++ {
		p.Hours[d] = [][2]int{{10 * 60, 13 * 60}, {15 * 60, 18 * 60}}
	}
	var bookings []vmail.Booking
	if id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64); err == nil && id > 0 {
		got, err := a.vayuMail.Accounts().BookingPage(r.Context(), mbox, id, "")
		if err != nil {
			http.Redirect(w, r, "/os/vayumail/calendar", http.StatusSeeOther)
			return
		}
		p = got
		bookings, _ = a.vayuMail.Accounts().BookingsFor(r.Context(), mbox, p.ID, 20)
	}
	a.writeBookingPage(w, r, rd, p, bookings, "")
}

func (a *App) writeBookingPage(w http.ResponseWriter, r *http.Request, rd vmail.Reader, p vmail.BookingPage, bookings []vmail.Booking, msg string) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	csrf := csrfTokenFor(w, r)
	home := a.calendarHome(r)
	now := time.Now()
	pages, _ := a.vayuMail.Accounts().BookingPagesFor(r.Context(), a.calendarAddress(rd))
	side := a.mailSide(r, rd, "", "", nil)
	on := p.ID
	if on == 0 {
		on = -1
	}
	side.Place, side.PlaceSide = "calendar", calSide(calViewFor("week", "", home, now), home, midnight(now.In(home)), pages, on)
	cfg.MailSide = side
	writeOSHTML(w, r, adminOSLayout(nonce, "Booking pages", "vayuos", cfg, htmpl.HTML(a.bookingEditor(r, p, bookings, csrf, msg))))
}

// handleVayuOSBookingAction saves or deletes one of the session's own pages.
func (a *App) handleVayuOSBookingAction(w http.ResponseWriter, r *http.Request) {
	rd, ok := a.calendarOwner(r)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "Booking pages belong to a mailbox, and this account has none.", "")
		return
	}
	_ = r.ParseForm()
	mbox := a.calendarAddress(rd)
	accts := a.vayuMail.Accounts()
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	if r.PostFormValue("op") == "delete" {
		_ = accts.DeleteBookingPage(r.Context(), mbox, id)
		http.Redirect(w, r, "/os/vayumail/calendar", http.StatusSeeOther)
		return
	}
	atoi := func(name string) int { n, _ := strconv.Atoi(strings.TrimSpace(r.PostFormValue(name))); return n }
	p := vmail.BookingPage{ID: id, Mailbox: mbox, Slug: r.PostFormValue("slug"), Title: r.PostFormValue("title"), Zone: r.PostFormValue("zone"),
		Before: atoi("before"), After: atoi("after"), Notice: atoi("notice") * 60, Reach: atoi("reach"), PerDay: atoi("perday"),
		Question: r.PostFormValue("question"), Remind: atoi("remind"), Repeats: atoi("repeats"), Enabled: r.PostFormValue("enabled") == "1"}
	for _, l := range r.PostForm["length"] {
		if n, err := strconv.Atoi(l); err == nil {
			p.Lengths = append(p.Lengths, n)
		}
	}
	msg := ""
	for d := range bookingDayNames {
		ws, ok := parseHours(r.PostFormValue("hours" + strconv.Itoa(d)))
		if !ok {
			msg = saCallout("danger", esc(bookingDayNames[d])+"'s hours are not in the shape 10:00-13:00, 15:00-18:00.")
		}
		p.Hours[d] = ws
	}
	if msg == "" {
		saved, err := accts.SaveBookingPage(r.Context(), p)
		if err == nil {
			http.Redirect(w, r, "/os/vayumail/calendar/booking?id="+strconv.FormatInt(saved, 10), http.StatusSeeOther)
			return
		}
		msg = saCallout("danger", esc(strings.TrimPrefix(err.Error(), "vayumail: "))+".")
	}
	var bookings []vmail.Booking
	if p.ID > 0 {
		bookings, _ = accts.BookingsFor(r.Context(), mbox, p.ID, 20)
	}
	a.writeBookingPage(w, r, rd, p, bookings, msg)
}
