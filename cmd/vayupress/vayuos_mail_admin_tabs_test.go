package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
	"github.com/johalputt/vayupress/internal/vayuos/kernel"
)

// Mail's Overview is the first tab of Mail administration, so it is an
// administrator's page. It used to render a reduced view for everyone else;
// now anyone below administrator is sent to their own inbox, as the other
// administration tabs already do. An author holding a mailbox must not see
// the install's queue, DNS state or mailbox counts.
func TestMailOverviewSendsNonAdminsToTheirInbox(t *testing.T) {
	a := appWithMailAccounts(t)
	// The page's own dependencies are present, so a missing role check
	// fails on the assertions below rather than on a nil monitor.
	a.vayuHealth = kernel.NewHealthMonitor()
	for _, role := range []string{users.RoleAuthor, users.RoleEditor} {
		u := &users.User{ID: "u-" + role, Email: role + "@example.com", Role: role}
		rec := httptest.NewRecorder()
		a.handleVayuOSDashboard(rec, withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail", nil), u))
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/os/vayumail/inbox" {
			t.Errorf("%s: status %d, Location %q; want 303 to /os/vayumail/inbox", role, rec.Code, rec.Header().Get("Location"))
		}
		if strings.Contains(rec.Body.String(), "Mail administration") {
			t.Errorf("%s: the administration page was written to a non-administrator", role)
		}
	}
}

func TestMailOverviewIsTheFirstAdministrationTab(t *testing.T) {
	a := appWithMailAccounts(t)
	a.vayuHealth = kernel.NewHealthMonitor()
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
	rec := httptest.NewRecorder()
	a.handleVayuOSDashboard(rec, withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail", nil), admin))
	if rec.Code != http.StatusOK {
		t.Fatalf("admin status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	nav := body[strings.Index(body, `<nav class="tabs"`):]
	nav = nav[:strings.Index(nav, `</nav>`)]
	want := []string{
		`<a class="tab" href="/os/vayumail" aria-current="page">Overview</a>`,
		`<a class="tab" href="/os/vayumail/accounts">Accounts</a>`,
		`<a class="tab" href="/os/vayumail/dns">DNS</a>`,
		`<a class="tab" href="/os/vayumail/pgp">PGP keys</a>`,
		`<a class="tab" href="/os/vayumail/security">Security</a>`,
	}
	at := 0
	for _, w := range want {
		i := strings.Index(nav[at:], w)
		if i < 0 {
			t.Fatalf("tab %q missing or out of order in %s", w, nav)
		}
		at += i + len(w)
	}
	if strings.Count(nav, `aria-current`) != 1 {
		t.Errorf("want exactly one current tab, got %s", nav)
	}
}
