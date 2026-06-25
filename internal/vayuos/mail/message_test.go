package mail

import (
	"strings"
	"testing"
)

func crlf(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }

// A multipart/alternative message (the common Gmail shape) must yield decoded
// text and HTML parts plus decoded headers.
func TestParseMessageMultipartAlternative(t *testing.T) {
	t.Parallel()
	raw := crlf(`From: =?UTF-8?Q?Ankush?= <ankush@example.com>
To: bob@example.com
Subject: =?UTF-8?Q?Hi_there?=
Date: Thu, 25 Jun 2026 17:41:35 +0530
MIME-Version: 1.0
Content-Type: multipart/alternative; boundary="BOUND1"

--BOUND1
Content-Type: text/plain; charset="UTF-8"
Content-Transfer-Encoding: quoted-printable

Hello=20World plain

--BOUND1
Content-Type: text/html; charset="UTF-8"
Content-Transfer-Encoding: quoted-printable

<div dir=3D"auto">Hello World html</div>
--BOUND1--
`)
	pm := ParseMessage([]byte(raw))
	if !strings.Contains(pm.From, "Ankush") || !strings.Contains(pm.From, "ankush@example.com") {
		t.Errorf("From not decoded: %q", pm.From)
	}
	if pm.Subject != "Hi there" {
		t.Errorf("Subject not decoded: %q", pm.Subject)
	}
	if !strings.Contains(pm.Text, "Hello World plain") {
		t.Errorf("text/plain not decoded: %q", pm.Text)
	}
	if !strings.Contains(pm.HTML, `Hello World html`) || !strings.Contains(pm.HTML, "<div") {
		t.Errorf("text/html not decoded: %q", pm.HTML)
	}
}

func TestParseMessageBase64(t *testing.T) {
	t.Parallel()
	raw := crlf(`From: a@example.com
Subject: b64
Content-Type: text/plain; charset="UTF-8"
Content-Transfer-Encoding: base64

SGVsbG8gQmFzZTY0
`)
	pm := ParseMessage([]byte(raw))
	if pm.Text != "Hello Base64" {
		t.Errorf("base64 body not decoded: %q", pm.Text)
	}
}

func TestParseMessagePlainNoContentType(t *testing.T) {
	t.Parallel()
	raw := crlf(`From: a@example.com
Subject: plain

just a plain body
`)
	pm := ParseMessage([]byte(raw))
	if !strings.Contains(pm.Text, "just a plain body") {
		t.Errorf("plain body missing: %q", pm.Text)
	}
	if pm.HTML != "" {
		t.Errorf("did not expect HTML: %q", pm.HTML)
	}
}

func TestParseMessageGarbageNeverPanics(t *testing.T) {
	t.Parallel()
	pm := ParseMessage([]byte("this is not a real message at all"))
	if pm.Text == "" {
		t.Errorf("expected raw fallback text for unparseable input")
	}
}

func TestEnvelopeAddress(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		`"Ankush" <a@b.com>`:                 "a@b.com",
		`Ankush Choudhary <ankush@johal.in>`: "ankush@johal.in",
		`a@b.com`:                            "a@b.com",
		`  spaced@b.com  `:                   "spaced@b.com",
		`<only@brackets.com>`:                "only@brackets.com",
	}
	for in, want := range cases {
		if got := envelopeAddress(in); got != want {
			t.Errorf("envelopeAddress(%q) = %q, want %q", in, got, want)
		}
	}
}
