// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	htmpl "html/template"
	"strings"
	"testing"
)

// TestOSNotifBell covers the topbar notification centre that replaced the New
// Post shortcut: the empty "caught up" state carries no badge, and a populated
// bell shows the summed count plus one clickable row per item linking straight to
// the page that clears it. Both states must be CSP-clean (no inline styles).
func TestOSNotifBell(t *testing.T) {
	// Empty → caught-up state, a toggle, and no count badge.
	empty := osNotifBell(&osSettings{})
	assertCSPSafe(t, "osNotifBell/empty", empty)
	if !strings.Contains(empty, "data-notif-toggle") || !strings.Contains(empty, "data-notif-panel") {
		t.Error("empty bell must still render the toggle + panel")
	}
	if !strings.Contains(empty, "Nothing needs you, and nothing has happened in the last day.") {
		t.Error("empty bell must show the caught-up state")
	}
	if strings.Contains(empty, "topbar-notif__badge") {
		t.Error("empty bell must not render a count badge")
	}

	// Populated → summed badge (2+3=5) + one linked row per item.
	s := &osSettings{AccessLevel: accessAdmin, Notifications: []osNotification{
		{Title: "New messages", Detail: "unread in your inbox", Href: "/os/messages", Count: 2, Kind: "message"},
		{Title: "Comments to review", Detail: "awaiting moderation", Href: "/os/comments", Count: 3, Kind: "comment"},
	}}
	out := osNotifBell(s)
	assertCSPSafe(t, "osNotifBell/items", out)
	for _, want := range []string{
		`href="/os/messages"`, `href="/os/comments"`,
		"New messages", "Comments to review",
		`class="topbar-notif__badge" data-notif-badge>5<`, // 2 + 3
		"topbar-notif__btn--active",                       // pulse ring while unread
	} {
		if !strings.Contains(out, want) {
			t.Errorf("populated bell missing %q", want)
		}
	}

	// A hostile title cannot break out — the row escapes it.
	hostile := osNotifBell(&osSettings{AccessLevel: accessAdmin, Notifications: []osNotification{
		{Title: `"><script>x`, Detail: "d", Href: "/os/messages", Count: 1, Kind: "message"},
	}})
	if strings.Contains(hostile, `"><script>`) {
		t.Errorf("notification title broke out of the markup:\n%s", hostile)
	}

	// Storage's count is a percentage: a disk at 80% is one thing to look at,
	// not eighty, and its row says the percentage.
	disk := osNotifBell(&osSettings{AccessLevel: accessAdmin, Notifications: []osNotification{
		{Title: "Comments to review", Detail: "awaiting moderation", Href: "/os/comments", Count: 2, Kind: "comment"},
		{Title: "Storage filling up", Detail: "of your storage quota is in use", Href: "/os/storage", Count: 80, Kind: "storage", Severity: "warn"},
	}})
	if !strings.Contains(disk, `topbar-notif__badge" data-notif-badge>3<`) {
		t.Error("a storage notice must add one to the badge, not its percentage")
	}
	if !strings.Contains(disk, "80% of your storage quota is in use") {
		t.Error("the storage row must read as a percentage")
	}

	// Counts clamp to 99+ so a big backlog never blows out the badge.
	big := osNotifBell(&osSettings{AccessLevel: accessAdmin, Notifications: []osNotification{
		{Title: "x", Detail: "d", Href: "/os/messages", Count: 250, Kind: "message"},
	}})
	if !strings.Contains(big, `topbar-notif__badge" data-notif-badge>99+<`) {
		t.Error("badge must clamp large totals to 99+")
	}
}

// TestTopbarNotificationCentre verifies the admin chrome now hosts the
// notification centre in place of the topbar New Post button.
func TestTopbarNotificationCentre(t *testing.T) {
	out := adminOSLayout("N", "Dashboard", "dashboard", &osSettings{SiteName: "Demo"}, htmpl.HTML("<p>x</p>"))
	assertCSPSafe(t, "topbar/notif", out)
	// The bell is named by its own content — "Notifications" and the count
	// beside it — so the name a screen reader hears always carries the badge
	// a sighted operator sees (an aria-label left the count out).
	if !strings.Contains(out, `data-notif`) || !strings.Contains(out, `<span class="vp-sr-only">Notifications</span>`) {
		t.Error("topbar must host the notification centre, named Notifications")
	}
	if strings.Contains(out, `data-notif-toggle aria-haspopup="true" aria-expanded="false" aria-label=`) {
		t.Error("the bell's aria-label hides its count from its name")
	}
	// The old topbar New post button must be gone. Matched without regard to
	// case, so relabelling it cannot make this pass while the button stays.
	if strings.Contains(strings.ToLower(out), `btn btn--primary btn--sm" href="/os/editor">new post`) {
		t.Error("topbar New Post button must be replaced by the notification centre")
	}
}

// An install with no backups says so on Home and the bell, to the administrator
// only: the page it opens is administrator-only.
func TestUnprovenBackupsReachTheBell(t *testing.T) {
	find := func(level int) *osNotification {
		for _, n := range (&App{}).osNotifications(context.Background(), &osSettings{AccessLevel: level}) {
			if n.Kind == "backup" {
				return &n
			}
		}
		return nil
	}
	if n := find(accessAdmin); n == nil || n.Detail != "Not set up" {
		t.Errorf("an administrator of an install with no backups got %+v, want a Not set up notice", n)
	} else if got := n.line(); got != "Not set up" {
		t.Errorf("a state reads %q; a count in front of a statement is noise", got)
	}
	if n := find(accessEditor); n != nil {
		t.Errorf("an editor was sent to the administrator-only backups page: %+v", n)
	}
}
