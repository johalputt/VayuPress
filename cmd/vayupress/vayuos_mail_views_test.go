// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
	"time"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// A follow-up is a person's message in the Inbox, read, not answered, older
// than two days and younger than thirty: one seed per condition.
func TestWhatIsAFollowUp(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	base := vmail.StoredMessage{Seen: true, Category: vmail.CategoryPrimary, MessageID: "a@x", Date: now.Add(-72 * time.Hour)}
	vc := mailViewCtx{inbox: true, now: now, answered: map[string]bool{"b@x": true}}
	if !mailInView(base, "followup", vc) {
		t.Fatal("a read, unanswered person's message of three days is not a follow-up")
	}
	for name, m := range map[string]func(vmail.StoredMessage, mailViewCtx) (vmail.StoredMessage, mailViewCtx){
		"answered": func(m vmail.StoredMessage, v mailViewCtx) (vmail.StoredMessage, mailViewCtx) {
			m.MessageID = "b@x"
			return m, v
		},
		"a day old": func(m vmail.StoredMessage, v mailViewCtx) (vmail.StoredMessage, mailViewCtx) {
			m.Date = now.Add(-24 * time.Hour)
			return m, v
		},
		"forty days old": func(m vmail.StoredMessage, v mailViewCtx) (vmail.StoredMessage, mailViewCtx) {
			m.Date = now.Add(-40 * 24 * time.Hour)
			return m, v
		},
		"unread": func(m vmail.StoredMessage, v mailViewCtx) (vmail.StoredMessage, mailViewCtx) {
			m.Seen = false
			return m, v
		},
		"an update": func(m vmail.StoredMessage, v mailViewCtx) (vmail.StoredMessage, mailViewCtx) {
			m.Category = vmail.CategoryUpdates
			return m, v
		},
		"in Archive": func(m vmail.StoredMessage, v mailViewCtx) (vmail.StoredMessage, mailViewCtx) {
			v.inbox = false
			return m, v
		},
		"no Message-ID": func(m vmail.StoredMessage, v mailViewCtx) (vmail.StoredMessage, mailViewCtx) {
			m.MessageID = ""
			return m, v
		},
	} {
		if mm, v := m(base, vc); mailInView(mm, "followup", v) {
			t.Errorf("%s: counted as a follow-up", name)
		}
	}
	if got := mailAnswered([]vmail.StoredMessage{{InReplyTo: "p@x"}, {References: []string{"q@x", "r@x"}}}); !got["p@x"] || !got["q@x"] || !got["r@x"] {
		t.Errorf("answered = %v", got)
	}
}

// The categories are tabs over the Inbox, each with its unread count, drawn
// only when the Inbox's mail falls into two of them; the sidebar never
// holds them, and offers a smart view only while it holds something.
func TestTheCategoryTabsAndSmartViews(t *testing.T) {
	now := time.Now()
	msgs := []vmail.StoredMessage{
		{Category: vmail.CategoryPrimary, From: "a@x.example", Date: now},
		{Category: vmail.CategoryUpdates, From: "ci@x.example", Date: now},
		{Category: vmail.CategoryUpdates, From: "ci@x.example", Seen: true, Date: now},
		{Category: vmail.CategoryNewsletters, From: "News <n@paper.example>", List: true, Date: now},
		{Category: vmail.CategoryNewsletters, From: "News <n@paper.example>", List: true, Date: now.Add(-time.Hour)},
	}
	counts := mailViewCounts(msgs, mailViewCtx{inbox: true, now: now})
	if counts[vmail.CategoryUpdates] != 1 || counts["all:"+vmail.CategoryUpdates] != 2 || counts["lists"] != 2 {
		t.Fatalf("counts = %v", counts)
	}
	tabs := mailCategoryTabs("en", "dana", "Inbox", vmail.CategoryUpdates, counts)
	for _, want := range []string{`>All</a>`, `view=updates" aria-current="page">Updates<span class="mx-tabs__n">1</span></a>`, `>Newsletters<span class="mx-tabs__n">2</span>`} {
		if !strings.Contains(tabs, want) {
			t.Errorf("the tabs lack %q:\n%s", want, tabs)
		}
	}
	if strings.Contains(tabs, ">Receipts") {
		t.Error("a category with no mail has a tab")
	}
	if mailCategoryTabs("en", "dana", "Archive", "", counts) != "" {
		t.Error("a folder other than the Inbox has category tabs")
	}
	if mailCategoryTabs("en", "dana", "Inbox", "", map[string]int{"all:primary": 9}) != "" {
		t.Error("an Inbox of one category has tabs")
	}
	if got := mailOnePerSender(msgs[3:]); len(got) != 1 || !got[0].Date.Equal(now) {
		t.Errorf("the Unsubscribe view lists %d of one list's issues", len(got))
	}

	nav := func(c map[string]int) string {
		return mailFolderNav(mailNav{User: "dana", Active: "Inbox", ViewCounts: c})
	}
	if s := nav(map[string]int{"followup": 0}); strings.Contains(s, "Follow-ups") || strings.Contains(s, ">Updates<") {
		t.Error("the sidebar offers an empty smart view, or a category")
	}
	if s := nav(map[string]int{"followup": 2}); !strings.Contains(s, "Follow-ups") {
		t.Error("the sidebar does not offer the follow-ups it has")
	}
}
