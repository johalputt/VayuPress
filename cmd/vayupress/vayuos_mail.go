// vayuos_mail.go — VayuMail panel: compose/send, admin mail-account management,
// and message folder actions (Junk/Trash/restore/delete). POST endpoints are
// CSRF-protected and admin-only (mounted under the session-guarded /os group).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	htmpl "html/template"
	"io"
	"net/http"
	netmail "net/mail"
	"strings"

	"github.com/microcosm-cc/bluemonday"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/totp"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
	vpgp "github.com/johalputt/vayupress/internal/vayuos/pgp"
)

// mailHTMLPolicy sanitises HTML mail bodies before they are rendered in the
// reader view. UGCPolicy strips scripts, event handlers, and inline styles, so
// the message can be shown without weakening the admin console's strict CSP.
var mailHTMLPolicy = bluemonday.UGCPolicy()

// ── Compose ──────────────────────────────────────────────────────────────────

func (a *App) handleVayuOSCompose(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>Compose</h1><span class="muted text-sm">Send DKIM-signed mail (auto-PGP-encrypted when the recipient key is known)</span></div>`)
	body.WriteString(vayuosNav("compose", a.isAdminRequest(r)))
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		body.WriteString(`<div class="empty-state">VayuMail is inactive. Set <code>DOMAIN</code> to enable outbound delivery.</div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Compose", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	domain := a.vayuMail.Config().Domain
	// Sender selector. Admins may send as any configured account (or postmaster);
	// non-admin staff may only send from their own assigned mailbox.
	fromOpts := ""
	if a.isAdminRequest(r) {
		fromOpts = `<option value="postmaster@` + html.EscapeString(domain) + `">postmaster@` + html.EscapeString(domain) + `</option>`
		if a.vayuMail.Accounts() != nil {
			if accs, err := a.vayuMail.Accounts().List(r.Context()); err == nil {
				for _, ac := range accs {
					fromOpts += `<option value="` + html.EscapeString(ac.Email) + `">` + html.EscapeString(ac.Email) + `</option>`
				}
			}
		}
	} else {
		_, ownEmail := a.ownMailbox(r)
		if ownEmail == "" {
			body.WriteString(`<div class="empty-state">No mailbox has been assigned to your account yet. Ask an administrator to assign you an email address under <strong>Members → Team &amp; roles</strong>.</div>`)
			writeOSHTML(w, adminOSLayout(nonce, "Compose", "vayuos", cfg, htmpl.HTML(body.String())))
			return
		}
		fromOpts = `<option value="` + html.EscapeString(ownEmail) + `">` + html.EscapeString(ownEmail) + `</option>`
	}

	// Prefill (reply / forward / direct). Reply and forward load the original
	// message server-side so URLs stay short and large bodies are handled.
	prefillTo, prefillSubject, prefillBody := a.composePrefill(r)

	body.WriteString(`<div class="card"><div class="card-title">New message</div>
<form data-mail-compose>
  <label class="field"><span class="field-label">From</span>
    <select class="input" data-c-from>` + fromOpts + `</select></label>
  <label class="field"><span class="field-label">To (comma-separated)</span>
    <input class="input" type="text" data-c-to placeholder="someone@example.com" value="` + html.EscapeString(prefillTo) + `" required></label>
  <label class="field"><span class="field-label">Subject</span>
    <input class="input" type="text" data-c-subject placeholder="Subject" value="` + html.EscapeString(prefillSubject) + `"></label>
  <label class="field"><span class="field-label">Message</span>
    <textarea class="input" rows="12" data-c-body placeholder="Write your message…">` + html.EscapeString(prefillBody) + `</textarea></label>
  <div class="vm-row">
    <button class="btn btn--primary" type="submit">Send</button>
    <button class="btn" type="button" data-c-draft>Save as draft</button>
    <span class="muted text-sm" data-c-status></span>
  </div>
</form></div>` + `<script nonce="` + nonce + `" src="/os/static/js/admin-os-mail.js"></script>`)
	writeOSHTML(w, adminOSLayout(nonce, "Compose", "vayuos", cfg, htmpl.HTML(body.String())))
}

// composePrefill derives the To/Subject/Body for the compose form from the
// request. It supports three modes:
//
//   - reply:   ?reply=1&user=&folder=&id=  → To=original From, "Re: ", quoted body
//   - forward: ?forward=1&user=&folder=&id= → "Fwd: ", quoted body, empty To
//   - direct:  ?to=&subject=&body=          → verbatim prefill
//
// Reply/forward load the stored message (PGP-decrypted for the owner) so the
// quoted text is readable.
func (a *App) composePrefill(r *http.Request) (to, subject, bodyText string) {
	q := r.URL.Query()
	// Draft: reopen a saved draft verbatim (To/Subject/body) for editing.
	if q.Get("draft") != "" {
		user := a.scopedMailUser(r, q.Get("user"))
		id := strings.TrimSpace(q.Get("id"))
		if a.vayuMail == nil || user == "" || id == "" {
			return "", "", ""
		}
		raw, err := a.vayuMail.ReadFolderMessage(user, "Drafts", id)
		if err != nil {
			return "", "", ""
		}
		if msg, perr := netmail.ReadMessage(bytes.NewReader(raw)); perr == nil {
			b, _ := io.ReadAll(msg.Body)
			return msg.Header.Get("To"), msg.Header.Get("Subject"), string(b)
		}
		return "", "", ""
	}
	reply := q.Get("reply") != ""
	forward := q.Get("forward") != ""
	if !reply && !forward {
		return q.Get("to"), q.Get("subject"), q.Get("body")
	}
	user := a.scopedMailUser(r, q.Get("user"))
	folder := strings.TrimSpace(q.Get("folder"))
	if folder == "" {
		folder = "Inbox"
	}
	id := strings.TrimSpace(q.Get("id"))
	if a.vayuMail == nil || user == "" || id == "" {
		return "", "", ""
	}
	raw, err := a.vayuMail.ReadFolderMessage(user, folder, id)
	if err != nil {
		return "", "", ""
	}
	origFrom, origSubject, origBody := parseForQuote(raw)
	quoted := quoteBody(origFrom, origBody)
	if reply {
		return origFrom, ensurePrefix(origSubject, "Re: "), "\r\n\r\n" + quoted
	}
	// forward
	return "", ensurePrefix(origSubject, "Fwd: "), "\r\n\r\n---------- Forwarded message ----------\r\n" + quoted
}

// parseForQuote extracts From, Subject and a plain-text body from a raw message.
func parseForQuote(raw []byte) (from, subject, bodyText string) {
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return "", "", string(raw)
	}
	from = msg.Header.Get("From")
	subject = msg.Header.Get("Subject")
	b, _ := io.ReadAll(msg.Body)
	return from, subject, string(b)
}

// quoteBody prefixes each line of the original body with "> " (RFC 3676 style).
func quoteBody(from, bodyText string) string {
	var sb strings.Builder
	if from != "" {
		sb.WriteString("On a previous message, " + from + " wrote:\r\n")
	}
	for _, line := range strings.Split(bodyText, "\n") {
		sb.WriteString("> " + strings.TrimRight(line, "\r") + "\r\n")
	}
	return sb.String()
}

// ensurePrefix adds prefix unless the string already starts with it (case-insensitive).
func ensurePrefix(s, prefix string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), strings.ToLower(strings.TrimSpace(prefix))) {
		return s
	}
	return prefix + s
}

func (a *App) handleVayuOSSend(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	var in struct {
		From    string `json:"from"`
		To      string `json:"to"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeAPIError(w, r, 400, "invalid_json", err.Error(), "")
		return
	}
	domain := a.vayuMail.Config().Domain
	from := strings.TrimSpace(in.From)
	if from == "" {
		from = "postmaster@" + domain
	}
	// Non-admin staff may only send from their own assigned mailbox.
	if !a.isAdminRequest(r) {
		_, ownEmail := a.ownMailbox(r)
		if ownEmail == "" {
			writeAPIError(w, r, http.StatusForbidden, "no-mailbox", "No mailbox is assigned to your account", "")
			return
		}
		from = ownEmail
	}
	var to []string
	for _, t := range strings.Split(in.To, ",") {
		if t = strings.TrimSpace(t); t != "" {
			to = append(to, t)
		}
	}
	if len(to) == 0 {
		writeAPIError(w, r, 400, "validation_error", "at least one recipient is required", "")
		return
	}
	// Resolve the sender's PGP userID (best-effort) for signing/encryption.
	senderUserID := ""
	if mu, err := (&vayuMailBridge{app: a}).GetUserByEmail(from); err == nil && mu != nil {
		senderUserID = mu.UserID
	}
	// Add the sender's display name to the From header so recipients (and the
	// Sent folder) show a friendly name instead of a bare address. The engine
	// still uses the bare address for the SMTP envelope.
	fromHeader := from
	if name := a.senderDisplayName(r.Context(), from); name != "" {
		fromHeader = (&netmail.Address{Name: name, Address: from}).String()
	}
	id, err := a.vayuMail.Compose(r.Context(), fromHeader, to, in.Subject, in.Body, senderUserID)
	if err != nil {
		writeAPIError(w, r, 500, "send-failed", err.Error(), "")
		return
	}
	writeJSON(w, r, 200, map[string]interface{}{"queued": true, "id": id})
}

// handleVayuOSDraft saves a composed message into the sender's Drafts folder so
// it can be reopened and finished later. CSRF-protected, admin-only.
func (a *App) handleVayuOSDraft(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	var in struct {
		From    string `json:"from"`
		To      string `json:"to"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeAPIError(w, r, 400, "invalid_json", err.Error(), "")
		return
	}
	domain := a.vayuMail.Config().Domain
	from := strings.TrimSpace(in.From)
	if from == "" {
		from = "postmaster@" + domain
	}
	if !a.isAdminRequest(r) {
		_, ownEmail := a.ownMailbox(r)
		if ownEmail == "" {
			writeAPIError(w, r, http.StatusForbidden, "no-mailbox", "No mailbox is assigned to your account", "")
			return
		}
		from = ownEmail
	}
	var to []string
	for _, t := range strings.Split(in.To, ",") {
		if t = strings.TrimSpace(t); t != "" {
			to = append(to, t)
		}
	}
	fromHeader := from
	if name := a.senderDisplayName(r.Context(), from); name != "" {
		fromHeader = (&netmail.Address{Name: name, Address: from}).String()
	}
	id, err := a.vayuMail.SaveDraft(fromHeader, to, in.Subject, in.Body)
	if err != nil {
		writeAPIError(w, r, 500, "draft-failed", err.Error(), "")
		return
	}
	writeJSON(w, r, 200, map[string]string{"saved": "Drafts", "id": id})
}

// senderDisplayName returns the friendly name to put in the From: header for a
// sending address: the admin-managed mail account's full name when set, else
// the matching CMS user's name. Empty when no name is known (the caller then
// sends with the bare address, as before).
func (a *App) senderDisplayName(ctx context.Context, emailAddr string) string {
	emailAddr = strings.TrimSpace(emailAddr)
	if emailAddr == "" {
		return ""
	}
	if a.vayuMail != nil && a.vayuMail.Accounts() != nil {
		if accs, err := a.vayuMail.Accounts().List(ctx); err == nil {
			for _, ac := range accs {
				if strings.EqualFold(ac.Email, emailAddr) && strings.TrimSpace(ac.FullName) != "" {
					return strings.TrimSpace(ac.FullName)
				}
			}
		}
	}
	if a.userStore != nil {
		if users, err := a.userStore.List(ctx); err == nil {
			for _, u := range users {
				if strings.EqualFold(u.Email, emailAddr) && strings.TrimSpace(u.Name) != "" {
					return strings.TrimSpace(u.Name)
				}
			}
		}
	}
	return ""
}

// ── Message folder actions ───────────────────────────────────────────────────

func (a *App) handleVayuOSMessageAction(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	var in struct {
		User   string `json:"user"`
		ID     string `json:"id"`
		Folder string `json:"folder"`
		To     string `json:"to"`     // target folder for move
		Delete bool   `json:"delete"` // permanent delete
		Mark   string `json:"mark"`   // "read" or "unread"
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&in); err != nil {
		writeAPIError(w, r, 400, "invalid_json", err.Error(), "")
		return
	}
	if in.User == "" || in.ID == "" {
		writeAPIError(w, r, 400, "validation_error", "user and id are required", "")
		return
	}
	// Non-admins may only act on messages in their own assigned mailbox.
	if !a.isAdminRequest(r) {
		local, _ := a.ownMailbox(r)
		if local == "" || !strings.EqualFold(local, in.User) {
			writeAPIError(w, r, http.StatusForbidden, "forbidden", "you can only manage your own mailbox", "")
			return
		}
	}
	from := in.Folder
	if from == "" {
		from = "Inbox"
	}
	if in.Mark == "read" || in.Mark == "unread" {
		var (
			newID string
			err   error
		)
		if in.Mark == "read" {
			newID, err = a.vayuMail.MarkRead(in.User, from, in.ID)
		} else {
			newID, err = a.vayuMail.MarkUnread(in.User, from, in.ID)
		}
		if err != nil {
			writeAPIError(w, r, 500, "mark-failed", err.Error(), "")
			return
		}
		writeJSON(w, r, 200, map[string]string{"marked": in.Mark, "id": newID})
		return
	}
	if in.Delete {
		if err := a.vayuMail.DeleteMessage(in.User, from, in.ID); err != nil {
			writeAPIError(w, r, 500, "delete-failed", err.Error(), "")
			return
		}
		writeJSON(w, r, 200, map[string]bool{"deleted": true})
		return
	}
	target := in.To
	if target == "" {
		target = "Trash"
	}
	if err := a.vayuMail.MoveMessage(in.User, in.ID, from, target); err != nil {
		writeAPIError(w, r, 500, "move-failed", err.Error(), "")
		return
	}
	writeJSON(w, r, 200, map[string]string{"moved_to": target})
}

// ── Admin mail accounts (email + password) ───────────────────────────────────

func (a *App) handleVayuOSAccounts(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>Mail accounts</h1><span class="muted text-sm">Admin-managed email IDs &amp; passwords (SMTP/IMAP login)</span></div>`)
	body.WriteString(vayuosNav("accounts", a.isAdminRequest(r)))
	if !a.isAdminRequest(r) {
		body.WriteString(`<div class="empty-state">Mail-account management is available to administrators only. Your own mailbox is under <a href="/os/vayuos/mail/inbox">Mailbox</a>.</div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Mail accounts", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || a.vayuMail.Accounts() == nil {
		body.WriteString(`<div class="empty-state">VayuMail is inactive. Set <code>DOMAIN</code> to manage mail accounts.</div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Mail accounts", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	domain := a.vayuMail.Config().Domain
	accs, _ := a.vayuMail.Accounts().List(r.Context())

	// Create form.
	body.WriteString(`<div class="card"><div class="card-title">Create mail account</div>
<form data-acct-create>
  <div class="vm-row vm-row--end">
    <label class="field vm-grow"><span class="field-label">Address</span>
      <input class="input" type="text" data-a-local placeholder="name" required>
      <span class="vm-suffix">@` + html.EscapeString(domain) + `</span></label>
    <label class="field vm-grow"><span class="field-label">Full name (optional)</span>
      <input class="input" type="text" data-a-name placeholder="Display name"></label>
    <label class="field"><span class="field-label">Role</span>
      <select class="input" data-a-role>
        <option value="author" selected>Author (mail + author console)</option>
        <option value="editor">Editor (mail + editor console)</option>
        <option value="reviewer">Reviewer (read-only, mail only)</option>
        <option value="mailbox">Mailbox (mail only, no console)</option>
        <option value="administrator">Administrator (full console)</option>
      </select></label>
    <label class="field vm-grow"><span class="field-label">Password (min 8)</span>
      <input class="input" type="password" data-a-pass placeholder="••••••••" required></label>
    <button class="btn btn--primary" type="submit">Create</button>
  </div>
  <span class="muted text-sm" data-a-status></span>
</form></div>`)

	// Existing accounts.
	body.WriteString(`<div class="card"><div class="card-title">Accounts</div><div class="table-wrap"><table class="table"><thead><tr><th>Email</th><th>Name</th><th>Role</th><th>Status</th><th>2FA</th><th>Created</th><th></th></tr></thead><tbody>`)
	if len(accs) == 0 {
		body.WriteString(`<tr><td colspan="7" class="muted">No mail accounts yet.</td></tr>`)
	}
	for _, ac := range accs {
		status := `<span class="badge badge--ok">active</span>`
		toggleLabel := "Disable"
		toggleActive := "false"
		if !ac.Active {
			status = `<span class="badge badge--warn">disabled</span>`
			toggleLabel = "Enable"
			toggleActive = "true"
		}
		roleSel := `<select class="input input--sm" data-acct-role="` + html.EscapeString(ac.Email) + `">`
		for _, rr := range vmail.BuiltinRoles {
			sel := ""
			if strings.EqualFold(rr, ac.Role) {
				sel = " selected"
			}
			label := strings.ToUpper(rr[:1]) + rr[1:]
			roleSel += `<option value="` + rr + `"` + sel + `>` + label + `</option>`
		}
		// Preserve a custom (non-builtin) role as a selected option.
		if ac.Role != "" && !vmail.IsBuiltinRole(ac.Role) {
			roleSel += `<option value="` + html.EscapeString(ac.Role) + `" selected>` + html.EscapeString(ac.Role) + `</option>`
		}
		roleSel += `</select>`
		// 2FA status badge + enrol/disable control.
		twofa := `<span class="badge badge--warn">off</span>`
		twofaBtn := `<button class="btn" data-acct-2fa-enable="` + html.EscapeString(ac.Email) + `">Enable 2FA</button>`
		if ac.TOTPEnabled {
			twofa = `<span class="badge badge--ok">on</span>`
			twofaBtn = `<button class="btn" data-acct-2fa-disable="` + html.EscapeString(ac.Email) + `">Disable 2FA</button>`
		}
		body.WriteString(`<tr><td>` + html.EscapeString(ac.Email) + `</td><td>` + html.EscapeString(ac.FullName) + `</td><td>` + roleSel + `</td><td>` + status + `</td><td>` + twofa + `</td><td class="muted text-sm">` + ac.CreatedAt.Format("2006-01-02") + `</td><td class="vm-row">` +
			`<button class="btn" data-acct-pass="` + html.EscapeString(ac.Email) + `">Set password</button>` +
			twofaBtn +
			`<button class="btn" data-acct-toggle="` + html.EscapeString(ac.Email) + `" data-active="` + toggleActive + `">` + toggleLabel + `</button>` +
			`<button class="btn btn--danger" data-acct-delete="` + html.EscapeString(ac.Email) + `">Delete</button></td></tr>`)
	}
	body.WriteString(`</tbody></table></div></div>`)
	body.WriteString(`<script nonce="` + nonce + `" src="/os/static/js/admin-os-mail.js"></script>`)
	writeOSHTML(w, adminOSLayout(nonce, "Mail accounts", "vayuos", cfg, htmpl.HTML(body.String())))
}

// mailPort extracts the port from a listen address (":993", "127.0.0.1:993"),
// falling back to def when the address binds an ephemeral/zero port.
func mailPort(listen, def string) string {
	if i := strings.LastIndexByte(listen, ':'); i >= 0 && i < len(listen)-1 {
		if p := listen[i+1:]; p != "" && p != "0" {
			return p
		}
	}
	return def
}

// handleVayuOSConnect renders the "Connect" tab: ready-to-use IMAP/POP3/SMTP
// client settings for each mailbox (so any standard mail app — Gmail, Apple
// Mail, Thunderbird, Outlook — can be set up by copying the values), plus the
// live up/down status of each mail listener so the operator can see at a glance
// whether the server side of the connection is reachable.
func (a *App) handleVayuOSConnect(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>Connect a mail app</h1><span class="muted text-sm">IMAP / POP3 / SMTP settings for Gmail, Apple Mail, Thunderbird, Outlook…</span></div>`)
	body.WriteString(vayuosNav("connect", a.isAdminRequest(r)))

	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		body.WriteString(`<div class="empty-state">VayuMail is inactive. Set <code>DOMAIN</code> to enable mailboxes and mail-client access.</div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Connect a mail app", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}

	mc := a.vayuMail.Config()
	host := mc.Hostname
	if host == "" {
		host = "mail." + mc.Domain
	}
	hHost := html.EscapeString(host)
	imapsPort := html.EscapeString(mailPort(mc.IMAPSListen, "993"))
	imapPort := html.EscapeString(mailPort(mc.IMAPListen, "143"))
	pop3sPort := html.EscapeString(mailPort(mc.POP3SListen, "995"))
	pop3Port := html.EscapeString(mailPort(mc.POP3Listen, "110"))
	subPort := html.EscapeString(mailPort(mc.SubmissionListen, "587"))
	smtpPort := html.EscapeString(mailPort(mc.SMTPListen, "25"))

	// ── Live service status ──────────────────────────────────────────────────
	badge := func(up bool) string {
		if up {
			return `<span class="badge badge--ok">online</span>`
		}
		return `<span class="badge badge--warn">offline</span>`
	}
	body.WriteString(`<div class="card"><div class="card-title">Service status</div>`)
	body.WriteString(`<div class="table-wrap"><table class="table"><thead><tr><th>Service</th><th>Address</th><th>Status</th></tr></thead><tbody>`)
	row := func(label, addr string, up bool) {
		body.WriteString(`<tr><td>` + label + `</td><td class="mono text-sm">` + addr + `</td><td>` + badge(up) + `</td></tr>`)
	}
	row("IMAP · SSL", hHost+":"+imapsPort, a.vayuMail.IMAPSActive())
	row("IMAP · STARTTLS", hHost+":"+imapPort, a.vayuMail.IMAPActive())
	row("POP3 · SSL", hHost+":"+pop3sPort, a.vayuMail.POP3SActive())
	row("POP3 · STLS", hHost+":"+pop3Port, a.vayuMail.POP3Active())
	row("SMTP submission · STARTTLS", hHost+":"+subPort, a.vayuMail.SubmissionActive())
	row("SMTP receive", hHost+":"+smtpPort, a.vayuMail.InboundActive())
	body.WriteString(`</tbody></table></div>`)
	if err := a.vayuMail.InboundError(); err != nil {
		body.WriteString(`<p class="muted text-sm">Some listeners are not bound: ` + html.EscapeString(err.Error()) +
			`. Ensure the ports are free and the service may bind them (grant CAP_NET_BIND_SERVICE for ports below 1024, or point the VAYUOS_MAIL_*_LISTEN vars at high ports), then restart.</p>`)
	}
	body.WriteString(`</div>`)

	// ── TLS certificate trust ────────────────────────────────────────────────
	// A reachable port with an untrusted (self-signed) certificate is the most
	// common cause of a mail app's "Couldn't open connection to server": the
	// connection and TLS handshake succeed, but the client rejects the
	// certificate. Surface this prominently with the exact remediation.
	acmeErr := a.vayuMail.ACMEChallengeError()
	if a.vayuMail.TLSActive() && !a.vayuMail.TLSTrusted() {
		body.WriteString(`<div class="card" style="border-left:4px solid #d9534f"><div class="card-title">⚠ Mail apps will reject this connection</div>`)
		body.WriteString(`<p class="text-sm">VayuMail is serving a <strong>self-signed TLS certificate</strong>, so mobile and desktop mail apps ` +
			`(the Gmail app, Apple Mail, Thunderbird, Outlook) report <em>"Couldn't open connection to server"</em> — even though the ports above are online.</p>`)
		// Surface the exact reason the engine recorded, so the operator isn't guessing.
		if note := a.vayuMail.TLSNote(); note != "" {
			body.WriteString(`<p class="text-sm muted">Reason: ` + html.EscapeString(note) + `</p>`)
		}
		if acmeErr != "" {
			body.WriteString(`<p class="text-sm muted">Built-in ACME could not run: ` + html.EscapeString(acmeErr) +
				` — port 80 is almost certainly already used by your website's nginx, so VayuMail cannot answer the Let's Encrypt challenge itself.</p>`)
		}
		body.WriteString(`<p class="text-sm">The easiest fix on a server that already runs nginx (like this one) is the guided script — it issues a real Let's Encrypt certificate for <code>` + hHost + `</code> <em>through</em> nginx (no port conflict) and wires it in automatically:</p>`)
		body.WriteString(`<pre class="mono text-sm" style="white-space:pre-wrap;background:var(--bg-surface-2);padding:10px;border-radius:8px">cd /tmp/VayuPress &amp;&amp; sudo bash deploy/vayumail-setup.sh</pre>`)
		body.WriteString(`<p class="text-sm">It auto-renews. Alternatives:</p>`)
		body.WriteString(`<ul class="text-sm">` +
			`<li><strong>Built-in ACME (only if port 80 is free):</strong> set <code>VAYUOS_MAIL_TLS_ACME=on</code> and <code>VAYUOS_MAIL_ACME_EMAIL=you@` + html.EscapeString(mc.Domain) + `</code>, then restart. On this box nginx owns port 80, so use the script above instead — or point a free port via <code>VAYUOS_MAIL_ACME_HTTP_ADDR=127.0.0.1:8081</code> and proxy <code>` + hHost + `/.well-known/acme-challenge/</code> to it in nginx.</li>` +
			`<li><strong>Manual / existing certbot cert:</strong> set <code>VAYUOS_MAIL_TLS_CERT</code> and <code>VAYUOS_MAIL_TLS_KEY</code> to a CA-signed pair (e.g. <code>/etc/letsencrypt/live/` + hHost + `/fullchain.pem</code> and <code>privkey.pem</code>), then restart. VayuMail hot-reloads on renewal.</li>` +
			`</ul>`)
		body.WriteString(`<p class="text-sm muted">Also make sure DNS has an A record for <code>` + hHost + `</code> pointing at this server, and that ports 25/143/993/587/995/110 are open in your firewall (the script handles the firewall + privileged-port binding too).</p>`)
		body.WriteString(`</div>`)
	} else if a.vayuMail.TLSActive() && a.vayuMail.TLSTrusted() {
		body.WriteString(`<div class="card"><div class="card-title">TLS certificate</div>`)
		body.WriteString(`<p class="text-sm">A trusted certificate is active — mail apps can connect over SSL/TLS. <span class="muted">(` + html.EscapeString(a.vayuMail.TLSNote()) + `)</span></p>`)
		// Even in ACME mode, warn if the challenge responder can't bind — renewals
		// will eventually fail and the cert will expire back into self-signed.
		if acmeErr != "" {
			body.WriteString(`<p class="text-sm" style="color:#d9844f">⚠ Auto-renewal may fail: ` + html.EscapeString(acmeErr) +
				` (port 80 is held by another service). Switch to the guided script (<code>sudo bash deploy/vayumail-setup.sh</code>), which renews through nginx, to avoid the certificate expiring.</p>`)
		}
		body.WriteString(`</div>`)
	}

	// ── Recommended settings ─────────────────────────────────────────────────
	body.WriteString(`<div class="card"><div class="card-title">Recommended settings</div>`)
	body.WriteString(`<div class="table-wrap"><table class="table"><tbody>`)
	body.WriteString(`<tr><th>Incoming · IMAP (recommended)</th><td class="mono text-sm">` + hHost + `</td><td>port ` + imapsPort + ` · SSL/TLS</td></tr>`)
	body.WriteString(`<tr><th>Incoming · IMAP (alternative)</th><td class="mono text-sm">` + hHost + `</td><td>port ` + imapPort + ` · STARTTLS</td></tr>`)
	body.WriteString(`<tr><th>Incoming · POP3</th><td class="mono text-sm">` + hHost + `</td><td>port ` + pop3sPort + ` SSL · or ` + pop3Port + ` STLS</td></tr>`)
	body.WriteString(`<tr><th>Outgoing · SMTP</th><td class="mono text-sm">` + hHost + `</td><td>port ` + subPort + ` · STARTTLS · authentication required</td></tr>`)
	body.WriteString(`<tr><th>Username</th><td colspan="2">your full email address (e.g. <span class="mono">you@` + html.EscapeString(mc.Domain) + `</span>)</td></tr>`)
	body.WriteString(`<tr><th>Password</th><td colspan="2">your mailbox password (set under <a href="/os/vayuos/mail/accounts">Accounts</a>)</td></tr>`)
	body.WriteString(`</tbody></table></div>`)
	body.WriteString(`<p class="muted text-sm">IMAP keeps mail in sync across all your devices; POP3 downloads to a single device. Prefer the SSL ports where your app supports them.</p></div>`)

	// ── Per-mailbox quick setup ──────────────────────────────────────────────
	var emails []string
	if a.isAdminRequest(r) && a.vayuMail.Accounts() != nil {
		if accs, err := a.vayuMail.Accounts().List(r.Context()); err == nil {
			for _, ac := range accs {
				if ac.Active {
					emails = append(emails, ac.Email)
				}
			}
		}
	} else if _, own := a.ownMailbox(r); own != "" {
		emails = append(emails, own)
	}

	body.WriteString(`<div class="card"><div class="card-title">Per-mailbox setup</div>`)
	body.WriteString(`<p class="muted text-sm">Use the email address as the <strong>username</strong> for all three protocols; the password is that mailbox's own password.</p>`)
	body.WriteString(`<div class="table-wrap"><table class="table"><thead><tr><th>Mailbox (username)</th><th>IMAP</th><th>POP3</th><th>SMTP (send)</th></tr></thead><tbody>`)
	if len(emails) == 0 {
		body.WriteString(`<tr><td colspan="4" class="muted">No active mailboxes yet. Create one under <a href="/os/vayuos/mail/accounts">Accounts</a>.</td></tr>`)
	}
	for _, em := range emails {
		e := html.EscapeString(em)
		body.WriteString(`<tr><td class="mono">` + e + `</td>` +
			`<td class="text-sm">` + hHost + `:` + imapsPort + ` SSL</td>` +
			`<td class="text-sm">` + hHost + `:` + pop3sPort + ` SSL</td>` +
			`<td class="text-sm">` + hHost + `:` + subPort + ` STARTTLS</td></tr>`)
	}
	body.WriteString(`</tbody></table></div></div>`)

	writeOSHTML(w, adminOSLayout(nonce, "Connect a mail app", "vayuos", cfg, htmpl.HTML(body.String())))
}

func (a *App) handleVayuOSAccountCreate(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	var in struct {
		Local string `json:"local"`
		Name  string `json:"name"`
		Pass  string `json:"pass"`
		Role  string `json:"role"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&in); err != nil {
		writeAPIError(w, r, 400, "invalid_json", err.Error(), "")
		return
	}
	local := strings.ToLower(strings.TrimSpace(in.Local))
	if local == "" || strings.ContainsAny(local, "@ \t") {
		writeAPIError(w, r, 400, "validation_error", "invalid local part", "")
		return
	}
	if len(in.Pass) < 8 {
		writeAPIError(w, r, 400, "validation_error", "password must be at least 8 characters", "")
		return
	}
	hash, err := auth.HashSecretArgon2id(in.Pass)
	if err != nil {
		writeAPIError(w, r, 500, "hash-failed", "could not hash password", "")
		return
	}
	email := local + "@" + a.vayuMail.Config().Domain
	if err := a.vayuMail.Accounts().Create(r.Context(), email, hash, in.Name, in.Role); err != nil {
		writeAPIError(w, r, 400, "create-failed", err.Error(), "")
		return
	}
	// Provision the Maildir folders for the new address.
	_ = a.vayuMail.CreateMailbox("", local)
	// Auto-generate a PGP keypair for the new mailbox (private key encrypted at
	// rest) so it appears in the VayuPGP panel and its mail can be encrypted /
	// transparently decrypted. Best-effort: a key failure must not fail account
	// creation.
	if a.vayuPGP != nil {
		if _, err := a.vayuPGP.EnsureKeypair(&vpgp.PGPUser{UserID: email, Name: in.Name, Email: email}); err != nil {
			logging.LogError("vayuos", "auto PGP keygen failed for "+email, err.Error())
		}
	}
	writeJSON(w, r, 201, map[string]string{"email": email})
}

func (a *App) handleVayuOSAccountDelete(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	var in struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&in); err != nil {
		writeAPIError(w, r, 400, "invalid_json", err.Error(), "")
		return
	}
	if err := a.vayuMail.Accounts().Delete(r.Context(), in.Email); err != nil {
		writeAPIError(w, r, 500, "delete-failed", err.Error(), "")
		return
	}
	writeJSON(w, r, 200, map[string]bool{"deleted": true})
}

// handleVayuOSAccountUpdate sets a new password and/or enables/disables an
// existing mail account. Exactly one of {password, active} should be provided
// per call; both are honoured if present.
func (a *App) handleVayuOSAccountUpdate(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	var in struct {
		Email  string `json:"email"`
		Pass   string `json:"pass"`
		Active *bool  `json:"active"`
		Role   string `json:"role"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&in); err != nil {
		writeAPIError(w, r, 400, "invalid_json", err.Error(), "")
		return
	}
	if strings.TrimSpace(in.Email) == "" {
		writeAPIError(w, r, 400, "validation_error", "email is required", "")
		return
	}
	if in.Pass != "" {
		if len(in.Pass) < 8 {
			writeAPIError(w, r, 400, "validation_error", "password must be at least 8 characters", "")
			return
		}
		hash, err := auth.HashSecretArgon2id(in.Pass)
		if err != nil {
			writeAPIError(w, r, 500, "hash-failed", "could not hash password", "")
			return
		}
		if err := a.vayuMail.Accounts().SetPasswordHash(r.Context(), in.Email, hash); err != nil {
			writeAPIError(w, r, 400, "update-failed", err.Error(), "")
			return
		}
	}
	if in.Active != nil {
		if err := a.vayuMail.Accounts().SetActive(r.Context(), in.Email, *in.Active); err != nil {
			writeAPIError(w, r, 400, "update-failed", err.Error(), "")
			return
		}
	}
	if strings.TrimSpace(in.Role) != "" {
		if err := a.vayuMail.Accounts().SetRole(r.Context(), in.Email, in.Role); err != nil {
			writeAPIError(w, r, 400, "update-failed", err.Error(), "")
			return
		}
	}
	writeJSON(w, r, 200, map[string]bool{"updated": true})
}

// handleVayuOSAccountTOTP manages two-factor authentication (TOTP) for a mail
// account. CSRF-protected, admin-only. The action field drives a small state
// machine:
//
//   - "begin":   generate a fresh secret, store it (still disabled), and return
//     the secret + otpauth:// URI for the operator to scan/enter.
//   - "verify":  validate a 6-digit code against the stored secret and, on
//     success, enable 2FA for the account.
//   - "disable": turn 2FA off and forget the secret.
//
// 2FA, once enabled, is enforced by the public "Sign in with VayuMail" flow
// (handleMemberVayuMailLogin) — it adds a second factor to mailbox-credential
// sign-in without affecting the passwordless magic-link path.
func (a *App) handleVayuOSAccountTOTP(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	var in struct {
		Email  string `json:"email"`
		Action string `json:"action"`
		Code   string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&in); err != nil {
		writeAPIError(w, r, 400, "invalid_json", err.Error(), "")
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if email == "" {
		writeAPIError(w, r, 400, "validation_error", "email is required", "")
		return
	}
	accts := a.vayuMail.Accounts()
	switch in.Action {
	case "begin":
		secret, err := totp.GenerateSecret()
		if err != nil {
			writeAPIError(w, r, 500, "totp-failed", "could not generate a secret", "")
			return
		}
		if err := accts.SetTOTPSecret(r.Context(), email, secret); err != nil {
			writeAPIError(w, r, 400, "totp-failed", err.Error(), "")
			return
		}
		uri := totp.ProvisioningURI(secret, a.vayuMail.Config().Domain, email)
		writeJSON(w, r, 200, map[string]string{"secret": secret, "uri": uri})
	case "verify":
		secret, _ := accts.TOTPStatus(r.Context(), email)
		if secret == "" {
			writeAPIError(w, r, 400, "totp-failed", "start enrolment first", "")
			return
		}
		if !totp.Validate(secret, in.Code) {
			writeAPIError(w, r, 400, "totp-invalid", "that code is not valid — check the time on the device", "")
			return
		}
		if err := accts.EnableTOTP(r.Context(), email); err != nil {
			writeAPIError(w, r, 400, "totp-failed", err.Error(), "")
			return
		}
		writeJSON(w, r, 200, map[string]bool{"enabled": true})
	case "disable":
		if err := accts.DisableTOTP(r.Context(), email); err != nil {
			writeAPIError(w, r, 400, "totp-failed", err.Error(), "")
			return
		}
		writeJSON(w, r, 200, map[string]bool{"enabled": false})
	default:
		writeAPIError(w, r, 400, "validation_error", "unknown action", "")
	}
}
