// SPDX-License-Identifier: Apache-2.0

package mail

// drafts.go — drafts as real messages, attachments included.
//
// A draft used to be a minimal From/To/Subject/Date message plus a body. Cc, Bcc
// and — worst of all — attachments were silently absent from it, so reopening a
// saved draft and pressing Send sent a message that was missing parts the sender
// had watched appear on screen. A draft now carries its recipients AND its files,
// and the send path can take them straight from the draft instead of asking for
// them a second time.

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	netmail "net/mail"
	"strings"
	"time"
)

// maxDraftAttachmentBytes bounds a single part lifted out of a stored draft, so a
// malformed or hostile draft cannot make the send path allocate without limit.
const maxDraftAttachmentBytes = 64 << 20 // 64 MiB per part

// SaveDraftWithAttachments files a composed message, with any attached files,
// into the sender's Drafts folder and returns its id.
func (e *Engine) SaveDraftWithAttachments(from string, to, cc, bcc []string, subject, body string, atts []Attachment) (string, error) {
	if e.maildir == nil {
		return "", errors.New("vayumail: not started")
	}
	local, _ := splitAddress(from)
	raw := e.buildDraftMessage(from, to, cc, bcc, subject, body, atts)
	return e.maildir.DeliverTo(e.senderDomain(from), local, "Drafts", raw)
}

// buildDraftMessage assembles the stored draft.
//
// With no attachments the result is byte-identical to the message this function
// always produced (no MIME headers at all), so nothing about plain drafts changes.
// With attachments it becomes multipart/mixed: the text part first, then one
// base64 part per file, which is what every other mail client stores.
func (e *Engine) buildDraftMessage(from string, to, cc, bcc []string, subject, body string, atts []Attachment) []byte {
	var head strings.Builder
	head.WriteString("From: " + from + "\r\n")
	head.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	if len(cc) > 0 {
		head.WriteString("Cc: " + strings.Join(cc, ", ") + "\r\n")
	}
	if len(bcc) > 0 {
		head.WriteString("Bcc: " + strings.Join(bcc, ", ") + "\r\n")
	}
	head.WriteString("Subject: " + subject + "\r\n")
	head.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	if len(atts) == 0 {
		return []byte(head.String() + "\r\n" + body + "\r\n")
	}

	boundary := draftBoundary()
	var b bytes.Buffer
	b.WriteString(head.String())
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n")
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	b.WriteString(body + "\r\n")
	for _, a := range atts {
		if len(a.Data) == 0 {
			continue // an empty file is not worth a part, and would look like a bug
		}
		ct := strings.TrimSpace(a.ContentType)
		if ct == "" {
			ct = "application/octet-stream"
		}
		name := sanitizeHeaderParam(a.Filename)
		if name == "" {
			name = "attachment"
		}
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: " + ct + "; name=\"" + name + "\"\r\n")
		b.WriteString("Content-Disposition: attachment; filename=\"" + name + "\"\r\n")
		b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		enc := base64.StdEncoding.EncodeToString(a.Data)
		for i := 0; i < len(enc); i += 76 {
			end := i + 76
			if end > len(enc) {
				end = len(enc)
			}
			b.WriteString(enc[i:end] + "\r\n")
		}
	}
	b.WriteString("--" + boundary + "--\r\n")
	return b.Bytes()
}

// DraftAttachments returns the files stored with a draft, so the send path can
// carry them onto the outgoing message rather than making the sender find and
// attach the same files again.
func (e *Engine) DraftAttachments(rd Reader, id string) ([]Attachment, error) {
	if err := e.readAuthorised(rd); err != nil {
		return nil, err
	}
	raw, err := e.ReadFolderMessage(rd, "Drafts", id)
	if err != nil {
		return nil, err
	}
	return parseAttachmentParts(raw), nil
}

// parseAttachmentParts lifts every attachment out of a stored message.
//
// Tolerant by design: a draft that is not multipart, a part that cannot be
// decoded, or an absurdly large part all simply yield nothing rather than failing
// the send. The message itself is what the sender cares about.
func parseAttachmentParts(raw []byte) []Attachment {
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return nil
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil
	}
	mr := multipart.NewReader(msg.Body, boundary)
	var out []Attachment
	for {
		part, perr := mr.NextPart()
		if perr != nil {
			break // io.EOF, or a malformed part: keep what we already read
		}
		disp, dparams, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		name := strings.TrimSpace(dparams["filename"])
		if name == "" {
			if _, cparams, cerr := mime.ParseMediaType(part.Header.Get("Content-Type")); cerr == nil {
				name = strings.TrimSpace(cparams["name"])
			}
		}
		// The body part has neither a filename nor an attachment disposition.
		if !strings.EqualFold(disp, "attachment") && name == "" {
			continue
		}
		var reader io.Reader = part
		if strings.EqualFold(strings.TrimSpace(part.Header.Get("Content-Transfer-Encoding")), "base64") {
			reader = base64.NewDecoder(base64.StdEncoding, part)
		}
		data, rerr := io.ReadAll(io.LimitReader(reader, maxDraftAttachmentBytes+1))
		if rerr != nil || int64(len(data)) > maxDraftAttachmentBytes {
			continue
		}
		ct := strings.TrimSpace(part.Header.Get("Content-Type"))
		if mt, _, merr := mime.ParseMediaType(ct); merr == nil {
			ct = mt
		}
		if ct == "" {
			ct = "application/octet-stream"
		}
		if name == "" {
			name = "attachment"
		}
		out = append(out, Attachment{Filename: name, ContentType: ct, Data: data})
	}
	return out
}

// draftBoundary mints a multipart boundary that cannot appear in the body: 32 hex
// characters from the CSPRNG.
func draftBoundary() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand does not fail on supported platforms; a time-derived value
		// is still unique enough for a boundary and is never a security decision.
		return "vp-draft-" + hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return "vp-draft-" + hex.EncodeToString(b)
}

// sanitizeHeaderParam makes a filename safe to place inside a quoted MIME
// parameter: quotes and control characters (including CR/LF, which would inject a
// header) are replaced with '_'.
func sanitizeHeaderParam(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x20 || r == 0x7f, r == '"', r == '\\':
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
