// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/users"
)

// A person the operator gave a mailbox could change none of its settings: every
// per-mailbox control answered "administrators only" (3cl). The operator's
// decision: the holder manages its personal settings, and only an
// administrator manages the address. Each case is one rule, on its own control.
func TestAHolderChangesOnlyTheirOwnMailbox(t *testing.T) {
	a := appWithMailAccounts(t)
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a.userStore = users.New(db)
	ctx := context.Background()
	hash, _ := auth.HashSecretArgon2id("erin-mailbox-pass")
	if err := a.vayuMail.Accounts().Create(ctx, "erin@example.com", hash, "Erin", "mailbox"); err != nil {
		t.Fatal(err)
	}
	dana := &users.User{ID: "vmail:dana@example.com", Email: "dana@example.com", Role: users.RoleAuthor, MailAddress: "dana@example.com"}
	post := func(h http.HandlerFunc, vals url.Values) (int, string) {
		rec := postAppPwForm(h, "/", vals, dana)
		return rec.Code, rec.Body.String()
	}

	// Her vacation reply: hers to set, and the answer is her own page, never
	// the accounts list that names every mailbox on the server.
	code, body := post(a.handleVayuOSAutoreplyAction, url.Values{"email": {"dana@example.com"}, "enabled": {"1"}, "body": {"Away until Monday"}})
	if code != http.StatusOK || !a.vayuMail.Accounts().AutoreplyFor(ctx, "dana@example.com").Enabled {
		t.Errorf("the holder's own vacation reply = %d, enabled %v; want it saved", code, a.vayuMail.Accounts().AutoreplyFor(ctx, "dana@example.com").Enabled)
	}
	if strings.Contains(body, "erin@example.com") {
		t.Errorf("the holder was answered with another mailbox in the page:\n%s", body)
	}
	if code, _ := post(a.handleVayuOSAutoreplyAction, url.Values{"email": {"erin@example.com"}, "enabled": {"1"}, "body": {"x"}}); code != http.StatusForbidden || a.vayuMail.Accounts().AutoreplyFor(ctx, "erin@example.com").Enabled {
		t.Errorf("the holder setting another mailbox's vacation reply = %d, want 403 and nothing changed", code)
	}

	// Her forwarding, yes; an alias, which is an address on the operator's
	// domain, no, even naming her own mailbox as both the change's and the
	// alias's.
	if code, _ := post(a.handleVayuOSAliasAction, url.Values{"op": {"forward-set"}, "email": {"dana@example.com"}, "forward": {"dana@elsewhere.example"}}); code != http.StatusOK {
		t.Errorf("the holder's own forwarding = %d, want 200", code)
	}
	if ac, _ := a.mailAccountByEmail(ctx, "dana@example.com"); ac.ForwardTo != "dana@elsewhere.example" {
		t.Errorf("forwarding after the holder set it = %q", ac.ForwardTo)
	}
	if code, _ := post(a.handleVayuOSAliasAction, url.Values{"op": {"forward-set"}, "email": {"erin@example.com"}, "forward": {"x@elsewhere.example"}}); code != http.StatusForbidden {
		t.Errorf("the holder forwarding another mailbox = %d, want 403", code)
	}
	if code, _ := post(a.handleVayuOSAliasAction, url.Values{"op": {"alias-create"}, "email": {"dana@example.com"}, "local": {"sales"}, "target": {"dana@example.com"}}); code != http.StatusForbidden {
		t.Errorf("the holder creating an alias = %d, want 403", code)
	}

	// Her filters.
	if code, _ := post(a.handleVayuOSFilterAction, url.Values{"op": {"create"}, "email": {"dana@example.com"}, "field": {"subject"}, "contains": {"invoice"}, "action": {"read"}}); code != http.StatusOK {
		t.Errorf("the holder's own filter = %d, want 200", code)
	}
	if code, _ := post(a.handleVayuOSFilterAction, url.Values{"op": {"create"}, "email": {"erin@example.com"}, "field": {"subject"}, "contains": {"x"}, "action": {"read"}}); code != http.StatusForbidden {
		t.Errorf("the holder adding a filter to another mailbox = %d, want 403", code)
	}

	// Her picture.
	if code, _ := post(a.handleVayuOSAvatarCartoon, url.Values{"email": {"erin@example.com"}, "n": {"1"}}); code != http.StatusForbidden {
		t.Errorf("the holder setting another mailbox's picture = %d, want 403", code)
	}
	if code, _ := post(a.handleVayuOSAvatarCartoon, url.Values{"email": {"dana@example.com"}, "n": {"1"}}); code != http.StatusOK {
		t.Errorf("the holder's own picture = %d, want 200", code)
	}

	// Her S/MIME certificate: past the boundary to the missing file for her
	// own mailbox, refused before it for another.
	if code, body := post(a.handleVayuOSSMIMERemove, url.Values{"email": {"erin@example.com"}}); code != http.StatusForbidden {
		t.Errorf("the holder removing another mailbox's certificate = %d %s, want 403", code, body)
	}

	// Her page carries the personal settings and not the address's.
	sec, _ := a.mailboxSettingsSections(ctx, "dana@example.com", true)
	for _, admin := range []string{`name="op" value="alias-create"`, "/os/vayumail/accounts/action"} {
		if strings.Contains(sec, admin) {
			t.Errorf("the holder's page carries an administrator's control (%s)", admin)
		}
	}
	if !strings.Contains(sec, `value="forward-set"`) {
		t.Error("the holder's page has no forwarding control")
	}
}

// The site's account panel asked /api/v1/members/mailbox, which knew only a
// mailbox a paying member claimed, and told the holder of a granted mailbox
// that their plan did not include one (3cl).
func TestTheSitesMailboxViewKnowsAGrantedMailbox(t *testing.T) {
	a := enrolApp(t)
	ctx := context.Background()
	m, err := a.members.Upsert(ctx, "boss@example.com")
	if err != nil {
		t.Fatal(err)
	}
	token, err := a.members.CreateSession(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/members/mailbox", nil)
	req.AddCookie(&http.Cookie{Name: memberCookie, Value: token})
	rec := httptest.NewRecorder()
	a.handleMemberMailboxStatus(rec, req)
	var got struct {
		HasMailbox bool   `json:"has_mailbox"`
		Address    string `json:"address"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if !got.HasMailbox || got.Address != "boss@example.com" {
		t.Errorf("a granted mailbox's holder = %d %s; want has_mailbox and the address", rec.Code, rec.Body.String())
	}
}

// The holder changes their own password with the current one, through the
// pipeline every reset takes (3cl). A wrong current password changes nothing.
func TestAHolderChangesTheirOwnPassword(t *testing.T) {
	a := appWithMailAccounts(t)
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a.userStore = users.New(db)
	ctx := context.Background()
	dana := &users.User{ID: "vmail:dana@example.com", Email: "dana@example.com", Role: users.RoleAuthor, MailAddress: "dana@example.com"}
	opens := func(pass string) bool {
		return auth.VerifySecretArgon2id(pass, a.vayuMail.Accounts().HashFor(ctx, "dana@example.com"))
	}

	rec := postAppPwForm(a.handleVayuOSMyPassword, "/", url.Values{"current": {"not-it"}, "new": {"a-new-mailbox-pass"}, "again": {"a-new-mailbox-pass"}}, dana)
	if !strings.Contains(rec.Body.String(), "not the current password") || opens("a-new-mailbox-pass") {
		t.Fatalf("a wrong current password: %s; new opens = %v", rec.Body.String(), opens("a-new-mailbox-pass"))
	}
	rec = postAppPwForm(a.handleVayuOSMyPassword, "/", url.Values{"current": {"main-mailbox-pass"}, "new": {"a-new-mailbox-pass"}, "again": {"a-new-mailbox-typo"}}, dana)
	if !strings.Contains(rec.Body.String(), "differ") || opens("a-new-mailbox-pass") {
		t.Fatalf("two different new passwords: %s", rec.Body.String())
	}
	rec = postAppPwForm(a.handleVayuOSMyPassword, "/", url.Values{"current": {"main-mailbox-pass"}, "new": {"a-new-mailbox-pass"}, "again": {"a-new-mailbox-pass"}}, dana)
	if !strings.Contains(rec.Body.String(), "Password changed") || !opens("a-new-mailbox-pass") || opens("main-mailbox-pass") {
		t.Fatalf("the holder's change: %s; new opens %v, old opens %v", rec.Body.String(), opens("a-new-mailbox-pass"), opens("main-mailbox-pass"))
	}

	nobody := &users.User{ID: "u9", Email: "editor@example.com", Role: users.RoleEditor}
	if rec := postAppPwForm(a.handleVayuOSMyPassword, "/", url.Values{"current": {"x"}, "new": {"yyyyyyyy"}, "again": {"yyyyyyyy"}}, nobody); rec.Code != http.StatusForbidden {
		t.Errorf("a session with no mailbox of its own = %d, want 403", rec.Code)
	}
}

// A mailbox-only session's profile is its mailbox's settings: it has no
// account, so the author profile page offered /author/vmail:<address>, a link
// to nothing, and a form with nowhere to save.
func TestAMailboxSessionsProfileIsItsMailboxSettings(t *testing.T) {
	a := appWithMailAccounts(t)
	dana := &users.User{ID: "vmail:dana@example.com", Email: "dana@example.com", Role: users.RoleAuthor, MailAddress: "dana@example.com"}
	req := withUser(httptest.NewRequest(http.MethodGet, "/os/profile", nil), dana)
	rec := httptest.NewRecorder()
	a.handleOSProfile(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/os/vayumail/settings" {
		t.Errorf("a mailbox session's profile = %d → %q, want its mailbox settings", rec.Code, rec.Header().Get("Location"))
	}
}
