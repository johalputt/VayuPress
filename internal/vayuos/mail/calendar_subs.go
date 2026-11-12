// SPDX-License-Identifier: Apache-2.0

package mail

// calendar_subs.go — calendars read from an address: a holder subscribes to
// a calendar another service publishes as an iCalendar feed (a private
// address of their calendar elsewhere, a team's, holidays), and its events
// are shown beside their own, read only, and count as busy on their booking
// pages unless they say not.
//
// The address is fetched by the server, so it goes through safefetch: http
// or https only (webcal is https), every address it resolves to and every
// redirect checked against the server's own network, the body bounded and
// the request timed. A feed is read again every calSubEvery by the worker,
// a few at a time; a failed read keeps the last good copy and says why on
// the page. The address is often a secret (a private feed's), so the page
// shows it cut short.

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-ical"

	"github.com/johalputt/vayupress/internal/safefetch"
)

const (
	// maxCalSubs is a mailbox's subscriptions; maxCalSubBytes one feed.
	// A large calendar of years is a few megabytes.
	maxCalSubs     = 5
	maxCalSubBytes = 4 << 20
	// calSubEvery is how often a feed is read again; calSubsPerPass how
	// many the worker reads in one pass, so a pass stays short.
	calSubEvery    = 30 * time.Minute
	calSubsPerPass = 5
	// calSubAgain is the soonest a holder's Refresh reads a feed again.
	calSubAgain = time.Minute
)

var (
	ErrCalSubAddress = errors.New("vayumail: a calendar's address is an http, https or webcal address")
	ErrCalSubsFull   = errors.New("vayumail: a mailbox subscribes to at most 5 calendars")
	errNoSuchCalSub  = errors.New("vayumail: there is no such subscription")
)

// CalSubscription is one calendar read from an address.
type CalSubscription struct {
	ID   int64
	Name string
	URL  string
	// User is the name it is signed in to another CalDAV server with, ""
	// for an address read as it is.
	User string
	// Busy: its events make the holder busy on their booking pages.
	Busy    bool
	Fetched time.Time // the last read, good or not; zero before the first
	Error   string    // why the last read failed, "" when it did not
	Size    int       // bytes of the last good copy
}

func (s *AccountStore) ensureCalSubTable() error {
	if s.db == nil {
		return sql.ErrConnDone
	}
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_cal_subs(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		mailbox TEXT NOT NULL,
		name TEXT NOT NULL,
		url TEXT NOT NULL,
		busy INTEGER NOT NULL DEFAULT 1,
		fetched INTEGER NOT NULL DEFAULT 0,
		error TEXT NOT NULL DEFAULT '',
		data BLOB);`)
	if err != nil {
		return err
	}
	// A calendar on another CalDAV server is signed in to: its user name,
	// and its password sealed (calendar_caldav.go). Added in place on an
	// install that made the table before.
	for _, col := range []string{"user", "secret"} {
		if _, err := s.db.Exec(`ALTER TABLE vayumail_cal_subs ADD COLUMN ` + col + ` TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	return nil
}

// calSubURL is a feed's address as it is fetched: webcal is https.
func calSubURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil {
		return "", ErrCalSubAddress
	}
	switch strings.ToLower(u.Scheme) {
	case "webcal", "webcals":
		u.Scheme = "https"
	case "http", "https":
		u.Scheme = strings.ToLower(u.Scheme)
	default:
		return "", ErrCalSubAddress
	}
	return u.String(), nil
}

// fetchCalendar reads a feed: e.calFetch in tests, safefetch otherwise.
func (e *Engine) fetchCalendar(ctx context.Context, addr string) ([]byte, error) {
	if e.calFetch != nil {
		return e.calFetch(ctx, addr)
	}
	res, err := safefetch.New(safefetch.Options{MaxBytes: maxCalSubBytes, Timeout: 20 * time.Second, AllowedSchemes: []string{"https", "http"}}).Get(ctx, addr)
	if err != nil {
		return nil, err
	}
	if res.Status != 200 {
		return nil, errors.New("the address answered HTTP " + strconv.Itoa(res.Status))
	}
	return res.Body, nil
}

// parseFeed is a feed's calendars, or why it is not one.
func parseFeed(data []byte) ([]*ical.Calendar, error) {
	dec := ical.NewDecoder(bytes.NewReader(data))
	var out []*ical.Calendar
	for {
		cal, err := dec.Decode()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if len(out) > 0 {
				break
			}
			return nil, errors.New("what the address sent is not a calendar")
		}
		out = append(out, cal)
	}
	if len(out) == 0 {
		return nil, errors.New("what the address sent is not a calendar")
	}
	return out, nil
}

// AddCalendarSubscription subscribes rd's calendar to the feed at rawURL and
// reads it once. The subscription is kept whether or not that first read
// works; the page says how it went.
func (e *Engine) AddCalendarSubscription(rd Reader, name, rawURL string, busy bool, now time.Time) (int64, error) {
	addr, err := calSubURL(rawURL)
	if err != nil {
		return 0, err
	}
	return e.addSubscription(rd, name, addr, "", "", busy, now)
}

// addSubscription keeps a subscription of rd's calendar and reads it once.
func (e *Engine) addSubscription(rd Reader, name, addr, user, sealed string, busy bool, now time.Time) (int64, error) {
	mbox, err := e.calendarOf(rd)
	if err != nil {
		return 0, err
	}
	if err := e.accounts.ensureCalSubTable(); err != nil {
		return 0, err
	}
	name = strings.TrimSpace(oneLine(name))
	if name == "" {
		name = "Subscribed calendar"
	}
	if len(name) > 80 {
		name = name[:80]
	}
	ctx := context.Background()
	var n int
	if err := e.accounts.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM vayumail_cal_subs WHERE mailbox=?`, mbox).Scan(&n); err != nil {
		return 0, err
	}
	if n >= maxCalSubs {
		return 0, ErrCalSubsFull
	}
	res, err := e.accounts.db.ExecContext(ctx, `INSERT INTO vayumail_cal_subs(mailbox,name,url,busy,user,secret) VALUES(?,?,?,?,?,?)`, mbox, name, addr, busy, user, sealed)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	e.readSubscription(ctx, id, now)
	return id, nil
}

// readSubscription reads one feed and keeps it if it is a calendar.
func (e *Engine) readSubscription(ctx context.Context, id int64, now time.Time) {
	var addr, user, sealed string
	if err := e.accounts.db.QueryRowContext(ctx, `SELECT url,user,secret FROM vayumail_cal_subs WHERE id=?`, id).Scan(&addr, &user, &sealed); err != nil {
		return
	}
	var data []byte
	var err error
	if user != "" {
		data, err = e.fetchCalDAV(ctx, addr, user, sealed)
	} else {
		data, err = e.fetchCalendar(ctx, addr)
	}
	if err == nil {
		_, err = parseFeed(data)
	}
	if err != nil {
		msg := strings.TrimPrefix(err.Error(), "safefetch: ")
		if errors.Is(err, safefetch.ErrBlockedAddress) {
			msg = "the address is on this server's own network, which a calendar is not read from"
		} else if errors.Is(err, safefetch.ErrTooLarge) {
			msg = "the calendar is larger than 4 MB"
		}
		_, _ = e.accounts.db.ExecContext(ctx, `UPDATE vayumail_cal_subs SET fetched=?, error=? WHERE id=?`, now.Unix(), msg, id)
		return
	}
	_, _ = e.accounts.db.ExecContext(ctx, `UPDATE vayumail_cal_subs SET fetched=?, error='', data=? WHERE id=?`, now.Unix(), data, id)
}

// CalendarSubscriptions is rd's subscriptions, by name.
func (e *Engine) CalendarSubscriptions(rd Reader) ([]CalSubscription, error) {
	mbox, err := e.calendarOf(rd)
	if err != nil {
		return nil, err
	}
	if err := e.accounts.ensureCalSubTable(); err != nil {
		return nil, err
	}
	rows, err := e.accounts.db.QueryContext(context.Background(), `SELECT id,name,url,user,busy,fetched,error,COALESCE(LENGTH(data),0) FROM vayumail_cal_subs WHERE mailbox=? ORDER BY name,id`, mbox)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CalSubscription
	for rows.Next() {
		var c CalSubscription
		var at int64
		if err := rows.Scan(&c.ID, &c.Name, &c.URL, &c.User, &c.Busy, &at, &c.Error, &c.Size); err != nil {
			return nil, err
		}
		if at > 0 {
			c.Fetched = time.Unix(at, 0)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RemoveCalendarSubscription stops one of rd's subscriptions.
func (e *Engine) RemoveCalendarSubscription(rd Reader, id int64) error {
	mbox, err := e.calendarOf(rd)
	if err != nil {
		return err
	}
	if err := e.accounts.ensureCalSubTable(); err != nil {
		return err
	}
	res, err := e.accounts.db.ExecContext(context.Background(), `DELETE FROM vayumail_cal_subs WHERE id=? AND mailbox=?`, id, mbox)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoSuchCalSub
	}
	return nil
}

// RefreshCalendarSubscription reads one of rd's feeds again now, unless it
// was read in the last calSubAgain.
func (e *Engine) RefreshCalendarSubscription(rd Reader, id int64, now time.Time) error {
	mbox, err := e.calendarOf(rd)
	if err != nil {
		return err
	}
	if err := e.accounts.ensureCalSubTable(); err != nil {
		return err
	}
	ctx := context.Background()
	var at int64
	if err := e.accounts.db.QueryRowContext(ctx, `SELECT fetched FROM vayumail_cal_subs WHERE id=? AND mailbox=?`, id, mbox).Scan(&at); err != nil {
		return errNoSuchCalSub
	}
	if now.Sub(time.Unix(at, 0)) < calSubAgain {
		return nil
	}
	e.readSubscription(ctx, id, now)
	return nil
}

// refreshSubscriptions reads the feeds whose last read is calSubEvery old,
// the oldest first, a few a pass.
func (e *Engine) refreshSubscriptions(now time.Time) {
	if e.accounts == nil || e.accounts.ensureCalSubTable() != nil {
		return
	}
	ctx := context.Background()
	rows, err := e.accounts.db.QueryContext(ctx, `SELECT id FROM vayumail_cal_subs WHERE fetched<=? ORDER BY fetched LIMIT ?`, now.Add(-calSubEvery).Unix(), calSubsPerPass)
	if err != nil {
		return
	}
	var all []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			all = append(all, id)
		}
	}
	if rows.Err() != nil || rows.Close() != nil {
		return
	}
	for _, id := range all {
		e.readSubscription(ctx, id, now)
	}
}

// subFeeds holds feeds parsed, by subscription and the size and time of the
// copy, so a view does not parse a few megabytes each time; a newer copy
// replaces its entry. Calendars are only read once parsed, so one parsed
// copy serves every view at once.
var subFeeds = struct {
	sync.Mutex
	m map[int64]parsedFeed
}{m: map[int64]parsedFeed{}}

type parsedFeed struct {
	key  int64 // fetched time and size, together
	cals []*ical.Calendar
}

// maxParsedFeeds bounds subFeeds: past it, the cache starts again.
const maxParsedFeeds = 64

// subscribedEvents is the occurrences in [from, to) of mbox's subscribed
// calendars, each marked with its calendar's name, and free where the
// holder said its events do not make them busy.
func (e *Engine) subscribedEvents(ctx context.Context, mbox string, from, to time.Time, home *time.Location, budget *int) []CalEvent {
	if e.accounts.ensureCalSubTable() != nil {
		return nil
	}
	rows, err := e.accounts.db.QueryContext(ctx, `SELECT id,name,busy,fetched,LENGTH(data) FROM vayumail_cal_subs WHERE mailbox=? AND data IS NOT NULL`, mbox)
	if err != nil {
		return nil
	}
	type sub struct {
		id       int64
		name     string
		busy     bool
		at, size int64
	}
	var subs []sub
	for rows.Next() {
		var s sub
		if rows.Scan(&s.id, &s.name, &s.busy, &s.at, &s.size) == nil {
			subs = append(subs, s)
		}
	}
	if rows.Err() != nil || rows.Close() != nil {
		return nil
	}
	var out []CalEvent
	for _, s := range subs {
		cals := e.parsedSub(ctx, s.id, s.at<<32^s.size)
		for _, cal := range cals {
			c := newCalReader(cal, home)
			c.self = mbox
			for _, ev := range c.occurrences(cal, from, to, budget) {
				ev.Sub = s.name
				// Its own UIDs say nothing about the holder's: the event is
				// named by its subscription too, so it is never taken for
				// one of theirs.
				ev.UID = "sub-" + strconv.FormatInt(s.id, 10) + "-" + ev.UID
				if !s.busy {
					ev.Free = true
				}
				out = append(out, ev)
			}
		}
	}
	return out
}

func (e *Engine) parsedSub(ctx context.Context, id, key int64) []*ical.Calendar {
	subFeeds.Lock()
	if f, ok := subFeeds.m[id]; ok && f.key == key {
		subFeeds.Unlock()
		return f.cals
	}
	subFeeds.Unlock()
	var data []byte
	if err := e.accounts.db.QueryRowContext(ctx, `SELECT data FROM vayumail_cal_subs WHERE id=?`, id).Scan(&data); err != nil {
		return nil
	}
	cals, err := parseFeed(data)
	if err != nil {
		return nil
	}
	subFeeds.Lock()
	if len(subFeeds.m) >= maxParsedFeeds {
		subFeeds.m = map[int64]parsedFeed{}
	}
	subFeeds.m[id] = parsedFeed{key: key, cals: cals}
	subFeeds.Unlock()
	return cals
}
