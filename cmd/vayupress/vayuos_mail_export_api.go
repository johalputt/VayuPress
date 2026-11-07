// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_export_api.go — Download all mail, for the VayuMail app: the
// holder's whole mailbox, every folder as mbox in one zip, the same archive
// the console's Settings → Import and export makes. A holder has to be able
// to take their mail out from the app as well as from the web before a
// mailbox can be sealed under a key the server does not hold: sealing first
// and exporting later is the order in which mail is lost for good.

import (
	"net/http"
	"strings"
	"sync"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// mailExportsRunning holds the mailboxes with an export streaming. The
// archive reads every message of every folder, so one mailbox gets one at a
// time: a client retrying in a loop, or a stolen device credential, cannot
// stack whole-mailbox reads on the disk.
var mailExportsRunning sync.Map

// handleMemberVayuMailExport streams the signed-in holder's mailbox. It signs
// in as every app endpoint that hands out mail does (vayuMailAppSignIn), and
// it only ever reads the caller's own mailbox: there is no parameter naming
// another.
func (a *App) handleMemberVayuMailExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.mailRunning() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	addr, ok := a.vayuMailAppSignIn(w, r)
	if !ok {
		return
	}
	rd := vmail.ReadAsOwner(mailKeyOf(addr, a.vayuMail.Config().Domain))
	if _, err := a.vayuMail.FoldersFor(rd); err != nil {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "This mailbox cannot be read from here.", "")
		return
	}
	if _, busy := mailExportsRunning.LoadOrStore(addr, struct{}{}); busy {
		w.Header().Set("Retry-After", "60")
		writeAPIError(w, r, http.StatusTooManyRequests, "export-running", "An export of this mailbox is already running. Try again when it has finished.", "")
		return
	}
	defer mailExportsRunning.Delete(addr)
	dbpkg.AuditLog("vayumail.export", addr, addr, "whole mailbox downloaded from the app")
	a.streamMailArchive(w, r, rd, addr)
}

// mailKeyOf is the mail engine's key for a mailbox address, the inverse of
// mailAddrOf: the local part for the primary domain, the whole address for
// any other, as the IMAP server keys a signed-in mailbox.
func mailKeyOf(addr, primaryDomain string) string {
	if at := strings.LastIndex(addr, "@"); at >= 0 && strings.EqualFold(addr[at+1:], primaryDomain) {
		return addr[:at]
	}
	return addr
}
