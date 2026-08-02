// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// TestMailConsoleAccessStrict locks in that only the explicit console roles
// (administrator/editor/author) grant VayuOS console access; mailbox, reviewer,
// an empty role, and any custom role are confined to the VayuMail surface
// (mailOnly). This prevents a mail-only account from seeing other tabs.
func TestMailConsoleAccessStrict(t *testing.T) {
	cases := []struct {
		mailRole    string
		wantConsole bool
	}{
		{vmail.RoleAdministrator, true},
		{vmail.RoleEditor, true},
		{vmail.RoleAuthor, true},
		{vmail.RoleReviewer, false},
		{vmail.RoleMailbox, false},
		{"", false},
		{"automation", false}, // custom role
	}
	for _, c := range cases {
		if _, console := mailConsoleAccess(c.mailRole); console != c.wantConsole {
			t.Errorf("mailConsoleAccess(%q) console=%v want %v", c.mailRole, console, c.wantConsole)
		}
	}
}

func TestAccessLevelFor(t *testing.T) {
	cases := []struct {
		role     string
		mailOnly bool
		want     int
	}{
		{users.RoleAdmin, false, accessAdmin},
		{users.RoleEditor, false, accessEditor},
		{users.RoleAuthor, false, accessAuthor},
		{"", false, accessAuthor},               // unknown role → author (least privilege)
		{users.RoleAdmin, true, accessMailOnly}, // mail-only overrides role
		{users.RoleAuthor, true, accessMailOnly},
	}
	for _, c := range cases {
		if got := accessLevelFor(c.role, c.mailOnly); got != c.want {
			t.Errorf("accessLevelFor(%q, %v) = %d, want %d", c.role, c.mailOnly, got, c.want)
		}
	}
}

func TestOSPathMinLevel(t *testing.T) {
	cases := map[string]int{
		// Author-level content (also covers the API action paths).
		"/os":                  accessAuthor,
		"/os/posts":            accessAuthor,
		"/os/editor":           accessAuthor,
		"/os/editor/my-post":   accessAuthor,
		"/os/media":            accessAuthor,
		"/os/api/media/upload": accessAuthor,
		"/os/api/editor/save":  accessAuthor,
		"/os/api/posts/status": accessAuthor,
		"/os/profile":          accessAuthor,
		"/os/vayumail/inbox":   accessAuthor,
		// Editor-level.
		"/os/comments":    accessEditor,
		"/os/pages":       accessEditor,
		"/os/messages":    accessEditor,
		"/os/seo":         accessEditor,
		"/os/theme/store": accessEditor,
		// Admin-level.
		"/os/settings":         accessAdmin,
		"/os/api/update/apply": accessAdmin,
		"/os/members":          accessAdmin,
		"/os/newsletter":       accessAdmin,
		"/os/security":         accessAdmin,
		"/os/adr":              accessAdmin,
		// Money, fulfilment & operator controls — the /os/api twins that were
		// silently author-accessible (fail-open) before the fail-closed default.
		"/os/api/credentials/reveal":      accessAdmin,
		"/os/api/payments/stripe/connect": accessAdmin,
		"/os/api/orders":                  accessAdmin,
		"/os/api/orders/o1/paid":          accessAdmin,
		"/os/api/mailids/m1/activate":     accessAdmin,
		"/os/api/domains/d1/sync":         accessAdmin,
		"/os/api/backup/export":           accessAdmin,
		"/os/api/power/restart":           accessAdmin,
		"/os/api/users":                   accessAdmin,
		"/os/api/mode":                    accessAdmin,
		"/os/api/budgets":                 accessAdmin,
		"/os/api/torworld/sites":          accessAdmin,
		"/os/api/branding/favicon":        accessAdmin,
		// Author-safe API areas must stay reachable at author level (self-service
		// 2FA, chat, dashboard activity, health, content authoring).
		"/os/api/totp/begin":      accessAuthor,
		"/os/api/talk/send":       accessAuthor,
		"/os/api/activity":        accessAuthor,
		"/os/api/vayuos/health":   accessAuthor,
		"/os/api/search/reindex":  accessAuthor,
		"/os/api/feed/regenerate": accessAuthor,
		"/os/api/embed/unfurl":    accessAuthor,
		"/os/api/diagram/render":  accessAuthor,
		// Infrastructure controls (ADR-0141): VayuTor + the Anonymous Tor Space
		// toggle each supervise network-facing services / a second server process,
		// so page AND action paths must be admin-only — never author/editor.
		"/os/tor":           accessAdmin,
		"/os/tor/toggle":    accessAdmin,
		"/os/spaces":        accessAdmin,
		"/os/spaces/toggle": accessAdmin,
	}
	for path, want := range cases {
		if got := osPathMinLevel(path); got != want {
			t.Errorf("osPathMinLevel(%q) = %d, want %d", path, got, want)
		}
	}
}

// TestOSPathMinLevelFailClosed is the anti-regression guard for the systemic
// fail-open authorization defect (audit C1/H1/H2/M1): any /os/api/* path that is
// not an explicitly enumerated author- or editor-safe area MUST require admin, so
// a future sensitive endpoint added without its own guard cannot silently inherit
// author access. If this test fails because a new author/editor API area was
// added, add that area to authorAPIAreas/editorAreas in osPathMinLevel — never
// weaken this default.
func TestOSPathMinLevelFailClosed(t *testing.T) {
	unknown := []string{
		"/os/api/secret-future-endpoint",
		"/os/api/billing/charge",
		"/os/api/keys/exfiltrate",
		"/os/api/system/exec",
	}
	for _, p := range unknown {
		if got := osPathMinLevel(p); got != accessAdmin {
			t.Errorf("osPathMinLevel(%q) = %d, want accessAdmin (%d) — unenumerated /os/api/* must fail closed to admin", p, got, accessAdmin)
		}
	}
	// A non-API /os page that is unenumerated stays at the permissive author
	// default (navigational; sensitive pages are admin-gated by name).
	if got := osPathMinLevel("/os/some-dashboard-widget"); got != accessAuthor {
		t.Errorf("osPathMinLevel(non-api page) = %d, want accessAuthor (%d)", got, accessAuthor)
	}
}

func TestMailOnlyPathAllowed(t *testing.T) {
	allowed := []string{
		"/os/vayumail/inbox", "/os/vayumail/message", "/os/profile",
		"/os/logout", "/os/static/css/admin-os.css",
		// The ADR-0144 recovery endpoints a confined mailbox genuinely needs.
		"/os/api/vayuos/mail/recovery/status", "/os/api/vayuos/mail/recovery/codes",
	}
	for _, p := range allowed {
		if !mailOnlyPathAllowed(p) {
			t.Errorf("mailOnlyPathAllowed(%q) = false, want true", p)
		}
	}
	denied := []string{
		"/os", "/os/posts", "/os/settings", "/os/members", "/os/editor", "/os/comments",
		// Operator infrastructure state, previously admitted by a blanket
		// /os/api/vayuos prefix. A mail-confined principal is a reader who claimed
		// a mailbox, not staff: the health snapshot names every component and its
		// detail string, and the security page is admin-only in the VayuMail nav.
		"/os/api/vayuos/health", "/os/api/vayuos/security/check",
	}
	for _, p := range denied {
		if mailOnlyPathAllowed(p) {
			t.Errorf("mailOnlyPathAllowed(%q) = true, want false (mail-only must be confined)", p)
		}
	}
}

// TestRoleReachabilityMatrix asserts the core promise: each role can reach only
// its tier and below, so "what a role sees" equals "what it can use".
func TestRoleReachabilityMatrix(t *testing.T) {
	can := func(level int, path string) bool { return level >= osPathMinLevel(path) }

	// mailbox/reviewer (mail-only) — confined to the mail surface.
	if can(accessMailOnly, "/os/posts") || can(accessMailOnly, "/os/settings") {
		t.Error("mail-only level must not satisfy console paths")
	}
	// author — content yes, editor/admin no.
	if !can(accessAuthor, "/os/posts") || !can(accessAuthor, "/os/editor") {
		t.Error("author must reach content paths")
	}
	if can(accessAuthor, "/os/comments") || can(accessAuthor, "/os/settings") {
		t.Error("author must NOT reach editor/admin paths")
	}
	// editor — content + editor yes, admin no.
	if !can(accessEditor, "/os/comments") || !can(accessEditor, "/os/posts") {
		t.Error("editor must reach editor + content paths")
	}
	if can(accessEditor, "/os/settings") || can(accessEditor, "/os/members") {
		t.Error("editor must NOT reach admin paths")
	}
	// admin — everything.
	for _, p := range []string{"/os/posts", "/os/comments", "/os/settings", "/os/members"} {
		if !can(accessAdmin, p) {
			t.Errorf("admin must reach %q", p)
		}
	}
}
