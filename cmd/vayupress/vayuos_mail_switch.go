// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"strings"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// handleVayuOSMailSwitcher is the mailbox switcher's list (Mail plan §4):
// every mailbox an administrator may open, grouped by domain with the primary
// first, each with its unread count and a check on the one open (?user=).
// It is fetched when the switcher opens, never with the page, so a closed
// switcher reads no mailbox. Anyone else has one mailbox and no switcher, and
// is answered with nothing.
func (a *App) handleVayuOSMailSwitcher(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() || !a.isAdminRequest(r) {
		writeOSFragment(w, "")
		return
	}
	primary := a.vayuMail.Config().Domain
	var doms []mailDomainBoxes
	// Readdir-only counts, the ones the new-mail poll reads.
	if boxes, err := a.vayuMail.Summaries(); err == nil {
		doms = append(doms, mailDomainBoxes{primary, boxes})
	}
	for _, h := range a.mailSecondaryHosts(r.Context()) {
		if boxes, err := a.vayuMail.SummariesForDomain(h); err == nil {
			doms = append(doms, mailDomainBoxes{h, boxes})
		}
	}
	writeOSFragment(w, mailSwitcherList(primary, mailUserParam(r), doms, a.mailboxAvatarSet()))
}

// mailDomainBoxes is one mail domain and its mailboxes.
type mailDomainBoxes struct {
	Name  string
	Boxes []vmail.MailboxSummary
}

// mailSwitcherList renders the switcher's groups and foot. open is the ?user=
// key of the mailbox on screen.
func mailSwitcherList(primary, open string, doms []mailDomainBoxes, avatars map[string]bool) string {
	var b strings.Builder
	total := 0
	for _, g := range doms {
		total += len(g.Boxes)
		b.WriteString(`<div class="mx-switch__group" role="group" aria-label="` + esc(g.Name) + `"><p class="mx-switch__dom"><span>` + esc(g.Name) + `</span><span>` + itoaSafe(len(g.Boxes)) + `</span></p>`)
		for _, bx := range g.Boxes {
			key, addr := mailboxRef(bx, g.Name, primary)
			cur := ""
			if strings.EqualFold(key, open) {
				cur = ` aria-current="true"`
			}
			b.WriteString(`<a class="mx-switch__row" role="menuitem" href="/os/vayumail/inbox?user=` + qparam(key) + `" data-mx-find="` + esc(strings.ToLower(addr)) + `"` + cur + `>` +
				mailAvatarImg(addr, avatars) + `<span class="mx-switch__addr">` + esc(addr) + `</span>`)
			if bx.Unseen > 0 {
				b.WriteString(`<span class="mx-switch__count" aria-label="` + itoaSafe(bx.Unseen) + ` unread">` + itoaSafe(bx.Unseen) + `</span>`)
			}
			if cur != "" {
				b.WriteString(`<span class="mx-switch__check" aria-hidden="true">` + saIcon("check") + `</span>`)
			}
			b.WriteString(`</a>`)
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`<p class="mx-switch__none" hidden>No mailbox matches that.</p>`)
	every := "All " + itoaSafe(total) + " mailboxes"
	if total == 1 {
		every = "The one mailbox"
	}
	b.WriteString(`<div class="mx-switch__foot"><a role="menuitem" href="/os/vayumail/inbox?all=1">` + esc(every) + `</a>` +
		`<a role="menuitem" href="/os/vayumail/accounts#new-mailbox">` + saIcon("plus") + `New mailbox</a></div>`)
	return b.String()
}
