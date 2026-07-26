// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_notify.go — the JSON feed behind the console's background new-mail
// notifier. The console has no server push for mail (inbound is a filesystem
// Maildir, not a live stream), so admin-os.js polls this endpoint on every page,
// diffs successive unseen counts per mailbox, and raises a desktop notification
// (click-through to that mailbox) when a count rises. Access mirrors the mailbox
// pages exactly: an admin sees every mailbox across the primary and any
// mail-enabled secondary domain; a non-admin sees only their own assigned box.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/users"
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
			// Cheap readdir-only counts (Summaries), never reading a message body.
			if primary, err := a.vayuMail.Summaries(); err == nil {
				add(primary)
			}
			for _, sh := range a.mailSecondaryHosts(r.Context()) {
				if sb, err := a.vayuMail.SummariesForDomain(sh); err == nil {
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
			if boxes, err := a.vayuMail.SummariesForDomain(dom); err == nil {
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

// mailUnseenForViewer totals the unseen mail across the mailboxes the viewer may
// see and returns that total plus the deep-link the topbar bell row should open.
// Scope mirrors the mailbox pages exactly: an admin sees every mailbox on the
// primary and each mail-enabled secondary domain; a staff member sees only their
// own assigned mailbox. Counting is readdir-only (Summaries), so this is cheap
// enough to run on every page render. Best-effort: any error yields (0, "").
func (a *App) mailUnseenForViewer(ctx context.Context, s *osSettings) (total int, href string) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		return 0, ""
	}
	primaryDom := a.vayuMail.Config().Domain

	// Determine scope. A real signed-in admin (not a mail-only account) sees every
	// mailbox; an API-key/no-session caller is admin-equivalent (like the console
	// shell). Everyone else is scoped to their own assigned mailbox.
	admin := true
	ownKey := ""
	if v := ctx.Value(ctxUserKey); v != nil {
		if u, ok := v.(*users.User); ok && u != nil {
			admin = u.Role == users.RoleAdmin && !s.MailOnly
			if !admin {
				email := strings.TrimSpace(u.MailAddress)
				if a.userStore != nil {
					if fresh, err := a.userStore.GetByID(ctx, u.ID); err == nil {
						email = strings.TrimSpace(fresh.MailAddress)
					}
				}
				if email == "" {
					return 0, ""
				}
				ownKey = email
				if at := strings.LastIndex(email, "@"); at >= 0 && strings.EqualFold(email[at+1:], primaryDom) {
					ownKey = email[:at] // bare local part on the primary domain
				}
			}
		}
	}

	if admin {
		if boxes, err := a.vayuMail.Summaries(); err == nil {
			for _, b := range boxes {
				total += b.Unseen
			}
		}
		for _, sh := range a.mailSecondaryHosts(ctx) {
			if boxes, err := a.vayuMail.SummariesForDomain(sh); err == nil {
				for _, b := range boxes {
					total += b.Unseen
				}
			}
		}
		return total, "/os/vayumail/inbox"
	}

	dom, user := primaryDom, ownKey
	if at := strings.LastIndex(ownKey, "@"); at >= 0 {
		user, dom = ownKey[:at], ownKey[at+1:]
	}
	if boxes, err := a.vayuMail.SummariesForDomain(dom); err == nil {
		for _, b := range boxes {
			if strings.EqualFold(b.Username, user) {
				total += b.Unseen
				break
			}
		}
	}
	return total, "/os/vayumail/inbox?user=" + qparam(ownKey)
}
