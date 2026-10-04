// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_unsubscribe.go — Unsubscribe in the reader, for mail from a
// list (internal/vayuos/mail/unsubscribe.go says when it is sent from here).

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// mailUnsubscribeLine is the reader's line for a message from a list: a
// control that leaves it from here when the message offers that, or its page
// to open when it offers only that. One-click alone may yet be refused as
// unsigned (checking needs DNS, so it is not done while drawing the reader),
// so then the page is offered beside the control, where the refusal points.
func mailUnsubscribeLine(user, folder, id string, o vmail.UnsubscribeOffer) string {
	host := ""
	if u, err := url.Parse(o.Link); err == nil {
		host = u.Hostname()
	}
	page := func(class, text string) string {
		return `<a class="` + class + `" href="` + esc(o.Link) + `" target="_blank" rel="noopener noreferrer">` + esc(text) + `</a>`
	}
	button := `<button type="button" class="mx-unsub__go" hx-post="/os/vayumail/unsubscribe" hx-swap="none"` +
		hxVals("user", user, "folder", folder, "id", id) + ` hx-confirm="Unsubscribe from this list?">Unsubscribe</button>`
	line := `<p class="mx-unsub">This is mail from a list. `
	switch {
	case o.Mailto != "":
		return line + button + `</p>`
	case o.OneClick != "":
		return line + button + ` <span aria-hidden="true">·</span> ` + page("mx-unsub__page", "or on "+host) + `</p>`
	default:
		return line + page("mx-unsub__go", "Unsubscribe on "+host) + `</p>`
	}
}

// handleVayuOSUnsubscribe leaves the list of one message, for the mailbox the
// request may change (mailReader, ADR-0152), and says how in a toast.
func (a *App) handleVayuOSUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	rd := a.mailReader(r, sanitizeMailUser(strings.TrimSpace(r.PostFormValue("user"))))
	if rd.Key() == "" {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "No mailbox you can change was named.", "")
		return
	}
	folder := sanitizeMailFolder(strings.TrimSpace(r.PostFormValue("folder")))
	said, err := a.vayuMail.Unsubscribe(r.Context(), rd, folder, sanitizeMailID(strings.TrimSpace(r.PostFormValue("id"))))
	warn := err != nil
	if err != nil {
		said = "Not unsubscribed: " + err.Error() + "."
	}
	detail, _ := json.Marshal(map[string]any{"vm-said": map[string]any{"text": said, "warn": warn}})
	w.Header().Set("HX-Trigger", string(detail))
	w.WriteHeader(http.StatusNoContent)
}
