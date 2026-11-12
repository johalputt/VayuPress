// SPDX-License-Identifier: Apache-2.0

package mail

// booking.go — booking pages: a public page per kind of meeting where anyone
// with the link books a free time in the holder's calendar.
//
// What the page shows is free and busy, never what is in the calendar: the
// times are worked out here from the expanded events (an all-day event or
// one marked free does not make the holder busy), and only the times leave.
//
// A booking is held for fifteen minutes and made only when the guest
// confirms by a link mailed to the address they gave, so nobody can fill a
// calendar with addresses they do not hold. The confirmation is mail from the
// holder's address to an address a stranger typed, so it is bounded: a page
// sends at most maxBookingMailsPerDay in a day, and an address is sent at
// most maxBookingMailsPerAddress; a page holds at most maxBookingHolds
// unconfirmed at once. The public routes are under Shield like every route.
//
// Confirmed, the booking is an event in the holder's calendar with the guest
// invited (itip.go sends them the invitation from the holder's address),
// carrying a link that cancels it, once. Every link's token is random,
// single-purpose and stored hashed.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	netmail "net/mail"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

const (
	bookingHoldFor            = 15 * time.Minute
	maxBookingHolds           = 20
	maxBookingMailsPerDay     = 50
	maxBookingMailsPerAddress = 3
	maxBookingPages           = 20
	// maxBookingMoves is how many times a guest may move one booking: each
	// move mails them the change and tells the holder.
	maxBookingMoves = 5
	// maxBookingReach is the furthest ahead a page may offer times.
	maxBookingReach = 180
)

// BookingPage is one kind of meeting a holder offers.
type BookingPage struct {
	ID      int64
	Mailbox string
	Slug    string // the page's name in its link
	Title   string
	Lengths []int // minutes offered, ascending
	// Hours are the weekly times offered in Zone, Monday first: windows of
	// minutes from midnight.
	Hours         [7][][2]int
	Zone          string
	Before, After int // minutes kept free around a booking
	Notice        int // minutes: the soonest a booking may start from now
	Reach         int // days: the furthest ahead
	PerDay        int // the most bookings in a day, 0 for no limit
	Question      string
	// Remind is how many minutes before a booking both the guest and the
	// holder are reminded of it: 0 (none), 60 or 1440.
	Remind int
	// Repeats is the most times a guest may book the same time as a series
	// (weekly or every two weeks): 0 for single bookings only, else 2 to 12.
	Repeats int
	Enabled bool
}

// Booking is one time booked, or held, on a page.
type Booking struct {
	ID         int64
	PageID     int64
	Start, End time.Time
	Name       string
	Email      string
	Answer     string
	Status     string // held, confirmed, cancelled
	UID        string // the calendar event's, once confirmed
	Created    time.Time
	Zone       string // the guest's own time zone, "" when not known
	// Every is the weeks between a series' bookings, 0 for one booking;
	// Count is how many there are, 1 for one.
	Every, Count int
}

// BookingGuest is who asks for a time, as the booking page's form gives
// them: Zone is the time zone the page was shown to them in.
type BookingGuest struct {
	Name, Email, Answer, Zone string
	// Every and Count ask for a series: Count bookings, Every weeks apart
	// (1 or 2). Count 0 or 1 is one booking.
	Every, Count int
}

// seriesStarts is a series' starts: the same clock time in the page's zone,
// every weeks apart, so a clock change does not move it.
func seriesStarts(start time.Time, p BookingPage, every, count int) []time.Time {
	if count < 1 {
		count = 1
	}
	loc := zoneNamed(p.Zone)
	if loc == nil {
		loc = time.UTC
	}
	at := start.In(loc)
	out := make([]time.Time, 0, count)
	for k := 0; k < count; k++ {
		out = append(out, time.Date(at.Year(), at.Month(), at.Day()+7*every*k, at.Hour(), at.Minute(), 0, 0, loc))
	}
	return out
}

// Series says a booking's series as its guest reads it: "" for one booking.
func (b Booking) Series() string { return seriesWords(b.Every, b.Count) }

// seriesWords says a series as its guest reads it: "" for one booking.
func seriesWords(every, count int) string {
	if count <= 1 {
		return ""
	}
	if every == 2 {
		return ", every two weeks, " + strconv.Itoa(count) + " times"
	}
	return ", every week, " + strconv.Itoa(count) + " times"
}

// bookingWhen is a booking's time as a guest reads it: in their own zone
// when it is known and not the page's, with the host's clock beside it.
func bookingWhen(start time.Time, p BookingPage, guestZone string) string {
	host := zoneNamed(p.Zone)
	if host == nil {
		host = time.UTC
	}
	at := start.In(host).Format("Monday 2 January 2006, 15:04") + " (" + p.Zone + ")"
	if g := zoneNamed(guestZone); g != nil && g.String() != host.String() {
		at = start.In(g).Format("Monday 2 January 2006, 15:04") + " (" + g.String() + "), which is " +
			start.In(host).Format("15:04 on Monday") + " for " + p.Mailbox + " (" + p.Zone + ")"
	}
	return at
}

var (
	// ErrBookingDetails and ErrBookingOwn are a guest's own mistakes, said
	// back to them; any other error of a hold is the server's and is not.
	ErrBookingDetails  = errors.New("vayumail: a booking needs your name and an address you can read")
	ErrBookingOwn      = errors.New("vayumail: a page is booked by someone else")
	ErrNoSuchBooking   = errors.New("vayumail: that link is not valid, was already used, or has expired")
	ErrSlotTaken       = errors.New("vayumail: that time is no longer free")
	ErrBookingsWait    = errors.New("vayumail: this page is taking no more bookings for now; try again later")
	ErrBookingMoves    = errors.New("vayumail: this booking has been moved as often as it can be; cancel it and book again")
	ErrBookingSeries   = errors.New("vayumail: a series of bookings is not moved; cancel it and book again")
	errNoBookingPage   = errors.New("vayumail: there is no such booking page")
	bookingSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
)

func (s *AccountStore) ensureBookingTables() error {
	if s.db == nil {
		return sql.ErrConnDone
	}
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_booking_pages(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		mailbox TEXT NOT NULL,
		slug TEXT NOT NULL,
		page TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 1,
		UNIQUE(mailbox, slug));`); err != nil {
		return err
	}
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_bookings(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		page_id INTEGER NOT NULL,
		mailbox TEXT NOT NULL,
		start INTEGER NOT NULL,
		end INTEGER NOT NULL,
		name TEXT NOT NULL,
		email TEXT NOT NULL,
		answer TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		confirm_hash TEXT NOT NULL DEFAULT '',
		cancel_hash TEXT NOT NULL DEFAULT '',
		uid TEXT NOT NULL DEFAULT '',
		created DATETIME NOT NULL);`)
	if err != nil {
		return err
	}
	// The guest's own time zone, for the mail they are sent; added in place
	// on an install that made the table before it was kept.
	if _, err := s.db.Exec(`ALTER TABLE vayumail_bookings ADD COLUMN guest_zone TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
		return err
	}
	// How many times the guest has moved it (MoveBooking).
	if _, err := s.db.Exec(`ALTER TABLE vayumail_bookings ADD COLUMN moves INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
		return err
	}
	// Whether the guest has been sent its reminder (sendBookingReminders),
	// and a series: weeks between, how many, and the last one's start and
	// end, so a range finds a series by any of its bookings.
	for _, col := range []string{"reminded INTEGER NOT NULL DEFAULT 0", "every INTEGER NOT NULL DEFAULT 0", "count INTEGER NOT NULL DEFAULT 1",
		"last_start INTEGER NOT NULL DEFAULT 0", "last_end INTEGER NOT NULL DEFAULT 0"} {
		if _, err := s.db.Exec(`ALTER TABLE vayumail_bookings ADD COLUMN ` + col); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	return nil
}

// check puts a page in its stored shape, or says what is wrong with it.
func (p *BookingPage) check() error {
	p.Slug = strings.ToLower(strings.TrimSpace(p.Slug))
	if !bookingSlugPattern.MatchString(p.Slug) {
		return errors.New("a page's name in its link is letters, digits and hyphens, at most 40")
	}
	p.Title = strings.TrimSpace(oneLine(p.Title))
	if p.Title == "" || len(p.Title) > 120 {
		return errors.New("a booking page needs a name, at most 120 characters")
	}
	sort.Ints(p.Lengths)
	var lengths []int
	for _, l := range p.Lengths {
		if l >= 5 && l <= 480 && (len(lengths) == 0 || lengths[len(lengths)-1] != l) {
			lengths = append(lengths, l)
		}
	}
	if len(lengths) == 0 {
		return errors.New("a booking page offers at least one length, from 5 minutes to 8 hours")
	}
	p.Lengths = lengths
	if zoneNamed(p.Zone) == nil {
		return errors.New("the page's time zone is not one the calendar knows")
	}
	offered := false
	for d := range p.Hours {
		for _, w := range p.Hours[d] {
			if w[0] < 0 || w[1] > 24*60 || w[0] >= w[1] {
				return errors.New("each window of hours starts before it ends, within the day")
			}
			offered = true
		}
	}
	if !offered {
		return errors.New("a booking page offers hours on at least one day")
	}
	if p.Before < 0 || p.After < 0 || p.Before > 240 || p.After > 240 || p.Notice < 0 || p.Notice > 30*24*60 {
		return errors.New("the gap around a booking is at most 4 hours, and the notice at most 30 days")
	}
	if p.Reach < 1 || p.Reach > maxBookingReach {
		return fmt.Errorf("a page offers times from 1 to %d days ahead", maxBookingReach)
	}
	if p.PerDay < 0 || p.PerDay > 100 {
		return errors.New("the most bookings in a day is at most 100")
	}
	p.Question = strings.TrimSpace(oneLine(p.Question))
	if len(p.Question) > 300 {
		return errors.New("the question for the guest is at most 300 characters")
	}
	if p.Remind != 0 && p.Remind != 60 && p.Remind != 1440 {
		return errors.New("a reminder is an hour or a day before, or none")
	}
	if p.Repeats != 0 && (p.Repeats < 2 || p.Repeats > 12) {
		return errors.New("a series is 2 to 12 bookings, or none")
	}
	return nil
}

// SaveBookingPage keeps a page for its mailbox: a new one (no ID), or that
// page of the same mailbox.
func (s *AccountStore) SaveBookingPage(ctx context.Context, p BookingPage) (int64, error) {
	if err := s.ensureBookingTables(); err != nil {
		return 0, err
	}
	p.Mailbox = normEmail(p.Mailbox)
	if err := p.check(); err != nil {
		return 0, err
	}
	body, err := json.Marshal(p)
	if err != nil {
		return 0, err
	}
	enabled := 0
	if p.Enabled {
		enabled = 1
	}
	if p.ID > 0 {
		res, err := s.db.ExecContext(ctx, `UPDATE vayumail_booking_pages SET slug=?, page=?, enabled=? WHERE id=? AND mailbox=?`, p.Slug, string(body), enabled, p.ID, p.Mailbox)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return 0, errors.New("another of your booking pages has that name")
			}
			return 0, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return 0, errNoBookingPage
		}
		return p.ID, nil
	}
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM vayumail_booking_pages WHERE mailbox=?`, p.Mailbox).Scan(&n)
	if n >= maxBookingPages {
		return 0, fmt.Errorf("a mailbox has at most %d booking pages", maxBookingPages)
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO vayumail_booking_pages(mailbox,slug,page,enabled) VALUES(?,?,?,?)`, p.Mailbox, p.Slug, string(body), enabled)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, errors.New("another of your booking pages has that name")
		}
		return 0, err
	}
	return res.LastInsertId()
}

func scanBookingPage(id int64, mailbox, body string, enabled int) (BookingPage, error) {
	var p BookingPage
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		return p, err
	}
	p.ID, p.Mailbox, p.Enabled = id, mailbox, enabled == 1
	return p, nil
}

// BookingPagesFor is a mailbox's pages, by name.
func (s *AccountStore) BookingPagesFor(ctx context.Context, mailbox string) ([]BookingPage, error) {
	if err := s.ensureBookingTables(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,mailbox,page,enabled FROM vayumail_booking_pages WHERE mailbox=? ORDER BY slug`, normEmail(mailbox))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BookingPage
	for rows.Next() {
		var id int64
		var mb, body string
		var en int
		if err := rows.Scan(&id, &mb, &body, &en); err != nil {
			return nil, err
		}
		if p, err := scanBookingPage(id, mb, body, en); err == nil {
			out = append(out, p)
		}
	}
	return out, rows.Err()
}

// BookingPage is one of a mailbox's pages, by its ID or, when id is 0, by
// its name in the link.
func (s *AccountStore) BookingPage(ctx context.Context, mailbox string, id int64, slug string) (BookingPage, error) {
	if err := s.ensureBookingTables(); err != nil {
		return BookingPage{}, err
	}
	var pid int64
	var mb, body string
	var en int
	err := s.db.QueryRowContext(ctx, `SELECT id,mailbox,page,enabled FROM vayumail_booking_pages WHERE mailbox=? AND (id=? OR (?=0 AND slug=?))`,
		normEmail(mailbox), id, id, strings.ToLower(slug)).Scan(&pid, &mb, &body, &en)
	if errors.Is(err, sql.ErrNoRows) {
		return BookingPage{}, errNoBookingPage
	}
	if err != nil {
		return BookingPage{}, err
	}
	return scanBookingPage(pid, mb, body, en)
}

// BookingOffered reports whether a page takes bookings: it is switched on
// and its mailbox is active. A mailbox an administrator has suspended
// offers none of its pages, which only deleting it did before.
func (s *AccountStore) BookingOffered(ctx context.Context, p BookingPage) bool {
	if !p.Enabled || s.db == nil {
		return false
	}
	var active int
	_ = s.db.QueryRowContext(ctx, `SELECT active FROM vayumail_accounts WHERE email=?`, normEmail(p.Mailbox)).Scan(&active)
	return active == 1
}

// DeleteBookingPage removes a page; its bookings stay in the calendar.
func (s *AccountStore) DeleteBookingPage(ctx context.Context, mailbox string, id int64) error {
	if err := s.ensureBookingTables(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM vayumail_booking_pages WHERE id=? AND mailbox=?`, id, normEmail(mailbox))
	return err
}

// BookingsFor is a page's bookings, the newest first.
func (s *AccountStore) BookingsFor(ctx context.Context, mailbox string, pageID int64, limit int) ([]Booking, error) {
	if err := s.ensureBookingTables(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,page_id,start,end,name,email,answer,status,uid,created,guest_zone FROM vayumail_bookings
		WHERE mailbox=? AND page_id=? ORDER BY created DESC LIMIT ?`, normEmail(mailbox), pageID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Booking
	for rows.Next() {
		var b Booking
		var s, e int64
		if err := rows.Scan(&b.ID, &b.PageID, &s, &e, &b.Name, &b.Email, &b.Answer, &b.Status, &b.UID, &b.Created, &b.Zone); err != nil {
			return nil, err
		}
		b.Start, b.End = time.Unix(s, 0).UTC(), time.Unix(e, 0).UTC()
		out = append(out, b)
	}
	return out, rows.Err()
}

type span struct{ from, to time.Time }

// busy is the holder's busy time around [from, to): timed events not marked
// free, and times held for bookings not yet confirmed. except is a
// confirmed booking whose event is left out (the one being moved).
func (e *Engine) busy(ctx context.Context, p BookingPage, from, to, now time.Time, except Booking) ([]span, error) {
	evs, err := e.CalendarBetween(ReadAsSystem(p.Mailbox, "booking"), from, to, time.UTC)
	if err != nil {
		return nil, err
	}
	var out []span
	for _, ev := range evs {
		if ev.AllDay || ev.Free || (except.UID != "" && ev.UID == except.UID) {
			continue
		}
		out = append(out, span{ev.Start, ev.End})
	}
	rows, err := e.accounts.db.QueryContext(ctx, `SELECT start,end,every,count FROM vayumail_bookings WHERE mailbox=? AND status='held' AND created>?
		AND MAX(end,last_end)>? AND start<?`, p.Mailbox, now.Add(-bookingHoldFor), from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s, en int64
		var every, count int
		if rows.Scan(&s, &en, &every, &count) == nil {
			// A series held is busy at each of its times, as it will be
			// once its event repeats in the calendar.
			for _, at := range seriesStarts(time.Unix(s, 0), p, every, count) {
				out = append(out, span{at, at.Add(time.Duration(en-s) * time.Second)})
			}
		}
	}
	return out, rows.Err()
}

// bookedOn counts a page's bookings, confirmed or held, on each of the
// page's days (dates in its zone) in [from, to).
func (e *Engine) bookedOn(ctx context.Context, p BookingPage, loc *time.Location, from, to, now time.Time, except int64) map[string]int {
	out := map[string]int{}
	rows, err := e.accounts.db.QueryContext(ctx, `SELECT start,every,count FROM vayumail_bookings WHERE page_id=? AND id<>? AND MAX(start,last_start)>=? AND start<?
		AND (status IN ('confirmed','moving') OR (status='held' AND created>?))`, p.ID, except, from.Unix(), to.Unix(), now.Add(-bookingHoldFor))
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var s int64
		var every, count int
		if rows.Scan(&s, &every, &count) == nil {
			for _, at := range seriesStarts(time.Unix(s, 0), p, every, count) {
				out[at.In(loc).Format("2006-01-02")]++
			}
		}
	}
	if rows.Err() != nil {
		// A count cut short would let a day take more than its most: one
		// that cannot be counted takes none.
		return map[string]int{"*": -1}
	}
	return out
}

// FreeSlots is the times a page offers for a booking of length minutes
// starting in [from, to), earliest first: within its weekly hours, after its
// notice and before its reach, under its most per day, and with its gap free
// on each side.
func (e *Engine) FreeSlots(p BookingPage, length int, from, to, now time.Time) ([]time.Time, error) {
	return e.freeSlots(p, length, from, to, now, Booking{})
}

// freeSlots is FreeSlots with one booking left out: its hold, its count on
// its day and its event in the calendar, so a booking being moved does not
// stand in its own way.
func (e *Engine) freeSlots(p BookingPage, length int, from, to, now time.Time, except Booking) ([]time.Time, error) {
	if e.accounts == nil {
		return nil, errNoBookingPage
	}
	if !slicesContainsInt(p.Lengths, length) {
		return nil, errors.New("vayumail: the page does not offer that length")
	}
	loc := zoneNamed(p.Zone)
	if loc == nil {
		return nil, errors.New("vayumail: the page's time zone is not known")
	}
	soonest, latest := now.Add(time.Duration(p.Notice)*time.Minute), now.AddDate(0, 0, p.Reach)
	if from.Before(soonest) {
		from = soonest
	}
	if to.After(latest) {
		to = latest
	}
	if !to.After(from) {
		return nil, nil
	}
	ctx := context.Background()
	gapB, gapA := time.Duration(p.Before)*time.Minute, time.Duration(p.After)*time.Minute
	busy, err := e.busy(ctx, p, from.Add(-gapB-24*time.Hour), to.Add(gapA+24*time.Hour), now, except)
	if err != nil {
		return nil, err
	}
	perDay := e.bookedOn(ctx, p, loc, from.Add(-24*time.Hour), to.Add(24*time.Hour), now, except.ID)
	dur := time.Duration(length) * time.Minute
	var out []time.Time
	for day := midnightIn(from, loc); day.Before(to); day = day.AddDate(0, 0, 1) {
		if _, uncounted := perDay["*"]; uncounted || (p.PerDay > 0 && perDay[day.Format("2006-01-02")] >= p.PerDay) {
			continue
		}
		for _, w := range p.Hours[(int(day.Weekday())+6)%7] {
			// The hours are clock times in the page's zone. Adding them to
			// midnight as real time put them an hour off on the days the
			// clock changes; slots then step in real time, so the hour that
			// does not exist is not offered and the one that comes twice is
			// offered as the two instants it is.
			wEnd := clockOn(day, w[1])
			for t := clockOn(day, w[0]); !t.Add(dur).After(wEnd); t = t.Add(dur) {
				if t.Before(from) || !t.Before(to) {
					continue
				}
				clear := true
				for _, b := range busy {
					if t.Add(-gapB).Before(b.to) && t.Add(dur+gapA).After(b.from) {
						clear = false
						break
					}
				}
				if clear {
					out = append(out, t)
				}
			}
		}
	}
	return out, nil
}

// clockOn is the instant the clock in day's zone reads minutes after
// midnight on day; 24:00 is the next midnight.
func clockOn(day time.Time, minutes int) time.Time {
	y, m, d := day.Date()
	return time.Date(y, m, d, minutes/60, minutes%60, 0, 0, day.Location())
}

func midnightIn(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

func slicesContainsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// slotFree reports whether a page still offers start for length minutes.
func (e *Engine) slotFree(p BookingPage, start time.Time, length int, now time.Time) bool {
	slots, err := e.FreeSlots(p, length, start, start.Add(time.Minute), now)
	return err == nil && len(slots) == 1 && slots[0].Equal(start)
}

// HoldBooking holds start for the guest and mails them the link that makes
// it a booking, from the holder's address: confirmBase with the token after
// it (the token is hex, so it needs no escaping). The link works for
// bookingHoldFor.
func (e *Engine) HoldBooking(ctx context.Context, p BookingPage, start time.Time, length int, g BookingGuest, now time.Time, confirmBase string) error {
	if !e.accounts.BookingOffered(ctx, p) {
		return errNoBookingPage
	}
	name := strings.TrimSpace(oneLine(g.Name))
	answer := strings.TrimSpace(g.Answer)
	zone := ""
	if loc := zoneNamed(g.Zone); loc != nil {
		zone = loc.String()
	}
	addr, err := netmail.ParseAddress(strings.TrimSpace(g.Email))
	if err != nil || name == "" || len(name) > 120 || len(answer) > 2000 || !strings.Contains(addr.Address, "@") {
		return ErrBookingDetails
	}
	email := normEmail(addr.Address)
	if email == normEmail(p.Mailbox) {
		return ErrBookingOwn
	}
	every, count := g.Every, g.Count
	if count <= 1 {
		every, count = 0, 1
	} else if count > p.Repeats || (every != 1 && every != 2) {
		return ErrBookingDetails
	}
	starts := seriesStarts(start, p, every, count)
	for _, at := range starts {
		if !e.slotFree(p, at, length, now) {
			return ErrSlotTaken
		}
	}
	dayAgo := now.Add(-24 * time.Hour)
	var holds, mails, toAddress int
	_ = e.accounts.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(status='held' AND created>?),0), COUNT(1), COALESCE(SUM(email=?),0)
		FROM vayumail_bookings WHERE page_id=? AND created>?`, now.Add(-bookingHoldFor), email, p.ID, dayAgo).Scan(&holds, &mails, &toAddress)
	if holds >= maxBookingHolds || mails >= maxBookingMailsPerDay || toAddress >= maxBookingMailsPerAddress {
		return ErrBookingsWait
	}
	tok, err := randomHex(32)
	if err != nil {
		return err
	}
	dur := time.Duration(length) * time.Minute
	last := starts[len(starts)-1]
	if _, err := e.accounts.db.ExecContext(ctx, `INSERT INTO vayumail_bookings(page_id,mailbox,start,end,name,email,answer,status,confirm_hash,created,guest_zone,every,count,last_start,last_end)
		VALUES(?,?,?,?,?,?,?,'held',?,?,?,?,?,?,?)`,
		p.ID, p.Mailbox, start.Unix(), start.Add(dur).Unix(), name, email, answer, hashRecoveryToken(tok), now.UTC(), zone, every, count, last.Unix(), last.Add(dur).Unix()); err != nil {
		return err
	}
	text := "You asked to book " + p.Title + " with " + p.Mailbox + ":\n" +
		bookingWhen(start, p, zone) + ", " + strconv.Itoa(length) + " minutes" + seriesWords(every, count) + ".\n\n" +
		"Confirm it here within 15 minutes:\n" + confirmBase + tok + "\n\n" +
		"If you did not ask for this, do nothing: nothing is booked until the link is confirmed.\n"
	_, err = e.SendSystemMail(ctx, p.Mailbox, []string{email}, "Confirm your booking: "+p.Title, "", text)
	return err
}

// heldBooking is the booking a link's token names, in the given status.
func (e *Engine) heldBooking(ctx context.Context, column, token string) (Booking, string, error) {
	if e.accounts == nil || e.accounts.ensureBookingTables() != nil || token == "" {
		return Booking{}, "", ErrNoSuchBooking
	}
	var b Booking
	var s, en int64
	var mbox string
	err := e.accounts.db.QueryRowContext(ctx, `SELECT id,page_id,mailbox,start,end,name,email,answer,status,uid,created,guest_zone,every,count FROM vayumail_bookings WHERE `+column+`=? AND `+column+`<>''`,
		hashRecoveryToken(token)).Scan(&b.ID, &b.PageID, &mbox, &s, &en, &b.Name, &b.Email, &b.Answer, &b.Status, &b.UID, &b.Created, &b.Zone, &b.Every, &b.Count)
	if err != nil {
		return Booking{}, "", ErrNoSuchBooking
	}
	b.Start, b.End = time.Unix(s, 0).UTC(), time.Unix(en, 0).UTC()
	return b, mbox, nil
}

// ConfirmBooking makes the held booking a link names an event in the
// holder's calendar, with the guest invited, once, while it is held and the
// time is still free. The event carries the link that moves or cancels it:
// cancelBase with the token after it.
func (e *Engine) ConfirmBooking(ctx context.Context, token string, now time.Time, cancelBase string) (Booking, error) {
	if e.accounts == nil || e.accounts.ensureBookingTables() != nil || token == "" {
		return Booking{}, ErrNoSuchBooking
	}
	// The link is claimed before anything is read: of two confirms at the
	// same moment (the button pressed twice) only one changes the row, and
	// the other stops here. Read first, the second went on, found the
	// first's event in its time and marked a booking that was made as
	// cancelled. Claimed, the hold is also no longer what makes the time
	// busy.
	res, err := e.accounts.db.ExecContext(ctx, `UPDATE vayumail_bookings SET status='checking' WHERE confirm_hash=? AND confirm_hash<>'' AND status='held'`, hashRecoveryToken(token))
	if err != nil {
		return Booking{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Booking{}, ErrNoSuchBooking
	}
	b, mbox, err := e.heldBooking(ctx, "confirm_hash", token)
	if err != nil {
		return Booking{}, ErrNoSuchBooking
	}
	release := func() {
		_, _ = e.accounts.db.ExecContext(ctx, `UPDATE vayumail_bookings SET status='held' WHERE id=?`, b.ID)
	}
	if !b.Created.After(now.Add(-bookingHoldFor)) {
		release()
		return Booking{}, ErrNoSuchBooking
	}
	p, err := e.accounts.BookingPage(ctx, mbox, b.PageID, "")
	if err != nil || !e.accounts.BookingOffered(ctx, p) {
		release()
		return Booking{}, ErrNoSuchBooking
	}
	length := int(b.End.Sub(b.Start) / time.Minute)
	for _, at := range seriesStarts(b.Start, p, b.Every, b.Count) {
		if !e.slotFree(p, at, length, b.Created) {
			_, _ = e.accounts.db.ExecContext(ctx, `UPDATE vayumail_bookings SET status='cancelled', confirm_hash='' WHERE id=?`, b.ID)
			return Booking{}, ErrSlotTaken
		}
	}
	cancel, err := randomHex(32)
	if err != nil {
		return Booking{}, err
	}
	notes := "Booked on " + p.Title + " by " + b.Name + " <" + b.Email + ">.\n"
	if b.Answer != "" {
		if p.Question != "" {
			notes += p.Question + "\n"
		}
		notes += b.Answer + "\n"
	}
	notes += "\nTo move or cancel this booking: " + cancelBase + cancel + "\n"
	loc := zoneNamed(p.Zone)
	in := EventInput{Summary: p.Title + " with " + b.Name, Description: notes, Start: b.Start.In(loc), End: b.End.In(loc), Guests: []string{b.Email}}
	if b.Count > 1 {
		// One event that repeats, in the page's zone, as the holder's
		// hours are: a clock change does not move it.
		in.Rule = "FREQ=WEEKLY;INTERVAL=" + strconv.Itoa(b.Every) + ";COUNT=" + strconv.Itoa(b.Count)
	}
	if p.Remind > 0 {
		// The holder's reminder, on their devices and by mail, at the time
		// the guest's comes.
		in.Remind, in.SetRemind = &Reminder{Before: time.Duration(p.Remind) * time.Minute, ByMail: true}, true
	}
	uid, err := e.SaveEvent(ReadAsSystem(mbox, "booking"), in, ScopeAll, time.Time{})
	if err != nil && !errors.Is(err, ErrInvitationsNotSent) {
		release()
		return Booking{}, err
	}
	if _, err := e.accounts.db.ExecContext(ctx, `UPDATE vayumail_bookings SET status='confirmed', confirm_hash='', cancel_hash=?, uid=? WHERE id=?`,
		hashRecoveryToken(cancel), uid, b.ID); err != nil {
		return Booking{}, err
	}
	b.Status, b.UID = "confirmed", uid
	e.tellHolder(mbox, "New booking: "+p.Title+" with "+b.Name,
		b.Name+" <"+b.Email+"> booked "+p.Title+" for "+b.Start.In(loc).Format("Monday 2 January 2006, 15:04")+" ("+p.Zone+")"+seriesWords(b.Every, b.Count)+".\n")
	return b, nil
}

// CancelBooking cancels the confirmed booking a link names, once: the event
// leaves the holder's calendar, the guest is sent its cancellation and the
// holder is told.
func (e *Engine) CancelBooking(ctx context.Context, token string) (Booking, error) {
	b, mbox, err := e.heldBooking(ctx, "cancel_hash", token)
	if err != nil || b.Status != "confirmed" {
		return Booking{}, ErrNoSuchBooking
	}
	if _, err := e.accounts.db.ExecContext(ctx, `UPDATE vayumail_bookings SET status='cancelled', cancel_hash='' WHERE id=?`, b.ID); err != nil {
		return Booking{}, err
	}
	if b.UID != "" {
		if err := e.DeleteEvent(ReadAsSystem(mbox, "booking"), b.UID, ScopeAll, time.Time{}); err != nil && !errors.Is(err, errNoSuchEvent) && !errors.Is(err, ErrInvitationsNotSent) {
			return Booking{}, err
		}
	}
	b.Status = "cancelled"
	// In the page's zone, as the holder was told of the booking; UTC only
	// when the page is gone.
	when := b.Start.UTC().Format("Monday 2 January 2006, 15:04") + " (UTC)"
	if p, err := e.accounts.BookingPage(ctx, mbox, b.PageID, ""); err == nil {
		if loc := zoneNamed(p.Zone); loc != nil {
			when = b.Start.In(loc).Format("Monday 2 January 2006, 15:04") + " (" + p.Zone + ")"
		}
	}
	e.tellHolder(mbox, "Booking cancelled: "+b.Name, b.Name+" <"+b.Email+"> cancelled the booking for "+when+".\n")
	return b, nil
}

// BookingByLink is the confirmed booking a move-or-cancel link names, with
// its page.
func (e *Engine) BookingByLink(ctx context.Context, token string) (Booking, BookingPage, error) {
	// A link's token is cleared when its booking is cancelled, so what it
	// names is confirmed, or being moved this moment.
	b, mbox, err := e.heldBooking(ctx, "cancel_hash", token)
	if err != nil {
		return Booking{}, BookingPage{}, ErrNoSuchBooking
	}
	p, err := e.accounts.BookingPage(ctx, mbox, b.PageID, "")
	if err != nil {
		return Booking{}, BookingPage{}, ErrNoSuchBooking
	}
	return b, p, nil
}

// MovingSlots is the times a booking may be moved to in [from, to): its
// page's free times, its own time not standing in its way.
func (e *Engine) MovingSlots(b Booking, p BookingPage, from, to, now time.Time) ([]time.Time, error) {
	return e.freeSlots(p, int(b.End.Sub(b.Start)/time.Minute), from, to, now, b)
}

// MoveBooking moves the confirmed booking a link names to start, while the
// page offers it: the event moves in the holder's calendar, the guest is
// sent the change, and the holder is told. The link stays the booking's.
// A booking moves at most maxBookingMoves times; a page switched off
// moves none, though its bookings can still be cancelled.
func (e *Engine) MoveBooking(ctx context.Context, token string, start, now time.Time) (Booking, error) {
	if e.accounts == nil || e.accounts.ensureBookingTables() != nil || token == "" {
		return Booking{}, ErrNoSuchBooking
	}
	// Claimed first, as a confirm is: of two moves at once only one goes
	// on, and a cancel meanwhile finds nothing to cancel until it is done.
	res, err := e.accounts.db.ExecContext(ctx, `UPDATE vayumail_bookings SET status='moving' WHERE cancel_hash=? AND cancel_hash<>'' AND status='confirmed'`, hashRecoveryToken(token))
	if err != nil {
		return Booking{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Booking{}, ErrNoSuchBooking
	}
	b, mbox, err := e.heldBooking(ctx, "cancel_hash", token)
	if err != nil {
		return Booking{}, ErrNoSuchBooking
	}
	defer func() {
		_, _ = e.accounts.db.ExecContext(ctx, `UPDATE vayumail_bookings SET status='confirmed' WHERE id=? AND status='moving'`, b.ID)
	}()
	if b.Count > 1 {
		return Booking{}, ErrBookingSeries
	}
	var moves int
	if err := e.accounts.db.QueryRowContext(ctx, `SELECT moves FROM vayumail_bookings WHERE id=?`, b.ID).Scan(&moves); err != nil {
		return Booking{}, err
	}
	if moves >= maxBookingMoves {
		return Booking{}, ErrBookingMoves
	}
	p, err := e.accounts.BookingPage(ctx, mbox, b.PageID, "")
	if err != nil || !e.accounts.BookingOffered(ctx, p) {
		return Booking{}, ErrNoSuchBooking
	}
	length := b.End.Sub(b.Start)
	slots, err := e.MovingSlots(b, p, start, start.Add(time.Minute), now)
	if err != nil || len(slots) != 1 || !slots[0].Equal(start) || start.Equal(b.Start) {
		return Booking{}, ErrSlotTaken
	}
	was := b.Start
	b.Start, b.End = start.UTC(), start.Add(length).UTC()
	if b.UID != "" {
		if err := e.moveEvent(ctx, mbox, b.UID, b.Start.In(zoneNamed(p.Zone)), b.End.In(zoneNamed(p.Zone))); err != nil &&
			!errors.Is(err, errNoSuchEvent) && !errors.Is(err, ErrInvitationsNotSent) {
			return Booking{}, err
		}
	}
	if _, err := e.accounts.db.ExecContext(ctx, `UPDATE vayumail_bookings SET start=?, end=?, moves=moves+1, reminded=0, status='confirmed' WHERE id=?`, b.Start.Unix(), b.End.Unix(), b.ID); err != nil {
		return Booking{}, err
	}
	loc := zoneNamed(p.Zone)
	e.tellHolder(mbox, "Booking moved: "+p.Title+" with "+b.Name, b.Name+" <"+b.Email+"> moved "+p.Title+" from "+
		was.In(loc).Format("Monday 2 January 2006, 15:04")+" to "+b.Start.In(loc).Format("Monday 2 January 2006, 15:04")+" ("+p.Zone+").\n")
	return b, nil
}

// moveEvent moves an event's start and end, keeping all else, and sends its
// guests the change.
func (e *Engine) moveEvent(ctx context.Context, mbox, uid string, start, end time.Time) error {
	o, err := e.accounts.eventByUID(ctx, mbox, uid)
	if err != nil {
		return err
	}
	cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
	if err != nil {
		return err
	}
	master, _ := parts(cal)
	if master == nil {
		return errNoSuchEvent
	}
	setWhen(master, EventInput{Start: start, End: end})
	stamp(master, time.Now().UTC().Truncate(time.Second), true)
	data, err := encodeCal(cal)
	if err != nil {
		return err
	}
	if _, err := e.accounts.davPut(ctx, mbox, davEvent, o.Name, uid, data, davCondition{ifMatch: o.ETag}); err != nil {
		return err
	}
	return e.sendInvitations(ctx, mbox, o.Data, data)
}

// maxBookingReminders bounds the guests reminded in one pass of the worker.
const maxBookingReminders = 200

// sendBookingReminders mails each guest whose booking's reminder time came
// in the last reminderLate, once, from the holder's address: the time in the
// guest's own zone. A booking made or moved after its reminder time is not
// reminded; the guest has just chosen it.
func (e *Engine) sendBookingReminders(now time.Time) {
	if e.accounts == nil || e.accounts.ensureBookingTables() != nil {
		return
	}
	ctx := context.Background()
	rows, err := e.accounts.db.QueryContext(ctx, `SELECT b.id,b.mailbox,b.start,b.end,b.email,b.guest_zone,p.page FROM vayumail_bookings b
		JOIN vayumail_booking_pages p ON p.id=b.page_id WHERE b.status='confirmed' AND b.reminded=0 AND b.start>? AND b.start<=?
		ORDER BY b.start LIMIT ?`, now.Unix(), now.Add(24*time.Hour+reminderLate).Unix(), maxBookingReminders)
	if err != nil {
		return
	}
	type due struct {
		id         int64
		mbox       string
		start, end int64
		email      string
		zone, page string
	}
	var all []due
	for rows.Next() {
		var d due
		if rows.Scan(&d.id, &d.mbox, &d.start, &d.end, &d.email, &d.zone, &d.page) == nil {
			all = append(all, d)
		}
	}
	if rows.Err() != nil || rows.Close() != nil {
		return
	}
	for _, d := range all {
		var p BookingPage
		// A page with no reminder has its time at the start, which a booking
		// still to come has not reached.
		if json.Unmarshal([]byte(d.page), &p) != nil {
			continue
		}
		// A page switched off still has its bookings, and they are still
		// reminded; a mailbox switched off sends nothing.
		p.Mailbox, p.Enabled = d.mbox, true
		if !e.accounts.BookingOffered(ctx, p) {
			continue
		}
		start := time.Unix(d.start, 0)
		at := start.Add(-time.Duration(p.Remind) * time.Minute)
		if now.Before(at) || now.Sub(at) > reminderLate {
			continue
		}
		// Claimed before it is sent: two passes at once send it once.
		res, err := e.accounts.db.ExecContext(ctx, `UPDATE vayumail_bookings SET reminded=1 WHERE id=? AND reminded=0`, d.id)
		if err != nil {
			continue
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		text := "A reminder of " + p.Title + " with " + p.Mailbox + ":\n" + bookingWhen(start, p, d.zone) + ", " +
			strconv.FormatInt((d.end-d.start)/60, 10) + " minutes.\n\nTo move or cancel it, use the link in the invitation you were sent.\n"
		_, _ = e.SendSystemMail(ctx, p.Mailbox, []string{d.email}, "Reminder: "+p.Title+", "+start.In(zoneNamed(p.Zone)).Format("Monday 2 January, 15:04"), "", text)
	}
}

// tellHolder delivers a short notice into the holder's own Inbox, where the
// console's new-mail notice raises it.
func (e *Engine) tellHolder(mbox, subject, text string) {
	if e.maildir == nil {
		return
	}
	local, dom := splitAddress(mbox)
	var raw strings.Builder
	raw.WriteString("From: Calendar <" + mbox + ">\r\nTo: " + mbox + "\r\nSubject: " + oneLine(subject) + "\r\nDate: " + time.Now().UTC().Format(time.RFC1123Z) +
		"\r\nMessage-ID: " + e.messageID(dom) + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	raw.WriteString(normalizeCRLF(text))
	_, _ = e.maildir.Deliver(dom, local, []byte(raw.String()))
}
