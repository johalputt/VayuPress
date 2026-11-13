// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_blocked.go — Block a sender from the reader, and the blocked
// list, with Unblock, at the foot of the mailbox's Contacts.

import (
	"github.com/johalputt/vayupress/internal/i18n"
	"net/http"
	"strings"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// blockedSection is the Contacts panel's list of blocked senders.
func (a *App) blockedSection(rd vmail.Reader, userKey string) string {
	lang := rd.Lang()
	blocked, err := a.vayuMail.BlockedSenders(rd)
	if err != nil || len(blocked) == 0 {
		return ""
	}
	writable := !a.vayuMail.ReaderReadOnly(rd)
	var b strings.Builder
	b.WriteString(`<div class="vm-contacts-head vm-blocked-head"><h3 class="vm-contacts-title">` + esc(i18n.T(lang, "Blocked")) + `</h3><span class="muted text-sm">` + itoaSafe(len(blocked)) + `</span></div><ul class="vm-contacts-list">`)
	for _, s := range blocked {
		b.WriteString(`<li class="vm-contact"><span class="vm-contact-meta"><span class="vm-contact-mail">` + esc(s) + `</span></span>`)
		if writable {
			b.WriteString(`<button class="btn btn--xs btn--ghost" type="button" hx-post="/os/vayumail/blocked/action"` + hxVals("user", userKey, "action", "unblock", "sender", s) +
				` hx-target="#vm-contacts-panel" hx-swap="outerHTML">` + esc(i18n.T(lang, "Unblock")) + `</button>`)
		}
		b.WriteString(`</li>`)
	}
	b.WriteString(`</ul>`)
	return b.String()
}

// handleVayuOSBlockedAction blocks or unblocks a sender for the mailbox the
// request may change (mailReader, ADR-0152; the engine refuses a read-only
// holder). Block answers with a toast, the reader staying as it is; Unblock
// redraws the Contacts panel it was pressed in.
func (a *App) handleVayuOSBlockedAction(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	userKey := sanitizeMailUser(strings.TrimSpace(r.PostFormValue("user")))
	rd := a.mailReader(r, userKey)
	lang := rd.Lang()
	if rd.Key() == "" {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(lang, "No mailbox you can change was named."), "")
		return
	}
	sender := strings.TrimSpace(r.PostFormValue("sender"))
	switch r.PostFormValue("action") {
	case "block":
		said, warn := i18n.T(lang, "Blocked {sender}. Their mail will be refused, or put straight in Trash.", "sender", sender), false
		if err := a.vayuMail.BlockSender(rd, sender); err != nil {
			said, warn = i18n.T(lang, "Not blocked: {reason}.", "reason", err.Error()), true
		}
		setMailSaid(w, said, warn)
		w.WriteHeader(http.StatusNoContent)
	case "unblock":
		errMsg := ""
		if err := a.vayuMail.UnblockSender(rd, sender); err != nil {
			errMsg = i18n.T(lang, "Not unblocked: {reason}.", "reason", err.Error())
		}
		owner, ok := a.contactOwner(r, userKey)
		if !ok {
			writeAPIError(w, r, http.StatusForbidden, "forbidden", "no authorized mailbox", "")
			return
		}
		writeOSFragment(w, a.vayuContactsPanelWith(r, owner, userKey, errMsg, "", ""))
	default:
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "unknown action", "")
	}
}
