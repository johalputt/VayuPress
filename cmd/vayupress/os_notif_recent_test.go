// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/mode"
)

// The bell (render 09): what needs the operator, then what happened. The
// badge counts the first in full and the second until it is marked read.

func bellFixture(seen time.Time) *osSettings {
	now := time.Date(2026, 9, 25, 14, 30, 0, 0, time.UTC)
	return &osSettings{
		Notifications: []osNotification{
			{Title: "Comments to review", Detail: "awaiting moderation", Href: "/os/comments", Count: 3, Kind: "comment"},
		},
		Recent: []osRecentEvent{
			{Title: "Message from Priya", Href: "/os/messages", Kind: "message", At: now.Add(-10 * time.Minute)},
			{Title: "Published “Hello”", Href: "/os/editor/hello", Kind: "post", At: now.Add(-5 * time.Hour)},
		},
		RecentSeen: seen,
	}
}

func TestTheBellPutsWhatNeedsYouAboveWhatHappened(t *testing.T) {
	out := osNotifBell(bellFixture(time.Time{}))
	needs, recent := strings.Index(out, ">Needs action<"), strings.Index(out, ">Last 24 hours<")
	if needs < 0 || recent < 0 || needs > recent {
		t.Fatalf("the two groups are missing or out of order (needs at %d, recent at %d):\n%s", needs, recent, out)
	}
	if !strings.Contains(out[needs:recent], "Comments to review") || !strings.Contains(out[recent:], "Message from Priya") {
		t.Errorf("an item sits in the wrong group:\n%s", out)
	}
	// An event says when; a condition does not pretend to.
	if !strings.Contains(out[recent:], `<time class="notif-item__time" datetime="2026-09-25T14:20:00Z">14:20 UTC</time>`) {
		t.Errorf("an event does not carry its time:\n%s", out[recent:])
	}
	if strings.Contains(out[needs:recent], "<time") {
		t.Error("a condition is given a time it does not have")
	}
}

func TestTheBadgeCountsEventsOnlyUntilTheyAreRead(t *testing.T) {
	badge := regexp.MustCompile(`data-notif-badge>([^<]+)<`)
	for _, tc := range []struct {
		name     string
		seen     time.Time
		badge    string
		markAll  bool
		unreadAt int
	}{
		{"never read", time.Time{}, "5", true, 2},
		{"read an hour ago", time.Date(2026, 9, 25, 13, 30, 0, 0, time.UTC), "4", true, 1},
		{"read just now", time.Date(2026, 9, 25, 14, 30, 0, 0, time.UTC), "3", false, 0},
	} {
		out := osNotifBell(bellFixture(tc.seen))
		m := badge.FindStringSubmatch(out)
		if m == nil || m[1] != tc.badge {
			t.Errorf("%s: badge %v, want %s (three to review, plus the unread events)", tc.name, m, tc.badge)
		}
		if got := strings.Contains(out, "data-notif-seen"); got != tc.markAll {
			t.Errorf("%s: Mark all read offered = %v, want %v", tc.name, got, tc.markAll)
		}
		if got := strings.Count(out, "is-unread"); got != tc.unreadAt {
			t.Errorf("%s: %d events marked unread, want %d", tc.name, got, tc.unreadAt)
		}
		// The page needs the needs-action total to recount after Mark all read.
		if !strings.Contains(out, `data-notif-needs="3"`) {
			t.Errorf("%s: the bell does not carry its needs-action total", tc.name)
		}
	}
}

// The events come from rows that recorded them, inside the window, and each
// is shown only to a viewer who could open the page it links to.
func TestRecentEventsAreRealWindowedAndGated(t *testing.T) {
	openMigratedDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO contact_messages(id,name,email,message,created_at) VALUES('m1','Priya','p@example.net','hi',?)`, []any{now.Add(-time.Hour)}},
		{`INSERT INTO contact_messages(id,name,email,message,created_at) VALUES('m2','Old','o@example.net','hi',?)`, []any{now.Add(-30 * time.Hour)}},
		{`INSERT INTO members(id,email,created_at) VALUES('u1','new@example.net',?)`, []any{now.Add(-2 * time.Hour)}},
		{`INSERT INTO comments(id,article_id,author,body,status,created_at) VALUES('c1','a1','Mehul','hello','pending',?)`, []any{now.Add(-3 * time.Hour)}},
	} {
		if _, err := dbpkg.DB.ExecContext(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	a := &App{}
	titles := func(level int) string {
		var got []string
		for _, e := range a.osRecentEvents(ctx, &osSettings{AccessLevel: level}, now) {
			got = append(got, e.Title+"|"+e.Detail)
		}
		return strings.Join(got, "\n")
	}
	admin := titles(accessAdmin)
	for _, want := range []string{"Message from Priya|", "new@example.net joined|", "Comment from Mehul|Held for review"} {
		if !strings.Contains(admin, want) {
			t.Errorf("an administrator's bell lacks %q:\n%s", want, admin)
		}
	}
	if strings.Contains(admin, "Message from Old") {
		t.Error("an event from 30 hours ago is in the last day's list")
	}
	if strings.Index(admin, "Message from Priya") > strings.Index(admin, "Comment from Mehul") {
		t.Errorf("the events are not newest first:\n%s", admin)
	}
	if author := titles(accessAuthor); strings.Contains(author, "joined") {
		t.Errorf("an author is shown a member sign-up, a page they cannot open:\n%s", author)
	}
}

// The mode journal is not in the database, so it applies the window itself.
func TestAModeChangeLeavesTheBellAfterADay(t *testing.T) {
	now := time.Now().UTC()
	mode.Global.Reset()
	t.Cleanup(mode.Global.Reset)
	mode.Global.Restore([]mode.Transition{
		{From: mode.ModeNormal, To: mode.ModeDegraded, Reason: "old", OccurredAt: now.Add(-30 * time.Hour)},
		{From: mode.ModeDegraded, To: mode.ModeNormal, Reason: "recovered", OccurredAt: now.Add(-time.Hour)},
	})
	var got []string
	for _, e := range (&App{}).osRecentEvents(context.Background(), &osSettings{AccessLevel: accessAdmin}, now) {
		got = append(got, e.Detail)
	}
	if strings.Join(got, ",") != "recovered" {
		t.Errorf("mode events in the last day: %v, want only the recovery an hour ago", got)
	}
}

func TestMarkAllReadIsRememberedPerViewer(t *testing.T) {
	openMigratedDB(t)
	a := &App{}
	before := time.Now().UTC().Add(-time.Second)
	rec := httptest.NewRecorder()
	a.handleOSNotificationsSeen(rec, httptest.NewRequest(http.MethodPost, "/os/api/notifications/seen", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("mark all read: %d %s", rec.Code, rec.Body.String())
	}
	if got := notifSeenAt(context.Background(), ""); got.Before(before) {
		t.Errorf("the read mark was not stored: %v", got)
	}
	if got := notifSeenAt(context.Background(), "someone-else"); !got.IsZero() {
		t.Errorf("another viewer inherits the read mark: %v", got)
	}
}

// Clear all empties the bell and it stays empty across a reload: the events
// leave the list and each condition on show is hidden. A condition comes back
// when it changes, or after a day; an event after the clear is shown.
func TestClearAllEmptiesTheBellUntilSomethingChanges(t *testing.T) {
	openMigratedDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	backups := osNotification{Title: "Backups unproven", Detail: "No verified backup yet", Href: "/os/vayukeep", Kind: "backup", Count: 1, State: true}
	comments := osNotification{Title: "Comments to review", Href: "/os/comments", Kind: "comment", Count: 3}
	older := osRecentEvent{Title: "Message from Priya", Href: "/os/messages", Kind: "message", At: now.Add(-time.Hour)}
	// The usual case: a viewer who has used Mark all read before.
	if _, err := dbpkg.DB.Exec(`INSERT INTO notification_seen(user_id, seen_at) VALUES('op', ?)`, now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := clearNotifications(ctx, "op", []osNotification{backups, comments}, now); err != nil {
		t.Fatal(err)
	}
	bell := func(user string, at time.Time, notifs []osNotification, recent ...osRecentEvent) string {
		return osNotifBell(&osSettings{AccessLevel: accessAdmin, Notifications: notifs, Recent: recent,
			RecentSeen: notifSeenAt(ctx, user), RecentCleared: notifClearedAt(ctx, user), NotifDismissed: notifDismissed(ctx, user, at)})
	}

	out := bell("op", now, []osNotification{backups, comments}, older)
	for _, gone := range []string{"Backups unproven", "Comments to review", "Message from Priya", "data-notif-badge", "data-notif-clear"} {
		if strings.Contains(out, gone) {
			t.Errorf("after Clear all the bell still shows %q:\n%s", gone, out)
		}
	}
	if !strings.Contains(out, "All cleared") {
		t.Errorf("an empty bell after Clear all does not say what comes back:\n%s", out)
	}

	more := comments
	more.Count = 4
	if out := bell("op", now, []osNotification{backups, more}); !strings.Contains(out, "Comments to review") || !strings.Contains(out, "data-notif-badge") {
		t.Errorf("a cleared condition that changed did not come back on the badge:\n%s", out)
	}
	if out := bell("op", now.Add(notifDismissFor+time.Minute), []osNotification{backups}); !strings.Contains(out, "Backups unproven") {
		t.Errorf("a cleared condition still unfixed a day later did not come back:\n%s", out)
	}
	newer := osRecentEvent{Title: "Comment from Mehul", Href: "/os/comments", Kind: "comment", At: now.Add(time.Minute)}
	if out := bell("op", now, nil, older, newer); !strings.Contains(out, "Comment from Mehul") || strings.Contains(out, "Message from Priya") {
		t.Errorf("after Clear all, want only the event that came later:\n%s", out)
	}
	if out := bell("someone-else", now, []osNotification{backups}, older); !strings.Contains(out, "Backups unproven") || !strings.Contains(out, "Message from Priya") {
		t.Errorf("another viewer's bell was cleared too:\n%s", out)
	}
}
