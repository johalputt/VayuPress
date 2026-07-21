package main

// vayuos_mail_notify.go — the JSON feed behind the console's background new-mail
// notifier. The console has no server push for mail (inbound is a filesystem
// Maildir, not a live stream), so admin-os.js polls this endpoint on every page,
// diffs successive unseen counts per mailbox, and raises a desktop notification
// (click-through to that mailbox) when a count rises. Access mirrors the mailbox
// pages exactly: an admin sees every mailbox across the primary and any
// mail-enabled secondary domain; a non-admin sees only their own assigned box.

import (
	"encoding/json"
	"net/http"
	"strings"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// unseenBox is one mailbox's live counts plus the deep-link key the notifier uses
// to open it. Key is the same ?user= value the mailbox directory links to: a bare
// local part on the primary domain, or the full local@domain on a secondary
// (VayuDomains) so the read path resolves that domain's own Maildir.
type unseenBox struct {
	Key     string `json:"key"`
	Address string `json:"address"`
	Unseen  int    `json:"unseen"`
	Total   int    `json:"total"`
}

// handleVayuOSUnseen returns per-mailbox unseen counts as JSON for the console's
// new-mail poller. It is deliberately read-only and cheap to call repeatedly.
func (a *App) handleVayuOSUnseen(w http.ResponseWriter, r *http.Request) {
	out := []unseenBox{}
	if a.vayuMail != nil && a.vayuMail.Config().Enabled {
		primaryDom := a.vayuMail.Config().Domain
		add := func(boxes []vmail.MailboxSummary) {
			for _, b := range boxes {
				dom := b.Domain
				if dom == "" {
					dom = primaryDom
				}
				addr := b.Username + "@" + dom
				key := b.Username
				if !strings.EqualFold(dom, primaryDom) {
					key = addr
				}
				out = append(out, unseenBox{Key: key, Address: addr, Unseen: b.Unseen, Total: b.Total})
			}
		}
		if a.isAdminRequest(r) {
			// Admin: every mailbox on the primary, then each mail-enabled secondary.
			if primary, err := a.vayuMail.Mailboxes(); err == nil {
				add(primary)
			}
			for _, sh := range a.mailSecondaryHosts(r.Context()) {
				if sb, err := a.vayuMail.MailboxesForDomain(sh); err == nil {
					add(sb)
				}
			}
		} else if key := a.ownMailboxKey(r); key != "" {
			// Non-admin: only their own assigned mailbox. Resolve its domain from the
			// key (bare local ⇒ primary; local@domain ⇒ that secondary) and emit one.
			dom, user := primaryDom, key
			if at := strings.LastIndex(key, "@"); at >= 0 {
				user, dom = key[:at], key[at+1:]
			}
			if boxes, err := a.vayuMail.MailboxesForDomain(dom); err == nil {
				for _, b := range boxes {
					if strings.EqualFold(b.Username, user) {
						out = append(out, unseenBox{Key: key, Address: mailAddrOf(key, primaryDom), Unseen: b.Unseen, Total: b.Total})
						break
					}
				}
			}
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(out)
}
