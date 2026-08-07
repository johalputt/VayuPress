// SPDX-License-Identifier: Apache-2.0

// vayuos_mail.go — VayuMail panel: compose/send, admin mail-account management,
// and message folder actions (Junk/Trash/restore/delete). POST endpoints are
// CSRF-protected and admin-only (mounted under the session-guarded /os group).
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	htmpl "html/template"
	"io"
	"net/http"
	netmail "net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/microcosm-cc/bluemonday"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
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
	body.WriteString(`<div class="page-header"><h1>Compose</h1></div>`)
	body.WriteString(`<p class="page-sub">Send DKIM-signed mail — auto-PGP-encrypted when the recipient's key is known.</p>`)
	body.WriteString(vayuosNav("compose", a.isAdminRequest(r)))
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		body.WriteString(`<div class="empty-state">VayuMail is inactive. Set <code>DOMAIN</code> to enable outbound delivery.</div>`)
		writeOSHTML(w, r, adminOSLayout(nonce, "Compose", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	domain := a.vayuMail.Config().Domain
	// Sender selector. Admins may send as any configured account (or postmaster);
	// non-admin staff may only send from their own assigned mailbox.
	acctStore := a.vayuMail.Accounts()
	// Each From option carries its account's signature (data-sig) so the composer
	// can preview/append it and swap it live when the sender changes.
	optSig := func(email, sig string) string {
		return `<option value="` + html.EscapeString(email) + `" data-sig="` + html.EscapeString(sig) + `">` + html.EscapeString(email) + `</option>`
	}
	fromOpts := ""
	if a.isAdminRequest(r) {
		pm := "postmaster@" + domain
		pmSig := ""
		if acctStore != nil {
			pmSig = acctStore.SignatureFor(r.Context(), pm)
		}
		// Group the sender identities by domain (VayuDomains) so an operator
		// serving multiple domains picks a From per domain instead of scanning one
		// long flat list. The primary domain's group is first (and holds
		// postmaster). A single-domain install emits a flat list with no optgroup
		// chrome, so it stays byte-identical.
		domOf := func(email string) string {
			if i := strings.LastIndexByte(email, '@'); i >= 0 && i < len(email)-1 {
				return strings.ToLower(email[i+1:])
			}
			return domain
		}
		byDom := map[string]string{domain: optSig(pm, pmSig)}
		order := []string{domain}
		if acctStore != nil {
			if accs, err := acctStore.List(r.Context()); err == nil {
				for _, ac := range accs {
					d := domOf(ac.Email)
					if _, seen := byDom[d]; !seen {
						order = append(order, d)
					}
					byDom[d] += optSig(ac.Email, ac.Signature)
				}
			}
		}
		if len(order) == 1 {
			fromOpts = byDom[domain]
		} else {
			for _, d := range order {
				fromOpts += `<optgroup label="` + html.EscapeString(d) + `">` + byDom[d] + `</optgroup>`
			}
		}
	} else {
		_, ownEmail := a.ownMailbox(r)
		if ownEmail == "" {
			body.WriteString(`<div class="empty-state">No mailbox has been assigned to your account yet. Ask an administrator to assign you an email address under <strong>Members → Team &amp; roles</strong>.</div>`)
			writeOSHTML(w, r, adminOSLayout(nonce, "Compose", "vayuos", cfg, htmpl.HTML(body.String())))
			return
		}
		ownSig := ""
		if acctStore != nil {
			ownSig = acctStore.SignatureFor(r.Context(), ownEmail)
		}
		fromOpts = optSig(ownEmail, ownSig)
	}

	// Prefill (reply / forward / direct). Reply and forward load the original
	// message server-side so URLs stay short and large bodies are handled.
	prefillTo, prefillSubject, prefillBody := a.composePrefill(r)

	// Feedback mode (the VayuOS topbar "Report a bug / suggest an improvement"
	// button links to ?feedback=1): address the feedback inbox, drop in a
	// structured template, and pre-enable PGP so the report is encrypted. Any
	// explicit ?to/subject/body still wins, so the mode only fills the blanks.
	feedback := r.URL.Query().Get("feedback") == "1"
	encryptChecked := r.URL.Query().Get("encrypt") == "1"
	if feedback {
		if prefillTo == "" {
			prefillTo = a.feedbackEmail(r.Context())
		}
		if prefillSubject == "" {
			prefillSubject = feedbackSubject
		}
		if prefillBody == "" {
			prefillBody = feedbackBody()
		}
		encryptChecked = true
	}
	encAttr := ""
	if encryptChecked {
		encAttr = " checked"
	}
	feedbackBanner := ""
	if feedback {
		feedbackBanner = `<div class="vm-feedback-banner">💡 <strong>Help improve VayuPress.</strong> Tell us about a bug, an improvement or a feature you'd like — attach screenshots or files if they help. Your report is <strong>PGP-encrypted end-to-end, attachments included</strong>. Just add your details and hit Send.</div>`
	}

	// Recipient autocomplete is scoped to the SENDING mailbox's own address book
	// only (never a shared/global directory), so one mailbox's contacts stay
	// private to it. The owner is the opened mailbox; an admin composing without a
	// specific mailbox falls back to postmaster's book.
	composeOwner := ""
	if o, ok := a.contactOwner(r, mailUserParam(r)); ok {
		composeOwner = o
	} else if a.isAdminRequest(r) {
		composeOwner = "postmaster@" + domain
	}
	body.WriteString(`<div class="card"><div class="card-title">New message</div>
` + feedbackBanner + `<form data-mail-compose>
  ` + a.composeContactsDatalistFor(r.Context(), composeOwner) + `
  <label class="field"><span class="field-label">From</span>
    <select class="input" data-c-from>` + fromOpts + `</select></label>

  <label class="field"><span class="field-label">To</span>
    <div class="vm-chips" data-c-chips="to"><input type="text" class="vm-chip-input" data-c-chip-input list="vm-contacts" placeholder="name@example.com" autocomplete="off" aria-label="To recipients"></div></label>
  <input type="hidden" data-c-to value="` + html.EscapeString(prefillTo) + `">

  <div class="vm-row vm-row--tight">
    <button class="btn btn--sm" type="button" data-c-toggle-cc>Cc/Bcc</button>
    <button class="btn btn--sm" type="button" data-c-toggle-reply>Reply-To</button>
    <span class="vm-pgp-hint" data-c-pgp aria-live="polite"></span>
  </div>

  <label class="field" data-c-cc-field hidden><span class="field-label">Cc</span>
    <div class="vm-chips" data-c-chips="cc"><input type="text" class="vm-chip-input" data-c-chip-input list="vm-contacts" placeholder="cc@example.com" autocomplete="off" aria-label="Cc recipients"></div></label>
  <input type="hidden" data-c-cc>
  <label class="field" data-c-bcc-field hidden><span class="field-label">Bcc</span>
    <div class="vm-chips" data-c-chips="bcc"><input type="text" class="vm-chip-input" data-c-chip-input list="vm-contacts" placeholder="bcc@example.com" autocomplete="off" aria-label="Bcc recipients"></div></label>
  <input type="hidden" data-c-bcc>
  <label class="field" data-c-reply-field hidden><span class="field-label">Reply-To</span>
    <input class="input" type="text" data-c-reply placeholder="reply@example.com"></label>

  <label class="field"><span class="field-label">Subject</span>
    <input class="input" type="text" data-c-subject placeholder="Subject" value="` + html.EscapeString(prefillSubject) + `"></label>
  <div class="field vm-editor">
    <div class="vm-ed-head">
      <span class="field-label">Message</span>
      <span class="muted text-xs" data-c-count aria-live="polite"></span>
    </div>
    <!-- Formatting inserts plain-text conventions, because a message body IS
         plain text end to end (mail.ComposeMessage.Body). A contenteditable
         WYSIWYG would imply an HTML alternative part the engine does not build,
         so it would promise formatting the recipient never receives. What goes in
         here is exactly what is sent, and it stays readable in every client. -->
    <div class="vm-ed-bar" role="toolbar" aria-label="Formatting" data-c-toolbar>
      <button class="vm-ed-btn" type="button" data-c-fmt="bold" title="Bold (Ctrl+B)" aria-label="Bold"><strong>B</strong></button>
      <button class="vm-ed-btn" type="button" data-c-fmt="italic" title="Italic (Ctrl+I)" aria-label="Italic"><em>I</em></button>
      <button class="vm-ed-btn" type="button" data-c-fmt="strike" title="Strikethrough" aria-label="Strikethrough"><s>S</s></button>
      <span class="vm-ed-sep" aria-hidden="true"></span>
      <button class="vm-ed-btn" type="button" data-c-fmt="h2" title="Heading" aria-label="Heading">H</button>
      <button class="vm-ed-btn" type="button" data-c-fmt="ul" title="Bulleted list" aria-label="Bulleted list">&bull;&nbsp;&#8801;</button>
      <button class="vm-ed-btn" type="button" data-c-fmt="ol" title="Numbered list" aria-label="Numbered list">1.&nbsp;&#8801;</button>
      <button class="vm-ed-btn" type="button" data-c-fmt="quote" title="Quote" aria-label="Quote">&rdquo;</button>
      <span class="vm-ed-sep" aria-hidden="true"></span>
      <button class="vm-ed-btn" type="button" data-c-fmt="code" title="Code block" aria-label="Code block">&lt;/&gt;</button>
      <button class="vm-ed-btn" type="button" data-c-fmt="link" title="Link (Ctrl+K)" aria-label="Insert link">&#128279;</button>
      <button class="vm-ed-btn" type="button" data-c-fmt="rule" title="Divider" aria-label="Divider">&mdash;</button>
      <span class="vm-ed-spacer"></span>
      <button class="vm-ed-btn vm-ed-btn--wide" type="button" data-c-preview title="Preview how the message will look" aria-pressed="false">Preview</button>
    </div>
    <textarea class="input vm-ed-area" rows="16" data-c-body placeholder="Write your message…">` + html.EscapeString(prefillBody) + `</textarea>
    <div class="vm-ed-preview" data-c-preview-pane hidden aria-live="polite"></div>
    <p class="field-hint">Formatting is written into the message itself, so it reads the same in every mail client — including ones that refuse HTML.</p>
  </div>

  <span class="field-label">Attachments</span>
  <div class="vm-dropzone" data-c-dropzone>
    <span class="muted text-sm">Drag &amp; drop files here, or </span><button class="btn btn--sm" type="button" data-c-attach-btn>Browse…</button>
    <span class="muted text-xs vm-dropzone-hint">Up to ` + strconv.Itoa(composeMaxAttachMB()) + ` MB total — more than most providers allow.</span>
    <input type="file" data-c-files multiple hidden>
  </div>
  <div class="vm-attach-tray" data-c-attach-list></div>

  <div class="vm-sig" data-c-sig>
    <label class="vm-filter-check"><input type="checkbox" data-c-sig-toggle checked> Append signature</label>
    <details class="vm-sig-edit">
      <summary>Edit signature</summary>
      <textarea class="input" rows="4" data-c-sig-text placeholder="Your signature — appended to messages sent from the selected address."></textarea>
      <div class="vm-row vm-row--tight">
        <button class="btn btn--sm" type="button" data-c-sig-save>Save signature</button>
        <span class="muted text-sm" data-c-sig-status></span>
      </div>
    </details>
    <pre class="vm-sig-preview" data-c-sig-preview></pre>
  </div>

  <div class="vm-row vm-row--tight">
    <label class="vm-filter-check"><input type="checkbox" data-c-rich> &#127912; Also send an HTML version</label>
    <span class="vm-pgp-hint">Off &mdash; the message goes as plain text, which is what a young sending domain delivers best. Turn on to add an HTML rendering of the same words alongside it; clients that prefer HTML show that one, the rest see your text unchanged.</span>
  </div>
  <div class="vm-row vm-row--tight">
    <label class="vm-filter-check"><input type="checkbox" data-c-encrypt` + encAttr + `> 🔒 Encrypt with PGP</label>
    <span class="vm-pgp-hint" data-c-encrypt-hint aria-live="polite">Off — the message is sent as readable text. Turn on to PGP-encrypt the message and attachments (RFC 3156) for recipients whose keys are known.</span>
  </div>
  <div class="vm-row vm-compose-actions">
    <button class="btn btn--primary" type="submit" data-c-send>Send</button>
    <button class="btn" type="button" data-c-draft>Save as draft</button>
    <span class="muted text-sm" data-c-status></span>
  </div>
  <div class="vm-undobar" data-c-undobar hidden role="status" aria-live="polite">
    <span data-c-undo-text>Sending…</span>
    <button class="btn btn--sm" type="button" data-c-undo>Undo</button>
  </div>
</form></div>` + `<script nonce="` + nonce + `" src="/os/static/js/admin-os-mail.js?v=` + assetVer("js/admin-os-mail.js") + `"></script>`)
	writeOSHTML(w, r, adminOSLayout(nonce, "Compose", "vayuos", cfg, htmpl.HTML(body.String())))
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
		rd := a.mailReader(r, q.Get("user"))
		id := strings.TrimSpace(q.Get("id"))
		if a.vayuMail == nil || rd.Key() == "" || id == "" {
			return "", "", ""
		}
		raw, err := a.vayuMail.ReadFolderMessage(rd, "Drafts", id)
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
	rd := a.mailReader(r, q.Get("user"))
	user := rd.Key()
	folder := strings.TrimSpace(q.Get("folder"))
	if folder == "" {
		folder = "Inbox"
	}
	id := strings.TrimSpace(q.Get("id"))
	if a.vayuMail == nil || user == "" || id == "" {
		return "", "", ""
	}
	raw, err := a.vayuMail.ReadFolderMessage(rd, folder, id)
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

// composeMaxAttachMB is the total attachment budget for a single message, in MB.
// Generous by design (default 50 MB, above Gmail's 25 / Outlook's 20); override
// with VAYUMAIL_MAX_ATTACH_MB.
func composeMaxAttachMB() int {
	if n := config.GetEnvAsInt("VAYUMAIL_MAX_ATTACH_MB", 50); n >= 1 {
		return n
	}
	return 1
}

// insertSignature places a plain-text signature after the freshly-written reply
// and before any quoted history, using the RFC 3676 "-- " delimiter. For a new
// message (no quote) the signature is appended at the end.
func insertSignature(body, sig string) string {
	sig = strings.TrimRight(sig, "\r\n")
	if sig == "" {
		return body
	}
	block := "\r\n\r\n-- \r\n" + sig
	if main, quoted := splitQuoted(body); quoted != "" {
		return strings.TrimRight(main, "\r\n") + block + "\r\n\r\n" + quoted
	}
	return strings.TrimRight(body, "\r\n") + block
}

// parseRecipientList extracts the bare email addresses from a recipient string
// that may hold "Name <email>" forms and/or bare addresses, comma-separated. It
// strips the display name and angle brackets so a recipient like
// `VayuPress Hello <hello@vayupress.com>` is delivered to hello@vayupress.com
// (and recognised as a local mailbox) instead of to a malformed `vayupress.com>`
// host — the cause of the "no such host" failures when replying to a display-name
// address. A token that cannot be parsed is kept verbatim so the operator still
// sees it (and gets a clear downstream error) rather than having it dropped.
func parseRecipientList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	// ParseAddressList handles quoted commas inside display names; when the whole
	// list parses, use the extracted bare addresses.
	if list, err := netmail.ParseAddressList(s); err == nil {
		out := make([]string, 0, len(list))
		for _, a := range list {
			if addr := strings.TrimSpace(a.Address); addr != "" {
				out = append(out, addr)
			}
		}
		return out
	}
	// One malformed token fails the whole list, so fall back to per-token parsing.
	var out []string
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if a, err := netmail.ParseAddress(t); err == nil && strings.TrimSpace(a.Address) != "" {
			out = append(out, strings.TrimSpace(a.Address))
		} else {
			out = append(out, t) // keep the raw token; the engine reports the error
		}
	}
	return out
}

func (a *App) handleVayuOSSend(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	// Accept EITHER multipart/form-data (when the composer has attachments) or the
	// legacy JSON body. maxAttachMB bounds total attachment bytes — generous by
	// design (default 50 MB, above Gmail's 25 MB / Outlook's 20 MB), tunable via
	// VAYUMAIL_MAX_ATTACH_MB.
	maxAttachMB := int64(composeMaxAttachMB())
	maxAttachBytes := maxAttachMB << 20

	var in struct {
		From, To, CC, BCC, ReplyTo, Subject, Body string
		AppendSig                                 *bool `json:"appendSig"`
		Encrypt                                   *bool `json:"encrypt"`
		// RichHTML opts into a multipart/alternative with an HTML rendering of the
		// same body. Off by default: a young sending IP scores worse with HTML than
		// with plain text, so this must be a deliberate choice, not a default.
		RichHTML *bool `json:"richHTML"`
	}
	var attachments []vmail.Attachment
	appendSig := true // default: append the sender's signature when one is set
	encrypt := false  // default OFF: plain messages are delivered as readable text
	richHTML := false // default OFF: see RichHTML above — deliverability, not preference

	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		// Cap the whole request at the attachment budget + 1 MB of text/fields.
		r.Body = http.MaxBytesReader(w, r.Body, maxAttachBytes+(1<<20))
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			writeAPIError(w, r, 400, "attach-too-large", "The message (with attachments) exceeds the "+strconv.FormatInt(maxAttachMB, 10)+" MB limit.", "")
			return
		}
		in.From = r.FormValue("from")
		in.To = r.FormValue("to")
		in.CC = r.FormValue("cc")
		in.BCC = r.FormValue("bcc")
		in.ReplyTo = r.FormValue("replyTo")
		in.Subject = r.FormValue("subject")
		in.Body = r.FormValue("body")
		if r.FormValue("appendSig") == "0" {
			appendSig = false
		}
		if r.FormValue("richHTML") == "1" {
			richHTML = true
		}
		if r.FormValue("encrypt") == "1" {
			encrypt = true
		}
		var total int64
		if r.MultipartForm != nil {
			for _, fhs := range r.MultipartForm.File["attachments"] {
				total += fhs.Size
				if total > maxAttachBytes {
					writeAPIError(w, r, 400, "attach-too-large", "Attachments exceed the "+strconv.FormatInt(maxAttachMB, 10)+" MB total limit.", "")
					return
				}
				f, ferr := fhs.Open()
				if ferr != nil {
					writeAPIError(w, r, 400, "attach-read", "Could not read an attachment.", "")
					return
				}
				data, rerr := io.ReadAll(io.LimitReader(f, maxAttachBytes+1))
				f.Close()
				if rerr != nil {
					writeAPIError(w, r, 400, "attach-read", "Could not read an attachment.", "")
					return
				}
				attachments = append(attachments, vmail.Attachment{
					Filename:    fhs.Filename,
					ContentType: fhs.Header.Get("Content-Type"),
					Data:        data,
				})
			}
		}
	} else {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
			writeAPIError(w, r, 400, "invalid_json", err.Error(), "")
			return
		}
		if in.AppendSig != nil {
			appendSig = *in.AppendSig
		}
		if in.Encrypt != nil {
			encrypt = *in.Encrypt
		}
		if in.RichHTML != nil {
			richHTML = *in.RichHTML
		}
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
	splitAddrs := parseRecipientList
	to := splitAddrs(in.To)
	cc := splitAddrs(in.CC)
	bcc := splitAddrs(in.BCC)
	if len(to)+len(cc)+len(bcc) == 0 {
		writeAPIError(w, r, 400, "validation_error", "at least one recipient is required", "")
		return
	}
	// Sending files a copy into the sender's Sent folder, so refuse when the
	// mailbox is already at/over its storage quota.
	if a.vayuMail.MailboxOverQuota(from) {
		writeAPIError(w, r, 400, "over-quota", "Your mailbox is full (storage quota reached). Delete some mail or ask an administrator to raise your quota, then try again.", "")
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
	// Append the sender's signature (unless the composer turned it off), placed
	// after the reply and before any quoted history.
	bodyText := in.Body
	if appendSig {
		if acc := a.vayuMail.Accounts(); acc != nil {
			if sig := acc.SignatureFor(r.Context(), from); sig != "" {
				bodyText = insertSignature(in.Body, sig)
			}
		}
	}
	// Render AFTER the signature is appended, so both parts carry it and neither
	// can be the odd one out.
	htmlBody := ""
	if richHTML {
		htmlBody = renderMailHTML(bodyText)
	}
	id, err := a.vayuMail.ComposeRich(r.Context(), vmail.ComposeMessage{
		From:         fromHeader,
		To:           to,
		CC:           cc,
		BCC:          bcc,
		ReplyTo:      strings.TrimSpace(in.ReplyTo),
		Subject:      in.Subject,
		Body:         bodyText,
		HTML:         htmlBody,
		Attachments:  attachments,
		SenderUserID: senderUserID,
		Encrypt:      encrypt,
	})
	if err != nil {
		writeAPIError(w, r, 500, "send-failed", err.Error(), "")
		return
	}
	writeJSON(w, r, 200, map[string]interface{}{"queued": true, "id": id, "attachments": len(attachments)})
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
	to := parseRecipientList(in.To)
	// Saving a draft files it into the Drafts folder, so refuse when full.
	if a.vayuMail.MailboxOverQuota(from) {
		writeAPIError(w, r, 400, "over-quota", "Your mailbox is full (storage quota reached). Delete some mail or ask an administrator to raise your quota.", "")
		return
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
		User   string   `json:"user"`
		ID     string   `json:"id"`
		IDs    []string `json:"ids"` // bulk: apply the action to each id
		Folder string   `json:"folder"`
		To     string   `json:"to"`     // target folder for move
		Delete bool     `json:"delete"` // permanent delete
		Mark   string   `json:"mark"`   // "read" or "unread"
		Pin    *bool    `json:"pin"`    // pin (true) / unpin (false)
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&in); err != nil {
		writeAPIError(w, r, 400, "invalid_json", err.Error(), "")
		return
	}
	// Accept either a single id or a list; the list is the bulk path.
	ids := in.IDs
	if len(ids) == 0 && in.ID != "" {
		ids = []string{in.ID}
	}
	if in.User == "" || len(ids) == 0 {
		writeAPIError(w, r, 400, "validation_error", "user and id(s) are required", "")
		return
	}
	if len(ids) > 500 {
		writeAPIError(w, r, 400, "too_many", "at most 500 messages per request", "")
		return
	}
	// The mailbox engine key. Admins act on the requested mailbox; a non-admin is
	// locked to their own — resolved server-side (domain included for a secondary
	// mailbox), so the client-supplied in.User can never target another mailbox
	// (VayuDomains Stage 3d).
	// One authority decision, minted in mailReader (ADR-0152).
	rd := a.mailReader(r, in.User)
	mbox := rd.Key()
	{
		if mbox == "" {
			writeAPIError(w, r, http.StatusForbidden, "forbidden", "you can only manage your own mailbox", "")
			return
		}
	}
	from := in.Folder
	if from == "" {
		from = "Inbox"
	}

	// One operation, applied to every id. We collect per-message failures rather
	// than aborting the whole batch, so one stale id can't fail a bulk action.
	var lastID, action string
	failed := 0
	apply := func(id string) error {
		switch {
		case in.Mark == "read":
			nid, err := a.vayuMail.MarkRead(rd, from, id)
			lastID, action = nid, "read"
			return err
		case in.Mark == "unread":
			nid, err := a.vayuMail.MarkUnread(rd, from, id)
			lastID, action = nid, "unread"
			return err
		case in.Pin != nil:
			nid, err := a.vayuMail.SetPinned(rd, from, id, *in.Pin)
			lastID = nid
			if *in.Pin {
				action = "pinned"
			} else {
				action = "unpinned"
			}
			return err
		case in.Delete:
			action = "deleted"
			return a.vayuMail.DeleteMessage(rd, from, id)
		default:
			target := in.To
			if target == "" {
				target = "Trash"
			}
			action = "moved"
			return a.vayuMail.MoveMessage(rd, id, from, target)
		}
	}
	var firstErr string
	for _, id := range ids {
		if err := apply(id); err != nil {
			failed++
			if firstErr == "" {
				firstErr = err.Error()
			}
		}
	}
	// A whole-batch failure (e.g. every id stale) is a real error; partial
	// failures are reported but still 200 so the UI can refresh.
	if failed == len(ids) {
		writeAPIError(w, r, 500, "action-failed", firstErr, "")
		return
	}
	resp := map[string]interface{}{"action": action, "count": len(ids) - failed, "failed": failed}
	if len(ids) == 1 && lastID != "" {
		resp["id"] = lastID
	}
	if in.To != "" {
		resp["moved_to"] = in.To
	}
	writeJSON(w, r, 200, resp)
}

// mailSafeFilename strips path components and header-breaking characters from
// an attachment filename so it is safe to place in a Content-Disposition header.
func mailSafeFilename(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexAny(s, `/\`); i >= 0 {
		s = s[i+1:]
	}
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, s)
	if s == "" {
		return "attachment"
	}
	return s
}

// handleVayuOSAttachment streams a single attachment from a stored message as a
// forced download. The message is PGP-decrypted (ReadFolderMessage) before the
// MIME part is extracted, so encrypted mail's attachments download in the clear.
func (a *App) handleVayuOSAttachment(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		http.Error(w, "VayuMail is not active", http.StatusServiceUnavailable)
		return
	}
	// One authority decision, minted in mailReader (ADR-0152).
	rd := a.mailReader(r, mailUserParam(r))
	user := rd.Key()
	folder := mailFolderParam(r)
	if folder == "" {
		folder = "Inbox"
	}
	id := mailIDParam(r)
	idx, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("idx")))
	if user == "" || id == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	raw, err := a.vayuMail.ReadFolderMessage(rd, folder, id)
	if err != nil {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}
	fn, ctype, data, ok := vmail.ExtractAttachment(raw, idx)
	if !ok {
		http.Error(w, "attachment not found", http.StatusNotFound)
		return
	}
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	// Force a download (never inline-render, so a text/html attachment cannot
	// script), and forbid content-type sniffing.
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", `attachment; filename="`+mailSafeFilename(fn)+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(data)
}

// ── Admin mail accounts (email + password) ───────────────────────────────────

func (a *App) handleVayuOSAccounts(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>Mail accounts</h1></div>`)
	body.WriteString(`<p class="page-sub">Admin-managed email IDs &amp; passwords (SMTP/IMAP login). Each mailbox is a card — tap to expand.</p>`)
	body.WriteString(vayuosNav("accounts", a.isAdminRequest(r)))
	if !a.isAdminRequest(r) {
		body.WriteString(`<div class="empty-state">Mail-account management is available to administrators only. Your own mailbox is under <a href="/os/vayumail/inbox">Mailbox</a>.</div>`)
		writeOSHTML(w, r, adminOSLayout(nonce, "Mail accounts", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || a.vayuMail.Accounts() == nil {
		body.WriteString(`<div class="empty-state">VayuMail is inactive. Set <code>DOMAIN</code> to manage mail accounts.</div>`)
		writeOSHTML(w, r, adminOSLayout(nonce, "Mail accounts", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	domain := a.vayuMail.Config().Domain

	// VayuDomains Stage 3b: when a mail_enabled secondary domain exists, let the
	// operator choose which domain a new mailbox belongs to (each domain has its
	// own isolated store). With none, the address suffix is the fixed primary
	// domain exactly as before.
	addrSuffix := `<span class="vm-suffix">@` + html.EscapeString(domain) + `</span>`
	if secs := a.mailSecondaryHosts(r.Context()); len(secs) > 0 {
		var opts strings.Builder
		opts.WriteString(`<option value="">@` + html.EscapeString(domain) + ` (primary)</option>`)
		for _, h := range secs {
			opts.WriteString(`<option value="` + html.EscapeString(h) + `">@` + html.EscapeString(h) + `</option>`)
		}
		addrSuffix = `<select class="input" data-a-domain aria-label="Mailbox domain">` + opts.String() + `</select>`
	}

	// Create form.
	body.WriteString(`<div class="section-head"><span class="section-head__title">Add a mailbox</span><span class="section-head__hint">Create a new email ID on a mail domain</span></div>`)
	body.WriteString(`<div class="card">
<form data-acct-create>
  <div class="vm-row vm-row--end">
    <label class="field vm-grow"><span class="field-label">Address</span>
      <input class="input" type="text" data-a-local placeholder="name" required>
      ` + addrSuffix + `</label>
    <label class="field vm-grow"><span class="field-label">Full name (optional)</span>
      <input class="input" type="text" data-a-name placeholder="Display name"></label>
    <label class="field"><span class="field-label">Role</span>
      <select class="input" data-a-role>
        <option value="mailbox" selected>Mailbox — mail only, no console (default)</option>
        <option value="reviewer">Reviewer — read-only, mail only</option>
        <option value="author">Author — mail + author console</option>
        <option value="editor">Editor — mail + editor console</option>
        <option value="administrator">Administrator — full console</option>
      </select>
      <span class="vm-suffix">Mail-only roles see just their own mailbox — no other tabs, no other inboxes.</span></label>
    <label class="field"><span class="field-label">Quota (MB, 0 = unlimited)</span>
      <input class="input" type="number" min="0" step="1" data-a-quota placeholder="0" value="0"></label>
    <label class="field vm-grow"><span class="field-label">Password (min 8)</span>
      <input class="input" type="password" data-a-pass placeholder="••••••••" required></label>
    <button class="btn btn--primary" type="submit">Create</button>
  </div>
  <span class="muted text-sm" data-a-status></span>
</form></div>`)

	// Account recovery (ADR-0144). Placed above the mailbox list because its
	// readiness view is a standing question about every mailbox below it, and
	// because a factor nobody enrolled is invisible until the day it is needed.
	{
		var boxes []string
		if accs, err := a.vayuMail.Accounts().List(r.Context()); err == nil {
			for _, ac := range accs {
				if ac.Active {
					boxes = append(boxes, ac.Email)
				}
			}
		}
		body.WriteString(a.recoveryCardHTML(r, nonce, boxes))
	}

	// Existing accounts — a live, HTMX-swappable list of collapsible mailbox cards
	// (VayuMail Accounts redesign). Every inline action swaps this fragment in
	// place, and the create / 2FA / set-password flows refresh it via htmx.ajax, so
	// the page never does a full reload.
	body.WriteString(`<div class="section-head"><span class="section-head__title">Mailboxes</span><span class="section-head__hint">Every email ID on this install</span></div>`)
	body.WriteString(`<div id="vm-accounts-list">` + a.vayuAccountsList(r.Context()) + `</div>`)

	// Devices — approval-gated sync credentials (ADR-0129): pending devices
	// need an explicit Approve here before any mail syncs to them. The card polls
	// itself so a newly-registered pending device surfaces without a page reload.
	body.WriteString(`<div id="vm-device-card">` + a.vayuDevicesCard(r.Context()) + `</div>`)

	// Every per-mailbox control — forwarding, vacation, aliases and filters — now
	// lives inside that mailbox's own card in #vm-accounts-list above, so there are
	// no separate account-wide alias/filter cards here.

	body.WriteString(`<script nonce="` + nonce + `" src="/os/static/js/admin-os-mail.js?v=` + assetVer("js/admin-os-mail.js") + `"></script>`)
	writeOSHTML(w, r, adminOSLayout(nonce, "Mail accounts", "vayuos", cfg, htmpl.HTML(body.String())))
}

// handleVayuOSFilterAction creates or deletes a delivery rule and returns the
// refreshed card (HTMX swap). Admin-only (the card lives on the Accounts page).
func (a *App) handleVayuOSFilterAction(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrators only", "")
		return
	}
	_ = r.ParseForm()
	accts := a.vayuMail.Accounts()
	email := strings.TrimSpace(r.FormValue("email"))
	var opErr error
	switch r.FormValue("op") {
	case "create":
		action, target := r.FormValue("action"), ""
		if f, ok := strings.CutPrefix(action, "move:"); ok {
			action, target = "move", f
		}
		opErr = accts.CreateFilter(r.Context(), vmail.FilterRule{
			Mailbox: email, Field: r.FormValue("field"),
			Contains: r.FormValue("contains"), Action: action, Target: target,
		})
		if opErr == nil {
			dbpkg.AuditLog("vayumail.filter.create", dbpkg.AuditActor(r), email, r.FormValue("field")+"~"+r.FormValue("contains"))
		}
	case "delete":
		id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
		opErr = accts.DeleteFilter(r.Context(), email, id)
		if opErr == nil {
			dbpkg.AuditLog("vayumail.filter.delete", dbpkg.AuditActor(r), email, r.FormValue("id"))
		}
	default:
		opErr = errors.New("unknown operation")
	}
	// Filters are driven from inside each mailbox's card, so refresh the list.
	card := a.vayuAccountsList(r.Context())
	if opErr != nil {
		card = `<div class="empty-state" role="alert">⚠ ` + html.EscapeString(opErr.Error()) + `</div>` + card
	}
	writeOSHTML(w, r, card)
}

// handleVayuOSAutoreplyAction saves a mailbox's autoresponder settings and
// returns the refreshed card (HTMX swap). Admin-only (the card lives on the
// admin Accounts page).
func (a *App) handleVayuOSAutoreplyAction(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrators only", "")
		return
	}
	_ = r.ParseForm()
	email := strings.TrimSpace(r.FormValue("email"))
	ar := vmail.Autoreply{
		Enabled: r.FormValue("enabled") == "1",
		Subject: strings.TrimSpace(r.FormValue("subject")),
		Body:    strings.TrimSpace(r.FormValue("body")),
	}
	// Dates are whole days in the operator's intent: the window opens at the
	// start of the first day and closes at the END of the last day.
	if v := strings.TrimSpace(r.FormValue("from")); v != "" {
		if t, err := time.ParseInLocation("2006-01-02", v, time.Local); err == nil {
			ar.From = t
		}
	}
	if v := strings.TrimSpace(r.FormValue("until")); v != "" {
		if t, err := time.ParseInLocation("2006-01-02", v, time.Local); err == nil {
			ar.Until = t.Add(24*time.Hour - time.Second)
		}
	}
	var opErr error
	if ar.Enabled && strings.TrimSpace(ar.Body) == "" {
		opErr = errors.New("an enabled autoresponder needs a message body")
	} else {
		opErr = a.vayuMail.Accounts().SetAutoreply(r.Context(), email, ar)
		if opErr == nil {
			onOff := "off"
			if ar.Enabled {
				onOff = "on"
			}
			dbpkg.AuditLog("vayumail.autoreply.set", dbpkg.AuditActor(r), email, onOff)
		}
	}
	// The vacation control now lives inside each mailbox's card, so refresh the
	// whole accounts list in place.
	card := a.vayuAccountsList(r.Context())
	if opErr != nil {
		card = `<div class="empty-state" role="alert">⚠ ` + html.EscapeString(opErr.Error()) + `</div>` + card
	}
	writeOSHTML(w, r, card)
}

// handleVayuOSAliasAction applies an alias/forward change and returns the
// refreshed card (HTMX swap). Admin-only.
func (a *App) handleVayuOSAliasAction(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrators only", "")
		return
	}
	_ = r.ParseForm()
	accts := a.vayuMail.Accounts()
	fallbackDomain := a.vayuMail.Config().Domain
	var opErr error
	switch r.FormValue("op") {
	case "alias-create":
		local := strings.ToLower(strings.TrimSpace(r.FormValue("local")))
		target := r.FormValue("target")
		// The alias lives on the target mailbox's own domain, so a secondary-domain
		// mailbox gets secondary-domain aliases (VayuDomains).
		aliasDomain := emailDomain(target, fallbackDomain)
		if local == "" || strings.ContainsAny(local, "@ \t") {
			opErr = errors.New("invalid alias name")
		} else {
			alias := local + "@" + aliasDomain
			opErr = accts.CreateAlias(r.Context(), alias, target)
			if opErr == nil {
				dbpkg.AuditLog("vayumail.alias.create", dbpkg.AuditActor(r), alias, target)
			}
		}
	case "alias-delete":
		opErr = accts.DeleteAlias(r.Context(), r.FormValue("alias"))
		if opErr == nil {
			dbpkg.AuditLog("vayumail.alias.delete", dbpkg.AuditActor(r), r.FormValue("alias"), "")
		}
	case "forward-set":
		fwd := strings.TrimSpace(r.FormValue("forward"))
		if fwd != "" {
			if _, perr := netmail.ParseAddress(fwd); perr != nil {
				opErr = errors.New("invalid forward address")
			}
		}
		if opErr == nil {
			opErr = accts.SetForward(r.Context(), r.FormValue("email"), fwd)
			if opErr == nil {
				dbpkg.AuditLog("vayumail.forward.set", dbpkg.AuditActor(r), r.FormValue("email"), fwd)
			}
		}
	default:
		opErr = errors.New("unknown operation")
	}
	// Aliases, forwarding and vacation are all driven from inside each mailbox's
	// card now, so every action refreshes the accounts list in place.
	card := a.vayuAccountsList(r.Context())
	if opErr != nil {
		card = `<div class="empty-state" role="alert">⚠ ` + html.EscapeString(opErr.Error()) + `</div>` + card
	}
	writeOSHTML(w, r, card)
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

// handleMailAutoconfig serves the Mozilla Autoconfig document so Thunderbird and
// K-9 / Thunderbird-for-Android configure an account from just the email address
// + password (no manual host/port entry). It is public and unauthenticated by
// design — it contains only the same server hostnames/ports already printed on
// the Connect tab, never any secret. Served at
// /.well-known/autoconfig/mail/config-v1.1.xml on the site's own (trusted-cert)
// domain, which is where these clients look first.
func (a *App) handleMailAutoconfig(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil {
		http.NotFound(w, r)
		return
	}
	mc := a.vayuMail.Config()
	primary := strings.TrimSpace(mc.Domain)
	if primary == "" {
		primary = config.Cfg.Domain
	}
	// Per-domain (Stage 3c): the account domain follows the request Host; the
	// advertised server host stays the primary mail host with its valid cert.
	domain := a.autoconfigDomain(r.Host, primary)
	host := strings.TrimSpace(mc.Hostname)
	if host == "" {
		host = "mail." + primary
	}
	imaps := mailPort(mc.IMAPSListen, "993")
	pop3s := mailPort(mc.POP3SListen, "995")
	sub := mailPort(mc.SubmissionListen, "587")

	esc := func(s string) string { return html.EscapeString(s) }
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<clientConfig version="1.1">
  <emailProvider id="` + esc(domain) + `">
    <domain>` + esc(domain) + `</domain>
    <displayName>` + esc(domain) + ` Mail</displayName>
    <displayShortName>` + esc(domain) + `</displayShortName>
    <incomingServer type="imap">
      <hostname>` + esc(host) + `</hostname>
      <port>` + esc(imaps) + `</port>
      <socketType>SSL</socketType>
      <authentication>password-cleartext</authentication>
      <username>%EMAILADDRESS%</username>
    </incomingServer>
    <incomingServer type="pop3">
      <hostname>` + esc(host) + `</hostname>
      <port>` + esc(pop3s) + `</port>
      <socketType>SSL</socketType>
      <authentication>password-cleartext</authentication>
      <username>%EMAILADDRESS%</username>
    </incomingServer>
    <outgoingServer type="smtp">
      <hostname>` + esc(host) + `</hostname>
      <port>` + esc(sub) + `</port>
      <socketType>STARTTLS</socketType>
      <authentication>password-cleartext</authentication>
      <username>%EMAILADDRESS%</username>
    </outgoingServer>
  </emailProvider>
</clientConfig>`
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(xml))
}

// VayuMailAutoconfigSchema versions the first-party autoconfig JSON. VayuMail
// clients read this to confirm they understand the document shape before
// trusting it. The value is pinned by a contract test shared with the
// VayuMail-Mobile client (autoconfig_contract_test.go on both sides) — bump it
// only alongside a coordinated client change.
const VayuMailAutoconfigSchema = "vayumail-autoconfig/1"

// vayuMailAutoconfig is the first-party mail-autoconfiguration document served
// at /.well-known/vayumail/autoconfig.json. It carries the same public server
// hostnames/ports as the Mozilla/Thunderbird XML (handleMailAutoconfig) but in
// an easy-to-parse JSON the VayuMail app consumes to set up an account from just
// an email address. It contains no secrets — only what the Connect tab already
// prints.
type vayuMailAutoconfig struct {
	Schema          string               `json:"schema"`
	Domain          string               `json:"domain"`
	DisplayName     string               `json:"displayName"`
	IMAP            vayuMailServerConfig `json:"imap"`
	POP3            vayuMailServerConfig `json:"pop3"`
	SMTP            vayuMailServerConfig `json:"smtp"`
	UsernameIsEmail bool                 `json:"usernameIsEmail"`
	Auth            string               `json:"auth"`
	WKD             bool                 `json:"wkd"`
	// Talk is the hostname the VayuTalk relay is reachable on — a dedicated
	// subdomain (e.g. talk.<domain>) the operator points STRAIGHT at the origin
	// with any CDN/proxy (Cloudflare) turned OFF, so the app's long-lived SSE
	// stream is never buffered or bot-challenged. Empty (the default, and the
	// omitted JSON field) means "no dedicated talk host" — the app then uses the
	// mail domain exactly as before, so this is fully backward compatible. It is
	// only populated once the deploy script has provisioned the subdomain's TLS
	// certificate and set VAYUOS_TALK_HOST, so a client is never pointed at a host
	// that isn't serving yet.
	Talk string `json:"talk,omitempty"`
}

// vayuMailServerConfig is one server endpoint in the autoconfig document. TLS is
// "tls" (implicit, from the first byte) or "starttls" (upgrade), matching the
// VayuMail client's account.TLSMode values verbatim.
type vayuMailServerConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	TLS  string `json:"tls"`
}

// buildVayuMailAutoconfig derives the autoconfig document from the running mail
// server configuration. Kept separate from the handler so the contract test can
// assert the emitted shape without spinning up HTTP.
// autoconfigDomain resolves which mail domain an autoconfig request is for: the
// primary, or a mail_enabled secondary derived from the request Host (VayuDomains
// Stage 3c). The advertised server host stays the primary mail host — its cert is
// valid and it serves every domain's mailboxes, whose users log in with their own
// address — so only the account domain / display name changes per host.
func (a *App) autoconfigDomain(reqHost, primary string) string {
	h := strings.ToLower(strings.TrimSpace(reqHost))
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	h = strings.TrimPrefix(h, "mail.")
	if h != "" && !strings.EqualFold(h, primary) && a.acceptsSecondaryMailDomain(h) {
		return h
	}
	return primary
}

// buildVayuMailAutoconfigFor returns the autoconfig document for the domain
// implied by reqHost (primary when empty or unrecognised), so a mail_enabled
// secondary domain's clients auto-configure with their own address.
func (a *App) buildVayuMailAutoconfigFor(reqHost string) vayuMailAutoconfig {
	mc := a.vayuMail.Config()
	primary := strings.TrimSpace(mc.Domain)
	if primary == "" {
		primary = config.Cfg.Domain
	}
	domain := a.autoconfigDomain(reqHost, primary)
	host := strings.TrimSpace(mc.Hostname)
	if host == "" {
		host = "mail." + primary
	}
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	return vayuMailAutoconfig{
		Schema:          VayuMailAutoconfigSchema,
		Domain:          domain,
		DisplayName:     domain + " Mail",
		IMAP:            vayuMailServerConfig{Host: host, Port: atoi(mailPort(mc.IMAPSListen, "993")), TLS: "tls"},
		POP3:            vayuMailServerConfig{Host: host, Port: atoi(mailPort(mc.POP3SListen, "995")), TLS: "tls"},
		SMTP:            vayuMailServerConfig{Host: host, Port: atoi(mailPort(mc.SubmissionListen, "587")), TLS: "starttls"},
		UsernameIsEmail: true,
		Auth:            "password",
		WKD:             true,
		Talk:            a.talkAutoconfigHost(),
	}
}

// talkAutoconfigHost returns the hostname to advertise for the VayuTalk relay, or
// "" to advertise none — and ONLY when the relay is actually enabled. The
// subdomain helper publishes the host after it has obtained that subdomain's TLS
// certificate, so the app is never handed a talk host that is not live. When
// empty the app falls back to the mail domain, so existing servers keep working.
//
// THE SETTING FIRST, THE ENVIRONMENT AS A FALLBACK (ADR-0155 P2).
//
// This used to read VAYUOS_TALK_HOST and nothing else, which is why publishing a
// talk subdomain restarted the whole install: a process's environment cannot
// change without an exec. nginx has no queue in front of :8080, so every second
// of that restart was a 502 for every visitor — an outage to advertise a
// hostname.
//
// Read from settings and the same change lands on the next request with nothing
// interrupted. The env var is still honoured, and that ordering is deliberate:
// an install provisioned before this existed has the variable and no setting, so
// it keeps working untouched; an install that has both is one where the operator
// set the newer value, and the newer value wins.
func (a *App) talkAutoconfigHost() string {
	if !a.vayuTalkEnabled() {
		return ""
	}
	if a.siteSettings != nil {
		if h := strings.ToLower(strings.TrimSpace(
			a.siteSettings.Get(context.Background(), settings.ForPrimary(), settings.KeyTalkHost))); h != "" {
			return h
		}
	}
	return strings.ToLower(strings.TrimSpace(config.EnvOr("VAYUOS_TALK_HOST", "")))
}

// handleVayuMailAutoconfigJSON serves the first-party autoconfig JSON. Public and
// unauthenticated by design (same rationale as handleMailAutoconfig): it exposes
// only public server coordinates, never a secret. VayuMail-Mobile fetches it at
// https://<domain>/.well-known/vayumail/autoconfig.json to onboard by email.
func (a *App) handleVayuMailAutoconfigJSON(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_ = json.NewEncoder(w).Encode(a.buildVayuMailAutoconfigFor(r.Host))
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
	body.WriteString(`<div class="page-header"><h1>Connect a mail app</h1></div>`)
	body.WriteString(`<p class="page-sub">IMAP / POP3 / SMTP settings for the Gmail app, Apple Mail, Thunderbird, Outlook and more.</p>`)
	body.WriteString(vayuosNav("connect", a.isAdminRequest(r)))

	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		body.WriteString(`<div class="empty-state">VayuMail is inactive. Set <code>DOMAIN</code> to enable mailboxes and mail-client access.</div>`)
		writeOSHTML(w, r, adminOSLayout(nonce, "Connect a mail app", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}

	// The holder's OWN recovery enrolment (ADR-0144 Phase 2). Placed here because
	// this is the page someone already visits when setting their mail up, which is
	// the one moment they are thinking about access to this mailbox at all. It
	// renders only for a signed-in holder with an assigned mailbox, and shows just
	// theirs — the install-wide readiness view stays on the admin Accounts page.
	body.WriteString(a.selfRecoveryCardHTML(r, nonce))

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
	body.WriteString(`<div class="section-head"><span class="section-head__title">Service status</span><span class="section-head__hint">Live mail listener health</span></div>`)
	body.WriteString(`<div class="card">`)
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
		body.WriteString(`<p class="text-sm"><strong>This is a one-time step and is SEPARATE from updating VayuPress</strong> — the update command only swaps the binary; it never provisions the mail certificate. Run this once on the server (it issues a real Let's Encrypt certificate for <code>` + hHost + `</code> through nginx, makes it readable by the mail service, and wires it in):</p>`)
		body.WriteString(`<pre class="mono text-sm" style="white-space:pre-wrap;background:var(--bg-surface-2);padding:10px;border-radius:8px">cd /tmp/VayuPress &amp;&amp; git pull origin main &amp;&amp; sudo bash deploy/vayumail-setup.sh</pre>`)
		body.WriteString(`<p class="text-sm">Then reload this page. It auto-renews and is auto-discovered on restart (no env vars needed). If the script reports a DNS or port-80 problem, fix that and re-run it. Alternatives:</p>`)
		body.WriteString(`<ul class="text-sm">` +
			`<li><strong>Built-in ACME (only if port 80 is free):</strong> set <code>VAYUOS_MAIL_TLS_ACME=on</code> and <code>VAYUOS_MAIL_ACME_EMAIL=you@` + html.EscapeString(mc.Domain) + `</code>, then restart. On this box nginx owns port 80, so use the script above instead — or point a free port via <code>VAYUOS_MAIL_ACME_HTTP_ADDR=127.0.0.1:8081</code> and proxy <code>` + hHost + `/.well-known/acme-challenge/</code> to it in nginx.</li>` +
			`<li><strong>Manual / existing certbot cert:</strong> set <code>VAYUOS_MAIL_TLS_CERT</code> and <code>VAYUOS_MAIL_TLS_KEY</code> to a CA-signed pair (e.g. <code>/etc/letsencrypt/live/` + hHost + `/fullchain.pem</code> and <code>privkey.pem</code>), then restart. VayuMail hot-reloads on renewal.</li>` +
			`</ul>`)
		body.WriteString(`<p class="text-sm muted">Also make sure DNS has an A record for <code>` + hHost + `</code> pointing at this server, and that ports 25/143/993/587/995/110 are open in your firewall (the script handles the firewall + privileged-port binding too).</p>`)
		body.WriteString(`</div>`)
	} else if a.vayuMail.TLSActive() && a.vayuMail.TLSTrusted() {
		body.WriteString(`<div class="card"><div class="card-title">TLS certificate</div>`)
		body.WriteString(`<p class="text-sm">A trusted certificate is active — mail apps can connect over SSL/TLS. <span class="muted">(` + html.EscapeString(a.vayuMail.TLSNote()) + `)</span></p>`)
		// Hostname-match check: a trusted cert that does NOT cover the hostname
		// clients are told to use is the classic "desktop syncs, mobile doesn't"
		// trap — desktop Thunderbird lets the user accept the mismatch, but the
		// Gmail app (validating from Google's servers) and Thunderbird for Android
		// silently refuse. Surface the mismatch and the exact connect hostname.
		if covered := a.vayuMail.TLSCertHosts(); len(covered) > 0 && !a.vayuMail.TLSCertCovers(host) {
			body.WriteString(`<p class="text-sm" style="color:#d9844f">⚠ The certificate does <strong>not</strong> cover <code>` + hHost + `</code>, the server your apps are told to connect to. Desktop apps let you accept this, but the <strong>Gmail app and Thunderbird for Android refuse it</strong> — which is why mobile won't sync.</p>`)
			body.WriteString(`<p class="text-sm">This certificate is valid for: <code>` + html.EscapeString(strings.Join(covered, "</code>, <code>")) + `</code>.</p>`)
			body.WriteString(`<p class="text-sm"><strong>Fix it one of two ways:</strong></p><ul class="text-sm">`)
			body.WriteString(`<li>Set the mail hostname to a name the certificate already covers — e.g. <code>VAYUOS_MAIL_HOSTNAME=` + html.EscapeString(covered[0]) + `</code> — and restart, so Connect/Autoconfig hand clients the matching name; or</li>`)
			body.WriteString(`<li>Reissue the certificate to include <code>` + hHost + `</code> (e.g. <code>sudo bash deploy/vayumail-setup.sh</code>, or add <code>-d ` + hHost + `</code> to your certbot command), then restart.</li>`)
			body.WriteString(`</ul>`)
		}
		// Even in ACME mode, warn if the challenge responder can't bind — renewals
		// will eventually fail and the cert will expire back into self-signed.
		if acmeErr != "" {
			body.WriteString(`<p class="text-sm" style="color:#d9844f">⚠ Auto-renewal may fail: ` + html.EscapeString(acmeErr) +
				` (port 80 is held by another service). Switch to the guided script (<code>sudo bash deploy/vayumail-setup.sh</code>), which renews through nginx, to avoid the certificate expiring.</p>`)
		}
		body.WriteString(`</div>`)
	}

	// ── Recommended app: VayuMail Mobile ──────────────────────────────────────
	// VayuPress's own official mobile client. Plain external links only —
	// CSP-safe (no third-party assets are loaded).
	body.WriteString(`<div class="card"><div class="card-title">📱 VayuMail — the official mobile app</div>`)
	body.WriteString(`<p class="text-sm">The easiest way to use your mailboxes on the go. <strong>VayuMail Mobile</strong> is VayuPress's own open-source app — connecting it takes about 30 seconds:</p>`)
	body.WriteString(`<ol class="text-sm">` +
		`<li><strong>Install the app</strong> (links below).</li>` +
		`<li><strong>Enter your email address and an app password</strong> — <a href="#vm-apppw-card">create one below</a>.</li>` +
		`<li>Done. The app <strong>auto-discovers every server setting</strong> from <span class="mono">/.well-known/vayumail/autoconfig.json</span> and <strong>auto-syncs PGP keys via WKD</strong>, so your mail stays end-to-end encrypted on your phone — no host, port or key typing.</li>` +
		`</ol>`)
	body.WriteString(`<div class="vm-row mt-1">` +
		`<a class="btn btn--primary btn--sm" href="https://github.com/johalputt/VayuMail-Mobile/releases" target="_blank" rel="noopener noreferrer">Download app ↗</a>` +
		`<a class="btn btn--ghost btn--sm" href="https://github.com/johalputt/VayuMail-Mobile" target="_blank" rel="noopener noreferrer">Source ↗</a>` +
		`</div>`)
	body.WriteString(`<p class="muted text-xs mt-2">Prefer another client? Any standard mail app (Apple Mail, the Gmail app, Outlook, Thunderbird) also connects using the manual settings below. With the trusted certificate active there is no security warning to accept.</p>`)
	body.WriteString(`</div>`)

	// ── App passwords — the credential the mobile app signs in with ──────────
	// Create/revoke swap the card in place (HTMX), same pattern as the alias /
	// autoreply / filter cards on the Accounts page.
	body.WriteString(`<div id="vm-apppw-card">` + a.vayuAppPasswordsCard(r) + `</div>`)

	// ── Instant setup (Mozilla Autoconfig) ────────────────────────────────────
	// Thunderbird and K-9/Thunderbird-for-Android auto-discover server settings
	// from a per-domain autoconfig XML: the user types only their email address
	// and password, and the client fills in IMAP/SMTP host, ports and security.
	body.WriteString(`<div class="card"><div class="card-title">Instant setup — no manual server entry</div>`)
	body.WriteString(`<p class="text-sm">The VayuMail app and most standard clients <strong>auto-configure</strong> from just your email address: choose <em>Add account</em>, enter your <span class="mono">you@` + html.EscapeString(mc.Domain) + `</span> address and mailbox password, and every server setting is filled in for you — no host/port typing.</p>`)
	body.WriteString(`<p class="muted text-xs">Published at <span class="mono">https://` + html.EscapeString(mc.Domain) + `/.well-known/autoconfig/mail/config-v1.1.xml</span> and <span class="mono">/.well-known/vayumail/autoconfig.json</span> (the VayuMail app reads the latter). If a client asks, the incoming server is <span class="mono">` + hHost + `</span> (IMAP ` + imapsPort + ` SSL) and outgoing is <span class="mono">` + hHost + `</span> (SMTP ` + subPort + ` STARTTLS).</p>`)
	body.WriteString(`</div>`)

	// ── Recommended settings ─────────────────────────────────────────────────
	body.WriteString(`<div class="card"><div class="card-title">Recommended settings</div>`)
	body.WriteString(`<div class="table-wrap"><table class="table"><tbody>`)
	body.WriteString(`<tr><th>Incoming · IMAP (recommended)</th><td class="mono text-sm">` + hHost + `</td><td>port ` + imapsPort + ` · SSL/TLS</td></tr>`)
	body.WriteString(`<tr><th>Incoming · IMAP (alternative)</th><td class="mono text-sm">` + hHost + `</td><td>port ` + imapPort + ` · STARTTLS</td></tr>`)
	body.WriteString(`<tr><th>Incoming · POP3</th><td class="mono text-sm">` + hHost + `</td><td>port ` + pop3sPort + ` SSL · or ` + pop3Port + ` STLS</td></tr>`)
	body.WriteString(`<tr><th>Outgoing · SMTP</th><td class="mono text-sm">` + hHost + `</td><td>port ` + subPort + ` · STARTTLS · authentication required</td></tr>`)
	body.WriteString(`<tr><th>Username</th><td colspan="2">your full email address (e.g. <span class="mono">you@` + html.EscapeString(mc.Domain) + `</span>)</td></tr>`)
	body.WriteString(`<tr><th>Password</th><td colspan="2">an <a href="#vm-apppw-card">app password</a> (recommended for devices) or your mailbox password (set under <a href="/os/vayumail/accounts">Accounts</a>)</td></tr>`)
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
		body.WriteString(`<tr><td colspan="4" class="muted">No active mailboxes yet. Create one under <a href="/os/vayumail/accounts">Accounts</a>.</td></tr>`)
	}
	for _, em := range emails {
		e := html.EscapeString(em)
		body.WriteString(`<tr><td class="mono">` + e + `</td>` +
			`<td class="text-sm">` + hHost + `:` + imapsPort + ` SSL</td>` +
			`<td class="text-sm">` + hHost + `:` + pop3sPort + ` SSL</td>` +
			`<td class="text-sm">` + hHost + `:` + subPort + ` STARTTLS</td></tr>`)
	}
	body.WriteString(`</tbody></table></div></div>`)

	writeOSHTML(w, r, adminOSLayout(nonce, "Connect a mail app", "vayuos", cfg, htmpl.HTML(body.String())))
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
		Local   string   `json:"local"`
		Name    string   `json:"name"`
		Pass    string   `json:"pass"`
		Role    string   `json:"role"`
		Domain  string   `json:"domain"` // "" or the primary => primary mailbox; a mail_enabled secondary => isolated secondary mailbox (Stage 3b)
		QuotaMB *float64 `json:"quota_mb"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&in); err != nil {
		writeAPIError(w, r, 400, "invalid_json", err.Error(), "")
		return
	}
	// Minting a console-capable identity requires a human session — see
	// isAdminSession. A mail:write key may provision an ordinary mailbox; it may
	// not create one that can sign in to the console, because that would be a
	// scoped key promoting itself to install owner.
	if mailRoleGrantsConsole(in.Role) && !a.isAdminSession(r) {
		writeAPIError(w, r, http.StatusForbidden, "session-admin-required",
			"creating a mailbox whose role grants console access requires an administrator session; an API key cannot do it", "")
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
	// VayuDomains Stage 3b: a mailbox may be created on the primary (default) or on
	// a mail_enabled secondary domain, provisioned into that domain's isolated
	// Maildir. mailDomain stays "" for the primary so the Maildir path is
	// byte-identical to before.
	mailDomain := ""
	targetHost := a.vayuMail.Config().Domain
	if in.Domain = strings.ToLower(strings.TrimSpace(in.Domain)); in.Domain != "" && !strings.EqualFold(in.Domain, targetHost) {
		if !a.acceptsSecondaryMailDomain(in.Domain) {
			writeAPIError(w, r, 400, "validation_error", "not a mail-enabled domain — enable mail for it under Domains first", "")
			return
		}
		mailDomain = in.Domain
		targetHost = in.Domain
		// A hosted client's mailboxes are metered: the studio grants a number "on
		// request" and the client never mints their own. Enforced HERE, at the one
		// path that can create a mailbox on a secondary domain — the member
		// self-claim paths pass an empty mailDomain and so only ever touch the
		// primary, which is the agency's own install and is not metered.
		if over, msg := a.mailboxAllowanceExceeded(r.Context(), in.Domain); over {
			writeAPIError(w, r, http.StatusConflict, "allowance-reached", msg, "")
			return
		}
	}
	var quotaBytes int64
	if in.QuotaMB != nil && *in.QuotaMB > 0 {
		quotaBytes = int64(*in.QuotaMB * 1024 * 1024)
	}
	email, perr := a.provisionMailbox(r.Context(), mailDomain, local, targetHost, hash, in.Name, in.Role, quotaBytes)
	if perr != nil {
		writeAPIError(w, r, 400, "create-failed", perr.Error(), "")
		return
	}
	writeJSON(w, r, 201, map[string]string{"email": email})
}

// provisionMailbox creates a VayuMail account, its Maildir folders and a PGP
// keypair in one place — shared by the operator create handler and the member
// self-service claim. mailDomain is "" for the primary domain (byte-identical
// Maildir path) or a mail-enabled secondary; quotaBytes 0 = unlimited. Returns
// the full address. The PGP keygen is best-effort (a key failure never fails the
// account creation).
func (a *App) provisionMailbox(ctx context.Context, mailDomain, local, targetHost, passwordHash, name, role string, quotaBytes int64) (string, error) {
	email := local + "@" + targetHost
	if err := a.vayuMail.Accounts().Create(ctx, email, passwordHash, name, role); err != nil {
		return "", err
	}
	if quotaBytes > 0 {
		_ = a.vayuMail.Accounts().SetQuota(ctx, email, quotaBytes)
	}
	_ = a.vayuMail.CreateMailbox(mailDomain, local)
	if a.vayuPGP != nil {
		if _, err := a.vayuPGP.EnsureKeypair(&vpgp.PGPUser{UserID: email, Name: name, Email: email}); err != nil {
			logging.LogError("vayuos", "auto PGP keygen failed for "+email, err.Error())
		}
	}
	return email, nil
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
	// Deleting a console-capable mailbox is a lockout, and it was reachable by a
	// mail:write key — see mailCredentialActionAuthorized.
	if !a.mailCredentialActionAuthorized(r, in.Email) {
		writeMailSessionRequired(w, r)
		return
	}
	if err := a.vayuMail.Accounts().Delete(r.Context(), in.Email); err != nil {
		writeAPIError(w, r, 500, "delete-failed", err.Error(), "")
		return
	}
	dbpkg.AuditLog("vayumail.account.delete", dbpkg.AuditActor(r), in.Email, "")
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
		Email     string   `json:"email"`
		Pass      string   `json:"pass"`
		Active    *bool    `json:"active"`
		Role      string   `json:"role"`
		QuotaMB   *float64 `json:"quota_mb"`  // mailbox storage limit in MB; 0 = unlimited
		Signature *string  `json:"signature"` // plain-text mail signature (nil = leave unchanged)
		// Retention window in days (ADR-0130): read mail auto-deletes this many
		// days after being read; 0 turns retention off; nil = leave unchanged.
		RetentionDays *int `json:"retention_days"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&in); err != nil {
		writeAPIError(w, r, 400, "invalid_json", err.Error(), "")
		return
	}
	if strings.TrimSpace(in.Email) == "" {
		writeAPIError(w, r, 400, "validation_error", "email is required", "")
		return
	}
	// Account management is admin-only, with ONE exception: a mailbox holder may
	// set their OWN signature (and nothing else) so signatures are self-service.
	if !a.isAdminRequest(r) {
		_, own := a.ownMailbox(r)
		onlySignature := in.Signature != nil && in.Pass == "" && in.Active == nil &&
			strings.TrimSpace(in.Role) == "" && in.QuotaMB == nil && in.RetentionDays == nil
		if own == "" || !strings.EqualFold(own, in.Email) || !onlySignature {
			writeAPIError(w, r, http.StatusForbidden, "forbidden", "you can only edit your own signature", "")
			return
		}
	}
	// PROMOTING an existing mailbox to a console-capable role is the same act as
	// creating one, and was reachable by the same scoped key — see isAdminSession.
	// Gated after the block above so a mailbox holder's signature edit (which
	// carries no Role) is unaffected.
	if mailRoleGrantsConsole(in.Role) && !a.isAdminSession(r) {
		writeAPIError(w, r, http.StatusForbidden, "session-admin-required",
			"promoting a mailbox to a role that grants console access requires an administrator session; an API key cannot do it", "")
		return
	}
	// And the other half of that door. The guard above reads the SUBMITTED role,
	// so a request carrying no Role at all sailed past it — which is all a
	// password reset needs. Taking over a mailbox that is ALREADY console-capable
	// is the same act as promoting one, so it takes the same session.
	//
	// Quota, retention and signature are excluded on purpose: none of them
	// changes who can sign in, and fencing them would break ordinary automation
	// for no security gain.
	credentialChange := in.Pass != "" || in.Active != nil || strings.TrimSpace(in.Role) != ""
	if credentialChange && !a.mailCredentialActionAuthorized(r, in.Email) {
		writeMailSessionRequired(w, r)
		return
	}
	if in.Signature != nil {
		if err := a.vayuMail.Accounts().SetSignature(r.Context(), in.Email, *in.Signature); err != nil {
			writeAPIError(w, r, 400, "update-failed", err.Error(), "")
			return
		}
	}
	if in.RetentionDays != nil {
		if err := a.vayuMail.Accounts().SetRetentionDays(r.Context(), in.Email, *in.RetentionDays); err != nil {
			writeAPIError(w, r, 400, "update-failed", err.Error(), "")
			return
		}
		dbpkg.AuditLog("vayumail.retention.set", dbpkg.AuditActor(r), in.Email,
			strconv.Itoa(*in.RetentionDays)+" days")
	}
	if in.QuotaMB != nil {
		bytes := int64(*in.QuotaMB * 1024 * 1024)
		if bytes < 0 {
			bytes = 0
		}
		if err := a.vayuMail.Accounts().SetQuota(r.Context(), in.Email, bytes); err != nil {
			writeAPIError(w, r, 400, "update-failed", err.Error(), "")
			return
		}
	}
	if in.Pass != "" {
		// An administrator reset runs the SAME pipeline as a self-service one
		// (ADR-0144). Setting the hash alone used to leave every app password,
		// webmail session and queued message intact — so an operator resetting a
		// compromised mailbox believed they had cut the attacker off, and had not.
		deps, ok := a.mailResetDepsFor()
		if !ok {
			writeAPIError(w, r, 503, "unavailable", "VayuMail is not running", "")
			return
		}
		out, err := applyMailPasswordReset(r.Context(), deps, in.Email, in.Pass,
			mailResetByAdmin, dbpkg.AuditActor(r))
		if err != nil {
			writeAPIError(w, r, 400, "update-failed", err.Error(), "")
			return
		}
		if len(out.Problems) > 0 {
			// The password DID change, so this is not an error — but the operator
			// must not be told the account is clean when a revocation step failed.
			writeJSON(w, r, 200, map[string]interface{}{
				"updated": true, "warnings": out.Problems,
				"app_passwords_revoked": out.AppPasswordsRevoked,
				"sessions_revoked":      out.SessionsRevoked,
			})
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
	// Stripping the second factor from a mailbox that can sign in to the console
	// is a credential change in everything but name — it is the step an attacker
	// takes between resetting a password and using it. Enrolling a NEW secret is
	// the same act from the other side: it hands the caller the factor.
	if !a.mailCredentialActionAuthorized(r, email) {
		writeMailSessionRequired(w, r)
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
		// Include a scannable QR (CSP-safe data: PNG) alongside the manual key so
		// the operator can point an authenticator app at it instead of typing the
		// secret. The otpauth:// label already carries "<domain>:<email>", so the
		// app auto-fills the account name on scan.
		writeJSON(w, r, 200, map[string]string{"secret": secret, "uri": uri, "qr": qrDataURI(uri)})
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

// ── App passwords — device credentials for VayuMail Mobile ──────────────────
//
// An app password is a per-device credential for IMAP/SMTP/POP3 sign-in:
// generated once, shown once, stored only as an Argon2id hash, and revocable
// individually without touching the mailbox's main password (ADR-0126). It is
// the credential the VayuMail Mobile onboarding asks for, and the only
// accepted mailbox credential when VAYUMAIL_2FA_ENFORCE is active.

// appPasswordAlphabet is the 62-character alphanumeric alphabet app-password
// secrets are drawn from. No symbols and no dashes: the secret is displayed in
// dash-grouped blocks, so the dashes stay pure presentation and stripping them
// at verification can never eat a real secret character.
const appPasswordAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// appPasswordLength is the secret length in characters: 20 alphanumerics are
// ~119 bits of entropy — far beyond online-guessing reach, and verification is
// additionally slowed by mailAuthThrottle on the protocol listeners.
const appPasswordLength = 20

// appPasswordMaxPerMailbox caps live credentials per mailbox. It matches the
// LIMIT the auth path reads back (AppPasswordHashes), so every stored secret
// is guaranteed to actually authenticate.
const appPasswordMaxPerMailbox = 20

// stalePendingDeviceAge is how long a never-approved device credential is kept
// before a later registration may reclaim its slot. A day is long enough that an
// operator who steps away mid-setup still finds the device waiting, and short
// enough that abandoned attempts cannot accumulate into a lockout.
const stalePendingDeviceAge = 24 * time.Hour

// generateAppPasswordSecret draws an appPasswordLength-character secret from
// appPasswordAlphabet with crypto/rand, using rejection sampling (62×4 = 248)
// so every character is equally likely — no modulo bias.
func generateAppPasswordSecret() (string, error) {
	out := make([]byte, 0, appPasswordLength)
	buf := make([]byte, 64)
	for len(out) < appPasswordLength {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if b >= 248 { // 4×62; rejecting the top 8 values keeps the draw uniform
				continue
			}
			out = append(out, appPasswordAlphabet[int(b)%len(appPasswordAlphabet)])
			if len(out) == appPasswordLength {
				break
			}
		}
	}
	return string(out), nil
}

// generateDeviceID draws a 32-hex-character (128-bit) random device identity
// (ADR-0129). It is an identifier, not a secret — the device password is the
// secret — but 128 bits keep it unguessable and collision-free.
func generateDeviceID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// groupAppPasswordSecret renders a secret in 4-character dash-separated blocks
// (abcd-efgh-ijkl-mnop-qrst) for readability. The dashes are presentation
// only: the hash is computed over the dashless form and the auth path strips
// dashes before verifying, so both spellings sign in.
func groupAppPasswordSecret(secret string) string {
	var b strings.Builder
	for i, c := range secret {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// canManageAppPassword reports whether this session may create/revoke app
// passwords for the given mailbox: an administrator for any mailbox, everyone
// else only for their own assigned mailbox (same self-service boundary as
// signatures — a holder minting a credential for their own mailbox gains no
// access they don't already have).
func (a *App) canManageAppPassword(r *http.Request, email string) bool {
	if strings.TrimSpace(email) == "" {
		return false
	}
	_, own := a.ownMailbox(r)
	isOwner := own != "" && strings.EqualFold(own, email)
	if isOwner {
		return true
	}
	// SEVERANCE (ADR-0152 D4). An administrator may provision credentials for a
	// mailbox the operator still administers — that is ordinary support. Once a
	// mailbox is handed over they may not, because minting an app password is a
	// way to read the whole mailbox over IMAP that leaves NO ledger entry, no
	// notice and no break-glass mark. It is quieter and cheaper than the loud
	// path, and while it existed the sentence in ADR-0152 D4 was false.
	if a.vayuMail != nil && a.vayuMail.IsHandedOver(email) {
		return false
	}
	return a.isAdminRequest(r)
}

// appPasswordMailboxes returns the mailboxes whose app passwords this session
// may manage — every active account for an administrator, otherwise just the
// caller's own mailbox. Mirrors the per-mailbox scoping of the Connect tab.
func (a *App) appPasswordMailboxes(r *http.Request) []string {
	if a.isAdminRequest(r) {
		var out []string
		if accs, err := a.vayuMail.Accounts().List(r.Context()); err == nil {
			for _, ac := range accs {
				if ac.Active {
					out = append(out, ac.Email)
				}
			}
		}
		return out
	}
	if _, own := a.ownMailbox(r); own != "" {
		return []string{own}
	}
	return nil
}

// vayuAppPasswordsCard renders the "App passwords" card on the Connect tab:
// a create form plus the per-mailbox list of live credentials (label + created
// date only — hashes never leave the store). Create/revoke POST the
// /os/vayumail/accounts/apppassword endpoints and swap this card in place.
func (a *App) vayuAppPasswordsCard(r *http.Request) string {
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		return `<div class="card"><div class="card-title">App passwords</div><p class="muted">VayuMail account storage is not available yet.</p></div>`
	}
	emails := a.appPasswordMailboxes(r)

	post := ` hx-target="#vm-apppw-card" hx-swap="innerHTML"`
	var b strings.Builder
	b.WriteString(`<div class="card"><div class="card-title">App passwords</div>`)
	b.WriteString(`<p class="text-sm">An <strong>app password</strong> is a sign-in credential for one device — the VayuMail app, or any IMAP/SMTP/POP3 client. It is <strong>shown once</strong> at creation, stored only as an Argon2id hash, and can be revoked here at any time without changing the mailbox password. With <span class="mono">VAYUMAIL_2FA_ENFORCE</span> on, mail apps on 2FA-protected mailboxes <em>must</em> use one.</p>`)

	if len(emails) == 0 {
		b.WriteString(`<p class="muted">No active mailboxes yet. Create one under <a href="/os/vayumail/accounts">Accounts</a>.</p></div>`)
		return b.String()
	}

	// Create form.
	b.WriteString(`<form class="vm-row vm-row--end" hx-post="/os/vayumail/accounts/apppassword"` + post + `>`)
	b.WriteString(`<label class="field"><span class="field-label">Mailbox</span><select class="input input--sm" name="email">`)
	for _, em := range emails {
		b.WriteString(`<option value="` + html.EscapeString(em) + `">` + html.EscapeString(em) + `</option>`)
	}
	b.WriteString(`</select></label>`)
	b.WriteString(`<label class="field vm-grow"><span class="field-label">Label (what device is this for?)</span><input class="input input--sm" type="text" name="label" placeholder="VayuMail Mobile" maxlength="64"></label>`)
	b.WriteString(`<button class="btn btn--primary btn--sm" type="submit">Create app password</button></form>`)

	// Existing credentials — metadata only, never the hash.
	b.WriteString(`<div class="table-wrap"><table class="table"><thead><tr><th>Mailbox</th><th>Label</th><th>Created</th><th></th></tr></thead><tbody>`)
	rows := 0
	for _, em := range emails {
		for _, p := range a.vayuMail.Accounts().ListAppPasswords(r.Context(), em) {
			rows++
			b.WriteString(`<tr><td class="mono">` + html.EscapeString(p.Email) + `</td><td>` + html.EscapeString(p.Label) + `</td><td class="muted text-sm">` + p.CreatedAt.Format("2006-01-02") + `</td><td>` +
				`<button type="button" class="btn btn--sm btn--danger" hx-post="/os/vayumail/accounts/apppassword/delete"` + post + hxVals("email", p.Email, "id", strconv.FormatInt(p.ID, 10)) + ` hx-confirm="Revoke this app password? Devices signed in with it stop syncing immediately.">Revoke</button></td></tr>`)
		}
	}
	if rows == 0 {
		b.WriteString(`<tr><td colspan="4" class="muted">No app passwords yet. Create one above to connect the VayuMail app.</td></tr>`)
	}
	b.WriteString(`</tbody></table></div></div>`)
	return b.String()
}

// handleVayuOSAppPasswordCreate mints a new app password for a mailbox and
// returns the refreshed card (HTMX swap) with the secret revealed ONCE at the
// top. Only the Argon2id hash of the dashless secret is stored; the plaintext
// exists solely in this one response.
func (a *App) handleVayuOSAppPasswordCreate(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	_ = r.ParseForm()
	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	if !a.canManageAppPassword(r, email) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "you can only manage app passwords for your own mailbox", "")
		return
	}
	label := strings.TrimSpace(r.FormValue("label"))
	if label == "" {
		label = "VayuMail Mobile"
	}
	if len(label) > 64 {
		label = label[:64]
	}
	accts := a.vayuMail.Accounts()

	banner := ""
	var opErr error
	switch {
	case accts.HashFor(r.Context(), email) == "":
		// Guard: a credential for a non-account address must never exist — the
		// auth bridge accepts any address with a matching app-password hash.
		opErr = errors.New("no active mailbox with that address")
	case len(accts.AppPasswordCredentials(r.Context(), email)) >= appPasswordMaxPerMailbox:
		// Counts device credentials too — the auth path reads back at most
		// appPasswordMaxPerMailbox rows, so any row beyond the cap would be a
		// credential that can never authenticate.
		opErr = errors.New("app-password limit reached for this mailbox — revoke an unused one first")
	default:
		secret, err := generateAppPasswordSecret()
		if err != nil {
			opErr = errors.New("could not generate a secret")
			break
		}
		hash, err := auth.HashSecretArgon2id(secret)
		if err != nil {
			opErr = errors.New("could not hash the secret")
			break
		}
		if _, err := accts.CreateAppPassword(r.Context(), email, label, hash); err != nil {
			opErr = err
			break
		}
		dbpkg.AuditLog("vayumail.apppassword.create", dbpkg.AuditActor(r), email, label)
		// One-time reveal: the grouped form is easier to read out / retype; the
		// dashes are optional at sign-in (the auth path strips them).
		banner = `<div class="card" style="border-left:4px solid #22c55e"><div class="card-title">App password created — copy it now</div>` +
			`<p class="text-sm">This password is <strong>shown only once</strong>. It is stored only as a hash and can never be displayed again — if it is lost, revoke it and create a new one.</p>` +
			`<pre class="mono text-sm" style="white-space:pre-wrap;background:var(--bg-surface-2);padding:10px;border-radius:8px">` + html.EscapeString(groupAppPasswordSecret(secret)) + `</pre>` +
			`<p class="muted text-xs">Sign in to <span class="mono">` + html.EscapeString(email) + `</span> (label: ` + html.EscapeString(label) + `) with this as the password — in the VayuMail app or any IMAP/SMTP client. The dashes are optional.</p></div>`
	}
	card := a.vayuAppPasswordsCard(r)
	if opErr != nil {
		card = `<div class="empty-state" role="alert">⚠ ` + html.EscapeString(opErr.Error()) + `</div>` + card
	}
	writeOSHTML(w, r, banner+card)
}

// handleVayuOSAppPasswordDelete revokes one app password by id (scoped to the
// mailbox) and returns the refreshed card (HTMX swap).
func (a *App) handleVayuOSAppPasswordDelete(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	_ = r.ParseForm()
	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	if !a.canManageAppPassword(r, email) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "you can only manage app passwords for your own mailbox", "")
		return
	}
	var opErr error
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	switch {
	case err != nil:
		opErr = errors.New("invalid app-password id")
	case a.vayuMail.Accounts().DeleteAppPassword(r.Context(), email, id) != nil:
		opErr = errors.New("app password not found — it may already be revoked")
	default:
		dbpkg.AuditLog("vayumail.apppassword.revoke", dbpkg.AuditActor(r), email, r.FormValue("id"))
	}
	card := a.vayuAppPasswordsCard(r)
	if opErr != nil {
		card = `<div class="empty-state" role="alert">⚠ ` + html.EscapeString(opErr.Error()) + `</div>` + card
	}
	writeOSHTML(w, r, card)
}

// ── Devices — approval-gated sync credentials (ADR-0129) ────────────────────
//
// A device is an app-password row carrying a device identity: the VayuMail
// app registers itself with the mailbox password (member API) and receives a
// device credential that starts 'pending'. Nothing syncs to it until an
// administrator approves it here — the web console's 2FA is the approval
// anchor IMAP can never have. Blocked devices stay listed as evidence.

// deviceStatusChip renders a device's approval state. Pending is the state
// that needs operator action, so it gets its own prominent chip style.
func deviceStatusChip(status string) string {
	switch status {
	case vmail.DeviceStatusApproved:
		return `<span class="badge badge--ok">approved</span>`
	case vmail.DeviceStatusBlocked:
		return `<span class="badge badge--danger">blocked</span>`
	default:
		return `<span class="badge badge--pending">pending approval</span>`
	}
}

// vayuDevicesCard renders the "Devices" card on the admin Accounts page: the
// per-mailbox "require device approval" toggle plus every registered device
// (label, platform, status, created, last used) with Approve/Block/Remove.
// All actions POST /os/vayumail/devices/action and swap this card in place.
func (a *App) vayuDevicesCard(ctx context.Context) string {
	accts := a.vayuMail.Accounts()
	accs, _ := accts.List(ctx)
	devices := accts.ListDevices(ctx)

	post := ` hx-post="/os/vayumail/devices/action" hx-target="#vm-device-card" hx-swap="innerHTML"`
	// Count pending devices so the card can flag work at a glance.
	pending := 0
	for _, d := range devices {
		if d.Status == vmail.DeviceStatusPending {
			pending++
		}
	}
	var b strings.Builder
	b.WriteString(`<div class="card"><div class="vm-card-head"><div class="card-title">Devices</div><span class="vm-live" title="Updates automatically">● live</span></div>`)
	// Self-refresh: a hidden poller re-fetches this card so a device that registers
	// out-of-band (from the mobile app) surfaces as "pending approval" within
	// seconds — no full-page reload (the redesign's headline fix).
	b.WriteString(`<div class="vm-poller" aria-hidden="true" hx-get="/os/vayumail/devices/fragment" hx-trigger="every 15s" hx-target="#vm-device-card" hx-swap="innerHTML"></div>`)
	if pending > 0 {
		noun := "device is"
		if pending > 1 {
			noun = "devices are"
		}
		b.WriteString(`<div class="vm-attention" role="status"><strong>` + strconv.Itoa(pending) + `</strong> ` + noun + ` awaiting approval — review and Approve or Block below.</div>`)
	}
	b.WriteString(`<p class="text-sm">A <strong>new device</strong> that signs into VayuMail registers itself here and starts <strong>pending</strong>: it cannot sync any mail — even with the correct password — until you approve it. With approval required, the mailbox password alone never syncs mail over IMAP/POP3/SMTP, so a stolen password is useless to an attacker's device.</p>`)

	// Registered devices — pending first (they need action).
	b.WriteString(`<div class="table-wrap"><table class="table"><thead><tr><th>Mailbox</th><th>Device</th><th>Platform</th><th>Status</th><th>Registered</th><th>Last used</th><th></th></tr></thead><tbody>`)
	if len(devices) == 0 {
		b.WriteString(`<tr><td colspan="7" class="muted">No devices registered yet. The VayuMail app registers itself on first sign-in.</td></tr>`)
	}
	for _, d := range devices {
		lastUsed := `<span class="muted">never</span>`
		if !d.LastUsedAt.IsZero() {
			lastUsed = d.LastUsedAt.Format("2006-01-02 15:04")
		}
		actions := ""
		if d.Status != vmail.DeviceStatusApproved {
			actions += `<button type="button" class="btn btn--primary btn--sm"` + post + hxVals("op", "approve", "email", d.Email, "id", strconv.FormatInt(d.ID, 10)) + `>Approve</button>`
		}
		if d.Status != vmail.DeviceStatusBlocked {
			actions += `<button type="button" class="btn btn--sm"` + post + hxVals("op", "block", "email", d.Email, "id", strconv.FormatInt(d.ID, 10)) + `>Block</button>`
		}
		actions += `<button type="button" class="btn btn--sm btn--danger"` + post + hxVals("op", "remove", "email", d.Email, "id", strconv.FormatInt(d.ID, 10)) + ` hx-confirm="Remove this device? It stops syncing immediately and must register again.">Remove</button>`
		b.WriteString(`<tr><td class="mono">` + html.EscapeString(d.Email) + `</td><td>` + html.EscapeString(d.Label) + `</td><td class="muted text-sm">` + html.EscapeString(d.Platform) + `</td><td>` + deviceStatusChip(d.Status) + `</td><td class="muted text-sm">` + d.CreatedAt.Format("2006-01-02") + `</td><td class="muted text-sm">` + lastUsed + `</td><td class="vm-row">` + actions + `</td></tr>`)
	}
	b.WriteString(`</tbody></table></div>`)

	// Per-mailbox enforcement toggle. Turning it OFF restores password sign-in
	// on the mail protocols for that mailbox (devices are auto-approved).
	b.WriteString(`<div class="card-title mt-2">Require device approval</div>`)
	b.WriteString(`<p class="muted text-sm">When required (recommended), only approved devices sync mail; the mailbox password still signs into the web console and registers new devices. Turning it off lets the mailbox password sign in from any mail app, unapproved.</p>`)
	b.WriteString(`<div class="table-wrap"><table class="table"><thead><tr><th>Mailbox</th><th>Device approval</th><th></th></tr></thead><tbody>`)
	if len(accs) == 0 {
		b.WriteString(`<tr><td colspan="3" class="muted">No mail accounts yet.</td></tr>`)
	}
	for _, ac := range accs {
		state := `<span class="badge badge--ok">required</span>`
		btn := `<button type="button" class="btn btn--sm"` + post + hxVals("op", "require-set", "email", ac.Email, "on", "0") + ` hx-confirm="Allow the mailbox password to sync mail from ANY device without approval?">Turn off</button>`
		if !ac.RequireDeviceApproval {
			state = `<span class="badge badge--warn">off — password syncs anywhere</span>`
			btn = `<button type="button" class="btn btn--primary btn--sm"` + post + hxVals("op", "require-set", "email", ac.Email, "on", "1") + `>Require approval</button>`
		}
		b.WriteString(`<tr><td class="mono">` + html.EscapeString(ac.Email) + `</td><td>` + state + `</td><td>` + btn + `</td></tr>`)
	}
	b.WriteString(`</tbody></table></div></div>`)
	return b.String()
}

// handleVayuOSDeviceAction applies a device-approval action (approve / block /
// remove / require-set) and returns the refreshed card (HTMX swap). Admin-only
// — approval from the 2FA-protected console is the whole security model, so
// mailbox holders cannot approve their own devices.
func (a *App) handleVayuOSDeviceAction(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrators only", "")
		return
	}
	_ = r.ParseForm()
	accts := a.vayuMail.Accounts()
	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	op := r.FormValue("op")
	var opErr error
	if op == "require-set" {
		on := r.FormValue("on") == "1"
		opErr = accts.SetRequireDeviceApproval(r.Context(), email, on)
		if opErr == nil {
			onOff := "off"
			if on {
				onOff = "on"
			}
			dbpkg.AuditLog("vayumail.device.require", dbpkg.AuditActor(r), email, onOff)
		}
	} else {
		id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
		switch {
		case err != nil:
			opErr = errors.New("invalid device id")
		case op == "approve":
			if opErr = accts.SetDeviceStatus(r.Context(), email, id, vmail.DeviceStatusApproved); opErr == nil {
				dbpkg.AuditLog("vayumail.device.approve", dbpkg.AuditActor(r), email, r.FormValue("id"))
			}
		case op == "block":
			if opErr = accts.SetDeviceStatus(r.Context(), email, id, vmail.DeviceStatusBlocked); opErr == nil {
				dbpkg.AuditLog("vayumail.device.block", dbpkg.AuditActor(r), email, r.FormValue("id"))
			}
		case op == "remove":
			if opErr = accts.DeleteAppPassword(r.Context(), email, id); opErr == nil {
				dbpkg.AuditLog("vayumail.device.remove", dbpkg.AuditActor(r), email, r.FormValue("id"))
			}
		default:
			opErr = errors.New("unknown operation")
		}
		if opErr != nil && errors.Is(opErr, sql.ErrNoRows) {
			opErr = errors.New("device not found — it may already be removed")
		}
	}
	card := a.vayuDevicesCard(r.Context())
	if opErr != nil {
		card = `<div class="empty-state" role="alert">⚠ ` + html.EscapeString(opErr.Error()) + `</div>` + card
	}
	writeOSHTML(w, r, card)
}
