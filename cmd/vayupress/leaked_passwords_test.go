// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/users"
)

// "password" is SHA-1 5BAA61E4C9B93F3F0682250B6CF8331B7EE68FD8, which Have I
// Been Pwned lists millions of times.
const breachedAnswer = "0018A45C4D1DEF81644B54AB7F969B88D65:1\r\n1E4C9B93F3F0682250B6CF8331B7EE68FD8:52256179\r\n"

// pwnedStub answers the range API with answer (or fails with err) and
// records each prefix asked for.
func pwnedStub(t *testing.T, answer string, err error) *[]string {
	t.Helper()
	asked := &[]string{}
	old := pwnedRange
	pwnedRange = func(_ context.Context, prefix string) ([]byte, error) {
		*asked = append(*asked, prefix)
		return []byte(answer), err
	}
	t.Cleanup(func() { pwnedRange = old })
	return asked
}

// changePassword submits the console's change-password form as the
// signed-in owner, and reports what the page said and whether the new
// password now signs in.
func changePassword(t *testing.T, a *App, pass string) (page string, changed bool) {
	t.Helper()
	ctx := context.Background()
	u, err := a.userStore.GetByEmail(ctx, "owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"password": {pass}, "confirm": {pass}}
	r := httptest.NewRequest(http.MethodPost, "/os/change-password", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(context.WithValue(r.Context(), ctxUserKey, u))
	rec := httptest.NewRecorder()
	a.handleOSChangePasswordSubmit(rec, r)
	_, err = a.userStore.Authenticate(ctx, "owner@example.test", pass)
	return rec.Body.String(), err == nil
}

func leakedApp(t *testing.T, on bool) *App {
	t.Helper()
	a, _ := shieldSitesApp(t)
	a.userStore = users.New(dbpkg.DB)
	if _, err := a.userStore.Create(context.Background(), "owner@example.test", "Owner", "a long first password", users.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	v := "off"
	if on {
		v = "on"
	}
	if err := a.siteSettings.SetMany(context.Background(), settings.ForPrimary(), map[string]string{settings.KeyShieldLeakedPasswords: v}); err != nil {
		t.Fatal(err)
	}
	return a
}

// Switched on, a password the range API lists is refused with the reason,
// and only the hash's first five characters were asked about.
func TestABreachedPasswordIsRefusedWhenTheCheckIsOn(t *testing.T) {
	a := leakedApp(t, true)
	asked := pwnedStub(t, breachedAnswer, nil)
	page, changed := changePassword(t, a, "password")
	if changed {
		t.Fatal("a breached password was set")
	}
	if !strings.Contains(page, "known data breaches") {
		t.Fatalf("the refusal does not say why: %q", page)
	}
	if len(*asked) != 1 || (*asked)[0] != "5BAA6" {
		t.Fatalf("the range API was asked %q; only the five-character prefix may leave", *asked)
	}
}

// Switched off, nothing is asked and the password is set.
func TestNothingIsAskedWhenTheCheckIsOff(t *testing.T) {
	a := leakedApp(t, false)
	asked := pwnedStub(t, breachedAnswer, nil)
	if _, changed := changePassword(t, a, "password"); !changed {
		t.Fatal("with the check off, the password was not set")
	}
	if len(*asked) != 0 {
		t.Fatalf("with the check off, the range API was asked %q", *asked)
	}
}

// A password the answer does not list is set, and so is any password when
// the API cannot be reached: the check fails open.
func TestAnUnlistedPasswordOrAnUnreachableListIsAllowed(t *testing.T) {
	a := leakedApp(t, true)
	pwnedStub(t, "0018A45C4D1DEF81644B54AB7F969B88D65:1\r\n", nil)
	if _, changed := changePassword(t, a, "password"); !changed {
		t.Fatal("a password the answer does not list was refused")
	}
	pwnedStub(t, "", errors.New("unreachable"))
	if _, changed := changePassword(t, a, "another long password"); !changed {
		t.Fatal("an unreachable list refused the password")
	}
}

// The Shield form saves the switch.
func TestTheShieldFormSavesTheBreachCheck(t *testing.T) {
	a := leakedApp(t, false)
	saveShieldForm(t, a, url.Values{"sh_enabled": {"on"}, "sh_leaked": {"on"}})
	if !a.leakedPasswordsOn(context.Background()) {
		t.Fatal("the switch was not saved")
	}
}
