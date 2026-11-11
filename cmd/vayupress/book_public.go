// SPDX-License-Identifier: Apache-2.0

package main

// book_public.go — a booking page as a guest sees it, at /book/<name>/<page>
// on the site's own host: the days with free times, a day's times in the
// guest's own zone, then their details. Nothing of the holder's calendar is
// on it but free times (internal/vayuos/mail/booking.go works them out).
//
// It is plain pages and links, with no session: a guest's zone comes from
// book.js on the first visit, as ?tz=, and the install's zone stands in
// until then. Its forms carry no CSRF token because there is nothing
// ambient to forge: the guest's address is the only thing they act with,
// and the booking waits for a link sent to it. The confirm and cancel links
// only ask; their button does it, so a mail scanner opening links cannot
// confirm or cancel a booking.

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

func (a *App) writeBookShell(w http.ResponseWriter, status int, title, inner string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(authPageShell(title, `<div class="login-card book-card">`+inner+`</div>`+
		`<script src="/os/static/js/book.js?v=`+assetVer("js/book.js")+`"></script>`)))
}

// publicBookingPage is the page a link names, when it is taking bookings.
func (a *App) publicBookingPage(r *http.Request) (vmail.BookingPage, bool) {
	if !a.mailRunning() || a.vayuMail.Accounts() == nil {
		return vmail.BookingPage{}, false
	}
	owner, slug := chi.URLParam(r, "owner"), chi.URLParam(r, "slug")
	if o, err := url.PathUnescape(owner); err == nil {
		owner = o
	}
	mbox := owner
	if !strings.Contains(owner, "@") {
		mbox = owner + "@" + a.vayuMail.Config().Domain
	}
	p, err := a.vayuMail.Accounts().BookingPage(r.Context(), mbox, 0, slug)
	return p, err == nil && p.Enabled
}

func (a *App) bookNotFound(w http.ResponseWriter) {
	a.writeBookShell(w, http.StatusNotFound, "Not found", `<h1 class="login-title">This booking page is not here</h1><p class="login-sub">It may have been switched off. Ask whoever sent you the link.</p>`)
}

// guestZone is the zone a guest's times are shown in.
func guestZone(r *http.Request, p vmail.BookingPage) *time.Location {
	if loc := calZone(r.URL.Query().Get("tz")); loc != nil {
		return loc
	}
	if loc := calZone(p.Zone); loc != nil {
		return loc
	}
	return time.UTC
}

// bookSlots bounds the booking pages worked out at once. A page's free times
// are a month of the holder's calendar expanded, for anyone who asks, with no
// session to limit them by: past these, a visit waits a moment and is then
// told the page is busy, so a flood of visits costs four expansions at a
// time and no more.
var bookSlots = make(chan struct{}, 4)

// bookWait is how long a visit waits for one of bookSlots.
var bookWait = 3 * time.Second

func (a *App) handleBookPage(w http.ResponseWriter, r *http.Request) {
	p, ok := a.publicBookingPage(r)
	if !ok {
		a.bookNotFound(w)
		return
	}
	select {
	case bookSlots <- struct{}{}:
		defer func() { <-bookSlots }()
	case <-time.After(bookWait):
		w.Header().Set("Retry-After", "5")
		a.writeBookShell(w, http.StatusServiceUnavailable, p.Title, `<h1 class="login-title">Busy</h1><p class="login-sub">This page is busy. Try again in a moment.</p>`)
		return
	}
	loc := guestZone(r, p)
	q := r.URL.Query()
	length := p.Lengths[0]
	if n, err := strconv.Atoi(q.Get("len")); err == nil {
		for _, l := range p.Lengths {
			if l == n {
				length = l
			}
		}
	}
	now := time.Now()
	if r.Method == http.MethodPost {
		a.bookHold(w, r, p, loc, length, now)
		return
	}
	// The link back to this page with one thing changed.
	self := func(kv ...string) string {
		v := url.Values{"tz": {loc.String()}, "len": {strconv.Itoa(length)}}
		for i := 0; i+1 < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		return r.URL.Path + "?" + v.Encode()
	}
	if t, err := strconv.ParseInt(q.Get("t"), 10, 64); err == nil {
		start := time.Unix(t, 0).In(loc)
		a.writeBookShell(w, http.StatusOK, p.Title, a.bookIntro(p, length, loc, self)+a.bookDetails(p, start, length, loc, "", "", "", ""))
		return
	}
	month := time.Date(now.In(loc).Year(), now.In(loc).Month(), 1, 0, 0, 0, 0, loc)
	if m, err := time.ParseInLocation("2006-01", q.Get("m"), loc); err == nil && !m.Before(month) && m.Before(now.AddDate(0, 0, p.Reach+31)) {
		month = m
	}
	slots, _ := a.vayuMail.FreeSlots(p, length, month, month.AddDate(0, 1, 0), now)
	byDay := map[string][]time.Time{}
	var days []string
	for _, s := range slots {
		d := s.In(loc).Format("2006-01-02")
		if len(byDay[d]) == 0 {
			days = append(days, d)
		}
		byDay[d] = append(byDay[d], s)
	}
	sort.Strings(days)
	day := q.Get("d")
	if _, ok := byDay[day]; !ok && len(days) > 0 {
		day = days[0]
	}
	var b strings.Builder
	zoned := ""
	if q.Get("tz") == "" {
		zoned = ` data-book-zone` // book.js asks again in the guest's zone
	}
	b.WriteString(`<div class="book"` + zoned + `>` + a.bookIntro(p, length, loc, self))
	// The month, its days with free times as links.
	b.WriteString(`<section class="book__month" aria-label="Days"><div class="book__monthhead"><h2>` + month.Format("January 2006") + `</h2>`)
	if month.After(now) {
		b.WriteString(`<a class="btn btn--ghost btn--sm" href="` + esc(self("m", month.AddDate(0, -1, 0).Format("2006-01"))) + `" aria-label="Earlier month">` + saIcon("chev-l") + `</a>`)
	}
	if month.AddDate(0, 1, 0).Before(now.AddDate(0, 0, p.Reach)) {
		b.WriteString(`<a class="btn btn--ghost btn--sm" href="` + esc(self("m", month.AddDate(0, 1, 0).Format("2006-01"))) + `" aria-label="Later month">` + saIcon("chev-r") + `</a>`)
	}
	b.WriteString(`</div><div class="book__grid">`)
	for _, w := range []string{"Mo", "Tu", "We", "Th", "Fr", "Sa", "Su"} {
		b.WriteString(`<span class="book__wd" aria-hidden="true">` + w + `</span>`)
	}
	for i := 0; i < (int(month.Weekday())+6)%7; i++ {
		b.WriteString(`<span></span>`)
	}
	for d := month; d.Month() == month.Month(); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		if _, free := byDay[key]; free {
			cur := ""
			if key == day {
				cur = ` aria-current="date"`
			}
			b.WriteString(`<a class="book__day" href="` + esc(self("m", month.Format("2006-01"), "d", key)) + `"` + cur + ` aria-label="` + d.Format("Monday 2 January") + `">` + d.Format("2") + `</a>`)
		} else {
			b.WriteString(`<span class="book__day is-off">` + d.Format("2") + `</span>`)
		}
	}
	b.WriteString(`</div></section><section class="book__times" aria-label="Times">`)
	if day == "" {
		b.WriteString(`<p class="login-sub">No free times this month.</p>`)
	} else {
		dd, _ := time.ParseInLocation("2006-01-02", day, loc)
		b.WriteString(`<h2>` + dd.Format("Monday 2") + `</h2>`)
		for _, s := range byDay[day] {
			b.WriteString(`<a class="btn book__time" href="` + esc(self("t", strconv.FormatInt(s.Unix(), 10))) + `">` + s.In(loc).Format("15:04") + `</a>`)
		}
	}
	b.WriteString(`</section></div>`)
	a.writeBookShell(w, http.StatusOK, p.Title, b.String())
}

// bookIntro is the page's side: who, what, how long, in which zone.
func (a *App) bookIntro(p vmail.BookingPage, length int, loc *time.Location, self func(...string) string) string {
	who := a.vayuMail.Accounts().FullNameFor(context.Background(), p.Mailbox)
	if who == "" {
		who = bookingOwner(p.Mailbox, a.vayuMail.Config().Domain)
	}
	var b strings.Builder
	b.WriteString(`<section class="book__intro"><p class="book__who">` + esc(who) + `</p><h1 class="login-title">` + esc(p.Title) + `</h1>`)
	if len(p.Lengths) > 1 {
		b.WriteString(`<p class="book__lengths">`)
		for _, l := range p.Lengths {
			cur := ""
			if l == length {
				cur = ` aria-current="true"`
			}
			b.WriteString(`<a class="btn btn--sm" href="` + esc(self("len", strconv.Itoa(l))) + `"` + cur + `>` + strconv.Itoa(l) + ` min</a>`)
		}
		b.WriteString(`</p>`)
	} else {
		b.WriteString(`<p class="book__row">` + saIcon("timer") + `<span>` + strconv.Itoa(length) + ` min</span></p>`)
	}
	b.WriteString(`<p class="book__row">` + saIcon("globe") + `<span>Your time: ` + esc(loc.String()) + `</span></p>`)
	b.WriteString(`<p class="login-sub">You will get a link by mail to confirm. Nothing is booked until you use it.</p></section>`)
	return b.String()
}

// bookDetails is the form for the guest's name, address and answer.
func (a *App) bookDetails(p vmail.BookingPage, start time.Time, length int, loc *time.Location, name, email, answer, said string) string {
	var b strings.Builder
	b.WriteString(`<section class="book__details"><h2>` + esc(start.Format("Monday 2 January, 15:04")) + `</h2>`)
	if said != "" {
		b.WriteString(`<div class="login-error" role="alert">` + esc(said) + `</div>`)
	}
	b.WriteString(`<form class="login-form" method="POST"><input type="hidden" name="t" value="` + strconv.FormatInt(start.Unix(), 10) + `">` +
		`<input type="hidden" name="len" value="` + strconv.Itoa(length) + `"><input type="hidden" name="tz" value="` + esc(loc.String()) + `">` +
		`<div class="field"><label class="field-label" for="book-name">Your name</label><input id="book-name" class="input" name="name" required maxlength="120" value="` + esc(name) + `" autocomplete="name"></div>` +
		`<div class="field"><label class="field-label" for="book-email">Your address</label><input id="book-email" class="input" type="email" name="email" required value="` + esc(email) + `" autocomplete="email"></div>`)
	if p.Question != "" {
		b.WriteString(`<div class="field"><label class="field-label" for="book-answer">` + esc(p.Question) + `</label><textarea id="book-answer" class="input" name="answer" rows="3" maxlength="2000">` + esc(answer) + `</textarea></div>`)
	}
	b.WriteString(`<button class="btn btn--primary" type="submit">Book this time</button></form></section>`)
	return b.String()
}

// bookHold takes the guest's details, holds the time and mails the link.
func (a *App) bookHold(w http.ResponseWriter, r *http.Request, p vmail.BookingPage, loc *time.Location, length int, now time.Time) {
	_ = r.ParseForm()
	t, err := strconv.ParseInt(r.PostFormValue("t"), 10, 64)
	if err != nil {
		a.bookNotFound(w)
		return
	}
	if l := calZone(r.PostFormValue("tz")); l != nil {
		loc = l
	}
	if n, err := strconv.Atoi(r.PostFormValue("len")); err == nil {
		length = n
	}
	start := time.Unix(t, 0).In(loc)
	name, email, answer := r.PostFormValue("name"), r.PostFormValue("email"), r.PostFormValue("answer")
	err = a.vayuMail.HoldBooking(r.Context(), p, start, length, name, email, answer, now, a.oauthBaseURL(r)+"/book/confirm?t=")
	self := func(kv ...string) string { return r.URL.Path + "?tz=" + url.QueryEscape(loc.String()) }
	if err != nil {
		said := "This could not be booked: " + strings.TrimPrefix(err.Error(), "vayumail: ") + "."
		status := http.StatusOK
		if errors.Is(err, vmail.ErrBookingsWait) {
			status = http.StatusTooManyRequests
		}
		a.writeBookShell(w, status, p.Title, a.bookIntro(p, length, loc, self)+a.bookDetails(p, start, length, loc, name, email, answer, said))
		return
	}
	a.writeBookShell(w, http.StatusOK, p.Title, `<h1 class="login-title">Check your mail</h1><p class="login-sub">A link to confirm `+esc(start.Format("Monday 2 January, 15:04"))+
		` (`+esc(loc.String())+`) is on its way to the address you gave. It works for fifteen minutes; nothing is booked until you use it.</p>`)
}

// handleBookConfirm asks, then confirms on its button, the booking a link
// names.
func (a *App) handleBookConfirm(w http.ResponseWriter, r *http.Request) {
	tok := r.FormValue("t")
	if !a.mailRunning() {
		a.bookNotFound(w)
		return
	}
	if r.Method != http.MethodPost {
		a.writeBookShell(w, http.StatusOK, "Confirm your booking", `<h1 class="login-title">Confirm your booking</h1><p class="login-sub">Press the button to book the time you chose.</p>`+
			`<form class="login-form" method="POST"><input type="hidden" name="t" value="`+esc(tok)+`"><button class="btn btn--primary" type="submit">Confirm</button></form>`)
		return
	}
	b, err := a.vayuMail.ConfirmBooking(r.Context(), tok, time.Now(), a.oauthBaseURL(r)+"/book/cancel?t=")
	if err != nil {
		msg := "This link is not valid, was already used, or has expired. Book again from the page you came from."
		if errors.Is(err, vmail.ErrSlotTaken) {
			msg = "That time was taken before you confirmed it. Choose another from the page you came from."
		}
		a.writeBookShell(w, http.StatusNotFound, "Not booked", `<h1 class="login-title">Not booked</h1><p class="login-sub">`+esc(msg)+`</p>`)
		return
	}
	a.writeBookShell(w, http.StatusOK, "Booked", `<h1 class="login-title">Booked</h1><p class="login-sub">`+esc(b.Start.UTC().Format("Monday 2 January 2006, 15:04 UTC"))+
		`. An invitation is on its way to `+esc(b.Email)+`; it has a link to cancel if you need to.</p>`)
}

// handleBookCancel asks, then cancels on its button.
func (a *App) handleBookCancel(w http.ResponseWriter, r *http.Request) {
	tok := r.FormValue("t")
	if !a.mailRunning() {
		a.bookNotFound(w)
		return
	}
	if r.Method != http.MethodPost {
		a.writeBookShell(w, http.StatusOK, "Cancel your booking", `<h1 class="login-title">Cancel your booking?</h1><p class="login-sub">The time will be freed, and you will be sent the cancellation.</p>`+
			`<form class="login-form" method="POST"><input type="hidden" name="t" value="`+esc(tok)+`"><button class="btn btn--danger" type="submit">Cancel the booking</button></form>`)
		return
	}
	if _, err := a.vayuMail.CancelBooking(r.Context(), tok); err != nil {
		a.writeBookShell(w, http.StatusNotFound, "Not cancelled", `<h1 class="login-title">Not cancelled</h1><p class="login-sub">This link is not valid, or the booking was already cancelled.</p>`)
		return
	}
	a.writeBookShell(w, http.StatusOK, "Cancelled", `<h1 class="login-title">Cancelled</h1><p class="login-sub">The booking is cancelled, and the time is free again.</p>`)
}
