// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_throwaway.go — a holder's throwaway addresses on their
// Settings (internal/vayuos/mail/throwaway.go keeps them): make one for a
// website in a click, see which take mail, turn one off, remove it.

import (
	"errors"
	"github.com/johalputt/vayupress/internal/i18n"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// throwawayBlock is the mailbox rd reads' throwaway addresses and the form
// that makes one.
func (a *App) throwawayBlock(rd vmail.Reader) string {
	lang := rd.Lang()
	list, _ := a.vayuMail.Throwaways(rd)
	post := ` hx-post="/os/vayumail/throwaway" hx-target="#vm-throwaways" hx-swap="outerHTML"`
	var b strings.Builder
	b.WriteString(`<div id="vm-throwaways"><p class="text-sm muted">` + esc(i18n.T(lang, "An address of its own for each website that asks for one. Its mail comes here; when a website passes the address on, turn it off.")) + `</p>` +
		`<form class="vm-row"` + post + `><input type="hidden" name="op" value="new"><input class="input input--sm vm-grow" name="label" maxlength="60" placeholder="` + esc(i18n.T(lang, "Which website is it for?")) + `" aria-label="` + esc(i18n.T(lang, "Which website is it for?")) + `" autocomplete="off"><button class="btn btn--sm" type="submit">` + esc(i18n.T(lang, "Make an address")) + `</button></form>`)
	if len(list) > 0 {
		b.WriteString(`<ul class="mx-throwaways">`)
		for _, t := range list {
			meta := i18n.T(lang, "No mail yet")
			if t.Received > 0 {
				meta = i18n.T(lang, "{n, plural, one {# message, the last on {date}} other {# messages, the last on {date}}}",
					"n", t.Received, "date", i18n.Date(lang, config.InSite(t.LastReceived), i18n.DayMonthShort))
			}
			if t.Label != "" {
				meta = t.Label + " · " + meta
			}
			op, verb := "off", i18n.T(lang, "Turn off")
			if t.Off {
				op, verb, meta = "on", i18n.T(lang, "Turn on"), meta+" · "+i18n.T(lang, "Off")
			}
			b.WriteString(`<li` + map[bool]string{true: ` class="is-off"`, false: ""}[t.Off] + `><span class="mx-throwaways__what"><span class="mx-throwaways__addr">` + esc(t.Address) + `</span><span class="mx-throwaways__meta">` + esc(meta) + `</span></span>` +
				`<button type="button" class="btn btn--sm"` + post + hxVals("op", op, "address", t.Address) + `>` + esc(verb) + `</button>` +
				`<button type="button" class="btn btn--ghost btn--sm"` + post + hxVals("op", "remove", "address", t.Address) + ` hx-confirm="` + esc(i18n.T(lang, "Remove {address}? Mail to it will be refused.", "address", t.Address)) + `">` + esc(i18n.T(lang, "Remove")) + `</button></li>`)
		}
		b.WriteString(`</ul>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// handleVayuOSThrowaway makes, turns off or on, or removes a throwaway
// address of the session's own mailbox, and answers with the block again.
func (a *App) handleVayuOSThrowaway(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-off", "VayuMail is not active.", "")
		return
	}
	rd := a.mailReader(r, "")
	if rd.Key() == "" {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(rd.Lang(), "This session has no mailbox of its own."), "")
		return
	}
	addr := strings.TrimSpace(r.PostFormValue("address"))
	var err error
	switch r.PostFormValue("op") {
	case "new":
		_, err = a.vayuMail.NewThrowaway(rd, r.PostFormValue("label"))
	case "off":
		err = a.vayuMail.SetThrowawayOff(rd, addr, true)
	case "on":
		err = a.vayuMail.SetThrowawayOff(rd, addr, false)
	case "remove":
		err = a.vayuMail.RemoveThrowaway(rd, addr)
	default:
		err = errors.New("unknown action")
	}
	if err != nil {
		setMailSaid(w, i18n.T(rd.Lang(), "Not done: {reason}.", "reason", strings.TrimPrefix(err.Error(), "vayumail: ")), true)
	}
	writeOSFragment(w, a.throwawayBlock(rd))
}
