// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_import.go — Bring mail in: copy a mailbox here from another
// provider over IMAP (internal/vayuos/mail/imapimport.go), with its progress.

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/pace"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/ui"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// importPacing paces the copy: a few messages at a time to start, up to the
// fifty a fetch takes, and a floor so a busy host still sees it finish.
var importPacing = pace.JobConfig{Min: 2, Max: 50, Step: 4, Floor: true}

// handleVayuOSMailImport is the Bring mail in page for the mailbox the
// request may change (mailReader, ADR-0152).
func (a *App) handleVayuOSMailImport(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() {
		a.writeMailSetup(w, r, "Bring mail in")
		return
	}
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	csrfTokenFor(w, r)
	rd := a.mailReader(r, mailUserParam(r))
	body := ui.HTML(`<div id="mx-import">` + a.mailImportBody(r.Context(), rd, "") + `</div>`)
	writeOSHTML(w, r, adminOSLayout(nonce, "Bring mail in", "vayuos", cfg, body))
}

// handleVayuOSMailImportFragment redraws the page while an import runs.
func (a *App) handleVayuOSMailImportFragment(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() {
		writeOSFragment(w, "")
		return
	}
	writeOSFragment(w, a.mailImportBody(r.Context(), a.mailReader(r, mailUserParam(r)), ""))
}

// handleVayuOSMailImportAction starts or stops an import and redraws the
// page. An operator's import into someone's mailbox is written to its
// access record, where its holder sees it.
func (a *App) handleVayuOSMailImportAction(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	rd := a.mailReader(r, strings.TrimSpace(r.PostFormValue("user")))
	if rd.Key() == "" {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "No mailbox you can change was named.", "")
		return
	}
	note := ""
	switch r.PostFormValue("action") {
	case "start":
		port, _ := strconv.Atoi(r.PostFormValue("port"))
		req := vmail.ImportRequest{Host: r.PostFormValue("host"), Port: port, Username: r.PostFormValue("username"), Password: r.PostFormValue("password")}
		if rd.IsOperator() {
			addr := mailAddrOf(rd.Key(), a.vayuMail.Config().Domain)
			if err := a.vayuMail.AppendLedger(r.Context(), addr, rd.Actor(), "import", "mail was brought in from "+strings.TrimSpace(req.Host)); err != nil {
				note = "Not started: it could not be recorded."
				break
			}
		}
		if err := a.vayuMail.StartImport(rd, req, pace.Host().NewJob(importPacing)); err != nil {
			note = "Not started: " + err.Error() + "."
		}
	case "stop":
		if err := a.vayuMail.StopImport(rd); err != nil {
			note = "Not stopped: " + err.Error() + "."
		}
	default:
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "unknown action", "")
		return
	}
	writeOSFragment(w, a.mailImportBody(r.Context(), rd, note))
}

// mailImportBody is the page: what the last import did or is doing, and
// while none runs, where to bring mail in from.
func (a *App) mailImportBody(ctx context.Context, rd vmail.Reader, note string) string {
	st, err := a.vayuMail.ImportStatusFor(rd)
	if err != nil {
		return string(ui.Status(ui.StatusPage{Title: "Bring mail in", Tone: "danger", State: "This mailbox cannot be read from here"}))
	}
	user := rd.Key()
	vals := func(action string) string {
		return ` hx-vals='{"user":"` + esc(user) + `","action":"` + action + `"}'`
	}
	page := ui.StatusPage{Title: "Bring mail in", Tone: "neutral", State: "Copy your mail here from another provider",
		Detail: ui.Text("Every folder, with what you have read, in the background at the server's pace. Nothing is deleted there.")}
	running := st != nil && st.State == vmail.ImportRunning
	if st != nil {
		n := itoaSafe(st.Copied) + " " + map[bool]string{true: "message", false: "messages"}[st.Copied == 1]
		switch st.State {
		case vmail.ImportRunning:
			page.State = "Bringing mail in from " + st.Host
			detail := n + " copied so far"
			if st.Folder != "" {
				detail += ", now " + st.Folder
			}
			page.Detail = ui.Text(detail + ".")
			page.Actions = ui.HTML(`<button type="button" class="btn" hx-post="/os/vayumail/import/action"` + vals("stop") + ` hx-target="#mx-import" hx-swap="innerHTML">Stop</button>`)
		case vmail.ImportDone:
			page.Tone, page.State = "ok", "Brought in "+n+" from "+st.Host
			page.Detail = ui.Text("Finished " + humanAgo(st.Updated, time.Now()) + ". Bringing mail in again copies only what has arrived there since.")
		case vmail.ImportStopped:
			page.State = "Stopped after " + n + " from " + st.Host
			page.Detail = ui.Text("Bring mail in again to carry on from there.")
		default:
			page.Tone, page.State = "warn", "Bringing mail in from "+st.Host+" stopped after "+n
			page.Detail = ui.Text(st.Error)
		}
		if st.Failed > 0 {
			page.Detail += ui.Text(" " + itoaSafe(st.Failed) + " could not be copied (larger than this server takes).")
		}
	}
	var b strings.Builder
	if note != "" {
		b.WriteString(string(ui.Callout("warn", ui.Text(note))))
	}
	if running {
		// Redrawn while it runs, and no longer once it ends.
		b.WriteString(`<div aria-hidden="true" hx-get="/os/vayumail/import/fragment?user=` + qparam(user) + `" hx-trigger="every 3s" hx-target="#mx-import" hx-swap="innerHTML"></div>`)
		return string(ui.Status(page, ui.HTML(b.String())))
	}
	host, username := "", ""
	if st != nil {
		host, username = st.Host, st.Username
	}
	if a.vayuMail.ReaderReadOnly(rd) {
		return string(ui.Status(page, ui.HTML(b.String()), ui.Callout("neutral", ui.Text("This mailbox is read-only, so mail cannot be brought into it."))))
	}
	field := func(name, typ, value, placeholder, extra string) ui.HTML {
		return ui.HTML(`<input class="input" name="` + name + `" type="` + typ + `" value="` + esc(value) + `" placeholder="` + esc(placeholder) + `" required autocomplete="off"` + extra + `>`)
	}
	form := `<form hx-post="/os/vayumail/import/action" hx-target="#mx-import" hx-swap="innerHTML"><input type="hidden" name="user" value="` + esc(user) + `"><input type="hidden" name="action" value="start">` +
		string(ui.Rows(
			ui.Row{Label: "Server", Hint: "Its IMAP server, as the provider's help names it", Control: field("host", "text", host, "imap.gmail.com", ` spellcheck="false"`)},
			ui.Row{Label: "Connection", Control: ui.HTML(`<select class="input" name="port"><option value="993">TLS, port 993</option><option value="143">STARTTLS, port 143</option></select>`)},
			ui.Row{Label: "User name", Hint: "Usually your address there", Control: field("username", "text", username, "you@gmail.com", ` spellcheck="false"`)},
			ui.Row{Label: "Password", Hint: "Gmail, Outlook and iCloud need an app password and IMAP turned on, not your usual password. It is used for this copy and not kept.", Control: field("password", "password", "", "", "")},
		)) + `<div><button type="submit" class="btn btn--primary">Bring mail in</button></div></form>`
	b.WriteString(string(ui.Section("Bring it in from", "", ui.HTML(form))))
	b.WriteString(string(ui.Disclosure(ui.HTML(saIcon("info")), "What comes across", "", "", false, ui.Text(
		"Inbox, Sent, Drafts, Junk, Trash and Archive come into theirs here; any other folder becomes one of your own, by its name there. "+
			"Gmail's All Mail and Starred are left out, since they repeat mail kept in other folders; its labels arrive as folders, so a message with two labels arrives twice. "+
			"What you have read and pinned there is read and pinned here. Running it again copies only what is new."))))
	return string(ui.Status(page, ui.HTML(b.String())))
}
