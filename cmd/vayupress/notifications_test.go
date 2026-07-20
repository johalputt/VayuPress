package main

import (
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
	if !strings.Contains(empty, "caught up") {
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
		`class="topbar-notif__badge">5<`, // 2 + 3
		"topbar-notif__btn--active",      // pulse ring while unread
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

	// Counts clamp to 99+ so a big backlog never blows out the badge.
	big := osNotifBell(&osSettings{AccessLevel: accessAdmin, Notifications: []osNotification{
		{Title: "x", Detail: "d", Href: "/os/messages", Count: 250, Kind: "message"},
	}})
	if !strings.Contains(big, `topbar-notif__badge">99+<`) {
		t.Error("badge must clamp large totals to 99+")
	}
}

// TestTopbarNotificationCentre verifies the admin chrome now hosts the
// notification centre in place of the topbar New Post button.
func TestTopbarNotificationCentre(t *testing.T) {
	out := adminOSLayout("N", "Dashboard", "dashboard", &osSettings{SiteName: "Demo"}, htmpl.HTML("<p>x</p>"))
	assertCSPSafe(t, "topbar/notif", out)
	if !strings.Contains(out, `data-notif`) || !strings.Contains(out, "aria-label=\"Notifications\"") {
		t.Error("topbar must host the notification centre")
	}
	// The old topbar New Post button must be gone (its exact markup).
	if strings.Contains(out, `btn btn--primary btn--sm" href="/os/editor">New Post`) {
		t.Error("topbar New Post button must be replaced by the notification centre")
	}
}
