// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_contactgroups.go — the Contacts panel's groups
// (internal/vayuos/mail/contactgroups.go), and its address book in and out
// as vCards (vcard.go). Each request names its mailbox through
// contactOwner, as every other change to the address book does.

import (
	"context"
	"io"
	"net/http"
	"strings"
	"unicode"

	"github.com/johalputt/vayupress/internal/i18n"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

const (
	// maxGroupForm bounds a group's form: 500 addresses of a few dozen bytes.
	maxGroupForm = 256 << 10
	// maxVCardFile bounds an imported address book. A few thousand cards
	// with photos left out fit well inside it; photos are what make an
	// export large, and none is kept.
	maxVCardFile = 4 << 20
)

// contactsTransfer is the address book's Export and Import, with what an
// import did said under them.
func contactsTransfer(lang, userKey, said string) string {
	var b strings.Builder
	b.WriteString(`<div class="vm-contacts-transfer">` +
		`<a class="btn btn--sm" href="/os/vayumail/contacts/export?user=` + qparam(userKey) + `" download>` + esc(i18n.T(lang, "Export as vCard")) + `</a>` +
		`<form class="vm-contacts-import" hx-post="/os/vayumail/contacts/import" hx-encoding="multipart/form-data" hx-trigger="change" hx-target="#vm-contacts-panel" hx-swap="outerHTML">` +
		`<input type="hidden" name="user" value="` + esc(userKey) + `">` +
		`<label class="btn btn--sm">` + esc(i18n.T(lang, "Import vCard")) + `<input class="vp-sr-only" type="file" name="file" accept=".vcf,text/vcard,text/x-vcard"></label></form></div>`)
	if said != "" {
		b.WriteString(`<p class="vm-keys-said" role="status">` + esc(said) + `</p>`)
	}
	return b.String()
}

// groupsSection is the panel's groups, each opening to its name and people
// to change, and New group.
func (a *App) groupsSection(ctx context.Context, owner, userKey, said string) string {
	groups, err := a.vayuMail.Accounts().ContactGroups(ctx, owner)
	if err != nil {
		return ""
	}
	lang := i18n.Lang(ctx)
	people := func(n int) string { return i18n.T(lang, "{n, plural, one {# person} other {# people}}", "n", n) }
	form := func(g vmail.ContactGroup) string {
		buttons := `<button class="btn btn--sm" type="submit" name="action" value="save">` + esc(i18n.T(lang, "Save")) + `</button>`
		if g.Name != "" {
			buttons += `<button class="btn btn--sm btn--ghost" type="submit" name="action" value="delete" hx-confirm="` + esc(i18n.T(lang, "Delete the group {group}? Its people stay in your contacts.", "group", g.Name)) + `">` + esc(i18n.T(lang, "Delete")) + `</button>`
		}
		return `<form class="vm-group__form" hx-post="/os/vayumail/contacts/groups" hx-target="#vm-contacts-panel" hx-swap="outerHTML">` +
			`<input type="hidden" name="user" value="` + esc(userKey) + `"><input type="hidden" name="was" value="` + esc(g.Name) + `">` +
			`<input class="input input--sm" name="name" value="` + esc(g.Name) + `" required maxlength="40" aria-label="` + esc(i18n.T(lang, "Group name")) + `" placeholder="` + esc(i18n.T(lang, "Group name")) + `" autocomplete="off">` +
			`<textarea class="input" name="members" rows="4" spellcheck="false" aria-label="` + esc(i18n.T(lang, "People in the group, one address a line")) + `" placeholder="` + esc(i18n.T(lang, "One address a line")) + `">` + esc(strings.Join(g.Members, "\n")) + `</textarea>` +
			`<div class="vm-group__buttons">` + buttons + `</div></form>`
	}
	var b strings.Builder
	b.WriteString(`<div class="vm-contacts-head vm-groups-head"><h3 class="vm-contacts-title">` + esc(i18n.T(lang, "Groups")) + `</h3><span class="muted text-sm">` + itoaSafe(len(groups)) + `</span></div>`)
	if said != "" {
		b.WriteString(`<p class="vm-keys-said" role="status">` + esc(said) + `</p>`)
	}
	if len(groups) > 0 {
		b.WriteString(`<ul class="vm-contacts-list">`)
		for _, g := range groups {
			b.WriteString(`<li class="vm-contact vm-group"><details><summary class="vm-group__sum"><span class="vm-contact-name">` + esc(g.Name) + `</span>` +
				`<span class="vm-contact-mail">` + esc(people(len(g.Members))) + `</span></summary>` + form(g) + `</details></li>`)
		}
		b.WriteString(`</ul>`)
	}
	b.WriteString(`<details class="vm-keys-add"><summary class="btn btn--sm">` + esc(i18n.T(lang, "New group")) + `</summary>` + form(vmail.ContactGroup{}) + `</details>`)
	return b.String()
}

// handleVayuOSContactGroups saves a group (renaming it when its name was
// changed) or deletes one, and redraws the panel with what happened.
func (a *App) handleVayuOSContactGroups(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxGroupForm)
	userKey := sanitizeMailUser(strings.TrimSpace(r.PostFormValue("user")))
	owner, ok := a.contactOwner(r, userKey)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "no authorized mailbox", "")
		return
	}
	ctx, store := r.Context(), a.vayuMail.Accounts()
	lang := i18n.Lang(ctx)
	name, was := strings.TrimSpace(r.PostFormValue("name")), r.PostFormValue("was")
	var said string
	switch r.PostFormValue("action") {
	case "save":
		members := strings.FieldsFunc(r.PostFormValue("members"), func(c rune) bool { return c == ',' || c == ';' || unicode.IsSpace(c) })
		if err := store.SaveContactGroup(ctx, owner, name, members); err != nil {
			said = i18n.T(lang, "Not saved: {reason}.", "reason", err.Error())
			break
		}
		if was != "" && !strings.EqualFold(was, name) {
			_ = store.DeleteContactGroup(ctx, owner, was)
		}
		said = i18n.T(lang, "Saved {group}.", "group", name)
	case "delete":
		if err := store.DeleteContactGroup(ctx, owner, was); err != nil {
			said = i18n.T(lang, "Not deleted: {reason}.", "reason", err.Error())
			break
		}
		said = i18n.T(lang, "Deleted {group}; its people are still in your contacts.", "group", was)
	default:
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "unknown action", "")
		return
	}
	writeOSFragment(w, a.contactsPanel(r, owner, userKey, "", "", "", contactsSaid{groups: said}))
}

// handleVayuOSContactsImport brings a vCard file into the address book and
// redraws the panel with what it did.
func (a *App) handleVayuOSContactsImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxVCardFile+(64<<10))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeAPIError(w, r, http.StatusRequestEntityTooLarge, "too-large", i18n.T(i18n.Lang(r.Context()), "A vCard file of at most 4 MB can be brought in."), "")
		return
	}
	userKey := sanitizeMailUser(strings.TrimSpace(r.PostFormValue("user")))
	owner, ok := a.contactOwner(r, userKey)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "no authorized mailbox", "")
		return
	}
	said := ""
	f, _, err := r.FormFile("file")
	if err != nil {
		said = i18n.T(i18n.Lang(r.Context()), "Not imported: no file was chosen.")
	} else {
		defer f.Close()
		said = a.importVCards(r.Context(), owner, f)
	}
	writeOSFragment(w, a.contactsPanel(r, owner, userKey, "", "", "", contactsSaid{book: said}))
}

// importVCards reads f into owner's address book and says what it did.
func (a *App) importVCards(ctx context.Context, owner string, f io.Reader) string {
	lang := i18n.Lang(ctx)
	data, err := io.ReadAll(f)
	if err != nil {
		return i18n.T(lang, "Not imported: the file could not be read.")
	}
	cards, err := vmail.ParseVCards(data)
	if err != nil {
		return i18n.T(lang, "Not imported: {reason}.", "reason", err.Error())
	}
	res, err := a.vayuMail.Accounts().ImportContacts(ctx, owner, cards)
	if err != nil {
		return i18n.T(lang, "Not imported: {reason}.", "reason", err.Error())
	}
	said := i18n.T(lang, "{n, plural, one {Imported # address.} other {Imported # addresses.}}", "n", res.Saved)
	if res.Skipped > 0 {
		said += " " + i18n.T(lang, "{n} passed over: not an address, or none on the card.", "n", res.Skipped)
	}
	if len(res.GroupsLeftOut) > 0 {
		said += " " + i18n.T(lang, "Groups not made, for a name other than letters, digits, spaces, '-' and '_', or past 50 groups or 500 people: {groups}.", "groups", strings.Join(res.GroupsLeftOut, ", "))
	}
	return said
}

// handleVayuOSContactsExport downloads the address book as a vCard file.
func (a *App) handleVayuOSContactsExport(w http.ResponseWriter, r *http.Request) {
	owner, ok := a.contactOwner(r, mailUserParam(r))
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "no authorized mailbox", "")
		return
	}
	cards, err := a.vayuMail.Accounts().ContactsAsVCards(r.Context(), owner)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "contacts-unreadable", i18n.T(i18n.Lang(r.Context()), "The contacts could not be read."), "")
		return
	}
	w.Header().Set("Content-Type", "text/vcard; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="contacts.vcf"`)
	w.Header().Set("Cache-Control", "no-store")
	_ = vmail.WriteVCards(w, cards)
}
