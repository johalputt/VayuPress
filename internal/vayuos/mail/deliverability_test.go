package mail

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	netmail "net/mail"
	"strings"
	"testing"
)

// TestMultipartSignedMessageIsWellFormed builds the same multipart/alternative
// message SendMail produces (text + HTML), DKIM-signs it via the vetted
// library, and confirms the result parses as a valid MIME message with both
// alternatives intact. This guards the outbound pipeline against the kind of
// malformed-message / broken-signature regressions that send mail to spam.
func TestMultipartSignedMessageIsWellFormed(t *testing.T) {
	t.Parallel()
	dk, err := LoadOrCreateDKIM(t.TempDir(), "vayu", "example.com")
	if err != nil {
		t.Fatalf("dkim: %v", err)
	}

	boundary := mimeBoundary()
	var body bytes.Buffer
	writeMIMEPart(&body, boundary, "text/plain; charset=utf-8", "Hello in plain text")
	writeMIMEPart(&body, boundary, "text/html; charset=utf-8", "<p>Hello in <b>HTML</b></p>")
	body.WriteString("--" + boundary + "--\r\n")

	raw := "From: Alice <alice@example.com>\r\n" +
		"To: bob@example.net\r\n" +
		"Subject: multipart test\r\n" +
		"Date: Mon, 02 Jan 2006 15:04:05 +0000\r\n" +
		"Message-ID: <abc@example.com>\r\n" +
		"MIME-Version: 1.0\r\n" +
		`Content-Type: multipart/alternative; boundary="` + boundary + "\"\r\n" +
		"\r\n" + body.String()

	signed, err := dk.SignMessage([]byte(raw))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	msg, err := netmail.ReadMessage(bytes.NewReader(signed))
	if err != nil {
		t.Fatalf("parse signed message: %v", err)
	}
	if msg.Header.Get("DKIM-Signature") == "" {
		t.Fatal("missing DKIM-Signature header on signed message")
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/alternative" {
		t.Fatalf("content-type = %q (%v)", mediaType, err)
	}

	mr := multipart.NewReader(msg.Body, params["boundary"])
	var types []string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next part: %v", err)
		}
		types = append(types, p.Header.Get("Content-Type"))
		_, _ = io.ReadAll(p)
	}
	if len(types) != 2 || !strings.HasPrefix(types[0], "text/plain") || !strings.HasPrefix(types[1], "text/html") {
		t.Fatalf("expected text/plain then text/html parts, got %v", types)
	}
}

// TestNormalizeCRLF ensures bare LF and lone CR are canonicalised to CRLF.
func TestNormalizeCRLF(t *testing.T) {
	t.Parallel()
	got := normalizeCRLF("a\nb\r\nc\rd")
	if got != "a\r\nb\r\nc\r\nd" {
		t.Fatalf("normalizeCRLF = %q", got)
	}
}
