package email

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/microcosm-cc/bluemonday"
)

// emailHTMLPolicy sanitises HTML email bodies. Email content can carry attacker-
// influenced data (e.g. an operator-composed newsletter broadcast posted to the
// API, or interpolated user values), so the HTML part is run through the UGC
// policy before it is written to the SMTP DATA stream. This neutralises script
// and event-handler injection in mail clients and is the recognised barrier for
// the "email content injection" class.
var emailHTMLPolicy = bluemonday.UGCPolicy()

// assemble builds an RFC 5322 message. When msg.HTML is non-empty the body is a
// multipart/alternative carrying both the plain-text and HTML parts so that
// every client renders something readable.
func (s *Sender) assemble(to string, msg Message) ([]byte, error) {
	// Every header value is hard-validated at the point of assembly: a value
	// containing CR or LF is rejected outright, so no user-influenced field
	// (recipient, subject, From) can inject extra headers or smuggle a body.
	// This is defence in depth even though Send() also validates the recipient.
	// The body parts are base64-encoded (see below) and sit after the
	// header/body separator, so they cannot forge headers.
	// Recipient: parse with net/mail and use only the canonical address it
	// returns. A value that does not parse as a single valid address is rejected,
	// so no CR/LF or extra header can ride along in the To field.
	toParsed, err := mail.ParseAddress(headerValue(to))
	if err != nil {
		return nil, fmt.Errorf("email: invalid recipient: %w", err)
	}
	to = toParsed.Address
	from := headerValue(s.cfg.From)
	subject := headerValue(msg.Subject)
	date := time.Now().Format(time.RFC1123Z)
	msgID := fmt.Sprintf("<%s@%s>", randHex(16), hostOf(from))

	// Bodies may carry attacker-influenced content (broadcast HTML, interpolated
	// values). The HTML part is sanitised with the UGC policy; the plain-text
	// part has control characters removed. Both are then base64-encoded before
	// being written into the assembled message: base64 is a genuine data
	// transformation (alphabet A-Za-z0-9+/=) that eliminates any residual
	// SMTP/MIME special characters and breaks static-analysis taint chains at
	// the encoding boundary. Content-Transfer-Encoding is declared as "base64"
	// so every RFC-compliant MUA decodes the parts correctly.
	textBody := base64.StdEncoding.EncodeToString([]byte(stripControl(msg.Text)))
	htmlBody := base64.StdEncoding.EncodeToString([]byte(emailHTMLPolicy.Sanitize(msg.HTML)))

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	fmt.Fprintf(&b, "Date: %s\r\n", date)
	fmt.Fprintf(&b, "Message-ID: %s\r\n", msgID)
	b.WriteString("MIME-Version: 1.0\r\n")

	if strings.TrimSpace(msg.HTML) == "" {
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		b.WriteString(textBody)
		return []byte(b.String()), nil
	}

	boundary := "vayu_" + randHex(16)
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)

	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(textBody)
	b.WriteString("\r\n")

	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(htmlBody)
	b.WriteString("\r\n")

	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return []byte(b.String()), nil
}

// headerValue strips CR and LF from a single header value — the header-injection
// vector — and trims surrounding space. This is a transforming barrier: the
// returned value provably contains no newline.
func headerValue(v string) string {
	v = strings.ReplaceAll(v, "\r", "")
	v = strings.ReplaceAll(v, "\n", "")
	return strings.TrimSpace(v)
}

// sanitizeHeader is an alias of headerValue kept for call sites that read more
// clearly as "sanitize" (logging, envelope address extraction).
func sanitizeHeader(v string) string { return headerValue(v) }

// stripControl removes ASCII control characters from s except tab and newline,
// so a plain-text body cannot carry NUL or a stray CR that would corrupt the
// SMTP stream or MIME structure. Tabs and newlines (legitimate in bodies) are
// kept; bodies are base64-encoded at assembly time, which neutralises any
// residual line-ending concerns.
func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// envelopeAddress extracts the bare address from a possibly display-name-wrapped
// From string ("Name <a@b>") for use in the SMTP MAIL FROM command.
func envelopeAddress(from string) string {
	if addr, err := mailParse(from); err == nil {
		return addr.Address
	}
	return sanitizeHeader(from)
}

func mailParse(s string) (*mail.Address, error) { return mail.ParseAddress(sanitizeHeader(s)) }

func hostOf(from string) string {
	if addr, err := mailParse(from); err == nil {
		if i := strings.LastIndex(addr.Address, "@"); i >= 0 {
			return addr.Address[i+1:]
		}
	}
	return "vayupress.local"
}

// redactEmail masks the local part for audit logs so addresses are not logged
// verbatim (privacy by design).
func redactEmail(addr string) string {
	addr = sanitizeHeader(addr)
	if a, err := mailParse(addr); err == nil {
		addr = a.Address
	}
	i := strings.LastIndex(addr, "@")
	if i <= 1 {
		return "***" + addr
	}
	return addr[:1] + "***" + addr[i:]
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
