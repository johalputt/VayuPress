// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/vayukeep"
)

// A notification is said in the page's language, and Clear all remembers it
// by its English words: an item cleared while the console spoke English stays
// hidden when the bell is drawn in German, and the other way round.
func TestAClearedNotificationStaysClearedInAnotherLanguage(t *testing.T) {
	openMigratedDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	notice := func(lang string) osNotification {
		n, ok := backupNotification(lang, vayukeep.Status{}, "", now, nil)
		if !ok {
			t.Fatal("backups that are not set up raise no notice; the test proves nothing")
		}
		return n
	}
	en, de := notice("en"), notice("de")
	if de.Title != "Sicherungen" || de.line() != "Nicht eingerichtet" {
		t.Errorf("the German notice reads %q / %q", de.Title, de.line())
	}
	if en.line() != "Not set up" {
		t.Errorf("the English notice reads %q", en.line())
	}

	if err := clearNotifications(ctx, "op", []osNotification{en}, now); err != nil {
		t.Fatal(err)
	}
	bell := osNotifBell(&osSettings{Lang: "de", AccessLevel: accessAdmin, Notifications: []osNotification{de},
		NotifDismissed: notifDismissed(ctx, "op", now)})
	if strings.Contains(bell, "Nicht eingerichtet") || strings.Contains(bell, "data-notif-badge") {
		t.Errorf("a notice cleared in English came back in German:\n%s", bell)
	}
}

// What an event quotes from the install is drawn as a name, escaped, so no
// translator rewrites it and no name can write markup into the bell.
func TestAnEventsQuotedNameIsMarkedAndEscaped(t *testing.T) {
	got := recentTitleHTML(osRecentEvent{Title: `Nachricht von <b>Priya</b>`, Name: `<b>Priya</b>`})
	if want := `Nachricht von <span translate="no">&lt;b&gt;Priya&lt;/b&gt;</span>`; got != want {
		t.Errorf("an event's title draws as\n%s\nwant\n%s", got, want)
	}
}
