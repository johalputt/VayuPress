// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_keys.go — the mailbox's keys for people outside this install
// (internal/vayuos/mail/contactkeys.go), at the foot of its Contacts: each
// with its fingerprint, to compare with the one its owner reads out, and a
// way to add one by pasting it.

import (
	"net/http"
	"strings"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// maxPastedKey bounds a pasted public key; real ones are a few kilobytes.
const maxPastedKey = 64 << 10

// keysSection is the Contacts panel's list of outside keys, with Add.
func (a *App) keysSection(rd vmail.Reader, userKey, said string) string {
	keys, err := a.vayuMail.ContactKeys(rd)
	if err != nil {
		return ""
	}
	writable := !a.vayuMail.ReaderReadOnly(rd)
	var b strings.Builder
	b.WriteString(`<div class="vm-contacts-head vm-keys-head"><h3 class="vm-contacts-title">Keys</h3><span class="muted text-sm">` + itoaSafe(len(keys)) + `</span></div>`)
	if said != "" {
		b.WriteString(`<p class="vm-keys-said" role="status">` + esc(said) + `</p>`)
	}
	if len(keys) > 0 {
		b.WriteString(`<ul class="vm-contacts-list">`)
		for _, k := range keys {
			b.WriteString(`<li class="vm-contact"><span class="vm-contact-meta"><span class="vm-contact-mail">` + esc(k.Email) + `</span>` +
				`<code class="vm-key-fp">` + esc(formatSafety(k.Fingerprint)) + `</code></span>`)
			if writable {
				b.WriteString(`<button class="btn btn--xs btn--ghost" type="button" hx-post="/os/vayumail/keys/action"` + hxVals("user", userKey, "action", "remove", "email", k.Email) +
					` hx-target="#vm-contacts-panel" hx-swap="outerHTML" hx-confirm="Remove the key for ` + esc(k.Email) + `? Mail to them is then sent readable, unless they publish a key.">Remove</button>`)
			}
			b.WriteString(`</li>`)
		}
		b.WriteString(`</ul>`)
	}
	if writable {
		b.WriteString(`<details class="vm-keys-add"><summary class="btn btn--sm">Add a key</summary>` +
			`<form hx-post="/os/vayumail/keys/action" hx-target="#vm-contacts-panel" hx-swap="outerHTML">` +
			`<input type="hidden" name="user" value="` + esc(userKey) + `"><input type="hidden" name="action" value="add">` +
			`<label class="vm-keys-label" for="vm-key-armor">Paste the public key someone gave you. Compare its fingerprint with the one they read to you before you rely on it.</label>` +
			`<textarea class="input" id="vm-key-armor" name="armor" rows="6" spellcheck="false" placeholder="-----BEGIN PGP PUBLIC KEY BLOCK-----" required></textarea>` +
			`<button class="btn btn--sm" type="submit">Add the key</button></form></details>`)
	}
	return b.String()
}

// handleVayuOSKeysAction adds or removes an outside key for the mailbox the
// request may change (mailReader, ADR-0152), and redraws the Contacts panel
// with what happened.
func (a *App) handleVayuOSKeysAction(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPastedKey+(4<<10))
	userKey := sanitizeMailUser(strings.TrimSpace(r.PostFormValue("user")))
	rd := a.mailReader(r, userKey)
	owner, ok := a.contactOwner(r, userKey)
	if rd.Key() == "" || !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "No mailbox you can change was named.", "")
		return
	}
	said := ""
	switch r.PostFormValue("action") {
	case "add":
		if emails, err := a.vayuMail.AddContactKey(rd, []byte(r.PostFormValue("armor"))); err != nil {
			said = "Not added: " + err.Error() + "."
		} else {
			said = "Added the key for " + strings.Join(emails, ", ") + "."
		}
	case "remove":
		email := strings.TrimSpace(r.PostFormValue("email"))
		if err := a.vayuMail.RemoveContactKey(rd, email); err != nil {
			said = "Not removed: " + err.Error() + "."
		} else {
			said = "Removed the key for " + email + "."
		}
	default:
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "unknown action", "")
		return
	}
	writeOSFragment(w, a.contactsPanel(r, owner, userKey, "", "", "", contactsSaid{keys: said}))
}
