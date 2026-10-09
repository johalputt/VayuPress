// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_labels.go — labels in Mail (internal/vayuos/mail/labels.go):
// the reader's Label menu and the message's labels under its subject, the
// labels on each list row, and the sidebar's Labels, each of which runs the
// search label:<name> across all mail (admin-os-mail.js reads ?search=).

import (
	"bytes"
	"net/http"
	"net/mail"
	"slices"
	"strings"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// labelSearchURL opens Mail on the messages that carry label.
func labelSearchURL(user, label string) string {
	return "/os/vayumail/inbox?user=" + qparam(user) + "&search=" + qparam(vmail.SearchQuery{Label: label}.String())
}

// mailLabelsGroup is the sidebar's labels. It is its own element so a new
// label reaches it out of band, the way saved searches do.
func mailLabelsGroup(user string, labels []string, oob bool) string {
	var b strings.Builder
	b.WriteString(`<div id="vm-labels"`)
	if oob {
		b.WriteString(` hx-swap-oob="true"`)
	}
	b.WriteString(`>`)
	if len(labels) > 0 {
		b.WriteString(`<div class="sa-appside__group">Labels</div>`)
		for _, l := range labels {
			b.WriteString(`<a class="sa-appside__item" href="` + esc(labelSearchURL(user, l)) + `">` + saIcon("tag") + `<span class="sa-appside__label">` + esc(l) + `</span></a>`)
		}
	}
	b.WriteString(`</div>`)
	return b.String()
}

// mailRowLabels is a list row's labels: words, not links, since the row is
// itself the link that opens the message.
func mailRowLabels(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<span class="mx-labels">`)
	for _, l := range labels {
		b.WriteString(`<span class="mx-label">` + esc(l) + `</span>`)
	}
	b.WriteString(`</span>`)
	return b.String()
}

// readerLabels is the open message's labels, each opening its search.
func readerLabels(user string, labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<p class="mx-labels">`)
	for _, l := range labels {
		b.WriteString(`<a class="mx-label" href="` + esc(labelSearchURL(user, l)) + `">` + esc(l) + `</a>`)
	}
	b.WriteString(`</p>`)
	return b.String()
}

// labelMenu is the reader's Label tool: every label in use, ticked when the
// message carries it, and a field for a new one.
func labelMenu(user, folder, id string, all, mine []string) string {
	vals := func(extra ...string) string {
		return hxVals(append([]string{"user", user, "folder", folder, "id", id}, extra...)...)
	}
	post := ` hx-post="/os/vayumail/labels/action" hx-target="#vm-readpane" hx-swap="innerHTML"`
	var b strings.Builder
	b.WriteString(`<details class="sa-pop"><summary class="mx-tool" title="Label" aria-label="Label">` + saIcon("tag") + `</summary><div class="sa-pop__panel sa-menu mx-labelmenu" role="menu">`)
	for _, l := range all {
		on := slices.ContainsFunc(mine, func(m string) bool { return strings.EqualFold(m, l) })
		next, checked := "1", "false"
		if on {
			next, checked = "0", "true"
		}
		b.WriteString(`<button type="button" class="sa-menu__item" role="menuitemcheckbox" aria-checked="` + checked + `"` + post + vals("label", l, "on", next) + `>` +
			saIcon("check") + `<span class="sa-menu__text">` + esc(l) + `</span></button>`)
	}
	b.WriteString(`<form class="mx-labelmenu__new"` + post + `><input type="hidden" name="user" value="` + esc(user) + `"><input type="hidden" name="folder" value="` + esc(folder) + `">` +
		`<input type="hidden" name="id" value="` + esc(id) + `"><input type="hidden" name="on" value="1">` +
		`<input class="input input--sm" name="label" required maxlength="30" placeholder="New label" aria-label="New label" autocomplete="off"><button class="btn btn--sm" type="submit">Add</button></form>`)
	b.WriteString(`</div></details>`)
	return b.String()
}

// handleVayuOSLabelsAction puts a label on the open message or takes it off,
// and answers with the message again, the sidebar's labels out of band, and
// word to the list that it changed.
func (a *App) handleVayuOSLabelsAction(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() {
		writeOSHTML(w, r, vayuReadpaneEmpty("VayuMail is not active."))
		return
	}
	rd := a.mailReader(r, sanitizeMailUser(strings.TrimSpace(r.PostFormValue("user"))))
	folder := sanitizeMailFolder(strings.TrimSpace(r.PostFormValue("folder")))
	id := sanitizeMailID(strings.TrimSpace(r.PostFormValue("id")))
	if rd.Key() == "" {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "No mailbox you can change was named.", "")
		return
	}
	// The Message-ID is read from the message itself, never taken from the
	// form, so a label can only land on a message this mailbox holds.
	raw, err := a.vayuMail.ReadFolderMessageStored(rd, folder, id)
	if err != nil {
		writeOSHTML(w, r, vayuReadpaneEmpty("Message not available."))
		return
	}
	msgID := ""
	if m, perr := mail.ReadMessage(bytes.NewReader(raw)); perr == nil {
		msgID = m.Header.Get("Message-ID")
	}
	if err := a.vayuMail.SetLabel(rd, msgID, r.PostFormValue("label"), r.PostFormValue("on") == "1"); err != nil {
		setMailSaid(w, "Not labelled: "+err.Error()+".", true)
	} else {
		w.Header().Add("HX-Trigger", "vm-mail-changed")
	}
	card, ok := a.vayuReaderCard(rd, folder, id, readerView{Pane: true, Peek: true})
	if !ok {
		card = vayuReadpaneEmpty("Message not available.")
	}
	labels, _ := a.vayuMail.Labels(rd)
	writeOSHTML(w, r, card+mailLabelsGroup(rd.Key(), labels, true))
}
