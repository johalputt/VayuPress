// SPDX-License-Identifier: Apache-2.0

package oauth

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// SECTION 3 — bounds on what an unauthenticated registration can store and put
// in front of an operator.
//
// Dynamic client registration is open by design (RFC 7591) and rate-limited, so
// this is hygiene rather than a breach. What makes it worth doing is WHERE the
// client name ends up: on the consent screen, above the one sentence that lets
// an operator detect a malicious app —
//
//	Approving sends your authorization code to <destination>. This app
//	registered itself and its name is not verified by VayuPress.
//
// The name is escaped, so this is not injection. It is layout: the field an
// attacker controls is rendered before the warning, and the request body allows
// 32 KiB of it. A name that long pushes the destination — the only thing on the
// page that distinguishes a real connector from an impostor — off the screen
// entirely.
//
// Truncation rather than rejection, because a refused registration surfaces to
// the operator as "couldn't register with <site>'s sign-in service" and this is
// a cosmetic field. The same reasoning covers the redirect list: a client with
// more than a handful of callbacks is not a real one, and the stored row should
// not grow with what a stranger sends.

func boundsStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS oauth_clients(
		client_id TEXT PRIMARY KEY, client_name TEXT NOT NULL DEFAULT '',
		redirect_uris TEXT NOT NULL DEFAULT '[]',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return New(db)
}

func TestAClientNameCannotPushTheConsentWarningOffTheScreen(t *testing.T) {
	s := boundsStore(t)
	huge := strings.Repeat("A", 30000)

	c, err := s.RegisterClient(context.Background(), huge, []string{"https://ok.example/cb"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if len(c.Name) > maxClientNameLen {
		t.Errorf("a registration stored a %d-character client name (cap %d).\n\n"+
			"It is rendered on the consent screen above the line naming the destination "+
			"— the only thing there that tells a real connector from an impostor. A name "+
			"this long puts that line off the page.", len(c.Name), maxClientNameLen)
	}
	// Re-read it: the bound must be on what is STORED, not on what this call returns.
	got, err := s.GetClient(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Name) > maxClientNameLen {
		t.Errorf("the stored client name is %d characters — the cap was applied to the "+
			"returned value only", len(got.Name))
	}
}

func TestTheRedirectListIsBounded(t *testing.T) {
	s := boundsStore(t)
	many := make([]string, 0, 500)
	for i := 0; i < 500; i++ {
		many = append(many, "https://ok.example/cb")
	}
	c, err := s.RegisterClient(context.Background(), "Many", many)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if len(c.RedirectURIs) > maxRedirectURIs {
		t.Errorf("a registration stored %d redirect URIs (cap %d) from one unauthenticated "+
			"request", len(c.RedirectURIs), maxRedirectURIs)
	}
}

// THE CONTROL. Ordinary registrations must be untouched — a bound that trims a
// real client's name or drops the callback it needs is a broken connect flow,
// which is exactly the failure this endpoint has already been through once.
func TestAnOrdinaryRegistrationIsUnchanged(t *testing.T) {
	s := boundsStore(t)
	const name = "Claude Code"
	uris := []string{"https://claude.ai/api/mcp/auth_callback", "http://localhost:0/callback"}

	c, err := s.RegisterClient(context.Background(), name, uris)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if c.Name != name {
		t.Errorf("client name = %q, want %q — a real app's name was altered", c.Name, name)
	}
	if len(c.RedirectURIs) != len(uris) {
		t.Fatalf("stored %d redirect URIs, want %d: %v", len(c.RedirectURIs), len(uris), c.RedirectURIs)
	}
	for i, want := range uris {
		if c.RedirectURIs[i] != want {
			t.Errorf("redirect %d = %q, want %q", i, c.RedirectURIs[i], want)
		}
	}
	// And the client still matches the callback it registered.
	if !c.RedirectAllowed("https://claude.ai/api/mcp/auth_callback") {
		t.Error("the registered redirect no longer matches; the connect flow is broken")
	}
}
