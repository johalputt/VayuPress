// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// A scoped API key must not be able to mint or promote an identity that can sign
// in to the console.
//
// The escalation this closes, end to end:
//
//  1. api_capabilities.go maps the prefix /os/vayumail to SectionMail, so a key
//     holding only mail:write passes keyMayCall for accounts/create.
//  2. isAdminRequest returns auth.HasValidAPIKey(r) when there is no session
//     user — ANY valid key, not a superuser key — so the handler's admin gate is
//     satisfied.
//  3. The key creates a mailbox with role "administrator".
//  4. mailConsoleAccess maps that role to users.RoleAdmin with console=true.
//  5. Signing in at /os/login with that mailbox credential yields full console
//     administration.
//
// A scoped key promoting itself to install owner is precisely what the
// capability system exists to prevent. accounts/update was the same hole by
// another door: it accepts a Role field and can promote an existing mailbox.

// TestMailRoleGrantsConsoleIdentifiesEveryConsoleRole pins step 4. If a new mail
// role gains console access and is not reported here, both guards below open
// silently — so this is asserted against mailConsoleAccess itself rather than
// against a list restated here.
func TestMailRoleGrantsConsoleIdentifiesEveryConsoleRole(t *testing.T) {
	for _, role := range []string{vmail.RoleAdministrator, vmail.RoleEditor, vmail.RoleAuthor} {
		if !mailRoleGrantsConsole(role) {
			t.Errorf("mailRoleGrantsConsole(%q) = false, but mailConsoleAccess grants it console "+
				"access — a key could mint this role and then sign in with it", role)
		}
		cmsRole, console := mailConsoleAccess(role)
		if !console {
			t.Errorf("mailConsoleAccess(%q) says console=false; this test's premise is stale", role)
		}
		_ = cmsRole
	}
	// A plain mailbox is the role an unattended provisioning key SHOULD be able to
	// create, so the guard must not refuse it — otherwise the fix breaks the
	// legitimate use and gets reverted.
	for _, role := range []string{"mailbox", "reviewer", "", "   "} {
		if mailRoleGrantsConsole(role) {
			t.Errorf("mailRoleGrantsConsole(%q) = true; a non-console role must stay "+
				"provisionable by a scoped key or the guard blocks ordinary mailbox creation", role)
		}
	}
}

// TestConsoleMintingRefusesAPIKeysAtEveryEntryPoint asserts the refusal exists on
// BOTH paths that can produce a console-capable credential. Fixing one and
// missing the other leaves the escalation open with a green suite, which is how
// it survived review the first time.
func TestConsoleMintingRefusesAPIKeysAtEveryEntryPoint(t *testing.T) {
	src := readSourceFile(t, "vayuos_mail.go")

	for _, fn := range []string{
		"handleVayuOSAccountCreate",
		"handleVayuOSAccountUpdate",
	} {
		body := goFuncBody(src, fn)
		if body == "" {
			t.Fatalf("%s not found — this test cannot verify what it cannot read", fn)
		}
		if !strings.Contains(body, "mailRoleGrantsConsole") {
			t.Errorf("%s never consults mailRoleGrantsConsole, so a mail:write API key can "+
				"mint or promote a mailbox whose role grants console access and then sign in "+
				"with it — a scoped key becoming install owner", fn)
		}
		if !strings.Contains(body, "isAdminSession") {
			t.Errorf("%s does not require an administrator SESSION for a console-capable role. "+
				"isAdminRequest is satisfied by any valid API key, so it cannot gate this", fn)
		}
	}
}

// TestIsAdminSessionRejectsKeyOnlyCallers pins the helper itself: it must derive
// authority from a session user and never from a key. Asserted at the source
// level because the alternative is standing up the whole App, and the property
// is a one-line invariant that a future edit could quietly widen back to
// isAdminRequest's behaviour.
func TestIsAdminSessionRejectsKeyOnlyCallers(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "handlers_auth.go"), "isAdminSession")
	if body == "" {
		t.Fatal("isAdminSession not found")
	}
	if strings.Contains(body, "HasValidAPIKey") {
		t.Error("isAdminSession consults HasValidAPIKey — that is isAdminRequest's behaviour, " +
			"and accepting a key here reopens the escalation this helper exists to close")
	}
	if !strings.Contains(body, "currentUser") {
		t.Error("isAdminSession does not resolve a session user, so it cannot be asserting " +
			"that the caller is a human administrator")
	}
}

// goFuncBody returns the source of a Go function, ending at the next top-level
// declaration.
//
// It terminates on a column-zero "func " rather than on a closing brace: these
// handlers contain struct literals and closures whose braces would truncate the
// body early, and a body silently cut short makes a test fail on code that is
// correct — worth no more than one that passes on code that is not.
func goFuncBody(src, name string) string {
	i := strings.Index(src, ") "+name+"(")
	if i < 0 {
		if i = strings.Index(src, "\nfunc "+name+"("); i < 0 {
			return ""
		}
	}
	// Rewind to the start of the declaration line so the signature is included.
	if j := strings.LastIndex(src[:i], "\nfunc "); j >= 0 {
		i = j + 1
	}
	rest := src[i:]
	// Search from after this declaration's own line; (?m)^ would otherwise match
	// the declaration itself and return a one-line body.
	head := strings.Index(rest, "\n")
	if head < 0 {
		return rest
	}
	if k := strings.Index(rest[head:], "\nfunc "); k >= 0 {
		return rest[:head+k]
	}
	return rest
}
