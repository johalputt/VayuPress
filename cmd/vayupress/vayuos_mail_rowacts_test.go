// SPDX-License-Identifier: Apache-2.0

package main

import (
	"html"
	"strings"
	"testing"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// A row offers what can be done to its message where it is: Archive but not
// in Archive, Delete (to Trash) but not in Trash, Snooze but not in Snoozed,
// and Mark read or Mark unread by what it is; each posts that one message.
func TestARowOffersWhatCanBeDoneWhereItIs(t *testing.T) {
	unread := vmail.StoredMessage{ID: "m-1"}
	read := vmail.StoredMessage{ID: "m-2", Seen: true}
	for _, c := range []struct {
		folder string
		m      vmail.StoredMessage
		has    []string
		hasNot []string
	}{
		{"Inbox", unread, []string{"Archive", "Delete", "Mark read", "Snooze until tomorrow"}, []string{"Mark unread"}},
		{"Inbox", read, []string{"Mark unread"}, []string{"Mark read\""}},
		{"Archive", read, []string{"Delete"}, []string{`aria-label="Archive"`}},
		{"Trash", read, []string{"Archive"}, []string{`aria-label="Delete"`}},
		{"Snoozed", read, []string{"Archive"}, []string{"Snooze"}},
	} {
		got := mailRowActs(c.m, c.folder)
		for _, want := range c.has {
			if !strings.Contains(got, want) {
				t.Errorf("%s, %s: no %q in %s", c.folder, c.m.ID, want, got)
			}
		}
		for _, not := range c.hasNot {
			if strings.Contains(got, not) {
				t.Errorf("%s, %s: offers %q", c.folder, c.m.ID, not)
			}
		}
		if vals := html.UnescapeString(got); !strings.Contains(vals, `"id":"`+c.m.ID+`"`) || !strings.Contains(vals, `hx-post="/os/vayumail/inbox/action"`) {
			t.Errorf("%s: the actions do not post %s through the list's action: %s", c.folder, c.m.ID, vals)
		}
	}
}
