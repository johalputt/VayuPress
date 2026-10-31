// SPDX-License-Identifier: Apache-2.0

package main

import (
	"database/sql"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// A holder makes a throwaway address from their Settings, turns it off and
// on, and removes it; the handler acts on the session's own mailbox, so
// another mailbox's address is out of reach whatever the form names.
func TestAHoldersThrowawayAddresses(t *testing.T) {
	a := appWithMailAccounts(t)
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a.userStore = users.New(db)
	hash, _ := auth.HashSecretArgon2id("erin-pass")
	if err := a.vayuMail.Accounts().Create(t.Context(), "erin@example.com", hash, "Erin", "mailbox"); err != nil {
		t.Fatal(err)
	}
	dana := &users.User{ID: "vmail:dana@example.com", Email: "dana@example.com", Role: users.RoleAuthor, MailAddress: "dana@example.com"}
	erin := &users.User{ID: "vmail:erin@example.com", Email: "erin@example.com", Role: users.RoleAuthor, MailAddress: "erin@example.com"}
	post := func(u *users.User, vals url.Values) (string, string) {
		rec := postAppPwForm(a.handleVayuOSThrowaway, "/os/vayumail/throwaway", vals, u)
		return rec.Body.String(), rec.Header().Get("HX-Trigger")
	}

	body, _ := post(erin, url.Values{"op": {"new"}, "label": {"Forum"}})
	erins := regexp.MustCompile(`forum-[a-z0-9]{6}@example\.com`).FindString(body)
	body, said := post(dana, url.Values{"op": {"new"}, "label": {"Shop"}})
	mine := regexp.MustCompile(`shop-[a-z0-9]{6}@example\.com`).FindString(body)
	if erins == "" || mine == "" || said != "" || strings.Contains(body, erins) {
		t.Fatalf("made %q and %q; dana's list:\n%s", erins, mine, body)
	}
	if body, _ := post(dana, url.Values{"op": {"off"}, "address": {mine}}); !strings.Contains(body, "Shop · No mail yet · Off") || !strings.Contains(body, ">Turn on</button>") {
		t.Errorf("turned off:\n%s", body)
	}
	if _, said := post(dana, url.Values{"op": {"off"}, "address": {erins}}); !strings.Contains(said, "Not done: that is not one of this mailbox") {
		t.Errorf("dana turning off erin's address: %q", said)
	}
	if list, _ := a.vayuMail.Throwaways(vmail.ReadAsOwner("erin")); len(list) != 1 || list[0].Off {
		t.Errorf("erin's address after dana's attempt: %+v", list)
	}
	if body, _ := post(dana, url.Values{"op": {"remove"}, "address": {mine}}); strings.Contains(body, mine) {
		t.Errorf("removed but still listed:\n%s", body)
	}
}
