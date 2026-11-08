// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_invite.go — an invitation in Mail: a card at the top of the
// message saying what, when, where, from whom, and what else is at that
// time, with Accept, Maybe and Decline. Answering puts the event in the
// holder's calendar with their answer and tells the organizer
// (internal/vayuos/mail/itip.go); nothing is in the calendar before. A
// cancellation's card takes the event out, when it came from its organizer.
//
// The card is the holder's alone: the engine reads no invitation for an
// administrator's reader, so one who opens the mailbox sees the message and
// no card.

import (
	"errors"
	"net/http"
	"strings"
	"time"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// inviteCard is the invitation card for a message, "" when it carries none
// for rd's holder.
func (a *App) inviteCard(rd vmail.Reader, folder, id string, raw []byte, home *time.Location, note string) string {
	inv, ok := a.vayuMail.InvitationIn(rd, raw, home)
	if !ok {
		return ""
	}
	ev := inv.Event
	org := esc(nonEmpty(inv.Organizer.Name, inv.Organizer.Email))
	var b strings.Builder
	b.WriteString(`<section class="mx-invite" id="mx-invite" aria-label="Invitation">`)
	if inv.Method == "CANCEL" {
		b.WriteString(`<p class="mx-invite__kind">` + saIcon("calendar") + `<span>Cancelled by ` + org + `</span></p>`)
	} else {
		b.WriteString(`<p class="mx-invite__kind">` + saIcon("calendar") + `<span>Invitation from ` + org + `</span></p>`)
	}
	b.WriteString(`<p class="mx-invite__title">` + esc(nonEmpty(ev.Summary, "(no title)")) + `</p>`)
	b.WriteString(`<p class="mx-invite__row">` + esc(calWhen(ev, home)) + `</p>`)
	if ev.Rule != "" {
		b.WriteString(`<p class="mx-invite__row">` + esc(repeatWords(ev.Rule, ev.Start)) + `</p>`)
	}
	if ev.Location != "" {
		b.WriteString(`<p class="mx-invite__row">` + saIcon("where") + `<span>` + esc(ev.Location) + `</span></p>`)
	}
	if inv.Method != "CANCEL" {
		// What else the holder has then, from their own calendar.
		var clash []string
		if evs, err := a.vayuMail.CalendarBetween(rd, ev.Start, ev.End.Add(time.Second), home); err == nil {
			for _, o := range evs {
				if o.UID != ev.UID && !o.AllDay {
					clash = append(clash, nonEmpty(o.Summary, "(no title)"))
				}
			}
		}
		if len(clash) > 0 {
			b.WriteString(`<p class="mx-invite__clash">At the same time: ` + esc(strings.Join(clash, ", ")) + `</p>`)
		}
	}
	if note != "" {
		b.WriteString(`<p class="mx-invite__note" role="status">` + note + `</p>`)
	}
	act := func(answer, label string) string {
		pressed := "false"
		if inv.Answer == answer {
			pressed = "true"
		}
		return `<button type="button" class="btn btn--sm" aria-pressed="` + pressed + `" hx-post="/os/vayumail/calendar/answer" hx-target="#mx-invite" hx-swap="outerHTML"` +
			hxVals("folder", folder, "id", id, "answer", answer) + `>` + label + `</button>`
	}
	b.WriteString(`<div class="mx-invite__acts">`)
	switch {
	case inv.Method == "CANCEL" && inv.Stored:
		b.WriteString(`<button type="button" class="btn btn--sm" hx-post="/os/vayumail/calendar/answer" hx-target="#mx-invite" hx-swap="outerHTML"` +
			hxVals("folder", folder, "id", id, "answer", "remove") + `>Remove from my calendar</button>`)
	case inv.Method == "CANCEL":
		b.WriteString(`<span class="muted">It is not in your calendar.</span>`)
	default:
		b.WriteString(act("ACCEPTED", "Accept") + act("TENTATIVE", "Maybe") + act("DECLINED", "Decline"))
	}
	b.WriteString(`</div></section>`)
	return b.String()
}

// handleVayuOSCalendarAnswer answers the invitation in one of the session's
// own messages, or removes a cancelled event, and redraws the card.
func (a *App) handleVayuOSCalendarAnswer(w http.ResponseWriter, r *http.Request) {
	rd, ok := a.calendarOwner(r)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "An invitation is answered only by its holder.", "")
		return
	}
	_ = r.ParseForm()
	folder, id := r.PostFormValue("folder"), r.PostFormValue("id")
	var err error
	note := ""
	switch answer := r.PostFormValue("answer"); answer {
	case "ACCEPTED", "TENTATIVE", "DECLINED":
		err = a.vayuMail.AnswerInvitation(rd, folder, id, answer)
		note = map[string]string{"ACCEPTED": "You are going; the organizer has been told.", "TENTATIVE": "You may go; the organizer has been told.", "DECLINED": "You are not going; the organizer has been told."}[answer]
	case "remove":
		err = a.vayuMail.RemoveCancelled(rd, folder, id)
		note = "Removed from your calendar."
	default:
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "unknown answer", "")
		return
	}
	if err != nil {
		note = "Not done: " + esc(calSaveRefusal(err)) + "."
		if errors.Is(err, vmail.ErrNotAnInvitation) {
			note = "This message carries no invitation for you."
		}
	}
	raw, rerr := a.vayuMail.ReadFolderMessage(rd, folder, id)
	if rerr != nil {
		writeOSHTML(w, r, saCallout("danger", "This message is no longer here."))
		return
	}
	writeOSHTML(w, r, a.inviteCard(rd, folder, id, raw, a.calendarHome(r), note))
}
