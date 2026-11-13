// SPDX-License-Identifier: Apache-2.0

package main

import (
	"github.com/johalputt/vayupress/internal/i18n"
	"net/http"
	"strings"
	"time"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// mailSuggestPeople is how many of the mailbox's contacts a suggestion list
// shows: enough to pick from, few enough to read at a glance.
const mailSuggestPeople = 5

// handleVayuOSSearchSuggest answers what is typed in Mail's search with
// suggestions under the field: the sentence read into terms ("pdf from priya
// last month" as From, a date and Has attachment), the mailbox's own
// contacts matching the word being typed, and the filters as chips. Picking
// one puts its terms in the field; nothing is searched until then. Recent
// searches are the browser's own (admin-os-mail.js), never sent here.
func (a *App) handleVayuOSSearchSuggest(w http.ResponseWriter, r *http.Request) {
	owner, ok := a.contactOwner(r, mailUserParam(r))
	if !ok {
		writeOSFragment(w, "")
		return
	}
	q := r.URL.Query().Get("q")
	if len(q) > 500 {
		q = q[:500]
	}
	writeOSFragment(w, a.mailSuggestions(r, owner, q, time.Now()))
}

// mailSuggestions is the suggestion list for q in owner's mailbox.
func (a *App) mailSuggestions(r *http.Request, owner, q string, now time.Time) string {
	lang := i18n.Lang(r.Context())
	var b strings.Builder
	group := func(title, attr string) {
		b.WriteString(`<div class="mx-suggest__head" role="presentation"` + attr + `>` + esc(title) + `</div>`)
	}
	option := func(attr, value, inner string) {
		b.WriteString(`<button type="button" class="mx-suggest__opt" role="option" aria-selected="false" ` + attr + `="` + esc(value) + `">` + inner + `</button>`)
	}

	typed := strings.TrimSpace(q)
	if typed != "" {
		reading := vmail.ReadSearchSentence(typed, now)
		if s := reading.String(); s != vmail.ParseSearchQuery(typed).String() && !reading.Empty() {
			// The script marks this one chosen for Enter, knowing it by
			// data-mx-reading, not by its words.
			group(i18n.T(lang, "Search for"), " data-mx-reading")
			option("data-mx-q", s, mailSuggestChips(lang, reading))
		}
	}

	// The word being typed, a from: or to: value included, finds people.
	last := ""
	if typed != "" && !strings.HasSuffix(q, " ") {
		fields := strings.Fields(typed)
		last = fields[len(fields)-1]
	}
	term, needle := "from", last
	for _, t := range []string{"from:", "to:"} {
		if v, ok := strings.CutPrefix(strings.ToLower(last), t); ok {
			term, needle = strings.TrimSuffix(t, ":"), v
		}
	}
	if typed == "" || last != "" {
		people, _ := a.vayuMail.Accounts().SearchContacts(r.Context(), owner, needle, mailSuggestPeople)
		if len(people) > 0 {
			group(i18n.T(lang, "People"), "")
			head := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(q), last))
			for _, c := range people {
				put := strings.TrimSpace(head + " " + term + ":" + c.Email)
				name := c.Name
				if name == "" {
					name = c.Email
				}
				option("data-mx-q", put, mailAvatarImg(c.Email, a.mailboxAvatarSet())+
					`<span class="mx-suggest__who"><span class="mx-suggest__name">`+esc(name)+`</span><span class="mx-suggest__addr">`+esc(c.Email)+`</span></span>`)
			}
		}
	}

	group(i18n.T(lang, "Filters"), "")
	b.WriteString(`<div class="mx-suggest__chips">`)
	day := func(back int) string { return now.UTC().AddDate(0, 0, -back).Format("2006-01-02") }
	for _, f := range [][2]string{
		{"from:", i18n.T(lang, "From")}, {"to:", i18n.T(lang, "To")}, {"has:attachment", i18n.T(lang, "Has attachment")}, {"is:unread", i18n.T(lang, "Unread")},
		{"after:" + day(7), i18n.T(lang, "Last 7 days")}, {"after:" + day(30), i18n.T(lang, "Last 30 days")},
	} {
		option("data-mx-add", f[0], `<span class="mx-suggest__chip">`+esc(f[1])+`</span>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// mailSuggestChips draws a reading as the chips it would search with, in
// the order the search keeps them.
func mailSuggestChips(lang string, q vmail.SearchQuery) string {
	var chips []string
	chip := func(s string) { chips = append(chips, `<span class="mx-suggest__chip">`+esc(s)+`</span>`) }
	for _, w := range q.Words {
		chip("“" + w + "”")
	}
	if q.From != "" {
		chip(i18n.T(lang, "From {who}", "who", q.From))
	}
	if q.To != "" {
		chip(i18n.T(lang, "To {who}", "who", q.To))
	}
	if q.Subject != "" {
		chip(i18n.T(lang, "Subject {words}", "words", q.Subject))
	}
	if q.Folder != "" {
		chip(i18n.T(lang, "In {folder}", "folder", mailFolderLabel(lang, q.Folder)))
	}
	if q.Label != "" {
		chip(i18n.T(lang, "Label {label}", "label", q.Label))
	}
	if !q.After.IsZero() {
		chip(i18n.T(lang, "Since {date}", "date", i18n.Date(lang, q.After, i18n.DayMonthYearShort)))
	}
	if !q.Before.IsZero() {
		chip(i18n.T(lang, "Until {date}", "date", i18n.Date(lang, q.Before.AddDate(0, 0, -1), i18n.DayMonthYearShort)))
	}
	if q.Attachment {
		chip(i18n.T(lang, "Has attachment"))
	}
	if q.Unread {
		chip(i18n.T(lang, "Unread"))
	}
	return `<span class="mx-suggest__chips">` + strings.Join(chips, "") + `</span>`
}
