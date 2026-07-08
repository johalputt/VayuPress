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
	From        string
	To          string
	Cc          string
	Subject     string
	Date        string
	Text        string           // decoded text/plain part, if present
	HTML        string           // decoded text/html part, if present (unsanitised)
	Attachments []AttachmentInfo // file attachments, in document order
}

// AttachmentInfo describes one downloadable attachment enumerated from a
// message. Index is 0-based in the same order ExtractAttachment walks, so the
// reader can build a stable download link (…/attachment?idx=N). Size is an
// approximate decoded byte count (base64 parts are estimated) for display only.
type AttachmentInfo struct {
	Filename    string
	ContentType string
	Size        int64
	Index       int
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

	collectPart(msg.Header.Get("Content-Type"), msg.Header.Get("Content-Transfer-Encoding"), msg.Header.Get("Content-Disposition"), msg.Body, &pm, 0)

	// Fallback: an unrecognised single-part message still shows its body.
	if pm.Text == "" && pm.HTML == "" {
		if b, rerr := io.ReadAll(io.LimitReader(msg.Body, maxPartBytes)); rerr == nil && len(b) > 0 {
			pm.Text = string(b)
		}
	}
	return pm
}

// collectPart walks one MIME entity, recursing into multipart containers,
// filling pm.Text / pm.HTML from the first text/plain and text/html leaves and
// enumerating every other leaf (or any leaf carrying a filename) as an
// attachment. Attachment bodies are not decoded here — only their approximate
// size is counted — so opening a message stays cheap; the bytes are decoded
// lazily by ExtractAttachment when the operator downloads one.
func collectPart(ctype, cte, cdisp string, body io.Reader, pm *ParsedMessage, depth int) {
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
			collectPart(part.Header.Get("Content-Type"), part.Header.Get("Content-Transfer-Encoding"), part.Header.Get("Content-Disposition"), part, pm, depth+1)
			_ = part.Close()
		}
		return
	}
	// A leaf with a filename, an explicit attachment disposition, or a non-text
	// media type is a downloadable attachment (not shown inline).
	filename := partFilename(params, cdisp)
	disp, _, _ := mime.ParseMediaType(cdisp)
	if disp == "attachment" || filename != "" || !strings.HasPrefix(mediaType, "text/") {
		cw := &countWriter{}
		_, _ = io.Copy(cw, body) // must drain to advance the multipart reader anyway
		if filename == "" {
			filename = "attachment"
		}
		pm.Attachments = append(pm.Attachments, AttachmentInfo{
			Filename:    filename,
			ContentType: mediaType,
			Size:        estimatedDecodedSize(cte, cw.n),
			Index:       len(pm.Attachments),
		})
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

// countWriter counts bytes without retaining them.
type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

// estimatedDecodedSize approximates the decoded size of a part from its encoded
// length (base64 expands ~4/3), for a display-only size hint.
func estimatedDecodedSize(cte string, encodedLen int64) int64 {
	if strings.EqualFold(strings.TrimSpace(cte), "base64") {
		return encodedLen * 3 / 4
	}
	return encodedLen
}

// partFilename extracts an attachment filename from the Content-Disposition
// (filename=) or Content-Type (name=) parameters, decoding RFC 2047 words.
func partFilename(ctypeParams map[string]string, cdisp string) string {
	if cdisp != "" {
		if _, params, err := mime.ParseMediaType(cdisp); err == nil {
			if fn := params["filename"]; fn != "" {
				return decodeMIMEWord(fn)
			}
		}
	}
	if fn := ctypeParams["name"]; fn != "" {
		return decodeMIMEWord(fn)
	}
	return ""
}

func decodeMIMEWord(s string) string {
	dec := &mime.WordDecoder{}
	if d, err := dec.DecodeHeader(s); err == nil {
		return d
	}
	return s
}

// maxAttachmentBytes bounds a single attachment download so a crafted message
// cannot exhaust memory.
const maxAttachmentBytes = 64 << 20 // 64 MiB

// ExtractAttachment re-walks a raw message and returns the idx-th attachment
// (0-based, in the same order ParseMessage enumerates them), fully decoded and
// ready to stream. ok is false when idx is out of range.
func ExtractAttachment(raw []byte, idx int) (filename, ctype string, data []byte, ok bool) {
	if idx < 0 {
		return "", "", nil, false
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return "", "", nil, false
	}
	counter := 0
	return findAttachment(msg.Header.Get("Content-Type"), msg.Header.Get("Content-Transfer-Encoding"), msg.Header.Get("Content-Disposition"), msg.Body, 0, idx, &counter)
}

func findAttachment(ctype, cte, cdisp string, body io.Reader, depth, target int, counter *int) (string, string, []byte, bool) {
	if depth > maxMIMEDepth {
		return "", "", nil, false
	}
	mediaType, params, err := mime.ParseMediaType(ctype)
	if err != nil || ctype == "" {
		mediaType = "text/plain"
		params = map[string]string{}
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return "", "", nil, false
		}
		mr := multipart.NewReader(body, boundary)
		for {
			part, perr := mr.NextPart()
			if perr != nil {
				break
			}
			fn, ct, d, found := findAttachment(part.Header.Get("Content-Type"), part.Header.Get("Content-Transfer-Encoding"), part.Header.Get("Content-Disposition"), part, depth+1, target, counter)
			_ = part.Close()
			if found {
				return fn, ct, d, true
			}
		}
		return "", "", nil, false
	}
	filename := partFilename(params, cdisp)
	disp, _, _ := mime.ParseMediaType(cdisp)
	if disp != "attachment" && filename == "" && strings.HasPrefix(mediaType, "text/") {
		return "", "", nil, false // an inline text body, not an attachment
	}
	if *counter == target {
		if filename == "" {
			filename = "attachment"
		}
		return filename, mediaType, []byte(decodeBodyLimited(cte, body, maxAttachmentBytes)), true
	}
	*counter++
	return "", "", nil, false
}

// decodeBody undoes the content-transfer-encoding for a leaf part, bounded to
// maxPartBytes (the display cap). Go's base64 decoder already skips the CRLF
// line breaks MIME inserts.
func decodeBody(cte string, body io.Reader) string {
	return decodeBodyLimited(cte, body, maxPartBytes)
}

// decodeBodyLimited undoes the content-transfer-encoding for a leaf part,
// bounded to limit bytes of decoded output.
func decodeBodyLimited(cte string, body io.Reader, limit int64) string {
	var r io.Reader = io.LimitReader(body, limit)
	switch strings.ToLower(strings.TrimSpace(cte)) {
	case "quoted-printable":
		r = quotedprintable.NewReader(r)
	case "base64":
		r = base64.NewDecoder(base64.StdEncoding, r)
	}
	b, _ := io.ReadAll(io.LimitReader(r, limit))
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
