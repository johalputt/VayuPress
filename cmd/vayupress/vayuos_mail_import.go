// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_import.go — Bring mail in: copy a mailbox here from another
// provider over IMAP (internal/vayuos/mail/imapimport.go), with its progress.

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/i18n"
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
		a.writeMailSetup(w, r, i18n.T(i18n.Lang(r.Context()), "Bring mail in"))
		return
	}
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	csrfTokenFor(w, r)
	rd := a.mailReader(r, mailUserParam(r))
	// The page is the mailbox's, so its sidebar stays, with this current.
	if rd.Key() != "" {
		cfg.MailSide = a.mailSide(r, rd, "", "", nil)
		cfg.MailSide.Importing = true
	}
	body := ui.HTML(`<div id="mx-import">` + a.mailImportBody(r.Context(), rd, "") + `</div>`)
	writeOSHTML(w, r, adminOSLayout(nonce, i18n.T(i18n.Lang(r.Context()), "Bring mail in"), "vayuos", cfg, body))
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
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", i18n.T(i18n.Lang(r.Context()), "VayuMail is not active"), "")
		return
	}
	lang := i18n.Lang(r.Context())
	rd := a.mailReader(r, strings.TrimSpace(r.PostFormValue("user")))
	if rd.Key() == "" {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(lang, "No mailbox you can change was named."), "")
		return
	}
	note := ""
	switch r.PostFormValue("action") {
	case "start":
		// The record names the server from the form itself, never from the
		// request that also holds the password: nothing the record keeps (it
		// is hashed into the ledger's chain) shares a value with a secret.
		host := strings.TrimSpace(r.PostFormValue("host"))
		if rd.IsOperator() {
			addr := mailAddrOf(rd.Key(), a.vayuMail.Config().Domain)
			if err := a.vayuMail.AppendLedger(r.Context(), addr, rd.Actor(), "import", "mail was brought in from "+host); err != nil {
				note = i18n.T(lang, "Not started: it could not be recorded.")
				break
			}
		}
		port, _ := strconv.Atoi(r.PostFormValue("port"))
		req := vmail.ImportRequest{Host: host, Port: port, Username: r.PostFormValue("username"), Password: r.PostFormValue("password")}
		if err := a.vayuMail.StartImport(rd, req, pace.Host().NewJob(importPacing)); err != nil {
			note = i18n.T(lang, "Not started: {reason}.", "reason", err.Error())
		}
	case "stop":
		if err := a.vayuMail.StopImport(rd); err != nil {
			note = i18n.T(lang, "Not stopped: {reason}.", "reason", err.Error())
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
	lang := i18n.Lang(ctx)
	title := i18n.T(lang, "Bring mail in")
	st, err := a.vayuMail.ImportStatusFor(rd)
	if err != nil {
		return string(ui.Status(ui.StatusPage{Title: title, Tone: "danger", State: i18n.T(lang, "This mailbox cannot be read from here")}))
	}
	user := rd.Key()
	vals := func(action string) string {
		return ` hx-vals='{"user":"` + esc(user) + `","action":"` + action + `"}'`
	}
	page := ui.StatusPage{Title: title, Tone: "neutral", State: i18n.T(lang, "Copy your mail here from another provider"),
		Detail: ui.Text(i18n.T(lang, "Every folder, with what you have read, in the background at the server's pace. Nothing is deleted there."))}
	running := st != nil && st.State == vmail.ImportRunning
	if st != nil {
		switch st.State {
		case vmail.ImportRunning:
			page.State = i18n.T(lang, "Bringing mail in from {host}", "host", st.Host)
			var detail string
			switch {
			case st.Total > 0 && st.Folder != "":
				detail = i18n.T(lang, "{done} of {total} messages done, now {folder}.", "done", st.Copied+st.Failed, "total", st.Total, "folder", st.Folder)
			case st.Total > 0:
				detail = i18n.T(lang, "{done} of {total} messages done.", "done", st.Copied+st.Failed, "total", st.Total)
			case st.Folder != "":
				detail = i18n.T(lang, "{n, plural, one {# message copied so far, now {folder}.} other {# messages copied so far, now {folder}.}}", "n", st.Copied, "folder", st.Folder)
			default:
				detail = i18n.T(lang, "{n, plural, one {# message copied so far.} other {# messages copied so far.}}", "n", st.Copied)
			}
			page.Detail = ui.Text(detail)
			page.Actions = ui.HTML(`<button type="button" class="btn" hx-post="/os/vayumail/import/action"` + vals("stop") + ` hx-target="#mx-import" hx-swap="innerHTML">` + esc(i18n.T(lang, "Stop")) + `</button>`)
		case vmail.ImportDone:
			page.Tone, page.State = "ok", i18n.T(lang, "{n, plural, one {Brought in # message from {host}} other {Brought in # messages from {host}}}", "n", st.Copied, "host", st.Host)
			page.Detail = ui.Text(i18n.T(lang, "Finished {when}. Bringing mail in again copies only what has arrived there since.", "when", i18n.Date(lang, st.Updated, i18n.DayMonthYearShortAt)))
		case vmail.ImportStopped:
			page.State = i18n.T(lang, "{n, plural, one {Stopped after # message from {host}} other {Stopped after # messages from {host}}}", "n", st.Copied, "host", st.Host)
			page.Detail = ui.Text(i18n.T(lang, "Bring mail in again to carry on from there."))
		default:
			page.Tone, page.State = "warn", i18n.T(lang, "{n, plural, one {Bringing mail in from {host} stopped after # message} other {Bringing mail in from {host} stopped after # messages}}", "n", st.Copied, "host", st.Host)
			page.Detail = ui.Text(st.Error)
		}
		if st.Failed > 0 {
			page.Detail += ui.Text(" " + i18n.T(lang, "{n} could not be copied (larger than this server takes).", "n", st.Failed))
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
		return string(ui.Status(page, ui.HTML(b.String()), ui.Callout("neutral", ui.Text(i18n.T(lang, "This mailbox is read-only, so mail cannot be brought into it.")))))
	}
	field := func(name, typ, value, placeholder, extra string) ui.HTML {
		return ui.HTML(`<input class="input" name="` + name + `" type="` + typ + `" value="` + esc(value) + `" placeholder="` + esc(placeholder) + `" required autocomplete="off"` + extra + `>`)
	}
	form := `<form hx-post="/os/vayumail/import/action" hx-target="#mx-import" hx-swap="innerHTML"><input type="hidden" name="user" value="` + esc(user) + `"><input type="hidden" name="action" value="start">` +
		string(ui.Rows(
			ui.Row{Label: i18n.T(lang, "Server"), Hint: i18n.T(lang, "Its IMAP server, as the provider's help names it"), Control: field("host", "text", host, "imap.example.com", ` spellcheck="false"`)},
			ui.Row{Label: i18n.T(lang, "Connection"), Control: ui.HTML(`<select class="input" name="port"><option value="993">` + esc(i18n.T(lang, "TLS, port 993")) + `</option><option value="143">` + esc(i18n.T(lang, "STARTTLS, port 143")) + `</option></select>`)},
			ui.Row{Label: i18n.T(lang, "User name"), Hint: i18n.T(lang, "Usually your address there"), Control: field("username", "text", username, "you@example.com", ` spellcheck="false"`)},
			ui.Row{Label: i18n.T(lang, "Password"), Hint: i18n.T(lang, "Most large providers ask for an app password and IMAP turned on, not your usual password. It is used for this copy and not kept."), Control: field("password", "password", "", "", "")},
		)) + `<div><button type="submit" class="btn btn--primary">` + esc(title) + `</button></div></form>`
	b.WriteString(string(ui.Section(i18n.T(lang, "Bring it in from"), "", ui.HTML(form))))
	b.WriteString(string(ui.Disclosure(ui.HTML(saIcon("info")), i18n.T(lang, "What comes across"), "", "", false, ui.Text(i18n.T(lang,
		"Inbox, Sent, Drafts, Junk, Trash and Archive come into theirs here; any other folder becomes one of your own, by its name there. A provider's All Mail and Starred folders are left out, since they repeat mail kept in other folders; where a provider shows labels as folders, a message with two labels arrives twice. What you have read and pinned there is read and pinned here. Running it again copies only what is new.")))))
	return string(ui.Status(page, ui.HTML(b.String())))
}
