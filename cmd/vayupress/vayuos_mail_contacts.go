// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_contacts.go — the per-mailbox address book UI: a collapsible
// Contacts panel, one-click "Save contact" from any message, and per-mailbox
// autocomplete in the composer. Every contact is owned by exactly one mailbox
// (isolation), enforced by contactOwner resolving the owner from the same
// authorization the inbox uses: admins may act on any ?user= mailbox; a
// non-admin is locked to their own assigned mailbox.

import (
	"context"
	"html"
	"net/http"
	"strings"
)

// contactOwner resolves the mailbox that owns (and is scoped to) the contacts in
// this request, applying the inbox authorization: a non-admin is forced onto
// their own mailbox regardless of the requested user. Returns the full lowercased
// owner address, or ok=false when there is no usable/authorized mailbox.
func (a *App) contactOwner(r *http.Request, userParam string) (string, bool) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || a.vayuMail.Accounts() == nil {
		return "", false
	}
	user := sanitizeMailUser(strings.TrimSpace(userParam))
	if !a.isAdminRequest(r) {
		local := a.ownMailboxKey(r)
		if local == "" {
			return "", false
		}
		user = local
	}
	if user == "" {
		return "", false
	}
	owner := strings.ToLower(mailAddrOf(user, a.vayuMail.Config().Domain))
	if !strings.Contains(owner, "@") {
		return "", false
	}
	return owner, true
}

// vayuContactsPanel renders the address-book panel for one mailbox: an add form,
// then the saved contacts with per-row delete. It is the universal HTMX swap
// target (#vm-contacts-panel); every mutation returns the whole panel via
// hx-swap="outerHTML". userKey is threaded through so an admin acting on another
// mailbox stays scoped to it.
func (a *App) vayuContactsPanel(ctx context.Context, owner, userKey string) string {
	esc := html.EscapeString
	contacts, _ := a.vayuMail.Accounts().ListContacts(ctx, owner)
	avSet := a.mailboxAvatarSet()

	var b strings.Builder
	b.WriteString(`<div id="vm-contacts-panel" class="vm-contacts">`)
	b.WriteString(`<div class="vm-contacts-head"><h2 class="vm-contacts-title">Contacts</h2>` +
		`<span class="muted text-sm">` + itoaSafe(len(contacts)) + ` saved · ` + esc(owner) + `</span></div>`)
	b.WriteString(`<p class="muted text-sm vm-contacts-sub">Private to this mailbox. These power the recipient suggestions when you compose from here.</p>`)

	b.WriteString(`<form class="vm-contacts-add" hx-post="/os/vayumail/contacts/add" hx-target="#vm-contacts-panel" hx-swap="outerHTML">`)
	b.WriteString(`<input type="hidden" name="user" value="` + esc(userKey) + `">`)
	b.WriteString(`<input class="input input--sm" type="email" name="email" placeholder="name@example.com" required aria-label="Contact email">`)
	b.WriteString(`<input class="input input--sm" type="text" name="name" placeholder="Name (optional)" aria-label="Contact name">`)
	b.WriteString(`<button class="btn btn--primary btn--sm" type="submit">Save contact</button>`)
	b.WriteString(`</form>`)

	if len(contacts) == 0 {
		b.WriteString(`<p class="muted text-sm vm-contacts-empty">No saved contacts yet. Add one above, or use “Save contact” on any message.</p>`)
	} else {
		b.WriteString(`<ul class="vm-contacts-list">`)
		for _, c := range contacts {
			display := c.Name
			if display == "" {
				display = c.Email
			}
			del := `<button class="btn btn--xs btn--ghost vm-contact-del" type="button" hx-post="/os/vayumail/contacts/delete" ` +
				hxVals("user", userKey, "email", c.Email) +
				` hx-target="#vm-contacts-panel" hx-swap="outerHTML" hx-confirm="Remove ` + esc(c.Email) + ` from this mailbox's contacts?">Remove</button>`
			compose := "/os/vayumail/compose?user=" + qparam(userKey) + "&to=" + qparam(c.Email)
			b.WriteString(`<li class="vm-contact">` +
				`<a class="vm-contact-id" href="` + compose + `">` + mailAvatarImg(c.Email, avSet) +
				`<span class="vm-contact-meta"><span class="vm-contact-name">` + esc(display) + `</span>` +
				`<span class="vm-contact-mail">` + esc(c.Email) + `</span></span></a>` +
				del + `</li>`)
		}
		b.WriteString(`</ul>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// handleVayuOSContacts returns the contacts panel for the resolved mailbox,
// swapped into the reading pane from the mailbox toolbar.
func (a *App) handleVayuOSContacts(w http.ResponseWriter, r *http.Request) {
	owner, ok := a.contactOwner(r, mailUserParam(r))
	if !ok {
		writeOSFragment(w, `<div class="empty-state">Select a mailbox to manage its contacts.</div>`)
		return
	}
	writeOSFragment(w, a.vayuContactsPanel(r.Context(), owner, mailUserParam(r)))
}

// handleVayuOSContactAdd saves a contact typed into the panel's add form and
// returns the refreshed panel.
func (a *App) handleVayuOSContactAdd(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	userKey := sanitizeMailUser(strings.TrimSpace(r.FormValue("user")))
	owner, ok := a.contactOwner(r, userKey)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "no authorized mailbox", "")
		return
	}
	// Best-effort save; a bad address just re-renders the panel unchanged so the
	// operator sees their entry did not take rather than a hard error page.
	_ = a.vayuMail.Accounts().AddContact(r.Context(), owner, r.FormValue("email"), r.FormValue("name"))
	writeOSFragment(w, a.vayuContactsPanel(r.Context(), owner, userKey))
}

// handleVayuOSContactDelete removes a contact and returns the refreshed panel.
func (a *App) handleVayuOSContactDelete(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	userKey := sanitizeMailUser(strings.TrimSpace(r.FormValue("user")))
	owner, ok := a.contactOwner(r, userKey)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "no authorized mailbox", "")
		return
	}
	_ = a.vayuMail.Accounts().DeleteContact(r.Context(), owner, r.FormValue("email"))
	writeOSFragment(w, a.vayuContactsPanel(r.Context(), owner, userKey))
}

// handleVayuOSContactSave is the one-click "Save contact" on a message: it files
// the sender/recipient into the current mailbox's address book and swaps the
// button to a saved state, with no page reload.
func (a *App) handleVayuOSContactSave(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	userKey := sanitizeMailUser(strings.TrimSpace(r.FormValue("user")))
	owner, ok := a.contactOwner(r, userKey)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "no authorized mailbox", "")
		return
	}
	name, addr := mailParseFrom(r.FormValue("email"))
	if addr == "" {
		addr = r.FormValue("email")
	}
	if fn := strings.TrimSpace(r.FormValue("name")); fn != "" {
		name = fn
	}
	if err := a.vayuMail.Accounts().AddContact(r.Context(), owner, addr, name); err != nil {
		writeOSFragment(w, `<button class="btn btn--xs btn--ghost" type="button" disabled>Could not save</button>`)
		return
	}
	writeOSFragment(w, `<button class="btn btn--xs vm-contact-saved" type="button" disabled>✓ Saved to contacts</button>`)
}

// contactSaveButton renders the one-click "Save contact" control for a message's
// sender/recipient, scoped to the mailbox currently in view.
func contactSaveButton(userKey, rawFrom string) string {
	name, addr := mailParseFrom(rawFrom)
	if addr == "" {
		addr = rawFrom
	}
	return `<button class="btn btn--xs vm-contact-add" type="button" hx-post="/os/vayumail/contacts/save" ` +
		hxVals("user", userKey, "email", addr, "name", name) +
		` hx-target="this" hx-swap="outerHTML" title="Save ` + html.EscapeString(addr) + ` to this mailbox's contacts">＋ Save contact</button>`
}

// composeContactsDatalistFor builds the native <datalist> the composer's To/Cc/Bcc
// inputs use for autocomplete, scoped to ONE mailbox's saved contacts only — a
// mailbox never sees another mailbox's address book. Replaces the old global
// directory-wide suggestion source.
func (a *App) composeContactsDatalistFor(ctx context.Context, owner string) string {
	var b strings.Builder
	b.WriteString(`<datalist id="vm-contacts">`)
	if owner != "" && a.vayuMail != nil && a.vayuMail.Accounts() != nil {
		if contacts, err := a.vayuMail.Accounts().SearchContacts(ctx, owner, "", 500); err == nil {
			for _, c := range contacts {
				b.WriteString(`<option value="` + html.EscapeString(c.Email) + `"`)
				if c.Name != "" {
					b.WriteString(` label="` + html.EscapeString(c.Name) + `"`)
				}
				b.WriteString(`>`)
			}
		}
	}
	b.WriteString(`</datalist>`)
	return b.String()
}
