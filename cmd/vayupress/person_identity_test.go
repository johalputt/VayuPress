// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/avatar"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/users"
)

// personApp is an install where dana@example.com holds a mailbox (created as
// "Dana") and a site account, signed in on the site.
func personApp(t *testing.T) (*App, *http.Cookie, *users.User) {
	t.Helper()
	a := appWithMailAccounts(t)
	openMigratedDB(t)
	a.members = members.New(dbpkg.DB)
	a.userStore = users.New(dbpkg.DB)
	m, err := a.members.UpsertScoped(t.Context(), "", "dana@example.com")
	if err != nil {
		t.Fatal(err)
	}
	token, err := a.members.CreateSession(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	holder := &users.User{ID: "vmail:dana@example.com", Email: "dana@example.com", Role: users.RoleAuthor, MailAddress: "dana@example.com"}
	return a, &http.Cookie{Name: memberCookie, Value: token}, holder
}

// A name changed on the site account went out on mail under the name the
// mailbox was created with (3cs). Changed in either place, it is one name.
func TestANameIsOneNameOnTheSiteAndOnMail(t *testing.T) {
	a, cookie, holder := personApp(t)
	ctx := context.Background()

	req := httptest.NewRequest(http.MethodPost, "/members/account", strings.NewReader(url.Values{"name": {"Er. Dana Rao"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	a.handleMemberAccountUpdate(httptest.NewRecorder(), req)
	if got := a.senderDisplayName(ctx, "dana@example.com"); got != "Er. Dana Rao" {
		t.Errorf("mail after the site account's name changed goes out as %q, want Er. Dana Rao", got)
	}

	// From the mailbox settings, the holder's own: the site account follows,
	// and a line break, which would start a header of its own, is dropped.
	rec := postAppPwForm(a.handleVayuOSAliasAction, "/", url.Values{"op": {"name-set"}, "email": {"dana@example.com"}, "name": {"Dana Traders\r\nBcc: x@evil.example"}}, holder)
	if rec.Code != http.StatusOK {
		t.Fatalf("the holder's own name = %d %s", rec.Code, rec.Body.String())
	}
	if got := a.senderDisplayName(ctx, "dana@example.com"); got != "Dana TradersBcc: x@evil.example" {
		t.Errorf("the name on mail = %q, want it without its line break", got)
	}
	if m, _ := a.members.Get(ctx, "dana@example.com"); m == nil || m.Name != "Dana TradersBcc: x@evil.example" {
		t.Errorf("the site account's name after the mailbox's changed = %+v", m)
	}

	// Another mailbox's name is not the holder's to set.
	if rec := postAppPwForm(a.handleVayuOSAliasAction, "/", url.Values{"op": {"name-set"}, "email": {"erin@example.com"}, "name": {"x"}}, holder); rec.Code != http.StatusForbidden {
		t.Errorf("the holder naming another mailbox = %d, want 403", rec.Code)
	}
}

// A cartoon chosen in the mail settings was the initials on the site, and one
// chosen on the site the initials in the mailbox (3cv). Chosen in either
// place, it is the picture in both.
func TestAPictureIsOnePictureOnTheSiteAndOnMail(t *testing.T) {
	a, cookie, holder := personApp(t)
	ctx := context.Background()

	if rec := postAppPwForm(a.handleVayuOSAvatarCartoon, "/", url.Values{"email": {"dana@example.com"}, "n": {"4"}}, holder); rec.Code != http.StatusOK {
		t.Fatalf("the holder's cartoon = %d %s", rec.Code, rec.Body.String())
	}
	if m, _ := a.members.Get(ctx, "dana@example.com"); m == nil || m.AvatarChoice != "cartoon:4" {
		t.Errorf("the site account's picture after the mailbox's cartoon = %+v, want cartoon:4", m)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/members/avatar/choose", strings.NewReader(`{"choice":"cartoon:7"}`))
	req.AddCookie(cookie)
	a.handleMemberAvatarChoose(httptest.NewRecorder(), req)
	blob, _, _ := a.vayuMail.Accounts().Avatar(ctx, "dana@example.com")
	if !bytes.Equal(blob, []byte(avatar.Cartoon(7, "dana@example.com"))) {
		t.Error("the mailbox's picture after the site account chose cartoon 7 is not that cartoon")
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/members/avatar/choose", strings.NewReader(`{"choice":""}`))
	req.AddCookie(cookie)
	a.handleMemberAvatarChoose(httptest.NewRecorder(), req)
	if blob, _, _ := a.vayuMail.Accounts().Avatar(ctx, "dana@example.com"); len(blob) != 0 {
		t.Error("the automatic picture on the site left the mailbox a picture")
	}
}

// The console's account menu named a mail-only holder Author, the role the
// session stands in for, and showed their initials over the picture they had
// chosen (3cu, 3cv).
func TestTheAccountMenuNamesAMailboxAndShowsItsPicture(t *testing.T) {
	a, _, holder := personApp(t)
	ctx := context.WithValue(context.WithValue(context.Background(), ctxUserKey, holder), ctxMailOnlyKey, true)

	s := a.getOSSettings(ctx)
	if s.RoleLabel != "Mailbox: mail only" {
		t.Errorf("the menu's role for a mail-only mailbox = %q, want Mailbox: mail only", s.RoleLabel)
	}
	if s.UserAvatar != "" {
		t.Errorf("with no picture chosen the menu shows %q", s.UserAvatar)
	}
	if rec := postAppPwForm(a.handleVayuOSAvatarCartoon, "/", url.Values{"email": {"dana@example.com"}, "n": {"2"}}, holder); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if s := a.getOSSettings(ctx); s.UserAvatar != "/os/vayumail/accounts/avatar?email=dana%40example.com" {
		t.Errorf("the menu's picture after the cartoon = %q, want the mailbox's", s.UserAvatar)
	}
}

// The site's bar showed initials for a member with a chosen picture.
func TestTheSitesBarShowsTheMembersChosenPicture(t *testing.T) {
	a, _, _ := personApp(t)
	ctx := context.Background()
	_ = a.members.SetAvatarChoice(ctx, "dana@example.com", "cartoon:1")
	m, _ := a.members.Get(ctx, "dana@example.com")
	snap := a.memberSnapshot(httptest.NewRequest(http.MethodGet, "/", nil), m)
	if got, _ := snap["avatar"].(string); !strings.HasPrefix(got, "/api/v1/members/avatar/"+m.ID+"?v=") {
		t.Errorf("the bar's picture for a member with a cartoon = %q", got)
	}
	_ = a.members.SetAvatarChoice(ctx, "dana@example.com", "")
	m, _ = a.members.Get(ctx, "dana@example.com")
	if _, has := a.memberSnapshot(httptest.NewRequest(http.MethodGet, "/", nil), m)["avatar"]; has {
		t.Error("the automatic picture replaced the initial in the bar")
	}
}

// A mailbox's session was named after its address ("dana"), or after a site
// account with no name, while its mail went out as the mailbox's name: the
// menu and the site's bar now say what recipients read.
func TestAMailboxSessionIsNamedAsItsMail(t *testing.T) {
	a, cookie, _ := personApp(t)
	ctx := context.Background()
	if u, _, ok := a.resolveMailSessionUser(ctx, "dana@example.com"); !ok || u.Name != "Dana" {
		t.Errorf("the console mailbox session's name = %+v, want Dana", u)
	}
	req := httptest.NewRequest(http.MethodGet, "/os/vayumail", nil)
	req.AddCookie(cookie)
	if u, _, ok := a.resolveMailMember(req); !ok || u.Name != "Dana" {
		t.Errorf("the site-session mailbox user's name = %+v, want Dana", u)
	}
}
