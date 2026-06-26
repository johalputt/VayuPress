package mail

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
)

// ParsedMessage is a decoded view of a stored RFC 5322 message for display in
// the reader view. Header fields have RFC 2047 encoded-words decoded; the Text
// and HTML bodies have their transfer-encoding undone. Bodies are NOT
// sanitised — the caller must escape Text and run HTML through a sanitiser
// before rendering.
type ParsedMessage struct {
	From    string
	To      string
	Cc      string
	Subject string
	Date    string
	Text    string // decoded text/plain part, if present
	HTML    string // decoded text/html part, if present (unsanitised)
}

// maxPartBytes bounds how much of any single MIME part we decode for display,
// keeping the reader view cheap on a low-resource host.
const maxPartBytes = 2 << 20 // 2 MiB

// maxMIMEDepth guards against pathologically nested multipart messages.
const maxMIMEDepth = 8

// ParseMessage decodes a raw message into headers plus best-effort text/HTML
// bodies. It understands multipart/* containers (alternative/mixed/related,
// recursively), quoted-printable and base64 transfer encodings, and RFC 2047
// encoded-word headers. It never errors: if the structure can't be parsed it
// falls back to the raw payload as Text so the reader always shows something.
func ParseMessage(raw []byte) ParsedMessage {
	pm := ParsedMessage{}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		pm.Text = string(raw)
		return pm
	}
	dec := &mime.WordDecoder{}
	decodeHdr := func(s string) string {
		if s == "" {
			return ""
		}
		if d, derr := dec.DecodeHeader(s); derr == nil {
			return d
		}
		return s
	}
	pm.From = decodeHdr(msg.Header.Get("From"))
	pm.To = decodeHdr(msg.Header.Get("To"))
	pm.Cc = decodeHdr(msg.Header.Get("Cc"))
	pm.Subject = decodeHdr(msg.Header.Get("Subject"))
	pm.Date = msg.Header.Get("Date")

	collectPart(msg.Header.Get("Content-Type"), msg.Header.Get("Content-Transfer-Encoding"), msg.Body, &pm, 0)

	// Fallback: an unrecognised single-part message still shows its body.
	if pm.Text == "" && pm.HTML == "" {
		if b, rerr := io.ReadAll(io.LimitReader(msg.Body, maxPartBytes)); rerr == nil && len(b) > 0 {
			pm.Text = string(b)
		}
	}
	return pm
}

// collectPart walks one MIME entity, recursing into multipart containers and
// filling pm.Text / pm.HTML from the first text/plain and text/html leaves.
func collectPart(ctype, cte string, body io.Reader, pm *ParsedMessage, depth int) {
	if depth > maxMIMEDepth {
		return
	}
	mediaType, params, err := mime.ParseMediaType(ctype)
	if err != nil || ctype == "" {
		mediaType = "text/plain" // no/garbled Content-Type → treat as plain text
		params = map[string]string{}
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return
		}
		mr := multipart.NewReader(body, boundary)
		for {
			part, perr := mr.NextPart()
			if perr != nil {
				break
			}
			collectPart(part.Header.Get("Content-Type"), part.Header.Get("Content-Transfer-Encoding"), part, pm, depth+1)
			_ = part.Close()
		}
		return
	}
	data := decodeBody(cte, body)
	switch {
	case strings.HasPrefix(mediaType, "text/html"):
		if pm.HTML == "" {
			pm.HTML = data
		}
	case strings.HasPrefix(mediaType, "text/"):
		if pm.Text == "" {
			pm.Text = data
		}
	}
}

// decodeBody undoes the content-transfer-encoding for a leaf part (bounded).
// Go's base64 decoder already skips the CRLF line breaks MIME inserts.
func decodeBody(cte string, body io.Reader) string {
	var r io.Reader = io.LimitReader(body, maxPartBytes)
	switch strings.ToLower(strings.TrimSpace(cte)) {
	case "quoted-printable":
		r = quotedprintable.NewReader(r)
	case "base64":
		r = base64.NewDecoder(base64.StdEncoding, r)
	}
	b, _ := io.ReadAll(io.LimitReader(r, maxPartBytes))
	return string(b)
}

// envelopeAddress extracts the bare address from a From value that may carry a
// display name (e.g. `"Ankush" <a@b>` → `a@b`). It is used for the SMTP
// envelope (MAIL FROM) and the outbound queue, which must never see a display
// name, while the RFC 5322 From: header keeps the friendly form.
func envelopeAddress(s string) string {
	if addr, err := mail.ParseAddress(s); err == nil && addr.Address != "" {
		return addr.Address
	}
	local, domain := splitAddress(s)
	if domain != "" {
		return local + "@" + domain
	}
	return strings.TrimSpace(local)
}
