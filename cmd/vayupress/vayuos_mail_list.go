// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// The message list of the Mail redesign (plan §3.2, render 01): rows under
// date groups, each with the sender, the time, the subject with its marks and
// two lines of the message, in place of the table of From, Subject and Date.

// Mail views are filters over the folder being read (plan §3.1). They store
// nothing: the folder's own listing is filtered after the header cache has
// answered it.
var mailViews = []struct{ Key, Label, Icon string }{
	{"unread", "Unread", "mail"},
	{"pinned", "Pinned", "pin"},
	{"encrypted", "Encrypted", "lock"},
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

func mailInView(m vmail.StoredMessage, view string) bool {
	switch view {
	case "unread":
		return !m.Seen
	case "pinned":
		return m.Flagged
	case "encrypted":
		return m.Encrypted
	}
	return true
}

// mailViewCounts is how many messages of the folder each view holds, for the
// sidebar. It is counted from the listing the page already made.
func mailViewCounts(msgs []vmail.StoredMessage) map[string]int {
	c := map[string]int{}
	for _, m := range msgs {
		for _, v := range mailViews {
			if mailInView(m, v.Key) {
				c[v.Key]++
			}
		}
	}
	return c
}

// mailListGroup files a message under a date group, in the site's time zone:
// Today, Yesterday, This week, then the month (with its year when it is not
// this one).
func mailListGroup(t, now time.Time) string {
	loc := config.SiteLocation()
	t, now = t.In(loc), now.In(loc)
	day := func(x time.Time) time.Time { return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, loc) }
	today := day(now)
	switch d := day(t); {
	case !d.Before(today):
		return "Today"
	case !d.Before(today.AddDate(0, 0, -1)):
		return "Yesterday"
	case !d.Before(today.AddDate(0, 0, -6)):
		return "This week"
	case t.Year() == now.Year():
		return t.Month().String()
	default:
		return t.Month().String() + " " + strconv.Itoa(t.Year())
	}
}

// mailListTime is a row's time, as much as its group leaves unsaid: the
// clock for today and yesterday, the weekday this week, else the date.
func mailListTime(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	switch mailListGroup(t, now) {
	case "Today", "Yesterday":
		return config.FormatSite(t, "15:04")
	case "This week":
		return config.FormatSite(t, "Mon")
	}
	if config.InSite(t).Year() == config.InSite(now).Year() {
		return config.FormatSite(t, "2 Jan")
	}
	return config.FormatSite(t, "2 Jan 2006")
}

// mailListRow is one message as the list draws it. link opens it: in the
// reader beside the list, or for a draft in compose. count is the number of
// messages in its conversation, 0 when it stands alone.
func mailListRow(m vmail.StoredMessage, who, link string, draft bool, count int, now time.Time, avSet map[string]bool) string {
	subj := m.Subject
	if subj == "" {
		subj = "(no subject)"
	}
	cls := "mx-row"
	state := ""
	if !m.Seen {
		cls += " is-unread"
		state = "Unread. "
	}
	if m.Flagged {
		cls += " is-pinned"
		state += "Pinned. "
	}
	var b strings.Builder
	b.WriteString(`<li class="` + cls + `" data-vm-row>`)
	// The gutter holds the unread dot or the pin, and the selection check in
	// the same place, so selecting never shifts a column.
	b.WriteString(`<span class="mx-row__gutter"><input type="checkbox" class="mx-check" name="id" value="` + esc(m.ID) + `" data-vm-check aria-label="Select the message from ` + esc(mailDisplay(who)) + `">`)
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
		b.WriteString(`<time class="mx-row__time" datetime="` + m.Date.UTC().Format(time.RFC3339) + `" title="` + esc(config.FormatSiteStamp(m.Date)) + `">` + esc(mailListTime(m.Date, now)) + `</time>`)
	}
	b.WriteString(`</span><span class="mx-row__subj"><span class="mx-row__subj-text">` + esc(subj) + `</span>`)
	if m.Encrypted || m.Attachment || count > 1 {
		b.WriteString(`<span class="mx-row__marks">`)
		if m.Encrypted {
			b.WriteString(`<span class="mx-mark" title="Encrypted">` + saIcon("lock") + `<span class="vp-sr-only">Encrypted.</span></span>`)
		}
		if m.Attachment {
			b.WriteString(`<span class="mx-mark" title="Has an attachment">` + saIcon("clip") + `<span class="vp-sr-only">Has an attachment.</span></span>`)
		}
		if count > 1 {
			n := strconv.Itoa(count)
			b.WriteString(`<span class="mx-thread" title="` + n + ` messages in this conversation">` + n + `<span class="vp-sr-only"> messages.</span></span>`)
		}
		b.WriteString(`</span>`)
	}
	b.WriteString(`</span>`)
	if m.Preview != "" {
		b.WriteString(`<span class="mx-row__pv">` + esc(m.Preview) + `</span>`)
	}
	b.WriteString(`</span></a></li>`)
	return b.String()
}
