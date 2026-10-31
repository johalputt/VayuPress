// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/signin"
)

// signinApp is an App with a migrated database and the record of attempts,
// holding failures from 198.51.100.23 on three accounts (so blocked for
// spraying) and from 203.0.113.9 on one.
func signinApp(t *testing.T) (*App, *signin.Store) {
	t.Helper()
	openMigratedDB(t)
	a := &App{}
	st := withSignins(t, a)
	for _, acct := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		st.Record(signin.Attempt{Account: acct, Door: signin.DoorIMAP, Stage: signin.StagePassword, IP: "198.51.100.23:993"})
	}
	st.Record(signin.Attempt{Account: "a@example.com", Door: signin.DoorWeb, Stage: signin.StageCode, IP: "203.0.113.9"})
	if err := st.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	return a, st
}

func sourceFP(t *testing.T, st *signin.Store, masked string) string {
	t.Helper()
	rows, _ := st.Attempts(context.Background(), "", "", 50)
	for _, x := range rows {
		if x.Masked == masked {
			return x.FP
		}
	}
	t.Fatalf("no attempt from %s", masked)
	return ""
}

func postSigninAction(a *App, h func(*App, http.ResponseWriter, *http.Request), from string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/os/api/shield/sign-ins/x", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = from + ":5000"
	rec := httptest.NewRecorder()
	h(a, rec, req)
	return rec
}

// The page lists each source masked, what it did and the block that spraying
// set, and never writes a whole address.
func TestTheSigninPageShowsSourcesMasked(t *testing.T) {
	a, _ := signinApp(t)
	page := a.signinsBody(context.Background(), 7, "", "")
	for _, want := range []string{"198.51.x.x", "3 attempts on 3 accounts through IMAP", "Blocked automatically", "203.0.x.x", "Knew a password", "4 failed sign-ins"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page has no %q", want)
		}
	}
	for _, leak := range []string{"198.51.100.23", "203.0.113.9"} {
		if strings.Contains(page, leak) {
			t.Errorf("the page writes the whole address %s", leak)
		}
	}
}

// A source's address, then its network, is blocked from the page; a block of
// the operator's own address is refused with a reason the toast can say; a
// lifted block refuses nothing.
func TestTheSigninPageBlocksAndLifts(t *testing.T) {
	a, st := signinApp(t)
	fp := sourceFP(t, st, "203.0.x.x")
	if rec := postSigninAction(a, (*App).handleOSSigninBlock, "203.0.113.9", url.Values{"fp": {fp}, "for": {"1"}}); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "stop your own sign-in") {
		t.Errorf("blocking your own address = %d %s, want 409 saying why", rec.Code, rec.Body.String())
	}
	if _, refused := st.Refuse("a@example.com", "203.0.113.9"); refused {
		t.Fatal("a refused block was set")
	}
	if rec := postSigninAction(a, (*App).handleOSSigninBlock, "192.0.2.1", url.Values{"fp": {fp}, "for": {"1"}}); rec.Code != http.StatusOK {
		t.Fatalf("block = %d %s", rec.Code, rec.Body.String())
	}
	if _, refused := st.Refuse("a@example.com", "203.0.113.9"); !refused {
		t.Error("the blocked address was not refused")
	}
	if _, refused := st.Refuse("a@example.com", "203.0.113.10"); refused {
		t.Error("an address block refused the address beside it")
	}
	if rec := postSigninAction(a, (*App).handleOSSigninBlock, "192.0.2.1", url.Values{"fp": {fp}, "for": {"net"}}); rec.Code != http.StatusOK {
		t.Fatalf("network block = %d", rec.Code)
	}
	if _, refused := st.Refuse("a@example.com", "203.0.113.10"); !refused {
		t.Error("the network block did not refuse an address in it")
	}
	for _, b := range st.Blocks("") {
		req := httptest.NewRequest(http.MethodPost, "/os/api/shield/sign-ins/lift?id="+strconv.FormatInt(b.ID, 10), nil)
		rec := httptest.NewRecorder()
		a.handleOSSigninLift(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("lift %d = %d", b.ID, rec.Code)
		}
	}
	if _, refused := st.Refuse("a@example.com", "198.51.100.23"); refused {
		t.Error("a lifted block still refused")
	}
	if rec := postSigninAction(a, (*App).handleOSSigninBlock, "192.0.2.1", url.Values{"fp": {fp}, "for": {"7"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("a length the menu does not offer = %d, want 400", rec.Code)
	}
}

// "It was one of us" lifts the spraying block and each account the source
// tried remembers the place.
func TestTheSigninPagesItWasOneOfUs(t *testing.T) {
	a, st := signinApp(t)
	fp := sourceFP(t, st, "198.51.x.x")
	if _, refused := st.Refuse("d@example.com", "198.51.100.23"); !refused {
		t.Fatal("the spraying source is not blocked to begin with")
	}
	if rec := postSigninAction(a, (*App).handleOSSigninMine, "192.0.2.1", url.Values{"fp": {fp}, "days": {"7"}}); rec.Code != http.StatusOK {
		t.Fatalf("It was one of us = %d", rec.Code)
	}
	if _, refused := st.Refuse("d@example.com", "198.51.100.23"); refused {
		t.Error("the source is still blocked")
	}
	if !strings.Contains(a.signinsBody(context.Background(), 7, "", ""), "It was us") {
		t.Error("the page does not mark the source as the install's own")
	}
}

// A country is refused from the page, for every account, unless it is the
// operator's own.
func TestTheSigninPageRefusesACountry(t *testing.T) {
	a, st := signinApp(t)
	prev := signin.Country
	signin.Country = func(ip string) string {
		return map[string]string{"198.51.100.23": "VN", "192.0.2.1": "IN"}[ip]
	}
	t.Cleanup(func() { signin.Country = prev })
	if rec := postSigninAction(a, (*App).handleOSSigninCountry, "192.0.2.1", url.Values{"country": {"in"}}); rec.Code != http.StatusConflict {
		t.Errorf("refusing your own country = %d, want 409", rec.Code)
	}
	if rec := postSigninAction(a, (*App).handleOSSigninCountry, "192.0.2.1", url.Values{"country": {"vn"}}); rec.Code != http.StatusOK {
		t.Fatalf("refusing a country = %d", rec.Code)
	}
	if _, refused := st.Refuse("z@example.com", "198.51.100.23"); !refused {
		t.Error("the country was not refused")
	}
}

// The page and its actions are the administrator's: Shield's area.
func TestTheSigninPageIsTheAdministrators(t *testing.T) {
	for _, p := range []string{"/os/shield/sign-ins", "/os/api/shield/sign-ins/block", "/os/api/shield/sign-ins/mine"} {
		if got, want := osPathMinLevel(p), osPathMinLevel("/os/settings"); got != want {
			t.Errorf("%s needs level %d, want the administrator's %d", p, got, want)
		}
	}
}
