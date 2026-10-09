// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_searches.go — saved searches (internal/vayuos/mail/
// savedsearches.go): Save this search, or Forget it, from the list's search
// results, and the sidebar group that runs them again.

import (
	"net/http"
	"strings"
)

// mailSearchesGroup is the sidebar's saved searches. Each opens Mail with
// the search run across all mail (admin-os-mail.js reads ?search=). It is
// its own element so saving or forgetting swaps it alone, out of band: the
// sidebar around it keeps the folder and view it shows, which the request
// that saves does not know.
func mailSearchesGroup(user string, searches []string, oob bool) string {
	var b strings.Builder
	b.WriteString(`<div id="vm-searches"`)
	if oob {
		b.WriteString(` hx-swap-oob="true"`)
	}
	b.WriteString(`>`)
	if len(searches) > 0 {
		b.WriteString(`<div class="sa-appside__group">Searches</div>`)
		for _, q := range searches {
			b.WriteString(`<a class="sa-appside__item" href="/os/vayumail/inbox?user=` + qparam(user) + `&amp;search=` + qparam(q) + `">` +
				saIcon("search") + `<span class="sa-appside__label">` + esc(q) + `</span></a>`)
		}
	}
	b.WriteString(`</div>`)
	return b.String()
}

// handleVayuOSSearchesAction saves the list's search, or forgets it, and
// answers with the results again, their button now saying which, and the
// sidebar's saved searches out of band.
func (a *App) handleVayuOSSearchesAction(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	rd := a.mailReader(r, sanitizeMailUser(strings.TrimSpace(r.PostFormValue("user"))))
	if rd.Key() == "" {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "No mailbox you can change was named.", "")
		return
	}
	// The button posts the search form's own fields, so they are read by the
	// one parser the search itself uses.
	asSearch := r.Clone(r.Context())
	asSearch.URL.RawQuery = r.PostForm.Encode()
	sf := parseSearchFilters(asSearch)
	var err error
	switch r.PostFormValue("action") {
	case "save":
		err = a.vayuMail.SaveSearch(rd, sf.savedQuery())
	case "forget":
		err = a.vayuMail.ForgetSearch(rd, sf.savedQuery())
	default:
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "unknown action", "")
		return
	}
	if err != nil {
		setMailSaid(w, "Not done: "+err.Error()+".", true)
	}
	searches, _ := a.vayuMail.SavedSearches(rd)
	writeOSFragment(w, a.vayuSearchResults(rd, sf)+mailSearchesGroup(rd.Key(), searches, true))
}
