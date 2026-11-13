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
	"github.com/johalputt/vayupress/internal/ui"
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
	page := a.signinsBody(context.Background(), 7, "", "", "")
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

// signinRow is the sources table's row for masked, from its opening tag.
func signinRow(t *testing.T, page, masked string) string {
	t.Helper()
	for _, row := range strings.Split(page, "<tr>") {
		if strings.Contains(row, "<strong>"+masked) {
			return row
		}
	}
	t.Fatalf("no row for %s", masked)
	return ""
}

// Each source names the accounts it tried, most tried first, up to three and
// a count of the rest, and when it was seen.
func TestTheSigninPageNamesTheAccountsASourceTried(t *testing.T) {
	a, st := signinApp(t)
	for _, acct := range []string{"d@example.com", "d@example.com"} {
		st.Record(signin.Attempt{Account: acct, Door: signin.DoorIMAP, Stage: signin.StagePassword, IP: "198.51.100.23:993"})
	}
	if err := st.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	row := signinRow(t, a.signinsBody(context.Background(), 7, "", "", ""), "198.51.x.x")
	if !strings.Contains(row, "Tried d@example.com, a@example.com, b@example.com and <a ") || !strings.Contains(row, ">1 more</a>") {
		t.Errorf("the row does not name d (tried twice) first, then a and b, and one more:\n%s", row)
	}
	if strings.Contains(row, "c@example.com") {
		t.Error("the row names a fourth account; the rest are counted")
	}
	if !strings.Contains(row, "Seen ") && !strings.Contains(row, "From ") {
		t.Errorf("the row does not say when the source was seen:\n%s", row)
	}
}

// The Block menu offers only what would be set: from the source's own
// address nothing of its address or network, from its network nothing of
// the network, and for an address on the install's own network nothing for
// the whole site; each time it says why.
func TestTheSigninMenuLeavesOutABlockThatWouldBeRefused(t *testing.T) {
	a, st := signinApp(t)
	st.Record(signin.Attempt{Account: "a@example.com", Door: signin.DoorWeb, Stage: signin.StagePassword, IP: "10.0.0.5"})
	if err := st.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	all := []string{`value="1"`, `value="30"`, `value="0"`, `value="net"`, `value="site"`, `value="site-net"`}
	for _, x := range []struct {
		self, masked, why string
		offered           []string
	}{
		{"192.0.2.1", "198.51.x.x", "", all},
		{"198.51.100.23", "198.51.x.x", "This is your own address", nil},
		{"198.51.100.99", "198.51.x.x", "Its network is yours", []string{`value="1"`, `value="30"`, `value="0"`, `value="site"`}},
		{"192.0.2.1", "10.0.x.x", "blocked from sign-in only", []string{`value="1"`, `value="30"`, `value="0"`, `value="net"`}},
	} {
		row := signinRow(t, a.signinsBody(context.Background(), 7, "", x.self, ""), x.masked)
		for _, v := range all {
			want := false
			for _, o := range x.offered {
				want = want || o == v
			}
			if strings.Contains(row, v) != want {
				t.Errorf("from %s, %s's menu offers %s: %v, want %v", x.self, x.masked, v, !want, want)
			}
		}
		if x.why != "" && !strings.Contains(row, x.why) {
			t.Errorf("from %s, %s's menu does not say %q", x.self, x.masked, x.why)
		}
		if x.why == "" && strings.Contains(row, "sa-menu__why") {
			t.Errorf("from %s, %s's menu explains a choice it offers", x.self, x.masked)
		}
	}
}

// The blocks in force are listed open while they are few, and folded once
// there are more than signinBlocksOpen, so the sources stay near the top.
func TestTheBlocksInForceFoldWhenThereAreMany(t *testing.T) {
	a, st := signinApp(t)
	open := `<details class="fold" open><summary class="section-head fold__head">`
	if page := a.signinsBody(context.Background(), 7, "", "", ""); !strings.Contains(page, open+string(ui.Icon("chev-r"))+`<h2 class="section-head__title">Blocks in force`) {
		t.Fatal("one block in force is folded")
	}
	for _, c := range []string{"DE", "FR", "VN", "BR", "US"} {
		if _, err := st.BlockCountry(context.Background(), c, "", 0, "admin", ""); err != nil {
			t.Fatal(err)
		}
	}
	page := a.signinsBody(context.Background(), 7, "", "", "")
	if strings.Contains(page, open+string(ui.Icon("chev-r"))+`<h2 class="section-head__title">Blocks in force`) || !strings.Contains(page, `>Blocks in force</h2><span class="section-head__hint">6 now`) {
		t.Errorf("six blocks in force are not listed folded")
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
	if !strings.Contains(a.signinsBody(context.Background(), 7, "", "", ""), "It was us") {
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
