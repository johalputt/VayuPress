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

	dbpkg "github.com/johalputt/vayupress/internal/db"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

func (a *App) writeBookShell(w http.ResponseWriter, status int, title, inner string) {
	writeGuestPage(w, status, title, `<div class="login-card book-card">`+inner+`</div>`+
		`<script src="/os/static/js/book.js?v=`+assetVer("js/book.js")+`"></script>`)
}

// writeGuestPage is a page for someone with no session who came by a link
// that is itself the secret (a booking's, a shared file's): never kept by a
// cache, never sent on in a Referer, never indexed.
func writeGuestPage(w http.ResponseWriter, status int, title, body string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(authPageShell(title, body)))
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
	return p, err == nil && a.vayuMail.Accounts().BookingOffered(r.Context(), p)
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
	zoned := ""
	if q.Get("tz") == "" {
		zoned = ` data-book-zone` // book.js asks again in the guest's zone
	}
	a.writeBookShell(w, http.StatusOK, p.Title, `<div class="book"`+zoned+`>`+a.bookIntro(p, length, loc, self)+bookChooser(month, slots, loc, now, p.Reach, q.Get("d"), "t", self)+`</div>`)
}

// bookChooser is a month's days with free times as links, and the chosen
// day's times, each a link to self with pick set to the time.
func bookChooser(month time.Time, slots []time.Time, loc *time.Location, now time.Time, reach int, day, pick string, self func(...string) string) string {
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
	if _, ok := byDay[day]; !ok && len(days) > 0 {
		day = days[0]
	}
	var b strings.Builder
	// The month, its days with free times as links.
	b.WriteString(`<section class="book__month" aria-label="Days"><div class="book__monthhead"><h2>` + month.Format("January 2006") + `</h2>`)
	if month.After(now) {
		b.WriteString(`<a class="btn btn--ghost btn--sm" href="` + esc(self("m", month.AddDate(0, -1, 0).Format("2006-01"))) + `" aria-label="Earlier month">` + saIcon("chev-l") + `</a>`)
	}
	if month.AddDate(0, 1, 0).Before(now.AddDate(0, 0, reach)) {
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
			b.WriteString(`<a class="btn book__time" href="` + esc(self(pick, strconv.FormatInt(s.Unix(), 10))) + `">` + s.In(loc).Format("15:04") + `</a>`)
		}
	}
	b.WriteString(`</section>`)
	return b.String()
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
	if p.Repeats > 1 {
		// A series: the same time weekly or every two weeks, as many times
		// as the page allows; every one of them must be free.
		counts := ""
		for n := 2; n <= p.Repeats; n++ {
			counts += `<option value="` + strconv.Itoa(n) + `">` + strconv.Itoa(n) + ` times</option>`
		}
		b.WriteString(`<div class="field"><label class="field-label" for="book-every">Repeat</label><select id="book-every" class="input" name="every">` +
			`<option value="0">Just this once</option><option value="1">Every week</option><option value="2">Every two weeks</option></select></div>` +
			`<div class="field"><label class="field-label" for="book-count">How many times, if it repeats</label><select id="book-count" class="input" name="count">` + counts + `</select></div>`)
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
	g := vmail.BookingGuest{Name: name, Email: email, Answer: answer, Zone: loc.String()}
	if every, _ := strconv.Atoi(r.PostFormValue("every")); every > 0 {
		g.Every = every
		g.Count, _ = strconv.Atoi(r.PostFormValue("count"))
	}
	err = a.vayuMail.HoldBooking(r.Context(), p, start, length, g, now, a.oauthBaseURL(r)+"/book/confirm?t=")
	self := func(kv ...string) string { return r.URL.Path + "?tz=" + url.QueryEscape(loc.String()) }
	if err != nil {
		// A guest's own mistake is said back to them; anything else is the
		// server's, and its words are not theirs to read.
		said, status := "This could not be booked just now. Try again in a moment.", http.StatusInternalServerError
		switch {
		case errors.Is(err, vmail.ErrBookingDetails), errors.Is(err, vmail.ErrBookingOwn), errors.Is(err, vmail.ErrSlotTaken):
			said, status = "This could not be booked: "+strings.TrimPrefix(err.Error(), "vayumail: ")+".", http.StatusOK
		case errors.Is(err, vmail.ErrBookingsWait):
			said, status = "This could not be booked: "+strings.TrimPrefix(err.Error(), "vayumail: ")+".", http.StatusTooManyRequests
		default:
			dbpkg.AuditLog("vayumail.booking.hold-failed", "guest", p.Mailbox, err.Error())
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
	b, err := a.vayuMail.ConfirmBooking(r.Context(), tok, time.Now(), a.oauthBaseURL(r)+"/book/change?t=")
	if err != nil {
		msg := "This link is not valid, was already used, or has expired. Book again from the page you came from."
		if errors.Is(err, vmail.ErrSlotTaken) {
			msg = "That time was taken before you confirmed it. Choose another from the page you came from."
		}
		a.writeBookShell(w, http.StatusNotFound, "Not booked", `<h1 class="login-title">Not booked</h1><p class="login-sub">`+esc(msg)+`</p>`)
		return
	}
	// In the zone the guest booked in; UTC only when it was not known.
	at := b.Start.UTC().Format("Monday 2 January 2006, 15:04") + " (UTC)"
	if loc := calZone(b.Zone); loc != nil {
		at = b.Start.In(loc).Format("Monday 2 January 2006, 15:04") + " (" + loc.String() + ")"
	}
	a.writeBookShell(w, http.StatusOK, "Booked", `<h1 class="login-title">Booked</h1><p class="login-sub">`+esc(at)+
		`. An invitation is on its way to `+esc(b.Email)+`; it has a link to move or cancel it if you need to.</p>`)
}

// handleBookChange is the page a booking's own link opens: the booking,
// the page's free times to move it to, and Cancel. Each change waits for
// its button, so a mail scanner opening the link changes nothing. The link
// a booking made before moves were offered (/book/cancel) opens it too.
func (a *App) handleBookChange(w http.ResponseWriter, r *http.Request) {
	tok := r.FormValue("t")
	if !a.mailRunning() {
		a.bookNotFound(w)
		return
	}
	ctx := r.Context()
	gone := func() {
		a.writeBookShell(w, http.StatusNotFound, "Not found", `<h1 class="login-title">This booking is not here</h1><p class="login-sub">The link is not valid, or the booking was cancelled.</p>`)
	}
	b, p, err := a.vayuMail.BookingByLink(ctx, tok)
	if err != nil {
		gone()
		return
	}
	loc := calZone(r.FormValue("tz"))
	if loc == nil {
		loc = calZone(b.Zone)
	}
	if loc == nil {
		loc = guestZone(r, p)
	}
	self := func(kv ...string) string {
		v := url.Values{"t": {tok}, "tz": {loc.String()}}
		for i := 0; i+1 < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		return "/book/change?" + v.Encode()
	}
	when := func(t time.Time) string {
		return t.In(loc).Format("Monday 2 January 2006, 15:04") + " (" + loc.String() + ")"
	}
	if r.Method == http.MethodPost {
		switch r.PostFormValue("op") {
		case "cancel":
			if _, err := a.vayuMail.CancelBooking(ctx, tok); err != nil {
				gone()
				return
			}
			a.writeBookShell(w, http.StatusOK, "Cancelled", `<h1 class="login-title">Cancelled</h1><p class="login-sub">The booking is cancelled, and the time is free again.</p>`)
		case "move":
			to, _ := strconv.ParseInt(r.PostFormValue("to"), 10, 64)
			moved, err := a.vayuMail.MoveBooking(ctx, tok, time.Unix(to, 0), time.Now())
			switch {
			case err == nil:
				a.writeBookShell(w, http.StatusOK, "Moved", `<h1 class="login-title">Moved</h1><p class="login-sub">`+esc(p.Title)+` is now `+esc(when(moved.Start))+
					`. The change is on its way to `+esc(moved.Email)+`; this link still moves or cancels it.</p>`)
			case errors.Is(err, vmail.ErrSlotTaken), errors.Is(err, vmail.ErrBookingMoves):
				a.writeBookShell(w, http.StatusConflict, "Not moved", `<h1 class="login-title">Not moved</h1><p class="login-sub">`+esc(strings.TrimPrefix(err.Error(), "vayumail: "))+
					`.</p><p><a class="btn" href="`+esc(self())+`">Choose another time</a></p>`)
			default:
				gone()
			}
		default:
			gone()
		}
		return
	}
	head := `<section class="book__intro"><h1 class="login-title">` + esc(p.Title) + `</h1><p class="book__row">` + saIcon("calendar") + `<span>` + esc(when(b.Start)+b.Series()) + `</span></p>` +
		`<p class="book__row">` + saIcon("globe") + `<span>Your time: ` + esc(loc.String()) + `</span></p>` +
		`<form class="login-form" method="POST"><input type="hidden" name="t" value="` + esc(tok) + `"><input type="hidden" name="op" value="cancel">` +
		`<button class="btn btn--danger" type="submit">Cancel the booking</button></form></section>`
	if !a.vayuMail.Accounts().BookingOffered(ctx, p) {
		// The page is switched off: the booking can still be cancelled, not
		// moved.
		a.writeBookShell(w, http.StatusOK, p.Title, `<div class="book">`+head+`<section class="book__details"><p class="login-sub">This booking page is switched off, so the booking cannot be moved.</p></section></div>`)
		return
	}
	if b.Count > 1 {
		a.writeBookShell(w, http.StatusOK, p.Title, `<div class="book">`+head+`<section class="book__details"><p class="login-sub">A series is not moved here: cancel it, and book the times you want from the page.</p></section></div>`)
		return
	}
	if to, err := strconv.ParseInt(r.URL.Query().Get("to"), 10, 64); err == nil {
		a.writeBookShell(w, http.StatusOK, p.Title, `<div class="book">`+head+`<section class="book__details"><h2>Move to `+esc(when(time.Unix(to, 0)))+`?</h2>`+
			`<form class="login-form" method="POST"><input type="hidden" name="t" value="`+esc(tok)+`"><input type="hidden" name="op" value="move"><input type="hidden" name="to" value="`+strconv.FormatInt(to, 10)+`">`+
			`<input type="hidden" name="tz" value="`+esc(loc.String())+`"><button class="btn btn--primary" type="submit">Move the booking</button></form></section></div>`)
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
	now := time.Now()
	month := time.Date(now.In(loc).Year(), now.In(loc).Month(), 1, 0, 0, 0, 0, loc)
	if m, err := time.ParseInLocation("2006-01", r.URL.Query().Get("m"), loc); err == nil && !m.Before(month) && m.Before(now.AddDate(0, 0, p.Reach+31)) {
		month = m
	}
	slots, _ := a.vayuMail.MovingSlots(b, p, month, month.AddDate(0, 1, 0), now)
	a.writeBookShell(w, http.StatusOK, p.Title, `<div class="book">`+strings.Replace(head, `</section>`, `<p class="login-sub">Or move it: choose a day, then a time.</p></section>`, 1)+
		bookChooser(month, slots, loc, now, p.Reach, r.URL.Query().Get("d"), "to", self)+`</div>`)
}
