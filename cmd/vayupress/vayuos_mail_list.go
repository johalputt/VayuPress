// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/i18n"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// The message list of the Mail redesign (plan §3.2, render 01): rows under
// date groups, each with the sender, the time, the subject with its marks and
// two lines of the message, in place of the table of From, Subject and Date.

// Mail views are filters over the folder being read (plan §3.1). They store
// nothing: the folder's own listing is filtered after the header cache has
// answered it. A Tab view is one of the Inbox's categories, drawn as a tab
// over the list rather than in the sidebar; a Smart view is shown in the
// sidebar only while it holds something.
var mailViews = []struct {
	Key, Icon  string
	Tab, Smart bool
}{
	{Key: "unread", Icon: "mail"},
	{Key: "pinned", Icon: "pin"},
	{Key: "encrypted", Icon: "lock"},
	{Key: "followup", Icon: "reply", Smart: true},
	{Key: "lists", Icon: "megaphone", Smart: true},
	{Key: vmail.CategoryPrimary, Tab: true},
	{Key: vmail.CategoryUpdates, Tab: true},
	{Key: vmail.CategoryNewsletters, Tab: true},
	{Key: vmail.CategoryReceipts, Tab: true},
	{Key: vmail.CategorySocial, Tab: true},
}

// mailViewParam is the view a request names, or "" for the whole folder. An
// unknown name is the whole folder, never an empty list.
func mailViewParam(r *http.Request) string {
	v := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("view")))
	for _, x := range mailViews {
		if x.Key == v {
			// The table's own key, never the request's string. They are equal,
			// but code scanning cannot tell, and read the request's value
			// written into the page as a reflected XSS (alert #119).
			return x.Key
		}
	}
	return ""
}

// mailViewCtx is what the views need beyond the message itself: whether
// the folder is the Inbox (follow-ups are the Inbox's), the Message-IDs this
// mailbox has answered (its Sent mail's In-Reply-To and References) and the
// time now.
type mailViewCtx struct {
	inbox    bool
	answered map[string]bool
	now      time.Time
}

// followUpAfter is how long a person's message waits, read and unanswered,
// before it is a follow-up; followUpUntil is when it stops being one.
const (
	followUpAfter = 48 * time.Hour
	followUpUntil = 30 * 24 * time.Hour
)

func mailInView(m vmail.StoredMessage, view string, vc mailViewCtx) bool {
	switch view {
	case "unread":
		return !m.Seen
	case "pinned":
		return m.Flagged
	case "encrypted":
		return m.Encrypted
	case "followup":
		// A person's message, read, not answered, and neither new nor stale.
		age := vc.now.Sub(m.Date)
		return vc.inbox && m.Seen && m.Category == vmail.CategoryPrimary && m.MessageID != "" && !vc.answered[m.MessageID] &&
			age >= followUpAfter && age <= followUpUntil
	case "lists":
		return m.List
	case vmail.CategoryPrimary, vmail.CategoryUpdates, vmail.CategoryNewsletters, vmail.CategoryReceipts, vmail.CategorySocial:
		return m.Category == view
	}
	return true
}

// mailAnswered is the set of Message-IDs the messages in sent answer.
func mailAnswered(sent []vmail.StoredMessage) map[string]bool {
	out := map[string]bool{}
	for _, m := range sent {
		if m.InReplyTo != "" {
			out[m.InReplyTo] = true
		}
		for _, r := range m.References {
			out[r] = true
		}
	}
	return out
}

// mailViewCounts is how many messages of the folder each view holds (unread
// ones for a category), for the sidebar and the tabs. It is counted from the
// listing the page already made.
func mailViewCounts(msgs []vmail.StoredMessage, vc mailViewCtx) map[string]int {
	c := map[string]int{}
	for _, m := range msgs {
		for _, v := range mailViews {
			if v.Tab {
				if m.Category == v.Key {
					c["all:"+v.Key]++
					if !m.Seen {
						c[v.Key]++
					}
				}
				continue
			}
			if mailInView(m, v.Key, vc) {
				c[v.Key]++
			}
		}
	}
	return c
}

// mailOnePerSender keeps the newest message of each sender, for the
// Unsubscribe view, which lists the lists rather than their every issue.
func mailOnePerSender(msgs []vmail.StoredMessage) []vmail.StoredMessage {
	seen := map[string]bool{}
	out := msgs[:0:0]
	for _, m := range msgs {
		_, addr := mailParseFrom(m.From)
		k := strings.ToLower(addr)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, m)
	}
	return out
}

// mailCategoryTabs is the Inbox's categories as tabs over the list: All,
// then each category with its unread count. They are drawn only in the
// Inbox and only when its mail falls into two categories or more, so a
// mailbox that is all people's mail is not given tabs that sort nothing.
func mailCategoryTabs(lang, user, folder, view string, counts map[string]int) string {
	if !strings.EqualFold(folder, "Inbox") {
		return ""
	}
	used := 0
	for _, c := range vmail.Categories {
		if counts["all:"+c] > 0 {
			used++
		}
	}
	if used < 2 {
		return ""
	}
	tab := func(key, label string, unread int) string {
		q := "user=" + qparam(user) + "&folder=Inbox"
		if key != "" {
			q += "&view=" + key
		}
		cur := ""
		if key == view {
			cur = ` aria-current="page"`
		}
		n := ""
		if unread > 0 {
			n = `<span class="mx-tabs__n">` + itoaSafe(unread) + `</span>`
		}
		return `<a class="mx-tabs__tab" href="/os/vayumail/inbox?` + q + `" hx-get="/os/vayumail/inbox/fragment?` + q + `" hx-target="#vm-inbox-list" hx-swap="innerHTML" hx-indicator="#vm-inbox-spin" hx-push-url="/os/vayumail/inbox?` + q + `"` + cur + `>` + label + n + `</a>`
	}
	var b strings.Builder
	b.WriteString(`<nav class="mx-tabs" aria-label="` + esc(i18n.T(lang, "Categories")) + `">` + tab("", esc(i18n.T(lang, "All")), 0))
	for _, v := range mailViews {
		if v.Tab && counts["all:"+v.Key] > 0 {
			b.WriteString(tab(v.Key, esc(mailViewLabel(lang, v.Key)), counts[v.Key]))
		}
	}
	b.WriteString(`</nav>`)
	return b.String()
}

// mailWhen is where a message falls in the list's date groups.
type mailWhen int

const (
	whenToday mailWhen = iota
	whenYesterday
	whenThisWeek
	whenThisYear
	whenBefore
)

// mailListWhen files a message under a date group, in the site's time zone:
// Today, Yesterday, This week, then the month (with its year when it is not
// this one).
func mailListWhen(t, now time.Time) mailWhen {
	loc := config.SiteLocation()
	t, now = t.In(loc), now.In(loc)
	day := func(x time.Time) time.Time { return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, loc) }
	today := day(now)
	switch d := day(t); {
	case !d.Before(today):
		return whenToday
	case !d.Before(today.AddDate(0, 0, -1)):
		return whenYesterday
	case !d.Before(today.AddDate(0, 0, -6)):
		return whenThisWeek
	case t.Year() == now.Year():
		return whenThisYear
	}
	return whenBefore
}

// mailListGroup is the head of a message's date group, in lang.
func mailListGroup(lang string, t, now time.Time) string {
	switch mailListWhen(t, now) {
	case whenToday:
		return i18n.T(lang, "Today")
	case whenYesterday:
		return i18n.T(lang, "Yesterday")
	case whenThisWeek:
		return i18n.T(lang, "This week")
	case whenThisYear:
		return i18n.Date(lang, config.InSite(t), i18n.Month)
	}
	return i18n.Date(lang, config.InSite(t), i18n.MonthYear)
}

// mailListTime is a row's time, as much as its group leaves unsaid: the
// clock for today and yesterday, the weekday this week, else the date.
func mailListTime(lang string, t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	switch mailListWhen(t, now) {
	case whenToday, whenYesterday:
		return config.FormatSite(t, "15:04")
	case whenThisWeek:
		return i18n.Date(lang, config.InSite(t), i18n.Weekday)
	case whenThisYear:
		return i18n.Date(lang, config.InSite(t), i18n.DayMonthShort)
	}
	return i18n.Date(lang, config.InSite(t), i18n.DayMonthYearShort)
}

// mailListRow is one message as the list draws it. link opens it: in the
// reader beside the list, or for a draft in compose. count is the number of
// messages in its conversation, 0 when it stands alone.
//
// acts is the row's own actions (mailRowActs), shown on hover in place of its
// time, or "" for a row that has none.
func mailListRow(lang string, m vmail.StoredMessage, who, link string, draft bool, count int, now time.Time, avSet map[string]bool, labels []string, acts string) string {
	subj := m.Subject
	if subj == "" {
		subj = i18n.T(lang, "(no subject)")
	}
	cls := "mx-row"
	state := ""
	if !m.Seen {
		cls += " is-unread"
		state = i18n.T(lang, "Unread.") + " "
	}
	if m.Flagged {
		cls += " is-pinned"
		state += i18n.T(lang, "Pinned.") + " "
	}
	var b strings.Builder
	b.WriteString(`<li class="` + cls + `" data-vm-row>`)
	// The gutter holds the unread dot or the pin, and the selection check in
	// the same place, so selecting never shifts a column.
	b.WriteString(`<span class="mx-row__gutter"><input type="checkbox" class="mx-check" name="id" value="` + esc(m.ID) + `" data-vm-check aria-label="` + esc(i18n.T(lang, "Select the message from {sender}", "sender", mailDisplay(who))) + `">`)
	if m.Flagged {
		b.WriteString(`<span class="mx-row__pin" aria-hidden="true">` + saIcon("pin") + `</span>`)
	} else {
		b.WriteString(`<span class="mx-row__dot" aria-hidden="true"></span>`)
	}
	b.WriteString(`</span>`)
	b.WriteString(`<a class="mx-row__open" href="` + esc(link) + `"`)
	if !draft {
		b.WriteString(` data-vm-open hx-get="` + esc(link) + `&amp;pane=1" hx-target="#vm-readpane" hx-swap="innerHTML"`)
	}
	b.WriteString(`>` + mailAvatarImg(who, avSet) + `<span class="mx-row__main">`)
	if state != "" {
		b.WriteString(`<span class="vp-sr-only">` + state + `</span>`)
	}
	b.WriteString(`<span class="mx-row__top"><span class="mx-row__who">` + esc(mailDisplay(who)) + `</span>`)
	if !m.Date.IsZero() {
		b.WriteString(`<time class="mx-row__time" datetime="` + m.Date.UTC().Format(time.RFC3339) + `" title="` + esc(config.FormatSiteStamp(m.Date)) + `">` + esc(mailListTime(lang, m.Date, now)) + `</time>`)
	}
	b.WriteString(`</span><span class="mx-row__subj"><span class="mx-row__subj-text">` + esc(subj) + `</span>`)
	if m.Encrypted || m.Attachment || count > 1 {
		b.WriteString(`<span class="mx-row__marks">`)
		if m.Encrypted {
			b.WriteString(`<span class="mx-mark" title="` + esc(i18n.T(lang, "Encrypted")) + `">` + saIcon("lock") + `<span class="vp-sr-only">` + esc(i18n.T(lang, "Encrypted.")) + `</span></span>`)
		}
		if m.Attachment {
			b.WriteString(`<span class="mx-mark" title="` + esc(i18n.T(lang, "Has an attachment")) + `">` + saIcon("clip") + `<span class="vp-sr-only">` + esc(i18n.T(lang, "Has an attachment.")) + `</span></span>`)
		}
		if count > 1 {
			// The number is seen; a screen reader hears its noun after it.
			b.WriteString(`<span class="mx-thread" title="` + esc(i18n.T(lang, "{n, plural, one {# message in this conversation} other {# messages in this conversation}}", "n", count)) + `">` +
				strconv.Itoa(count) + `<span class="vp-sr-only"> ` + esc(i18n.T(lang, "{n, plural, one {message.} other {messages.}}", "n", count)) + `</span></span>`)
		}
		b.WriteString(`</span>`)
	}
	b.WriteString(mailRowLabels(labels) + `</span>`)
	if m.Preview != "" {
		b.WriteString(`<span class="mx-row__pv">` + esc(m.Preview) + `</span>`)
	}
	b.WriteString(`</span></a>` + acts + `</li>`)
	return b.String()
}

// mailRowActs is what a received message's row offers on hover: Archive,
// Delete (to Trash, as the reader's Delete is), Mark read or unread, and
// Snooze until tomorrow. Each posts the one message through the list's own
// action, with the folder its row was drawn in (the list's scope inputs).
func mailRowActs(lang string, m vmail.StoredMessage, folder string) string {
	act := func(icon, label string, vals ...string) string {
		return `<button type="button" class="mx-row__quick-btn" hx-post="/os/vayumail/inbox/action"` + hxVals(append(vals, "id", m.ID)...) +
			` hx-include="[data-vm-scope]" hx-target="#vm-inbox-list" hx-swap="innerHTML" title="` + esc(label) + `" aria-label="` + esc(label) + `">` + saIcon(icon) + `</button>`
	}
	var b strings.Builder
	b.WriteString(`<span class="mx-row__quick">`)
	if !strings.EqualFold(folder, "Archive") {
		b.WriteString(act("archive", i18n.T(lang, "Archive"), "action", "move", "to", "Archive"))
	}
	if !strings.EqualFold(folder, "Trash") {
		b.WriteString(act("trash", i18n.T(lang, "Delete"), "action", "move", "to", "Trash"))
	}
	if m.Seen {
		b.WriteString(act("mail", i18n.T(lang, "Mark unread"), "action", "mark", "mark", "unread"))
	} else {
		b.WriteString(act("check", i18n.T(lang, "Mark read"), "action", "mark", "mark", "read"))
	}
	if !strings.EqualFold(folder, "Snoozed") {
		b.WriteString(act("timer", i18n.T(lang, "Snooze until tomorrow"), "action", "snooze", "snooze", "tomorrow"))
	}
	b.WriteString(`</span>`)
	return b.String()
}
