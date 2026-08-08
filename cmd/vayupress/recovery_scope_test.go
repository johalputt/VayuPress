// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
)

// SECTION 4 AUDIT — pinning a property that was found CLEAN, because the way it
// is clean is easy to mistake for a bug.
//
// recoveryScope narrows a non-admin to their own mailbox. It does so SILENTLY:
// a caller asking about someone else's address is answered about their own,
// with ok=true, rather than refused. Read quickly that looks like a missing
// check, and the obvious "fix" — return ("", false) on a mismatch — is a
// regression, not a correction.
//
// It is a regression because the endpoints behind it (recovery status, code
// issue, contact) would then answer differently for an address that exists and
// one that does not: "that is not your mailbox" versus "no mailbox". That is an
// enumeration oracle over every mailbox on the install, handed to any mailbox
// holder or agency client, on an endpoint their own console polls.
//
// Nothing here reports a defect. It exists so the next person to look at that
// function finds a failing test instead of a plausible idea.

// scopeReq builds a request carrying a signed-in non-admin who holds one mailbox.
func scopeReq(t *testing.T, a *App, role, mailbox string) *http.Request {
	t.Helper()
	return withUser(httptest.NewRequest(http.MethodGet, "/os/api/vayuos/mail/recovery/status", nil),
		&users.User{ID: "u-holder", Email: mailbox, Role: role, MailAddress: mailbox})
}

func TestRecoveryScopeNarrowsSilentlyRatherThanRefusing(t *testing.T) {
	a := resetSessionApp(t)
	const own = "boss@example.com"

	for _, requested := range []string{
		"",                               // "mine"
		own,                              // mine, said out loud
		"ANOTHER-CLIENT@EXAMPLE.COM",     // someone else's, and a real mailbox
		"nobody-at-all@example.com",      // an address that does not exist
		"  another-client@example.com  ", // padded, to defeat a trim-only comparison
	} {
		got, ok := a.recoveryScope(scopeReq(t, a, users.RoleClient, own), requested)
		if !ok {
			t.Errorf("recoveryScope refused a holder who asked about %q.\n\n"+
				"A refusal here distinguishes an address that exists from one that does "+
				"not, which turns recovery status into a mailbox enumeration oracle for "+
				"every client on the install. The answer is their own mailbox, quietly.",
				requested)
			continue
		}
		if got != own {
			t.Errorf("recoveryScope(%q) = %q for a holder of %s — the requested address "+
				"is being honoured for a non-admin", requested, got, own)
		}
	}

	// A session with no mailbox has nothing to narrow TO, and that is the one
	// case that must refuse — returning "" with ok=true would scope a query to
	// the empty address.
	req := withUser(httptest.NewRequest(http.MethodGet, "/", nil),
		&users.User{ID: "u-none", Email: "nomail@example.com", Role: users.RoleClient})
	if got, ok := a.recoveryScope(req, "another-client@example.com"); ok {
		t.Errorf("recoveryScope gave a mailbox-less session the scope %q; there is nothing "+
			"to narrow to and an empty scope is not a safe default", got)
	}
}

// The admin half, so the narrowing above cannot be "fixed" by making it
// unconditional: an operator asking about a mailbox must get that mailbox, or
// recovery support stops working for the people who run the install.
func TestRecoveryScopeStillLetsAnAdministratorNameAMailbox(t *testing.T) {
	a := resetSessionApp(t)
	req := withUser(httptest.NewRequest(http.MethodGet, "/", nil),
		&users.User{ID: "u-admin", Email: "root@example.com", Role: users.RoleAdmin})

	got, ok := a.recoveryScope(req, "  ANOTHER-CLIENT@example.com ")
	if !ok || got != "another-client@example.com" {
		t.Fatalf("recoveryScope for an admin = (%q, %v), want the normalised address they "+
			"asked for — an operator who cannot name a mailbox cannot run a recovery",
			got, ok)
	}
	// An admin naming nothing is the install-wide view, which the handler gates
	// separately; the scope itself is empty rather than the admin's own mailbox.
	if got, ok := a.recoveryScope(req, ""); !ok || got != "" {
		t.Errorf("recoveryScope for an admin naming nothing = (%q, %v), want (\"\", true) — "+
			"substituting their own mailbox would silently change which view they get",
			got, ok)
	}
}
