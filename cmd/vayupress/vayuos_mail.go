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
	// Aliased: the stdlib "html" package (escaping) is already imported here.
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/totp"
	"github.com/johalputt/vayupress/internal/ui"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
	vpgp "github.com/johalputt/vayupress/internal/vayuos/pgp"
)

// mailHTMLPolicy sanitises HTML mail for the reader: bluemonday's UGCPolicy strips
// scripts, event handlers and inline styles, so a message can never weaken the
// console's strict CSP. Image SOURCES are removed separately, by
// mailHTMLNoImages — bluemonday cannot take back an attribute a policy already
// allows, so that pass walks the sanitised tree instead of pretending otherwise.
var mailHTMLPolicy = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.RequireNoReferrerOnLinks(true)
	return p
}()

// mailHTMLPolicyImages is mailHTMLPolicy WITH image sources, used only when the
// reader explicitly asks to load pictures for one message (?images=1). Still
// script-free and handler-free; the difference is entirely about who gets told.
var mailHTMLPolicyImages = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("src", "srcset", "alt", "title", "width", "height").OnElements("img")
	p.AllowURLSchemes("http", "https", "data", "cid")
	p.RequireNoReferrerOnLinks(true)
	return p
}()

// mailHTMLNoImages sanitises a message body and then strips every image source.
//
// This is a privacy boundary, not a safety one: fetching a remote image tells the
// sender — and every tracker embedded in the message — that this mailbox opened
// it, and roughly when. The alt text stays, so the layout still reads.
func mailHTMLNoImages(raw string) string {
	return stripImageSources(mailHTMLPolicy.Sanitize(raw))
}

// stripImageSources removes src/srcset/background from every <img> in already
// sanitised HTML. It parses as a fragment in a <div> context so the result stays
// a snippet (a full-document parse would wrap it in html/body and break nesting).
func stripImageSources(sanitized string) string {
	ctx := &xhtml.Node{Type: xhtml.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := xhtml.ParseFragment(strings.NewReader(sanitized), ctx)
	if err != nil {
		// The input is already sanitised, so a parse failure is not a reason to
		// drop the message — but it IS a reason not to trust image sources.
		return ""
	}
	var out bytes.Buffer
	for _, n := range nodes {
		stripImgAttrs(n)
		if err := xhtml.Render(&out, n); err != nil {
			return ""
		}
	}
	return out.String()
}

// stripImgAttrs walks a parsed subtree removing the attributes that make a browser
// fetch a URL from an image element.
func stripImgAttrs(n *xhtml.Node) {
	if n.Type == xhtml.ElementNode && n.Data == "img" {
		keep := n.Attr[:0]
		for _, a := range n.Attr {
			switch strings.ToLower(a.Key) {
			case "src", "srcset", "background", "lowsrc", "dynsrc":
				continue
			}
			keep = append(keep, a)
		}
		n.Attr = keep
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		stripImgAttrs(c)
	}
}

// ── Compose ──────────────────────────────────────────────────────────────────

// composePrefill derives the To/Cc/Bcc/Subject/Body for the compose form from the
// request. It supports three modes:
//
//   - reply:   ?reply=1&user=&folder=&id=  → To=original From, "Re: ", quoted body
//   - forward: ?forward=1&user=&folder=&id= → "Fwd: ", quoted body, empty To
//   - direct:  ?to=&subject=&body=          → verbatim prefill
//
// Reply/forward load the stored message (PGP-decrypted for the owner) so the
// quoted text is readable, and a reopened draft restores the Cc/Bcc it was saved
// with rather than quietly dropping recipients. A reply's quote comes back on
// its own, for the sheet to fold away (Mail plan §5); the composer puts it
// back under the reply when it sends, where insertSignature expects it.
func (a *App) composePrefill(r *http.Request) (to, cc, bcc, subject, bodyText, quote string) {
	q := r.URL.Query()
	// Draft: reopen a saved draft verbatim (To/Cc/Bcc/Subject/body) for editing.
	if q.Get("draft") != "" {
		rd := a.mailReader(r, q.Get("user"))
		id := strings.TrimSpace(q.Get("id"))
		if a.vayuMail == nil || rd.Key() == "" || id == "" {
			return "", "", "", "", "", ""
		}
		raw, err := a.vayuMail.ReadFolderMessage(rd, "Drafts", id)
		if err != nil {
			return "", "", "", "", "", ""
		}
		if msg, perr := netmail.ReadMessage(bytes.NewReader(raw)); perr == nil {
			b, _ := io.ReadAll(msg.Body)
			return msg.Header.Get("To"), msg.Header.Get("Cc"), msg.Header.Get("Bcc"),
				msg.Header.Get("Subject"), string(b), ""
		}
		return "", "", "", "", "", ""
	}
	// A message left through a site's contact form, answered from Content ›
	// Messages. It was a mailto: link, which opens whatever mail program the
	// operator's computer has, not this install's own mail. Read by id like
	// Reply, so a long message never rides in the address, and only for an
	// administrator: compose is open to client accounts, the inbox is not.
	if id := strings.TrimSpace(q.Get("contact")); id != "" {
		if !a.isAdminRequest(r) {
			return "", "", "", "", "", ""
		}
		m, err := scanContactMessage(dbpkg.Reader().QueryRowContext(r.Context(),
			`SELECT `+contactMessageCols+` FROM contact_messages WHERE id=?`, id))
		if err != nil {
			return "", "", "", "", "", ""
		}
		return m.Email, "", "", "Re: your message", "",
			quoteBody(m.Name+" <"+m.Email+">", config.FormatSite(m.Created, "2 Jan 2006"), m.Message)
	}
	replyAll := q.Get("replyall") != ""
	reply := q.Get("reply") != "" || replyAll
	forward := q.Get("forward") != ""
	if !reply && !forward {
		return q.Get("to"), "", "", q.Get("subject"), q.Get("body"), ""
	}
	rd := a.mailReader(r, q.Get("user"))
	user := rd.Key()
	folder := strings.TrimSpace(q.Get("folder"))
	if folder == "" {
		folder = "Inbox"
	}
	id := strings.TrimSpace(q.Get("id"))
	if a.vayuMail == nil || user == "" || id == "" {
		return "", "", "", "", "", ""
	}
	raw, err := a.vayuMail.ReadFolderMessage(rd, folder, id)
	if err != nil {
		return "", "", "", "", "", ""
	}
	origFrom, origSubject, origBody, origDate := parseForQuote(raw)
	quoted := quoteBody(origFrom, origDate, origBody)
	if replyAll {
		to, cc := mailReplyAll(raw, mailAddrOf(user, a.cfgDomain()))
		return to, cc, "", ensurePrefix(origSubject, "Re: "), "", quoted
	}
	if reply {
		return origFrom, "", "", ensurePrefix(origSubject, "Re: "), "", quoted
	}
	// A forward's history is what is being sent, so it stays in the body.
	return "", "", "", ensurePrefix(origSubject, "Fwd: "), "\r\n\r\n---------- Forwarded message ----------\r\n" + quoted, ""
}

// parseForQuote extracts From, Subject, Date and a plain-text body from a raw
// message. The date is carried so the reply attribution can be dated the way
// every other mail client dates it.
// mailReplyAll is who a reply to everyone goes to: the sender and everyone
// it was sent to, then everyone copied, each once, and never the mailbox
// answering (own).
func mailReplyAll(raw []byte, own string) (to, cc string) {
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return "", ""
	}
	seen := map[string]bool{strings.ToLower(own): true}
	pick := func(fields ...string) string {
		var out []string
		for _, f := range fields {
			list, err := netmail.ParseAddressList(msg.Header.Get(f))
			if err != nil {
				continue
			}
			for _, a := range list {
				if k := strings.ToLower(a.Address); !seen[k] {
					seen[k] = true
					out = append(out, a.String())
				}
			}
		}
		return strings.Join(out, ", ")
	}
	to = pick("From", "To")
	return to, pick("Cc")
}

func parseForQuote(raw []byte) (from, subject, bodyText, date string) {
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return "", "", string(raw), ""
	}
	from = msg.Header.Get("From")
	subject = msg.Header.Get("Subject")
	b, _ := io.ReadAll(msg.Body)
	return from, subject, string(b), humanMailDate(msg.Header.Get("Date"))
}

// humanMailDate renders a Date header for the reply attribution line. An
// unparseable date returns "" so the quote falls back to the dateless wording
// rather than pasting a raw RFC 5322 header into the middle of a sentence.
func humanMailDate(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	for _, layout := range []string{
		time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822,
		"Mon, 2 Jan 2006 15:04:05 -0700", "2 Jan 2006 15:04:05 -0700",
		"Mon, 2 Jan 2006 15:04 -0700", "2 Jan 2006 15:04 -0700",
	} {
		if t, perr := time.Parse(layout, raw); perr == nil {
			return t.Format("2 Jan 2006")
		}
	}
	return ""
}

// quoteBody prefixes each line of the original body with "> " (RFC 3676 style).
func quoteBody(from, date, bodyText string) string {
	var sb strings.Builder
	if from != "" {
		if date != "" {
			sb.WriteString("On " + date + ", " + from + " wrote:\r\n")
		} else {
			sb.WriteString("On a previous message, " + from + " wrote:\r\n")
		}
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

// mailSendErrText turns an outbound failure into something an operator can act
// on. The engine reports the real reason — "dial tcp: lookup mx.example: no such
// host" is diagnostic gold in a log and meaningless in a form field. Anything
// unrecognised keeps a plain sentence; the raw error is never shown.
func mailSendErrText(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	// A full mailbox on THIS server that the message was addressed to. It must be
	// matched before the generic quota case below, which is about the sender: the
	// sender's own quota is refused before compose, so a quota error that reaches
	// here names someone else, and "your mailbox is full" sent people deleting
	// their own mail to fix a colleague's.
	if rest, ok := strings.CutPrefix(msg, "vayumail: local delivery to "); ok && strings.Contains(rest, "quota") {
		rcpt, _, _ := strings.Cut(rest, ":")
		// The engine stops at the first local recipient it cannot deliver to,
		// after the Sent copy is filed, so "not sent" would be false for anyone
		// delivered before them and "sent" would be false for everyone after.
		return "The mailbox of " + rcpt + " is full, so sending stopped there and some recipients may not have this message. Your copy is in Sent."
	}
	switch {
	case strings.Contains(msg, "no such host"), strings.Contains(msg, "no such domain"):
		return "Couldn’t find the recipient’s mail server — check the address after the @ for a typo."
	case strings.Contains(msg, "connection refused"), strings.Contains(msg, "timeout"), strings.Contains(msg, "timed out"):
		return "The recipient’s mail server didn’t answer in time — try again in a moment."
	case strings.Contains(msg, "no recipients"):
		return "Add at least one recipient."
	case strings.Contains(msg, "quota"), strings.Contains(msg, "mailbox full"):
		return "Your mailbox is full (storage quota reached). Delete some mail or ask an administrator to raise your quota."
	case strings.Contains(msg, "relay"), strings.Contains(msg, "auth"):
		return "The outgoing relay refused the message. Check the smarthost settings under VayuMail → DNS."
	case strings.Contains(msg, "disabled"), strings.Contains(msg, "domain not set"):
		return "Outbound mail is not configured yet — set a DOMAIN and check VayuMail → DNS."
	}
	return "Could not send the message — it has not been delivered. Try again, and check Deliverability under VayuMail → DNS if it keeps failing."
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
		// Sign has the message signed with the sender's OpenPGP key.
		Sign *bool `json:"sign"`
		// RichHTML opts into a multipart/alternative with an HTML rendering of the
		// same body. Off by default: a young sending IP scores worse with HTML than
		// with plain text, so this must be a deliberate choice, not a default.
		RichHTML *bool `json:"richHTML"`
		// DraftID is the draft this message was written from, when the composer is
		// finishing a saved draft. The files stored with that draft are merged into
		// this send (see below) so the sender never has to attach them again.
		DraftID string `json:"draft_id"`
		// SendAt, when set, holds the message until then (Send later): an
		// RFC 3339 time, which the composer writes from the sender's own
		// clock and zone.
		SendAt string `json:"sendAt"`
	}
	var attachments []vmail.Attachment
	appendSig := true // default: append the sender's signature when one is set
	encrypt := false  // default OFF: plain messages are delivered as readable text
	richHTML := false // default OFF: see RichHTML above — deliverability, not preference
	sign := false     // the composer sends the sender's default (SignsByDefault)

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
		in.DraftID = r.FormValue("draft_id")
		in.SendAt = r.FormValue("sendAt")
		if r.FormValue("appendSig") == "0" {
			appendSig = false
		}
		if r.FormValue("richHTML") == "1" {
			richHTML = true
		}
		if r.FormValue("encrypt") == "1" {
			encrypt = true
		}
		sign = r.FormValue("sign") == "1"
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
		if in.Sign != nil {
			sign = *in.Sign
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
		if a.vayuMail.MailboxReadOnly(ownEmail) {
			writeAPIError(w, r, http.StatusForbidden, "read-only", "This mailbox is read-only: it can read mail but not send it.", "")
			return
		}
		from = ownEmail
	}
	var sendAt time.Time
	if v := strings.TrimSpace(in.SendAt); v != "" {
		t, msg := sendLaterTime(v, time.Now())
		if msg != "" {
			writeAPIError(w, r, 400, "send-at", msg, "")
			return
		}
		sendAt = t
	}
	splitAddrs := parseRecipientList
	to := splitAddrs(in.To)
	cc := splitAddrs(in.CC)
	bcc := splitAddrs(in.BCC)
	if len(to)+len(cc)+len(bcc) == 0 {
		writeAPIError(w, r, 400, "validation_error", "at least one recipient is required", "")
		return
	}
	// Finishing a saved draft: carry the files it stored onto this message, so the
	// sender does not have to find and attach them a second time. Drafts are read
	// through the same authority the reader uses, so this can only ever move the
	// sender's OWN attachments forward.
	if draftID := strings.TrimSpace(in.DraftID); draftID != "" {
		if rd := a.mailReader(r, from); rd.Key() != "" {
			if extra, derr := a.vayuMail.DraftAttachments(rd, draftID); derr == nil && len(extra) > 0 {
				carried := int64(0)
				for _, f := range attachments {
					carried += int64(len(f.Data))
				}
				for _, f := range extra {
					carried += int64(len(f.Data))
					if carried > maxAttachBytes {
						writeAPIError(w, r, 400, "attach-too-large",
							"This draft's saved attachments push the message over the "+strconv.FormatInt(maxAttachMB, 10)+" MB limit.", "")
						return
					}
				}
				attachments = append(attachments, extra...)
			}
		}
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
	msg := vmail.ComposeMessage{
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
		Sign:         sign,
	}
	if !sendAt.IsZero() {
		id, err := a.vayuMail.Schedule(r.Context(), from, msg, in.Body, sendAt)
		if err != nil {
			writeAPIError(w, r, 500, "schedule-failed", "Could not schedule the message: "+err.Error(), "")
			return
		}
		writeJSON(w, r, 200, map[string]interface{}{"scheduled": true, "id": id, "sendAt": sendAt.UTC().Format(time.RFC3339)})
		return
	}
	id, err := a.vayuMail.ComposeRich(r.Context(), msg)
	if errors.Is(err, vmail.ErrNoSigningKey) {
		writeAPIError(w, r, 400, "no-signing-key", "Not sent: this mailbox has no key to sign with. Send it unsigned, or ask an administrator for a key.", "")
		return
	}
	if err != nil {
		writeAPIError(w, r, 500, "send-failed", mailSendErrText(err), "")
		return
	}
	writeJSON(w, r, 200, map[string]interface{}{"queued": true, "id": id, "attachments": len(attachments)})
}

// sendLaterLimit is how far ahead Send later reaches: a year, past which a
// time is far more likely a mistyped year than a plan.
const sendLaterLimit = 366 * 24 * time.Hour

// sendLaterTime reads a Send later time, or says why it cannot be used.
func sendLaterTime(v string, now time.Time) (time.Time, string) {
	t, err := time.Parse(time.RFC3339, v)
	switch {
	case err != nil:
		return time.Time{}, "That send time could not be read."
	case !t.After(now):
		return time.Time{}, "That time has passed. Choose a later one, or send it now."
	case t.Sub(now) > sendLaterLimit:
		return time.Time{}, "Choose a time within a year."
	}
	return t, ""
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
		Cc      string `json:"cc"`
		Bcc     string `json:"bcc"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	var attachments []vmail.Attachment
	// Accept multipart/form-data when the composer has files to store, or JSON
	// (the no-attachment path) exactly as before.
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		maxAttachBytes := int64(composeMaxAttachMB()) << 20
		r.Body = http.MaxBytesReader(w, r.Body, maxAttachBytes+(1<<20))
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			writeAPIError(w, r, 400, "attach-too-large", "The draft (with attachments) exceeds the size limit.", "")
			return
		}
		in.From = r.FormValue("from")
		in.To = r.FormValue("to")
		in.Cc = r.FormValue("cc")
		in.Bcc = r.FormValue("bcc")
		in.Subject = r.FormValue("subject")
		in.Body = r.FormValue("body")
		atts, errMsg := readComposeAttachments(r, maxAttachBytes)
		if errMsg != "" {
			writeAPIError(w, r, 400, "attach-read", errMsg, "")
			return
		}
		attachments = atts
	} else if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
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
	// Cc/Bcc are part of the draft, not just the eventual send: a draft that
	// silently forgets them sends the finished message to the wrong people.
	cc := parseRecipientList(in.Cc)
	bcc := parseRecipientList(in.Bcc)
	// Saving a draft files it into the Drafts folder, so refuse when full.
	if a.vayuMail.MailboxOverQuota(from) {
		writeAPIError(w, r, 400, "over-quota", "Your mailbox is full (storage quota reached). Delete some mail or ask an administrator to raise your quota.", "")
		return
	}
	fromHeader := from
	if name := a.senderDisplayName(r.Context(), from); name != "" {
		fromHeader = (&netmail.Address{Name: name, Address: from}).String()
	}
	id, err := a.vayuMail.SaveDraftWithAttachments(fromHeader, to, cc, bcc, in.Subject, in.Body, attachments)
	if err != nil {
		// The text is still in the composer, so say that rather than implying
		// work was lost.
		writeAPIError(w, r, 500, "draft-failed", "Could not save the draft — your message is still in the editor, so nothing is lost.", "")
		return
	}
	writeJSON(w, r, 200, map[string]string{"saved": "Drafts", "id": id})
}

// readComposeAttachments lifts the "attachments" files out of an already-parsed
// multipart request, enforcing the total budget. Shared by send and draft so the
// two can never disagree about what a message is allowed to carry.
func readComposeAttachments(r *http.Request, maxBytes int64) ([]vmail.Attachment, string) {
	if r.MultipartForm == nil {
		return nil, ""
	}
	var out []vmail.Attachment
	var total int64
	for _, fhs := range r.MultipartForm.File["attachments"] {
		total += fhs.Size
		if total > maxBytes {
			return nil, "Attachments exceed the total size limit."
		}
		f, ferr := fhs.Open()
		if ferr != nil {
			return nil, "Could not read an attachment."
		}
		data, rerr := io.ReadAll(io.LimitReader(f, maxBytes+1))
		f.Close()
		if rerr != nil {
			return nil, "Could not read an attachment."
		}
		out = append(out, vmail.Attachment{
			Filename:    fhs.Filename,
			ContentType: fhs.Header.Get("Content-Type"),
			Data:        data,
		})
	}
	return out, ""
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
	// Two indexed lookups, not two table reads. Both stores hold the address
	// lowercased at insert and nothing updates the column, so an exact match on
	// the stored value finds precisely what walking the table with EqualFold
	// found — which the control test asserts across three capitalisations.
	if a.vayuMail != nil && a.vayuMail.Accounts() != nil {
		if name := a.vayuMail.Accounts().FullNameFor(ctx, emailAddr); name != "" {
			return name
		}
	}
	if a.userStore != nil {
		if u, err := a.userStore.GetByEmail(ctx, emailAddr); err == nil && u != nil {
			if name := strings.TrimSpace(u.Name); name != "" {
				return name
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
		// NotJunk: back to the Inbox from Junk, and the sender to contacts.
		NotJunk bool `json:"notjunk"`
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
		writeAPIError(w, r, 400, "validation_error", "user and at least one id are required", "")
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
		case in.NotJunk:
			_, err := a.vayuMail.NotJunk(rd, id)
			action = "notjunk"
			return err
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
	var firstErr error
	for _, id := range ids {
		if err := apply(id); err != nil {
			failed++
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	// A whole-batch failure (e.g. every id stale) is a real error; partial
	// failures are reported but still 200 so the UI can refresh. A read-only
	// mailbox is a refusal, not a fault, and says so.
	if failed == len(ids) {
		if errors.Is(firstErr, vmail.ErrReadOnlyMailbox) {
			writeAPIError(w, r, http.StatusForbidden, "read-only", mailChangeRefusal(firstErr), "")
			return
		}
		writeAPIError(w, r, 500, "action-failed", firstErr.Error(), "")
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

// mailPreviewTypes are what an attachment may open as in place, by its bytes:
// pictures the browser draws, and PDFs its own viewer shows. Never SVG.
var mailPreviewTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "application/pdf": true}

// mailPreviewable reports whether an attachment, by what it declares, is one
// to offer opening in place; the endpoint decides by its bytes.
func mailPreviewable(ctype string) (picture, ok bool) {
	ct := strings.ToLower(strings.TrimSpace(strings.SplitN(ctype, ";", 2)[0]))
	return ct != "application/pdf", mailPreviewTypes[ct]
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
	// Opened in place (?inline=1) only when the bytes are a picture or a PDF,
	// whatever the message declares, and typed by what they are: anything
	// else, a text/html attachment above all, is a download, so it can never
	// render as a page of this origin.
	disposition := "attachment"
	if r.URL.Query().Get("inline") == "1" {
		if sniffed := http.DetectContentType(data); mailPreviewTypes[sniffed] {
			ctype, disposition = sniffed, "inline"
			if sniffed != "application/pdf" {
				w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
			}
		}
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", disposition+`; filename="`+mailSafeFilename(fn)+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(data)
}

// ── Admin mail accounts (email + password) ───────────────────────────────────

func (a *App) handleVayuOSAccounts(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	if !a.isAdminRequest(r) {
		a.denyAccess(w, r, "/os/vayumail/inbox")
		return
	}
	if !a.mailRunning() || a.vayuMail.Accounts() == nil {
		a.writeMailSetup(w, r, "Mail accounts")
		return
	}
	domain := a.vayuMail.Config().Domain

	// VayuDomains Stage 3b: when a mail_enabled secondary domain exists, let the
	// operator choose which domain a new mailbox belongs to (each domain has its
	// own isolated store). With none, the address suffix is the fixed primary
	// domain exactly as before.
	addrSuffix := `<span class="vm-suffix">@` + esc(domain) + `</span>`
	if secs := a.mailSecondaryHosts(r.Context()); len(secs) > 0 {
		var opts strings.Builder
		opts.WriteString(`<option value="">@` + esc(domain) + ` (primary)</option>`)
		for _, h := range secs {
			opts.WriteString(`<option value="` + esc(h) + `">@` + esc(h) + `</option>`)
		}
		addrSuffix = `<select class="input" data-a-domain aria-label="Mailbox domain">` + opts.String() + `</select>`
	}
	// New mailbox rises in a sheet from the page's button (the grammar keeps
	// forms out of a list's flow). The form is the one the page's script has
	// always driven; /os/vayumail/accounts#new-mailbox opens it with the page.
	form := `<form class="vm-acct-new" data-acct-create>
  <label class="field"><span class="field-label">Address</span>
    <span class="vm-addr"><input class="input" type="text" data-a-local placeholder="name" required>` + addrSuffix + `</span></label>
  <label class="field"><span class="field-label">Full name (optional)</span>
    <input class="input" type="text" data-a-name placeholder="Display name"></label>
  <label class="field"><span class="field-label">Role</span>
    <select class="input" data-a-role>
      <option value="mailbox" selected>Mailbox: mail only, no console</option>
      <option value="reviewer">Reviewer: read-only, mail only</option>
      <option value="author">Author: mail and the author console</option>
      <option value="editor">Editor: mail and the editor console</option>
      <option value="administrator">Administrator: the whole console</option>
    </select>
    <span class="field-hint">Mail-only roles see their own mailbox and nothing else.</span></label>
  <label class="field"><span class="field-label">Quota in MB (0 for none)</span>
    <input class="input" type="number" min="0" step="1" data-a-quota value="0"></label>
  <label class="field"><span class="field-label">Password (8 characters or more)</span>
    <input class="input" type="password" data-a-pass required></label>
  <div class="sa-sheet__foot"><span class="field-hint" data-a-status role="status"></span><button class="btn btn--primary" type="submit">Create mailbox</button></div>
</form>`

	// Account recovery (ADR-0144), above the list: its readiness view is a
	// standing question about every mailbox below it, and a factor nobody
	// enrolled is invisible until the day it is needed.
	var boxes []string
	count := 0
	if accs, err := a.vayuMail.Accounts().List(r.Context()); err == nil {
		count = len(accs)
		for _, ac := range accs {
			if ac.Active {
				boxes = append(boxes, ac.Email)
			}
		}
	}
	noun := "mailboxes"
	if count == 1 {
		noun = "mailbox"
	}
	// The mailboxes: a live, swappable list of the mailbox rows. Every inline
	// action swaps it in place, and the create, 2FA and set-password flows
	// refresh it, so the page never reloads. Devices waiting for approval
	// (ADR-0129) follow, polling themselves.
	list := a.recoveryCardHTML(r, nonce, boxes) +
		`<span id="vm-accounts-spin" class="htmx-indicator vm-spin" aria-hidden="true">working…</span>` +
		`<div id="vm-accounts-list">` + a.vayuAccountsList(r.Context()) + `</div>` +
		`<div id="vm-device-card">` + a.vayuDevicesCard(r.Context()) + `</div>`
	body := string(ui.List(ui.ListPage{Title: "Mail administration", Count: itoaSafe(count) + " " + noun,
		Actions: ui.HTML(`<button type="button" class="btn btn--primary btn--sm" data-sheet="new-mailbox">` + saIcon("plus") + `New mailbox</button>`),
		Tabs:    saTabsFor(cfg, "mail", "/os/vayumail/accounts")}, ui.HTML(list), "")) +
		string(ui.Sheet("new-mailbox", "New mailbox", ui.HTML(form)))
	body += `<script nonce="` + nonce + `" src="/os/static/js/admin-os-mail.js?v=` + assetVer("js/admin-os-mail.js") + `"></script>`
	writeOSHTML(w, r, adminOSLayout(nonce, "Mail accounts", "vayuos", cfg, htmpl.HTML(body)))
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
	// Filters are driven from the mailbox's own card (list) or its settings page;
	// acctRefresh returns whichever surface the control lives on.
	card := a.acctRefresh(r, email)
	if opErr != nil {
		card = saCallout("danger", html.EscapeString(opErr.Error())) + card
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
	// The vacation control lives on the mailbox's card (list) or its settings page;
	// acctRefresh returns whichever surface the control lives on.
	card := a.acctRefresh(r, email)
	if opErr != nil {
		card = saCallout("danger", html.EscapeString(opErr.Error())) + card
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
	// The mailbox this change belongs to. The settings page has to know which one
	// to re-render, so each operation names its own: a forward names the mailbox,
	// an alias-create names its target, and an alias-delete names only the alias —
	// which is resolved below, before the row disappears.
	mailbox := strings.TrimSpace(r.FormValue("email"))
	switch r.FormValue("op") {
	case "alias-create":
		local := strings.ToLower(strings.TrimSpace(r.FormValue("local")))
		target := r.FormValue("target")
		mailbox = strings.TrimSpace(target)
		// The alias lives on the target mailbox's own domain, so a secondary-domain
		// mailbox gets secondary-domain aliases (VayuDomains).
		aliasDomain := emailDomain(target, fallbackDomain)
		switch {
		case local == "" || strings.ContainsAny(local, "@ \t"):
			opErr = errors.New("invalid alias name")
		case strings.Contains(local, "*") && local != "*":
			// The catch-all is "*" alone (mailboxFor); "a*" would read as a
			// pattern and match nothing but itself.
			opErr = errors.New("* alone is the catch-all; it cannot be part of an alias")
		default:
			alias := local + "@" + aliasDomain
			opErr = accts.CreateAlias(r.Context(), alias, target)
			if opErr == nil {
				dbpkg.AuditLog("vayumail.alias.create", dbpkg.AuditActor(r), alias, target)
			}
		}
	case "alias-delete":
		alias := r.FormValue("alias")
		if aliases, lerr := accts.ListAliases(r.Context()); lerr == nil {
			for _, al := range aliases {
				if strings.EqualFold(al.Alias, alias) {
					mailbox = strings.TrimSpace(al.Target)
					break
				}
			}
		}
		opErr = accts.DeleteAlias(r.Context(), alias)
		if opErr == nil {
			dbpkg.AuditLog("vayumail.alias.delete", dbpkg.AuditActor(r), alias, "")
		}
	case "forward-set":
		fwd := strings.TrimSpace(r.FormValue("forward"))
		mailbox = strings.TrimSpace(r.FormValue("email"))
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
	// Aliases, forwarding and vacation are driven from the mailbox's card (list) or
	// its settings page; acctRefresh returns whichever surface the control lives on.
	card := a.acctRefresh(r, mailbox)
	if opErr != nil {
		card = saCallout("danger", html.EscapeString(opErr.Error())) + card
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

// handleVayuOSConnect is Connect a device, a status page: whether a mail app
// can connect to this server, said first, because it is what the page is
// opened to learn; then what the app needs. The listeners each say whether
// they are up, the certificate whether apps will trust it (with the remedy
// when they will not), and the settings are there to copy for any app the
// VayuMail app does not cover.
func (a *App) handleVayuOSConnect(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() {
		a.writeMailSetup(w, r, "Connect a mail app")
		return
	}
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	mc := a.vayuMail.Config()
	host := mc.Hostname
	if host == "" {
		host = "mail." + mc.Domain
	}
	hHost := html.EscapeString(host)
	imapsPort := mailPort(mc.IMAPSListen, "993")
	imapPort := mailPort(mc.IMAPListen, "143")
	pop3sPort := mailPort(mc.POP3SListen, "995")
	pop3Port := mailPort(mc.POP3Listen, "110")
	subPort := mailPort(mc.SubmissionListen, "587")
	smtpPort := mailPort(mc.SMTPListen, "25")

	listeners := []struct {
		label, port string
		up          bool
	}{
		{"IMAP · SSL", imapsPort, a.vayuMail.IMAPSActive()},
		{"IMAP · STARTTLS", imapPort, a.vayuMail.IMAPActive()},
		{"POP3 · SSL", pop3sPort, a.vayuMail.POP3SActive()},
		{"POP3 · STLS", pop3Port, a.vayuMail.POP3Active()},
		{"SMTP submission · STARTTLS", subPort, a.vayuMail.SubmissionActive()},
		{"SMTP receive", smtpPort, a.vayuMail.InboundActive()},
	}
	var lrows []ui.Row
	down := 0
	for _, l := range listeners {
		st := ui.State("ok", "Listening")
		if !l.up {
			st = ui.State("warn", "Not listening")
			down++
		}
		lrows = append(lrows, ui.Row{Label: l.label, Hint: host + ":" + l.port, Control: st})
	}

	// The state, worst first: an untrusted certificate refuses every app
	// even with every port open, so it outranks a listener being down.
	untrusted := a.vayuMail.TLSActive() && !a.vayuMail.TLSTrusted()
	covered := a.vayuMail.TLSCertHosts()
	mismatch := a.vayuMail.TLSActive() && a.vayuMail.TLSTrusted() && len(covered) > 0 && !a.vayuMail.TLSCertCovers(host)
	page := ui.StatusPage{Title: "Connect a mail app", Tone: "ok", State: "Mail apps can connect",
		Detail: ui.HTML(`Every mail service is listening at <code>` + hHost + `</code>.`)}
	switch {
	case untrusted:
		page.Tone, page.State = "danger", "Mail apps will refuse to connect"
		page.Detail = ui.HTML(`The mail services offer a self-signed certificate, which phones and mail apps reject. The fix is below.`)
	case down == len(listeners):
		page.Tone, page.State = "danger", "No mail service is listening"
		page.Detail = ui.HTML(`Nothing answers at <code>` + hHost + `</code>; see Listeners below.`)
	case mismatch:
		page.Tone, page.State = "warn", "Phones will refuse this server's certificate"
		page.Detail = ui.HTML(`It does not cover <code>` + hHost + `</code>. Desktop apps let you accept it; the Gmail app and Thunderbird for Android do not.`)
	case down > 0:
		page.Tone, page.State = "warn", itoaSafe(down)+" of "+itoaSafe(len(listeners))+" mail services are not listening"
		page.Detail = ui.HTML(`Apps that use them cannot connect; see Listeners below.`)
	}

	var sections []ui.HTML
	var sheets strings.Builder

	// The holder's own recovery enrolment (ADR-0144 Phase 2), here because this
	// is the page someone visits when setting their mail up: the one moment
	// they think about access to this mailbox at all.
	if row, sheet, ok := a.selfRecovery(r, nonce); ok {
		sections = append(sections, ui.Section("Getting back in", "", ui.Rows(row)))
		sheets.WriteString(sheet)
	}

	// The certificate: the most common cause of an app's "Couldn't open
	// connection to server" is a reachable port whose certificate it rejects.
	acmeErr := a.vayuMail.ACMEChallengeError()
	var cert strings.Builder
	switch {
	case untrusted:
		var fix strings.Builder
		fix.WriteString(`<p><strong>Apps reject the self-signed certificate</strong> with "Couldn't open connection to server", even though the ports are open.</p>`)
		if note := a.vayuMail.TLSNote(); note != "" {
			fix.WriteString(`<p class="muted">Reason: ` + html.EscapeString(note) + `</p>`)
		}
		if acmeErr != "" {
			fix.WriteString(`<p class="muted">Built-in ACME could not run: ` + html.EscapeString(acmeErr) + `. Port 80 is almost certainly your website's nginx, so VayuMail cannot answer the Let's Encrypt challenge itself.</p>`)
		}
		fix.WriteString(`<p>Run this once on the server. It is separate from updating VayuPress: it issues a Let's Encrypt certificate for <code>` + hHost + `</code> through nginx, lets the mail service read it, and wires it in. It renews itself.</p>`)
		fix.WriteString(`<pre class="code-block code-block--wrap">cd /tmp/VayuPress &amp;&amp; git pull origin main &amp;&amp; sudo bash deploy/vayumail-setup.sh</pre>`)
		fix.WriteString(`<p>Then reload this page.` + string(ui.Tip("Alternatives: with port 80 free, set VAYUOS_MAIL_TLS_ACME=on and VAYUOS_MAIL_ACME_EMAIL, then restart; or point VAYUOS_MAIL_TLS_CERT and VAYUOS_MAIL_TLS_KEY at a CA-signed pair (such as /etc/letsencrypt/live/"+host+"/fullchain.pem and privkey.pem) and restart. Either way DNS needs an A record for "+host+" and the firewall must open ports 25, 143, 993, 587, 995 and 110; the script does both.")) + `</p>`)
		cert.WriteString(string(ui.Callout("danger", ui.HTML(fix.String()))))
	case a.vayuMail.TLSActive():
		cert.WriteString(string(ui.Rows(ui.Row{Label: "Certificate", Hint: a.vayuMail.TLSNote(), Control: ui.State("ok", "Trusted")})))
		if mismatch {
			cert.WriteString(string(ui.Callout("warn", ui.HTML(`This certificate is valid for <code>`+html.EscapeString(strings.Join(covered, "</code>, <code>"))+`</code>, not <code>`+hHost+`</code>. `+
				`Set <code>VAYUOS_MAIL_HOSTNAME=`+html.EscapeString(covered[0])+`</code> and restart, so apps are handed a name it covers; or reissue it to include <code>`+hHost+`</code> (<code>sudo bash deploy/vayumail-setup.sh</code>, or <code>-d `+hHost+`</code> on your certbot command) and restart.`))))
		}
		// Even in ACME mode a challenge responder that cannot bind means the
		// renewal will fail and the certificate will lapse back to self-signed.
		if acmeErr != "" {
			cert.WriteString(string(ui.Callout("warn", ui.HTML(`Renewal may fail: `+html.EscapeString(acmeErr)+` (port 80 is held by another service). The guided script, <code>sudo bash deploy/vayumail-setup.sh</code>, renews through nginx instead.`))))
		}
	}
	if cert.Len() > 0 {
		sections = append(sections, ui.Section("Certificate", "", ui.HTML(cert.String())))
	}

	lis := string(ui.Rows(lrows...))
	if err := a.vayuMail.InboundError(); err != nil {
		lis += `<p class="sa-list__note">Some services could not bind: ` + html.EscapeString(err.Error()) + `.` +
			string(ui.Tip("Make sure the ports are free and the service may bind them: grant CAP_NET_BIND_SERVICE for ports below 1024, or point the VAYUOS_MAIL_*_LISTEN variables at high ports. Then restart.")) + `</p>`
	}
	sections = append(sections, ui.Section("Listeners", "Live", ui.HTML(lis)))

	// The official app, which needs nothing typed but an address and an app
	// password. Plain external links: CSP-safe, no third-party assets.
	sections = append(sections, ui.Section("The VayuMail app", "", ui.Rows(ui.Row{
		Label: "VayuMail for your phone",
		Hint:  "Sign in with your address and an app password; it fills in every server setting and keeps your PGP keys in step.",
		Control: ui.HTML(`<a class="btn btn--sm btn--ghost" href="https://github.com/johalputt/VayuMail-Mobile" target="_blank" rel="noopener noreferrer">Source</a>` +
			`<a class="btn btn--primary btn--sm" href="https://github.com/johalputt/VayuMail-Mobile/releases" target="_blank" rel="noopener noreferrer">Download</a>` +
			string(ui.Tip("VayuMail Mobile is VayuPress's own open-source app. It reads every server setting from /.well-known/vayumail/autoconfig.json and syncs PGP keys through WKD, so your mail stays end-to-end encrypted on your phone with no host, port or key typing."))),
	})))

	// App passwords: the list swaps in place on create and revoke; the form
	// is a sheet beside it.
	pwSheet := a.vayuAppPasswordSheet(r)
	pwHead := ""
	if pwSheet != "" {
		pwHead = `<div class="vm-row vm-row--end"><button type="button" class="btn btn--sm" data-sheet="new-app-password">` + saIcon("plus") + `New app password</button></div>`
		sheets.WriteString(pwSheet)
	}
	sections = append(sections, ui.Section("App passwords", "One per device, revocable", ui.HTML(pwHead+`<div id="vm-apppw-card">`+a.vayuAppPasswordsCard(r)+`</div>`)))

	// Any other app: autoconfig fills these in from the address; the rows are
	// for the apps that ask.
	val := func(s string) ui.HTML {
		return ui.HTML(`<span class="mono vm-connect__val">` + html.EscapeString(s) + `</span>`)
	}
	sections = append(sections, ui.Section("Any other mail app", "Thunderbird, Apple Mail, Outlook, K-9",
		ui.HTML(`<p class="sa-list__note">Choose Add account and enter your address and password: Thunderbird and K-9 fill in the rest.`+
			string(ui.Tip("Published at https://"+mc.Domain+"/.well-known/autoconfig/mail/config-v1.1.xml, and at /.well-known/vayumail/autoconfig.json for the VayuMail app."))+
			` For an app that asks:</p>`+string(ui.Rows(
			ui.Row{Label: "Incoming · IMAP", Hint: "Recommended: every device stays in step", Control: val(host + " · " + imapsPort + " SSL/TLS")},
			ui.Row{Label: "Incoming · IMAP, alternative", Control: val(host + " · " + imapPort + " STARTTLS")},
			ui.Row{Label: "Incoming · POP3", Hint: "Downloads to one device", Control: val(host + " · " + pop3sPort + " SSL, or " + pop3Port + " STLS")},
			ui.Row{Label: "Outgoing · SMTP", Hint: "Authentication required", Control: val(host + " · " + subPort + " STARTTLS")},
			ui.Row{Label: "Username", Control: ui.Text("Your full address, such as you@" + mc.Domain)},
			ui.Row{Label: "Password", Hint: "An app password for a device; the mailbox password works too", Control: ui.Text("App password")},
		)))))

	// Per mailbox, for the administrator setting up several; a holder sees
	// their own.
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
	var mrows [][]ui.HTML
	for _, em := range emails {
		mrows = append(mrows, []ui.HTML{ui.HTML(`<span class="mono">` + html.EscapeString(em) + `</span>`),
			ui.Text(imapsPort + " SSL/TLS"), ui.Text(pop3sPort + " SSL/TLS"), ui.Text(subPort + " STARTTLS")})
	}
	sections = append(sections, ui.Section("Per mailbox", "The username is the full address",
		ui.Table([]string{"Mailbox", "IMAP", "POP3", "SMTP (send)"}, mrows, "No active mailboxes yet. Make one under Accounts.")))

	// The new password's Copy and Download, and the sheet that closes once it
	// is made, are driven by admin-os-mail.js. This page never loaded it, so
	// both buttons rendered and did nothing.
	body := string(ui.Status(page, sections...)) + sheets.String() +
		`<script nonce="` + nonce + `" src="/os/static/js/admin-os-mail.js?v=` + assetVer("js/admin-os-mail.js") + `"></script>`
	writeOSHTML(w, r, adminOSLayout(nonce, "Connect a mail app", "vayuos", cfg, htmpl.HTML(body)))
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
	// DeleteMailbox, never Accounts().Delete: the store only clears SQLite, and a
	// mailbox whose messages stay on disk is inherited whole by whoever is given
	// the address next. The engine owns the ordering (rows first, so nothing is
	// still delivering) and reports where the mail was set aside.
	retired, err := a.vayuMail.DeleteMailbox(r.Context(), in.Email)
	if err != nil {
		writeAPIError(w, r, 500, "delete-failed", err.Error(), "")
		return
	}
	dbpkg.AuditLog("vayumail.account.delete", dbpkg.AuditActor(r), in.Email, retired)
	// The operator is told the mail was kept and roughly where, without being
	// handed a server path — an panel message naming a directory on the box is
	// infrastructure detail leaking into product copy.
	msg := "The mailbox was deleted. It had no stored mail."
	if retired != "" {
		msg = "The mailbox was deleted. Its messages were moved out of the delivery " +
			"tree and kept, so the address can be reissued without the new holder " +
			"seeing them."
	}
	writeJSON(w, r, 200, map[string]any{"deleted": true, "retained": retired != "", "detail": msg})
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
		// SignByDefault: compose signs this account's messages (OpenPGP)
		// unless the message says otherwise; nil = leave unchanged.
		SignByDefault *bool `json:"sign_by_default"`
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
	// set their OWN signature and whether it signs by default (and nothing
	// else), so the way they write is theirs to choose.
	if !a.isAdminRequest(r) {
		_, own := a.ownMailbox(r)
		onlySignature := (in.Signature != nil || in.SignByDefault != nil) && in.Pass == "" && in.Active == nil &&
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
	if in.SignByDefault != nil {
		if err := a.vayuMail.Accounts().SetSignByDefault(r.Context(), in.Email, *in.SignByDefault); err != nil {
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
		step, ok := totp.MatchAt(secret, in.Code, time.Now())
		if !ok {
			writeAPIError(w, r, 400, "totp-invalid", "that code is not valid — check the time on the device", "")
			return
		}
		// Single-use (audit): consume the enabling code like any other.
		if consumed, cerr := accts.ConsumeTOTPStep(r.Context(), email, int64(step)); cerr != nil || !consumed {
			writeAPIError(w, r, 400, "totp-invalid", "that code has already been used — try the next one", "")
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

// vayuAppPasswordsCard renders the app-password list on the Connect page:
// each mailbox's live credentials (label and created date only: hashes never
// leave the store), flush, with Revoke. Create and revoke POST the
// /os/vayumail/accounts/apppassword endpoints and swap this list in place; the
// create form is a sheet beside it (vayuAppPasswordSheet), so a swap never
// takes the open form away.
func (a *App) vayuAppPasswordsCard(r *http.Request) string {
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		return `<p class="muted text-sm">Mailbox storage is not available yet.</p>`
	}
	emails := a.appPasswordMailboxes(r)
	if len(emails) == 0 {
		return `<p class="muted text-sm">No active mailboxes yet. Make one under <a href="/os/vayumail/accounts">Accounts</a>.</p>`
	}
	post := ` hx-target="#vm-apppw-card" hx-swap="innerHTML"`
	var rows [][]ui.HTML
	for _, em := range emails {
		for _, p := range a.vayuMail.Accounts().ListAppPasswords(r.Context(), em) {
			rows = append(rows, []ui.HTML{
				ui.HTML(`<span class="mono">` + html.EscapeString(p.Email) + `</span>`),
				ui.Text(p.Label),
				ui.Text(p.CreatedAt.Format("2 Jan 2006")),
				ui.HTML(`<button type="button" class="btn btn--sm btn--ghost" hx-post="/os/vayumail/accounts/apppassword/delete"` + post + hxVals("email", p.Email, "id", strconv.FormatInt(p.ID, 10)) + ` hx-confirm="Revoke this app password? Devices signed in with it stop syncing immediately.">Revoke</button>`),
			})
		}
	}
	return string(ui.Table([]string{"Mailbox", "Device", "Created", ""}, rows, "No app passwords yet. One lets a device sign in without the mailbox password."))
}

// vayuAppPasswordSheet is the create form, in the sheet the section's button
// opens. The response swaps the list, which then carries the new password,
// shown once; the page's script closes the sheet so it can be read.
func (a *App) vayuAppPasswordSheet(r *http.Request) string {
	emails := a.appPasswordMailboxes(r)
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil || len(emails) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<form class="vm-acct-new" data-apppw-create hx-post="/os/vayumail/accounts/apppassword" hx-target="#vm-apppw-card" hx-swap="innerHTML">`)
	b.WriteString(`<label class="field"><span class="field-label">Mailbox</span><select class="input" name="email">`)
	for _, em := range emails {
		b.WriteString(`<option value="` + html.EscapeString(em) + `">` + html.EscapeString(em) + `</option>`)
	}
	b.WriteString(`</select></label>`)
	b.WriteString(`<label class="field"><span class="field-label">Which device is it for?</span><input class="input" type="text" name="label" placeholder="VayuMail Mobile" maxlength="64"></label>`)
	b.WriteString(`<div class="sa-sheet__foot"><span class="field-hint">Shown once, then stored only as a hash.</span><button class="btn btn--primary" type="submit">Create app password</button></div></form>`)
	return string(ui.Sheet("new-app-password", "New app password", ui.HTML(b.String())))
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
		// dashes are optional at sign-in (the auth path strips them). Copy and
		// save affordances match the recovery-code sheet — retyping a 20-character
		// secret onto a phone is where this used to go wrong.
		grouped := groupAppPasswordSecret(secret)
		// html.EscapeString called directly, not through a local alias: the label
		// and address come from the form, and CodeQL credits the escaper only
		// when it can see the call (go/reflected-xss).
		banner = string(ui.Callout("ok", ui.HTML(`<strong>Copy this app password now.</strong> It is shown only once and stored only as a hash: if it is lost, revoke it and make another.`+
			`<pre class="code-block code-block--wrap vm-apppw-secret">`+html.EscapeString(grouped)+`</pre>`+
			`<span class="vm-row vm-row--tight">`+
			`<button type="button" class="btn btn--sm" data-apppw-copy="`+html.EscapeString(grouped)+`">Copy password</button>`+
			`<button type="button" class="btn btn--sm btn--ghost" data-apppw-save="`+html.EscapeString(grouped)+`" data-apppw-label="`+html.EscapeString(label)+`" data-apppw-email="`+html.EscapeString(email)+`">Download .txt</button>`+
			`</span>`+
			`<span class="muted text-xs">Sign in to <span class="mono">`+html.EscapeString(email)+`</span> (`+html.EscapeString(label)+`) with it as the password, in the VayuMail app or any mail app. The dashes are optional.</span>`)))
	}
	card := a.vayuAppPasswordsCard(r)
	if opErr != nil {
		card = saCallout("danger", html.EscapeString(opErr.Error())) + card
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
		card = saCallout("danger", html.EscapeString(opErr.Error())) + card
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
		return `<span class="badge badge--ok">Approved</span>`
	case vmail.DeviceStatusBlocked:
		return `<span class="badge badge--danger">Blocked</span>`
	default:
		return `<span class="badge badge--pending">Pending approval</span>`
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
	b.WriteString(`<section class="vm-devices"><div class="vm-card-head"><h2 class="vm-devices__title">Devices</h2><span class="vm-live" title="Updates automatically">● live</span></div>`)
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
	b.WriteString(`<p class="text-sm">A <strong>new device</strong> waits here for your approval before it syncs any mail.` +
		string(ui.Tip("It cannot sync mail even with the correct password until you approve it. With approval required, the mailbox password alone never syncs mail over IMAP, POP3 or SMTP, so a stolen password is useless to an attacker's device.")) + `</p>`)

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
	b.WriteString(`<h3 class="vm-devices__sub">Require device approval</h3>`)
	b.WriteString(`<p class="muted text-sm">Recommended on.` +
		string(ui.Tip("When required, only approved devices sync mail; the mailbox password still signs into the web console and registers new devices. Turning it off lets the mailbox password sign in from any mail app, unapproved.")) + `</p>`)
	b.WriteString(`<div class="table-wrap"><table class="table"><thead><tr><th>Mailbox</th><th>Device approval</th><th></th></tr></thead><tbody>`)
	if len(accs) == 0 {
		b.WriteString(`<tr><td colspan="3" class="muted">No mail accounts yet.</td></tr>`)
	}
	for _, ac := range accs {
		state := `<span class="badge badge--ok">Required</span>`
		btn := `<button type="button" class="btn btn--sm"` + post + hxVals("op", "require-set", "email", ac.Email, "on", "0") + ` hx-confirm="Allow the mailbox password to sync mail from ANY device without approval?">Turn off</button>`
		if !ac.RequireDeviceApproval {
			state = `<span class="badge badge--warn">off — password syncs anywhere</span>`
			btn = `<button type="button" class="btn btn--primary btn--sm"` + post + hxVals("op", "require-set", "email", ac.Email, "on", "1") + `>Require approval</button>`
		}
		b.WriteString(`<tr><td class="mono">` + html.EscapeString(ac.Email) + `</td><td>` + state + `</td><td>` + btn + `</td></tr>`)
	}
	b.WriteString(`</tbody></table></div></section>`)
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
		card = saCallout("danger", html.EscapeString(opErr.Error())) + card
	}
	writeOSHTML(w, r, card)
}

// setMailSaid has Mail say text in its toast (admin-os-mail.js listens for
// vm-said), as a warning when warn, beside whatever the response swaps.
func setMailSaid(w http.ResponseWriter, text string, warn bool) {
	detail, _ := json.Marshal(map[string]any{"vm-said": map[string]any{"text": text, "warn": warn}})
	w.Header().Set("HX-Trigger", string(detail))
}
