// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_scheduled.go — the mailbox's Scheduled list: what Send later is
// holding, with Send now and Cancel.

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/ui"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// scheduledFolder names the list in the sidebar and in ?folder=. It is not a
// Maildir folder (the held messages are rows, internal/vayuos/mail/
// scheduled.go), so it is answered before any folder read: an unknown folder
// name reads as Inbox there.
const scheduledFolder = "Scheduled"

func isScheduledFolder(f string) bool { return strings.EqualFold(f, scheduledFolder) }

// scheduledOwner is the mailbox address rd reads, which is whose held
// messages it may see and act on; "" when rd carries no authority.
func (a *App) scheduledOwner(rd vmail.Reader) string {
	if rd.Key() == "" {
		return ""
	}
	return mailAddrOf(rd.Key(), a.vayuMail.Config().Domain)
}

// scheduledCount is how many messages rd's mailbox has held, for the sidebar.
func (a *App) scheduledCount(ctx context.Context, rd vmail.Reader) int {
	owner := a.scheduledOwner(rd)
	if owner == "" {
		return 0
	}
	list, err := a.vayuMail.ScheduledFor(ctx, owner)
	if err != nil {
		return 0
	}
	return len(list)
}

// vayuScheduledBody is the Scheduled list as #vm-inbox-list holds it. note,
// when set, is the outcome of the action that redrew it.
func (a *App) vayuScheduledBody(ctx context.Context, rd vmail.Reader, note string) string {
	user := rd.Key()
	list, err := a.vayuMail.ScheduledFor(ctx, a.scheduledOwner(rd))
	var b strings.Builder
	b.WriteString(`<h1 class="vp-sr-only">` + esc(a.scheduledOwner(rd)) + ` · Scheduled</h1>`)
	b.WriteString(`<header class="mx-head"><div class="mx-head__main"><button type="button" class="mx-back mx-back--boxes" data-mx-go="boxes">` + saIcon("chev-l") + `Mailboxes</button><h2 class="mx-head__title">Scheduled</h2>`)
	if len(list) > 0 {
		b.WriteString(`<span class="mx-head__note">` + itoaSafe(len(list)) + ` waiting to send</span>`)
	}
	b.WriteString(`</div></header>`)
	// A message leaves the list when it goes, so the list is redrawn while
	// open, as a folder is for new mail.
	b.WriteString(`<div class="vm-poller" aria-hidden="true" hx-get="/os/vayumail/inbox/fragment?user=` + qparam(user) + `&amp;folder=` + scheduledFolder + `" hx-trigger="every 60s, vm-mail-changed from:body" hx-target="#vm-inbox-list" hx-swap="innerHTML" hx-indicator="#vm-inbox-spin"></div>`)
	if note != "" {
		b.WriteString(`<div class="mx-banner" role="alert">` + saIcon("warn") + ` <span>` + esc(note) + `</span></div>`)
	}
	switch {
	case err != nil:
		b.WriteString(`<div class="empty-state">Could not read what is scheduled: ` + esc(err.Error()) + `</div>`)
		return b.String()
	case len(list) == 0:
		b.WriteString(string(ui.Empty("calendar", "Nothing is scheduled", "A message you send later waits here until its time, and can be sent now or cancelled until then.", "")))
		return b.String()
	}
	avSet := a.mailboxAvatarSet()
	b.WriteString(`<ol class="mx-list" aria-label="Scheduled messages">`)
	for _, s := range list {
		b.WriteString(scheduledRow(user, s, avSet))
	}
	b.WriteString(`</ol>`)
	return b.String()
}

// scheduledRow is one held message: to whom, when it goes (or why it did
// not), and its two actions.
func scheduledRow(user string, s vmail.ScheduledMessage, avSet map[string]bool) string {
	subj := s.Subject
	if subj == "" {
		subj = "(no subject)"
	}
	who := strings.Join(s.To, ", ")
	when := `<time class="mx-row__time" datetime="` + s.Due.UTC().Format(time.RFC3339) + `">` + esc(config.FormatSite(s.Due, "Mon 2 Jan, 15:04 MST")) + `</time>`
	line := "Sends " + config.FormatSite(s.Due, "Monday 2 January at 15:04 MST")
	if s.State == vmail.ScheduledFailed {
		when = string(ui.State("warn", "Not sent"))
		line = s.LastError
	}
	act := func(action, label, cls string) string {
		return `<button type="button" class="btn btn--sm` + cls + `" hx-post="/os/vayumail/scheduled/action" hx-vals='{"user":"` + esc(user) + `","id":"` + strconv.FormatInt(s.ID, 10) + `","action":"` + action + `"}' hx-target="#vm-inbox-list" hx-swap="innerHTML" hx-indicator="#vm-inbox-spin">` + label + `</button>`
	}
	var b strings.Builder
	b.WriteString(`<li class="mx-row mx-row--held" data-vm-row><span class="mx-row__gutter"><span class="mx-row__dot" aria-hidden="true"></span></span>`)
	b.WriteString(`<div class="mx-row__open">` + mailAvatarImg(firstOf(s.To), avSet) + `<span class="mx-row__main">`)
	b.WriteString(`<span class="mx-row__top"><span class="mx-row__who">` + esc(mailDisplay(who)) + `</span>` + when + `</span>`)
	b.WriteString(`<span class="mx-row__subj"><span class="mx-row__subj-text">` + esc(subj) + `</span></span>`)
	b.WriteString(`<span class="mx-row__pv">` + esc(line) + `</span>`)
	b.WriteString(`<span class="mx-row__acts">` + act("send", "Send now", "") + act("cancel", "Cancel", " btn--ghost") + `</span>`)
	b.WriteString(`</span></div></li>`)
	return b.String()
}

func firstOf(list []string) string {
	if len(list) == 0 {
		return ""
	}
	return list[0]
}

// handleVayuOSScheduledAction is Send now or Cancel on one held message, for
// the mailbox the request may read (mailReader, ADR-0152), and redraws the
// list. Cancel files the message back in Drafts.
func (a *App) handleVayuOSScheduledAction(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	rd := a.mailReader(r, strings.TrimSpace(r.PostFormValue("user")))
	owner := a.scheduledOwner(rd)
	if owner == "" {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "No mailbox you can act on was named.", "")
		return
	}
	id, _ := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("id")), 10, 64)
	if id <= 0 {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "id required", "")
		return
	}
	var err error
	note := ""
	switch r.PostFormValue("action") {
	case "send":
		err = a.vayuMail.SendScheduledNow(r.Context(), owner, id)
	case "cancel":
		_, err = a.vayuMail.CancelScheduled(r.Context(), owner, id)
	default:
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "unknown action", "")
		return
	}
	switch {
	case errors.Is(err, vmail.ErrScheduledBusy):
		note = "That message is being sent now, so it can no longer be changed."
	case err != nil:
		note = "Not done: " + err.Error()
	}
	writeOSFragment(w, a.vayuScheduledBody(r.Context(), rd, note)+
		a.mailNavFor(rd, scheduledFolder, "", nil, true))
}
