// SPDX-License-Identifier: Apache-2.0

package main

import (
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
	var b strings.Builder
	group := func(title string) {
		b.WriteString(`<div class="mx-suggest__head" role="presentation">` + esc(title) + `</div>`)
	}
	option := func(attr, value, inner string) {
		b.WriteString(`<button type="button" class="mx-suggest__opt" role="option" aria-selected="false" ` + attr + `="` + esc(value) + `">` + inner + `</button>`)
	}

	typed := strings.TrimSpace(q)
	if typed != "" {
		reading := vmail.ReadSearchSentence(typed, now)
		if s := reading.String(); s != vmail.ParseSearchQuery(typed).String() && !reading.Empty() {
			group("Search for")
			option("data-mx-q", s, mailSuggestChips(reading))
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
			group("People")
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

	group("Filters")
	b.WriteString(`<div class="mx-suggest__chips">`)
	day := func(back int) string { return now.UTC().AddDate(0, 0, -back).Format("2006-01-02") }
	for _, f := range [][2]string{
		{"from:", "From"}, {"to:", "To"}, {"has:attachment", "Has attachment"}, {"is:unread", "Unread"},
		{"after:" + day(7), "Last 7 days"}, {"after:" + day(30), "Last 30 days"},
	} {
		option("data-mx-add", f[0], `<span class="mx-suggest__chip">`+esc(f[1])+`</span>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// mailSuggestChips draws a reading as the chips it would search with, in
// the order the search keeps them.
func mailSuggestChips(q vmail.SearchQuery) string {
	var chips []string
	chip := func(s string) { chips = append(chips, `<span class="mx-suggest__chip">`+esc(s)+`</span>`) }
	for _, w := range q.Words {
		chip("“" + w + "”")
	}
	for _, t := range [][2]string{{"From ", q.From}, {"To ", q.To}, {"Subject ", q.Subject}, {"In ", q.Folder}, {"Label ", q.Label}} {
		if t[1] != "" {
			chip(t[0] + t[1])
		}
	}
	if !q.After.IsZero() {
		chip("Since " + q.After.Format("2 Jan 2006"))
	}
	if !q.Before.IsZero() {
		chip("Until " + q.Before.AddDate(0, 0, -1).Format("2 Jan 2006"))
	}
	if q.Attachment {
		chip("Has attachment")
	}
	if q.Unread {
		chip("Unread")
	}
	return `<span class="mx-suggest__chips">` + strings.Join(chips, "") + `</span>`
}
