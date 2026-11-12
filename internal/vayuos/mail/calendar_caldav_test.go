// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
)

// remoteCalDAV is another CalDAV server, over TLS: this product's own,
// serving groupOwner's calendar to davPass, with one event in it.
func remoteCalDAV(t *testing.T) *httptest.Server {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "remote.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := NewAccountStore(db)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer((&Engine{accounts: s}).DAVHandler(func(user, password string) (string, bool) {
		return groupOwner, user == groupOwner && password == davPass
	}))
	t.Cleanup(srv.Close)
	c, err := caldav.NewClient(webdav.HTTPClientWithBasicAuth(srv.Client(), groupOwner, davPass), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	cal, err := ical.NewDecoder(strings.NewReader(string(feed("Board meeting", "20261110T140000Z", "20261110T150000Z")))).Decode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutCalendarObject(context.Background(), davCal+"board.ics", cal); err != nil {
		t.Fatal(err)
	}
	return srv
}

// testSealer stands in for the secrets store: sealed values carry its
// prefix, and the test can see that what is kept is not the password.
func testSealer(e *Engine) { e.totpCodec = sealedHexCodec{} }

type sealedHexCodec struct{}

func (sealedHexCodec) SealField(p string) (string, error) {
	return sealedSecret + hex.EncodeToString([]byte(p)), nil
}
func (sealedHexCodec) OpenField(s string) (string, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(s, sealedSecret))
	return string(b), err
}

// clearCodec hands a value back as it is, as a store with no key would.
type clearCodec struct{}

func (clearCodec) SealField(p string) (string, error) { return p, nil }
func (clearCodec) OpenField(p string) (string, error) { return p, nil }

// TestACalendarOnAnotherCalDAVServerIsReadSignedIn — its events are shown
// and make the holder busy; the password is kept sealed; a wrong password
// is said, and keeps nothing.
func TestACalendarOnAnotherCalDAVServerIsReadSignedIn(t *testing.T) {
	e, s, _ := itipEngine(t)
	srv := remoteCalDAV(t)
	e.calDAVTransport = srv.Client().Transport
	testSealer(e)
	rd := ReadAsOwner("ankush")
	if _, err := e.AddCalDAVSubscription(rd, "Board", srv.URL+davCal, groupOwner, davPass, true, bookingNow); err != nil {
		t.Fatal(err)
	}
	subs, _ := e.CalendarSubscriptions(rd)
	if len(subs) != 1 || subs[0].Error != "" || subs[0].User != groupOwner {
		t.Fatalf("the subscription: %+v", subs)
	}
	from := tuesdayAt("00:00")
	evs := between(t, e, from, from.AddDate(0, 0, 1), time.UTC)
	if len(evs) != 1 || evs[0].Summary != "Board meeting" || evs[0].Sub != "Board" {
		t.Fatalf("the other server's event: %+v", evs)
	}
	var kept string
	_ = s.db.QueryRow(`SELECT secret FROM vayumail_cal_subs`).Scan(&kept)
	if !strings.HasPrefix(kept, sealedSecret) || strings.Contains(kept, davPass) {
		t.Errorf("the password is kept as %q", kept)
	}

	if _, err := e.AddCalDAVSubscription(rd, "Wrong", srv.URL+davCal, groupOwner, "not-it", true, bookingNow); err != nil {
		t.Fatal(err)
	}
	subs, _ = e.CalendarSubscriptions(rd)
	if subs[1].Name != "Wrong" || subs[1].Error != "the server refused the user name and password" || subs[1].Size != 0 {
		t.Errorf("a wrong password: %+v", subs[1])
	}
}

// TestACalendarSignedInToIsRefusedWithoutItsSafeguards — not over plain
// http, not without a store to seal the password, and a password that is
// not sealed is never sent.
func TestACalendarSignedInToIsRefusedWithoutItsSafeguards(t *testing.T) {
	e, s, _ := itipEngine(t)
	rd := ReadAsOwner("ankush")
	if _, err := e.AddCalDAVSubscription(rd, "", "https://dav.example/cal/", "me", "pw", true, bookingNow); !errors.Is(err, ErrCalDAVNoSealer) {
		t.Errorf("with no secrets store: %v", err)
	}
	// A store that hands the password back as it is (one that keeps no
	// key) is no store.
	e.totpCodec = clearCodec{}
	if _, err := e.AddCalDAVSubscription(rd, "", "https://dav.example/cal/", "me", "pw", true, bookingNow); !errors.Is(err, ErrCalDAVNoSealer) {
		t.Errorf("with a store that does not seal: %v", err)
	}
	testSealer(e)
	if _, err := e.AddCalDAVSubscription(rd, "", "http://dav.example/cal/", "me", "pw", true, bookingNow); err == nil || !strings.Contains(err.Error(), "over https") {
		t.Errorf("over plain http: %v", err)
	}
	if _, err := e.AddCalDAVSubscription(rd, "", "https://dav.example/cal/", "me", "", true, bookingNow); err == nil {
		t.Error("with no password")
	}
	// Through the real transport, a server on this one's own network is
	// not reached, and the page says why.
	if _, err := e.AddCalDAVSubscription(rd, "Inside", "https://127.0.0.1:1/cal/", "me", "pw", true, bookingNow); err != nil {
		t.Fatal(err)
	}
	if subs, _ := e.CalendarSubscriptions(rd); len(subs) != 1 || !strings.Contains(subs[0].Error, "this server's own network") {
		t.Errorf("a CalDAV server on loopback: %+v", subs)
	}
	if _, err := s.db.Exec(`DELETE FROM vayumail_cal_subs`); err != nil {
		t.Fatal(err)
	}
	// A row whose password was kept in the clear is not read with it.
	srv := remoteCalDAV(t)
	e.calDAVTransport = srv.Client().Transport
	if err := s.ensureCalSubTable(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO vayumail_cal_subs(mailbox,name,url,busy,user,secret) VALUES(?,?,?,?,?,?)`, calBox, "Clear", srv.URL+davCal, 1, groupOwner, davPass); err != nil {
		t.Fatal(err)
	}
	e.refreshSubscriptions(bookingNow.Add(time.Hour))
	subs, _ := e.CalendarSubscriptions(rd)
	if len(subs) != 1 || subs[0].Size != 0 || !strings.Contains(subs[0].Error, "no secrets store") {
		t.Errorf("a password kept in the clear: %+v", subs)
	}
}

// TestACalDAVServersAnswerIsBounded — an answer past 4 MB is not read.
func TestACalDAVServersAnswerIsBounded(t *testing.T) {
	b := &boundedBody{rc: nopCloser{strings.NewReader(strings.Repeat("x", maxCalSubBytes+10))}, left: maxCalSubBytes}
	buf := make([]byte, 64<<10)
	var n int
	var err error
	for i := 0; err == nil && i < 1000; i++ {
		var k int
		k, err = b.Read(buf)
		n += k
	}
	if !errors.Is(err, errCalDAVTooLarge) || n != maxCalSubBytes {
		t.Errorf("read %d bytes, then %v", n, err)
	}
}

type nopCloser struct{ *strings.Reader }

func (nopCloser) Close() error { return nil }
