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
		"/os":                   accessAuthor,
		"/os/posts":             accessAuthor,
		"/os/editor":            accessAuthor,
		"/os/editor/my-post":    accessAuthor,
		"/os/media":             accessAuthor,
		"/os/api/media/upload":  accessAuthor,
		"/os/api/editor/save":   accessAuthor,
		"/os/api/posts/status":  accessAuthor,
		"/os/profile":           accessAuthor,
		"/os/vayuos/mail/inbox": accessAuthor,
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
	}
	for path, want := range cases {
		if got := osPathMinLevel(path); got != want {
			t.Errorf("osPathMinLevel(%q) = %d, want %d", path, got, want)
		}
	}
}

func TestMailOnlyPathAllowed(t *testing.T) {
	allowed := []string{
		"/os/vayuos/mail/inbox", "/os/vayuos/mail/message", "/os/profile",
		"/os/logout", "/os/static/css/admin-os.css", "/os/api/vayuos/health",
	}
	for _, p := range allowed {
		if !mailOnlyPathAllowed(p) {
			t.Errorf("mailOnlyPathAllowed(%q) = false, want true", p)
		}
	}
	denied := []string{"/os", "/os/posts", "/os/settings", "/os/members", "/os/editor", "/os/comments"}
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
