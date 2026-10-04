// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_download.go — Download all mail: the mailbox, every folder, as
// a zip of mbox files, streamed as it is written.

import (
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
)

// handleVayuOSMailDownload streams the mailbox the request may read
// (mailReader, ADR-0152; a handed-over mailbox refuses an operator). An
// operator's download is the largest read there is, so it is written to the
// mailbox's access record first, where its holder sees it; a holder's own is
// not.
func (a *App) handleVayuOSMailDownload(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	rd := a.mailReader(r, mailUserParam(r))
	if rd.Key() == "" {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "No mailbox you can read was named.", "")
		return
	}
	addr := mailAddrOf(rd.Key(), a.vayuMail.Config().Domain)
	if _, err := a.vayuMail.FoldersFor(rd); err != nil {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "This mailbox cannot be read from here.", "")
		return
	}
	if rd.IsOperator() {
		if err := a.vayuMail.AppendLedger(r.Context(), addr, rd.Actor(), "download", "every folder of this mailbox was downloaded"); err != nil {
			// No record, no download: an unrecorded operator read is the
			// thing the record exists to rule out.
			writeAPIError(w, r, http.StatusInternalServerError, "ledger", "The download could not be recorded, so it was not made.", "")
			return
		}
	}
	name := strings.NewReplacer("@", "-at-", "/", "-").Replace(addr) + "-mail-" + time.Now().UTC().Format("2006-01-02") + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if n, err := a.vayuMail.ArchiveMailbox(r.Context(), rd, w); err != nil {
		// The status is already sent; a cut-short zip fails to open, which
		// says enough to the person, and this says why to the operator.
		logging.LogError("vayumail", "mailbox download stopped after "+itoaSafe(n)+" messages", err.Error())
	}
}
