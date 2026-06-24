// vayuos_mail.go — VayuMail panel: compose/send, admin mail-account management,
// and message folder actions (Junk/Trash/restore/delete). POST endpoints are
// CSRF-protected and admin-only (mounted under the session-guarded /os group).
package main

import (
	"bytes"
	"encoding/json"
	"html"
	htmpl "html/template"
	"io"
	"net/http"
	netmail "net/mail"
	"strings"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/render"
)

// ── Compose ──────────────────────────────────────────────────────────────────

func (a *App) handleVayuOSCompose(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>Compose</h1><span class="muted text-sm">Send DKIM-signed mail (auto-PGP-encrypted when the recipient key is known)</span></div>`)
	body.WriteString(vayuosNav("compose"))
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		body.WriteString(`<div class="empty-state">VayuMail is inactive. Set <code>DOMAIN</code> to enable outbound delivery.</div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Compose", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	domain := a.vayuMail.Config().Domain
	// Sender selector: the configured mail accounts + a default postmaster.
	fromOpts := `<option value="postmaster@` + html.EscapeString(domain) + `">postmaster@` + html.EscapeString(domain) + `</option>`
	if a.vayuMail.Accounts() != nil {
		if accs, err := a.vayuMail.Accounts().List(r.Context()); err == nil {
			for _, ac := range accs {
				fromOpts += `<option value="` + html.EscapeString(ac.Email) + `">` + html.EscapeString(ac.Email) + `</option>`
			}
		}
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
	reply := q.Get("reply") != ""
	forward := q.Get("forward") != ""
	if !reply && !forward {
		return q.Get("to"), q.Get("subject"), q.Get("body")
	}
	user := strings.TrimSpace(q.Get("user"))
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
	id, err := a.vayuMail.Compose(r.Context(), from, to, in.Subject, in.Body, senderUserID)
	if err != nil {
		writeAPIError(w, r, 500, "send-failed", err.Error(), "")
		return
	}
	writeJSON(w, r, 200, map[string]interface{}{"queued": true, "id": id})
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
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&in); err != nil {
		writeAPIError(w, r, 400, "invalid_json", err.Error(), "")
		return
	}
	if in.User == "" || in.ID == "" {
		writeAPIError(w, r, 400, "validation_error", "user and id are required", "")
		return
	}
	from := in.Folder
	if from == "" {
		from = "Inbox"
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
	body.WriteString(vayuosNav("accounts"))
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
    <label class="field vm-grow"><span class="field-label">Password (min 8)</span>
      <input class="input" type="password" data-a-pass placeholder="••••••••" required></label>
    <button class="btn btn--primary" type="submit">Create</button>
  </div>
  <span class="muted text-sm" data-a-status></span>
</form></div>`)

	// Existing accounts.
	body.WriteString(`<div class="card"><div class="card-title">Accounts</div><div class="table-wrap"><table class="table"><thead><tr><th>Email</th><th>Name</th><th>Status</th><th>Created</th><th></th></tr></thead><tbody>`)
	if len(accs) == 0 {
		body.WriteString(`<tr><td colspan="5" class="muted">No mail accounts yet.</td></tr>`)
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
		body.WriteString(`<tr><td>` + html.EscapeString(ac.Email) + `</td><td>` + html.EscapeString(ac.FullName) + `</td><td>` + status + `</td><td class="muted text-sm">` + ac.CreatedAt.Format("2006-01-02") + `</td><td class="vm-row">` +
			`<button class="btn" data-acct-pass="` + html.EscapeString(ac.Email) + `">Set password</button>` +
			`<button class="btn" data-acct-toggle="` + html.EscapeString(ac.Email) + `" data-active="` + toggleActive + `">` + toggleLabel + `</button>` +
			`<button class="btn btn--danger" data-acct-delete="` + html.EscapeString(ac.Email) + `">Delete</button></td></tr>`)
	}
	body.WriteString(`</tbody></table></div></div>`)
	body.WriteString(`<script nonce="` + nonce + `" src="/os/static/js/admin-os-mail.js"></script>`)
	writeOSHTML(w, adminOSLayout(nonce, "Mail accounts", "vayuos", cfg, htmpl.HTML(body.String())))
}

func (a *App) handleVayuOSAccountCreate(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	var in struct {
		Local string `json:"local"`
		Name  string `json:"name"`
		Pass  string `json:"pass"`
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
	if err := a.vayuMail.Accounts().Create(r.Context(), email, hash, in.Name); err != nil {
		writeAPIError(w, r, 400, "create-failed", err.Error(), "")
		return
	}
	// Provision the Maildir folders for the new address.
	_ = a.vayuMail.CreateMailbox("", local)
	writeJSON(w, r, 201, map[string]string{"email": email})
}

func (a *App) handleVayuOSAccountDelete(w http.ResponseWriter, r *http.Request) {
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
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	var in struct {
		Email  string `json:"email"`
		Pass   string `json:"pass"`
		Active *bool  `json:"active"`
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
	writeJSON(w, r, 200, map[string]bool{"updated": true})
}
