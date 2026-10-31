// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/signin"
	"github.com/johalputt/vayupress/internal/users"
)

// holderOf is the session of a mailbox's holder who is not an administrator.
func holderOf(addr string) *users.User {
	return &users.User{ID: "vmail:" + addr, Email: addr, Role: users.RoleAuthor, MailAddress: addr}
}

// postAsFrom posts the mailbox's sign-in card's form as u from addr.
func postAsFrom(a *App, u *users.User, addr string, vals url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/os/vayumail/accounts/signin", strings.NewReader(vals.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = addr + ":5000"
	req = withUser(req, u)
	rec := httptest.NewRecorder()
	a.handleVayuOSAccountSignin(rec, req)
	return rec
}

// A holder sees their mailbox's attempts, masked, with the warning when
// their password was right and the code was not; says "It was me" only of a
// source that tried them; and sets their fence, but not one that shuts out
// the address they set it from. Another holder may do none of it to them.
func TestAHolderSeesAndFencesTheirOwnSignIns(t *testing.T) {
	a := credentialFloodApp(t)
	st := withSignins(t, a)
	ctx := context.Background()
	prev := signin.Country
	signin.Country = func(ip string) string {
		return map[string]string{"192.0.2.10": "IN", "198.51.100.66": "VN"}[ip]
	}
	t.Cleanup(func() { signin.Country = prev })
	st.Record(signin.Attempt{Account: lockedMailbox, Door: signin.DoorIMAP, Stage: signin.StagePassword, IP: "198.51.100.66"})
	st.Record(signin.Attempt{Account: lockedMailbox, Door: signin.DoorWeb, Stage: signin.StageCode, IP: "198.51.100.66"})
	st.Record(signin.Attempt{Account: "someone@example.com", Door: signin.DoorIMAP, Stage: signin.StagePassword, IP: "203.0.113.70"})
	_ = st.Flush(ctx)
	boss, erin := holderOf(lockedMailbox), holderOf("erin@example.com")

	sec, _ := a.mailboxSettingsSections(ctx, lockedMailbox, true)
	for _, want := range []string{"198.51.x.x · ", "Your password was right but the code was wrong", "It was me"} {
		if !strings.Contains(sec, want) {
			t.Errorf("the holder's card has no %q", want)
		}
	}
	if strings.Contains(sec, "198.51.100.66") || strings.Contains(sec, "203.0.x.x") {
		t.Error("the holder's card shows a whole address, or another mailbox's attempt")
	}

	theirs, _ := st.Attempts(ctx, "", lockedMailbox, 1)
	other, _ := st.Attempts(ctx, "", "someone@example.com", 1)
	if rec := postAsFrom(a, boss, "192.0.2.10", url.Values{"email": {lockedMailbox}, "op": {"mine"}, "fp": {other[0].FP}}); !strings.Contains(rec.Body.String(), "has not tried this mailbox") {
		t.Errorf("It was me on a source that never tried the mailbox was taken: %d", rec.Code)
	}
	if rec := postAsFrom(a, erin, "192.0.2.10", url.Values{"email": {lockedMailbox}, "op": {"mine"}, "fp": {theirs[0].FP}}); rec.Code != http.StatusForbidden {
		t.Errorf("another holder's It was me = %d, want 403", rec.Code)
	}
	if rec := postAsFrom(a, boss, "192.0.2.10", url.Values{"email": {lockedMailbox}, "op": {"mine"}, "fp": {theirs[0].FP}}); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "callout--danger") {
		t.Errorf("the holder's It was me = %d %s", rec.Code, rec.Body.String())
	}

	if rec := postAsFrom(a, boss, "192.0.2.10", url.Values{"email": {lockedMailbox}, "op": {"fence"}, "countries": {"DE"}}); !strings.Contains(rec.Body.String(), "not on the list") || len(st.Fence(lockedMailbox)) != 0 {
		t.Errorf("a fence shutting out the holder's own country was saved: %v", st.Fence(lockedMailbox))
	}
	if rec := postAsFrom(a, erin, "192.0.2.10", url.Values{"email": {lockedMailbox}, "op": {"fence"}, "countries": {"IN"}}); rec.Code != http.StatusForbidden {
		t.Errorf("another holder setting the fence = %d, want 403", rec.Code)
	}
	if rec := postAsFrom(a, boss, "192.0.2.10", url.Values{"email": {lockedMailbox}, "op": {"fence"}, "countries": {"in, de"}}); rec.Code != http.StatusOK || strings.Join(st.Fence(lockedMailbox), ",") != "DE,IN" {
		t.Errorf("the holder's fence = %v (%d), want DE,IN", st.Fence(lockedMailbox), rec.Code)
	}
	if _, refused := st.Refuse(lockedMailbox, "198.51.100.66"); !refused {
		t.Error("the fence let in a country not on it")
	}
	if rec := postAsFrom(a, boss, "192.0.2.10", url.Values{"email": {lockedMailbox}, "op": {"fence"}, "countries": {"India"}}); !strings.Contains(rec.Body.String(), "not a two-letter country code") {
		t.Error("a country written out in full was taken")
	}
}
