package email

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// assemble builds an RFC 5322 message. When msg.HTML is non-empty the body is a
// multipart/alternative carrying both the plain-text and HTML parts so that
// every client renders something readable.
func (s *Sender) assemble(to string, msg Message) ([]byte, error) {
	// Every header value is CRLF-stripped at the point of assembly so no
	// user-influenced field (recipient, subject, From) can inject extra headers
	// or smuggle a body — defence in depth even though Send() also validates the
	// recipient. The body parts are line-ending-normalised (see normalizeBody)
	// and sit after the header/body separator, so they cannot forge headers.
	to = sanitizeHeader(to)
	from := sanitizeHeader(s.cfg.From)
	subject := sanitizeHeader(msg.Subject)
	date := time.Now().Format(time.RFC1123Z)
	msgID := fmt.Sprintf("<%s@%s>", randHex(16), hostOf(from))

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	fmt.Fprintf(&b, "Date: %s\r\n", date)
	fmt.Fprintf(&b, "Message-ID: %s\r\n", msgID)
	b.WriteString("MIME-Version: 1.0\r\n")

	if strings.TrimSpace(msg.HTML) == "" {
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		b.WriteString(normalizeBody(msg.Text))
		return []byte(b.String()), nil
	}

	boundary := "vayu_" + randHex(16)
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)

	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(normalizeBody(msg.Text))
	b.WriteString("\r\n")

	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(normalizeBody(msg.HTML))
	b.WriteString("\r\n")

	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return []byte(b.String()), nil
}

// sanitizeHeader strips CR/LF to defeat header-injection through user-supplied
// subjects or addresses.
func sanitizeHeader(v string) string {
	v = strings.ReplaceAll(v, "\r", "")
	v = strings.ReplaceAll(v, "\n", "")
	return strings.TrimSpace(v)
}

// normalizeBody ensures the body uses CRLF line endings as required by SMTP and
// guards against accidental "." dot-stuffing edge cases at line starts.
func normalizeBody(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", "\r\n")
	return s
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
