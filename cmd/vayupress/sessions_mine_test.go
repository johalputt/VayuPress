// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// The device a session was opened from, in the words a person would use.
func TestDeviceLabel(t *testing.T) {
	for ua, want := range map[string]string{
		"Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0":                                                    "Firefox on Linux",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36 Edg/130.0":     "Edge on Windows",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/604.1": "Safari on iPhone",
		"Mozilla/5.0 (Linux; Android 15) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Mobile Safari/537.36":                  "Chrome on Android",
		"curl/8.9": "",
	} {
		if got := deviceLabel(ua); got != want {
			t.Errorf("%q: %q, want %q", ua, got, want)
		}
	}
}

// A principal's sessions are listed with their devices and this one marked;
// one is ended by its id, never another principal's and never this one by
// that route; and everywhere else ends all but this. Against the shipped
// schema (migration 128), not a hand-written one.
func TestMySessions(t *testing.T) {
	dir := t.TempDir()
	os.Setenv("DB_PATH", filepath.Join(dir, "s.db"))
	os.Setenv("API_KEY", "test-key")
	config.Load()
	if err := dbpkg.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dbpkg.ClosePools(); dbpkg.DB.Close() })
	a := &App{userStore: users.New(dbpkg.DB), sessions: auth.NewSessionStore(dbpkg.DB)}
	ctx := context.Background()
	me := &users.User{ID: "vmail:dana@example.com", Email: "dana@example.com", Role: users.RoleAuthor, MailAddress: "dana@example.com"}

	open := func(uid, ua string) string {
		r := httptest.NewRequest(http.MethodPost, "/os/login", nil)
		r.Header.Set("User-Agent", ua)
		tok, err := a.startSession(r, uid)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	this := open(me.ID, "Mozilla/5.0 (X11; Linux x86_64) Firefox/131.0")
	phone := open(me.ID, "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Safari/604.1")
	other := open(me.ID, "Mozilla/5.0 (Windows NT 10.0) Chrome/130.0 Safari/537.36")
	theirs := open("vmail:erin@example.com", "Mozilla/5.0 (X11; Linux x86_64) Firefox/131.0")

	req := func(path string, vals url.Values) *http.Request {
		r := withUser(httptest.NewRequest(http.MethodPost, path, strings.NewReader(vals.Encode())), me)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: this})
		return r
	}
	block := a.sessionsBlock(req("/", nil))
	for _, want := range []string{"Firefox on Linux", "Safari on iPhone", "Chrome on Windows", `<span class="mx-sessions__this">This one</span>`, "Sign out everywhere else"} {
		if !strings.Contains(block, want) {
			t.Errorf("the list lacks %q:\n%s", want, block)
		}
	}
	if strings.Count(block, ">Sign out</button>") != 2 {
		t.Errorf("not every other session can be signed out:\n%s", block)
	}

	alive := func(tok string) bool { _, err := a.sessions.Validate(ctx, tok); return err == nil }
	end := func(path string, vals url.Values) {
		a.endSessions(httptest.NewRecorder(), req(path, vals), path == "/os/api/sessions/end-others")
	}

	end("/os/api/sessions/end", url.Values{"id": {auth.SessionID(theirs)}})
	if !alive(theirs) {
		t.Fatal("another principal's session was ended by its id")
	}
	end("/os/api/sessions/end", url.Values{"id": {auth.SessionID(this)}})
	if !alive(this) {
		t.Fatal("this session was ended from its own list")
	}
	end("/os/api/sessions/end", url.Values{"id": {auth.SessionID(phone)}})
	if alive(phone) || !alive(other) {
		t.Fatal("the phone's session was not the one ended")
	}
	end("/os/api/sessions/end-others", nil)
	if alive(other) || !alive(this) || !alive(theirs) {
		t.Fatalf("everywhere else: other %v, this %v, theirs %v", alive(other), alive(this), alive(theirs))
	}
}

// The checklist counts what is done and says where to do the rest; an app
// password counts as forgotten only when it is approved and unused.
func TestTheSecurityChecklist(t *testing.T) {
	now := time.Now()
	for name, c := range map[string]struct {
		p    vmail.AppPassword
		want bool
	}{
		"never used, two months old": {vmail.AppPassword{Status: vmail.DeviceStatusApproved, CreatedAt: now.Add(-60 * 24 * time.Hour)}, true},
		"never used, a week old":     {vmail.AppPassword{Status: vmail.DeviceStatusApproved, CreatedAt: now.Add(-7 * 24 * time.Hour)}, false},
		"last used four months ago":  {vmail.AppPassword{Status: vmail.DeviceStatusApproved, CreatedAt: now.Add(-400 * 24 * time.Hour), LastUsedAt: now.Add(-120 * 24 * time.Hour)}, true},
		"used last week":             {vmail.AppPassword{Status: vmail.DeviceStatusApproved, CreatedAt: now.Add(-400 * 24 * time.Hour), LastUsedAt: now.Add(-7 * 24 * time.Hour)}, false},
		"blocked":                    {vmail.AppPassword{Status: vmail.DeviceStatusBlocked, CreatedAt: now.Add(-400 * 24 * time.Hour)}, false},
	} {
		if got := appPasswordForgotten(c.p, now); got != c.want {
			t.Errorf("%s: forgotten = %v", name, got)
		}
	}
	a := appWithMailAccounts(t)
	dana := &users.User{ID: "vmail:dana@example.com", Email: "dana@example.com", Role: users.RoleAuthor, MailAddress: "dana@example.com"}
	ac, _ := a.mailAccountByEmail(context.Background(), "dana@example.com")
	out := a.securityChecklist(withUser(httptest.NewRequest(http.MethodGet, "/", nil), dana), ac)
	if !strings.Contains(out, "<strong>1 of 4</strong> done") || !strings.Contains(out, "Turn on two-factor") || !strings.Contains(out, "No forgotten app passwords") {
		t.Errorf("a fresh mailbox's checklist:\n%s", out)
	}
	ac.TOTPEnabled = true
	if out := a.securityChecklist(withUser(httptest.NewRequest(http.MethodGet, "/", nil), dana), ac); !strings.Contains(out, "<strong>2 of 4</strong> done") || !strings.Contains(out, "Two-factor is on") {
		t.Errorf("with two-factor on:\n%s", out)
	}
}
