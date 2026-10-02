// SPDX-License-Identifier: Apache-2.0

package mail

// summary_test.go — what a folder listing reads of a message (summarize). One
// fixture per rule, so each mutation is caught by the test named for it and
// not by a neighbour that happens to trip too.

import (
	"io"
	"strings"
	"testing"
)

func summary(t *testing.T, raw string) cachedHeaders {
	t.Helper()
	var h cachedHeaders
	summarize(strings.NewReader(strings.ReplaceAll(raw, "\n", "\r\n")), &h)
	return h
}

// An encrypted message's preview is the words "Encrypted message", never a
// byte of what it carries, whichever way it is encrypted.
func TestThePreviewNeverShowsCiphertext(t *testing.T) {
	for name, raw := range map[string]string{
		"PGP/MIME": `From: a@x.test
Subject: s
Content-Type: multipart/encrypted; protocol="application/pgp-encrypted"; boundary="b"

--b
Content-Type: application/pgp-encrypted

Version: 1
--b
Content-Type: application/octet-stream

-----BEGIN PGP MESSAGE-----
hQEMA0secretciphertextQQ
-----END PGP MESSAGE-----
--b--
`,
		"inline PGP": `From: a@x.test
Subject: s
Content-Type: text/plain

-----BEGIN PGP MESSAGE-----
hQEMA0secretciphertextQQ
-----END PGP MESSAGE-----
`,
		"S/MIME": `From: a@x.test
Subject: s
Content-Type: application/pkcs7-mime; smime-type=enveloped-data; name=smime.p7m
Content-Transfer-Encoding: base64

MIAGCSqGSIb3DQEHA6CAMIACAQAxggFAMIIBPAIBADCBpDCBnjELMAkGA1UEBhMC
`,
	} {
		h := summary(t, raw)
		if h.preview != encryptedPreview || !h.encrypted {
			t.Errorf("%s: preview %q, encrypted %v", name, h.preview, h.encrypted)
		}
		if h.attachment {
			t.Errorf("%s: the ciphertext is marked as an attachment", name)
		}
	}
}

// Each encoding a body arrives in is undone before its first words are taken.
func TestThePreviewDecodesTheBody(t *testing.T) {
	for _, c := range []struct{ name, raw, want string }{
		{"quoted-printable", `From: a@x.test
Content-Type: text/plain; charset=utf-8
Content-Transfer-Encoding: quoted-printable

Caf=C3=A9 at nine, then the=20
long walk home.
`, "Café at nine, then the long walk home."},
		{"base64", `From: a@x.test
Content-Type: text/plain; charset=utf-8
Content-Transfer-Encoding: base64

VGhlIG1pbnV0ZXMgYXJlIGF0dGFjaGVkLg==
`, "The minutes are attached."},
		{"nested alternative", `From: a@x.test
Content-Type: multipart/mixed; boundary="outer"

--outer
Content-Type: multipart/alternative; boundary="inner"

--inner
Content-Type: text/plain

Plain words win.
--inner
Content-Type: text/html

<p>HTML words lose.</p>
--inner--
--outer--
`, "Plain words win."},
		{"HTML only", `From: a@x.test
Content-Type: text/html

<html><head><title>Newsletter</title><style>p{color:red}</style></head>
<body><script>track()</script><p>Hello <b>Priya</b>,</p><p>the &amp; plan is ready.</p></body></html>
`, "Hello Priya, the & plan is ready."},
	} {
		if got := summary(t, c.raw).preview; got != c.want {
			t.Errorf("%s: preview %q, want %q", c.name, got, c.want)
		}
	}
}

// A long preview is cut at a word's worth of runes, with an ellipsis.
func TestThePreviewIsClipped(t *testing.T) {
	h := summary(t, "From: a@x.test\n\n"+strings.Repeat("ünïcode ", 60)+"\n")
	if n := len([]rune(h.preview)); n > previewRunes+1 || !strings.HasSuffix(h.preview, "…") {
		t.Errorf("preview of %d runes: %q", n, h.preview)
	}
}

// A message is listed by its start: a 50 MB message costs its header and
// 32 KB of body, never its whole size.
func TestTheSummaryReadsABoundedPrefix(t *testing.T) {
	head := "From: a@x.test\r\nSubject: big\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nSee attached.\r\n--b\r\nContent-Type: application/pdf\r\nContent-Transfer-Encoding: base64\r\n\r\n"
	src := &countingReader{r: io.MultiReader(strings.NewReader(head), io.LimitReader(endlessA{}, 50<<20))}
	var h cachedHeaders
	summarize(src, &h)
	if h.preview != "See attached." || !h.attachment {
		t.Fatalf("preview %q, attachment %v", h.preview, h.attachment)
	}
	// The body allowance plus at most one buffer of read-ahead.
	if limit := int64(len(head)) + summaryBodyBytes + 4096; src.n > limit {
		t.Errorf("read %d bytes of a 50 MB message; at most %d", src.n, limit)
	}
}

// The fields a listing shows come after the relays' Received lines, so a long
// chain must not push From and Subject out of what is read.
func TestALongHeaderBlockKeepsItsFields(t *testing.T) {
	received := strings.Repeat("Received: from relay.example.test (relay.example.test [192.0.2.1]) by mx.example.test with ESMTPS id abcdefghij; Mon, 2 Jan 2006 15:04:05 -0700\n", 400)
	h := summary(t, received+"From: late@x.test\nSubject: Still here\n\nbody\n")
	if h.from != "late@x.test" || h.subject != "Still here" || h.preview != "body" {
		t.Errorf("from %q, subject %q, preview %q", h.from, h.subject, h.preview)
	}
}

// A file marks the row; a signature does not, and a signed message says so.
func TestAttachmentAndSignatureMarks(t *testing.T) {
	att := summary(t, `From: a@x.test
Content-Type: multipart/mixed; boundary="b"

--b
Content-Type: text/plain

Invoice inside.
--b
Content-Type: application/pdf; name="invoice.pdf"
Content-Disposition: attachment; filename="invoice.pdf"
Content-Transfer-Encoding: base64

JVBERi0xLjQK
--b--
`)
	if !att.attachment || att.signed || att.preview != "Invoice inside." {
		t.Errorf("attached file: %+v", att)
	}
	signed := summary(t, `From: a@x.test
Content-Type: multipart/signed; protocol="application/pgp-signature"; micalg=pgp-sha256; boundary="b"

--b
Content-Type: text/plain

Signed words.
--b
Content-Type: application/pgp-signature; name="signature.asc"

-----BEGIN PGP SIGNATURE-----
iQEzBAEBCAAdFiEE
-----END PGP SIGNATURE-----
--b--
`)
	if signed.attachment || !signed.signed || signed.encrypted || signed.preview != "Signed words." {
		t.Errorf("signed message: %+v", signed)
	}
	clear := summary(t, `From: a@x.test
Content-Type: text/plain

-----BEGIN PGP SIGNED MESSAGE-----
Hash: SHA256

Clear-signed words.
-----BEGIN PGP SIGNATURE-----
iQEzBAEBCAAdFiEE
-----END PGP SIGNATURE-----
`)
	if !clear.signed || clear.preview != "Clear-signed words." {
		t.Errorf("clear-signed message: preview %q, signed %v", clear.preview, clear.signed)
	}
}

// The listing carries the summary to its callers.
func TestTheFolderListingCarriesThePreview(t *testing.T) {
	t.Parallel()
	e := newLoopbackEngine(t, nil)
	raw := "From: s@x.test\r\nTo: alice@test\r\nSubject: Hi\r\n\r\nSee you at nine.\r\n"
	if _, err := e.maildir.DeliverTo("example.com", "alice", "Inbox", []byte(raw)); err != nil {
		t.Fatal(err)
	}
	got, err := e.ListFolder(ReadAsSystem("alice", "test"), "Inbox")
	if err != nil || len(got) != 1 || got[0].Preview != "See you at nine." {
		t.Fatalf("listing: %+v, %v", got, err)
	}
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

type endlessA struct{}

func (endlessA) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'A'
	}
	return len(p), nil
}
