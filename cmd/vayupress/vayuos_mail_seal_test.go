// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	vpgp "github.com/johalputt/vayupress/internal/vayuos/pgp"
)

// fakeSeal answers every check with one verdict, and records what it was given.
type fakeSeal struct {
	v       vpgp.SigVerdict
	gotData []byte
}

func (f *fakeSeal) DecryptAndCheck(ct []byte, _, _ string) ([]byte, vpgp.SigVerdict, error) {
	f.gotData = ct
	return []byte("plain"), f.v, nil
}
func (f *fakeSeal) CheckDetached(data, _ []byte, _ string) vpgp.SigVerdict {
	f.gotData = data
	return f.v
}
func (f *fakeSeal) CheckClearSigned(text []byte, _ string) vpgp.SigVerdict {
	f.gotData = text
	return f.v
}

const pgpMIMESigned = "From: Ananya <ananya@iyer.example>\r\nContent-Type: multipart/signed; protocol=\"application/pgp-signature\"; micalg=pgp-sha256; boundary=\"s\"\r\n\r\n" +
	"--s\r\nContent-Type: text/plain\r\n\r\nSigned words.\r\n--s\r\nContent-Type: application/pgp-signature\r\n\r\n-----BEGIN PGP SIGNATURE-----\r\niQE\r\n-----END PGP SIGNATURE-----\r\n--s--\r\n"

// The seal's colour is the verdict's: the same signed message reads green only
// when the signature verified, neutral with no key to check it, and red when it
// does not hold. Its Content-Type, which says "signed" every time, decides none
// of it.
func TestTheSealTellsTheTruth(t *testing.T) {
	for _, c := range []struct {
		v    vpgp.SigVerdict
		tone string
		word string
	}{
		{vpgp.SigVerified, "ok", "key is verified"},
		{vpgp.SigUnknownKey, "", "no key on file for ananya@iyer.example"},
		{vpgp.SigBad, "danger", "does not match ananya@iyer.example's key"},
	} {
		f := &fakeSeal{v: c.v}
		s := mailSealFor(f, "me@example.com", []byte(pgpMIMESigned), "ananya@iyer.example")
		if s.Tone != c.tone || !strings.Contains(s.Text, c.word) {
			t.Errorf("verdict %d: seal %+v, want tone %q and %q", c.v, s, c.tone, c.word)
		}
	}
	// What was checked is the signed part exactly: its own headers and body,
	// without the line break before the next delimiter.
	f := &fakeSeal{v: vpgp.SigVerified}
	mailSealFor(f, "me@example.com", []byte(pgpMIMESigned), "ananya@iyer.example")
	if string(f.gotData) != "Content-Type: text/plain\r\n\r\nSigned words." {
		t.Errorf("checked %q", f.gotData)
	}
	// An encrypted message: green only when signed and verified.
	enc := []byte("From: r@x\r\nContent-Type: text/plain\r\n\r\n-----BEGIN PGP MESSAGE-----\r\nhQE\r\n-----END PGP MESSAGE-----\r\n")
	if s := mailSealFor(&fakeSeal{v: vpgp.SigNone}, "me@x", enc, "r@x"); s.Tone != "" || s.Text != "Encrypted" {
		t.Errorf("encrypted, unsigned: %+v", s)
	}
	if s := mailSealFor(&fakeSeal{v: vpgp.SigVerified}, "me@x", enc, "r@x"); s.Tone != "ok" || s.Text != "Signed and encrypted · r@x's key is verified" {
		t.Errorf("encrypted and verified: %+v", s)
	}
	// Plain mail has no seal at all.
	if s := mailSealFor(&fakeSeal{v: vpgp.SigVerified}, "me@x", []byte("From: a@x\r\n\r\nhello\r\n"), "a@x"); s != (mailSeal{}) {
		t.Errorf("plain mail: %+v", s)
	}
}

// Anyone holding one genuine signed message from a sender can wrap their own
// words round it and send the whole thing in that sender's name. The reader
// shows every word; the signature covers only its block. So the seal vouches
// for a message only when what the reader shows is the signed or encrypted
// block alone. One seed per way of adding words the signature does not cover.
func TestTheSealVouchesOnlyForWhatItChecked(t *testing.T) {
	const from = "From: Ananya <ananya@iyer.example>\r\n"
	clear := "-----BEGIN PGP SIGNED MESSAGE-----\r\nHash: SHA256\r\n\r\nLunch on Friday?\r\n-----BEGIN PGP SIGNATURE-----\r\niQE\r\n-----END PGP SIGNATURE-----"
	armour := "-----BEGIN PGP MESSAGE-----\r\nhQE\r\n-----END PGP MESSAGE-----"
	plain := from + "Content-Type: text/plain\r\n\r\n"
	alt := from + "Content-Type: multipart/alternative; boundary=\"a\"\r\n\r\n--a\r\nContent-Type: text/plain\r\n\r\n" + clear + "\r\n--a\r\nContent-Type: text/html\r\n\r\n<p>Pay invoice 4411 to account 999 today.</p>\r\n--a--\r\n"
	mixed := from + "Content-Type: multipart/mixed; boundary=\"m\"\r\n\r\n--m\r\nContent-Type: text/plain\r\n\r\n" + clear + "\r\n--m\r\nContent-Type: application/pdf; name=\"invoice.pdf\"\r\n\r\n%PDF\r\n--m--\r\n"
	third := strings.Replace(pgpMIMESigned, "--s--", "--s\r\nContent-Type: text/html\r\n\r\n<p>Pay invoice 4411 to account 999 today.</p>\r\n--s--", 1)
	for _, c := range []struct{ name, msg string }{
		{"words before a clear-signed block", plain + "Pay invoice 4411 to account 999 today.\r\n\r\n" + clear + "\r\n"},
		{"words after a clear-signed block", plain + clear + "\r\n\r\nPay invoice 4411 to account 999 today.\r\n"},
		{"words between two clear-signed blocks", plain + clear + "\r\nPay invoice 4411 to account 999 today.\r\n" + clear + "\r\n"},
		{"an HTML part beside a clear-signed block", alt},
		{"a file beside a clear-signed block", mixed},
		{"a third part beside a PGP/MIME signature", third},
		{"words before an inline encrypted block", plain + "Pay invoice 4411 to account 999 today.\r\n\r\n" + armour + "\r\n"},
	} {
		s := mailSealFor(&fakeSeal{v: vpgp.SigVerified}, "me@x", []byte(c.msg), "ananya@iyer.example")
		if s.Tone == "ok" || strings.Contains(s.Text, "verified") {
			t.Errorf("%s: the seal vouches for words its signature does not cover: %+v", c.name, s)
		}
		if !strings.Contains(s.Text, "the rest is not") {
			t.Errorf("%s: the seal does not say part of the message is unchecked: %+v", c.name, s)
		}
	}
	// A signature that fails is red however much of the message it covers.
	if s := mailSealFor(&fakeSeal{v: vpgp.SigBad}, "me@x", []byte(plain+"Pay today.\r\n"+clear+"\r\n"), "ananya@iyer.example"); s.Tone != "danger" {
		t.Errorf("a bad signature inside other words: %+v", s)
	}
	// And the block alone still reads green, inline or PGP/MIME.
	for name, msg := range map[string]string{
		"a clear-signed message":   plain + "\r\n" + clear + "\r\n\r\n",
		"an inline encrypted one":  plain + armour + "\r\n",
		"a PGP/MIME signed one":    pgpMIMESigned,
		"a PGP/MIME encrypted one": from + "Content-Type: multipart/encrypted; protocol=\"application/pgp-encrypted\"; boundary=\"e\"\r\n\r\n--e\r\nContent-Type: application/pgp-encrypted\r\n\r\nVersion: 1\r\n--e\r\nContent-Type: application/octet-stream\r\n\r\n" + armour + "\r\n--e--\r\n",
	} {
		if s := mailSealFor(&fakeSeal{v: vpgp.SigVerified}, "me@x", []byte(msg), "ananya@iyer.example"); s.Tone != "ok" {
			t.Errorf("%s, wholly signed: %+v", name, s)
		}
	}
}

// What a signature proves is the address whose key made it. The name beside
// the address is whatever the sender typed, so a green seal names the
// address: "PayPal Security <mallory@evil.example>" signing with Mallory's own
// key on file is Mallory's key verified, never PayPal's.
func TestTheSealNamesTheAddressItChecked(t *testing.T) {
	msg := strings.Replace(pgpMIMESigned, "Ananya <ananya@iyer.example>", "PayPal Security <mallory@evil.example>", 1)
	s := mailSealFor(&fakeSeal{v: vpgp.SigVerified}, "me@x", []byte(msg), "mallory@evil.example")
	if strings.Contains(s.Text, "PayPal") || !strings.Contains(s.Text, "mallory@evil.example's key is verified") {
		t.Errorf("the green seal names %q", s.Text)
	}
}

// End to end with the real engine: a PGP/MIME message signed with a mailbox's
// own key verifies, and the same message with one word changed does not.
func TestTheSealVerifiesARealPGPMIMESignature(t *testing.T) {
	cfg := vpgp.DefaultConfig()
	cfg.StorageDir = t.TempDir()
	cfg.MasterSecret = []byte("test-master-secret-do-not-use-in-prod")
	e := vpgp.NewEngine(&cfg)
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.GenerateKeypair(&vpgp.PGPUser{UserID: "snd", Name: "Snd", Email: "snd@example.com"}); err != nil {
		t.Fatal(err)
	}
	part := "Content-Type: text/plain; charset=utf-8\r\n\r\nThe numbers are final.\r\nSee you Saturday."
	sig, err := e.Sign([]byte(part), "snd")
	if err != nil {
		t.Fatal(err)
	}
	msg := func(body string) []byte {
		return []byte("From: Snd <snd@example.com>\r\nContent-Type: multipart/signed; protocol=\"application/pgp-signature\"; micalg=pgp-sha256; boundary=\"b\"\r\n\r\n" +
			"--b\r\n" + body + "\r\n--b\r\nContent-Type: application/pgp-signature\r\n\r\n" + string(bytes.ReplaceAll(sig, []byte("\n"), []byte("\r\n"))) + "\r\n--b--\r\n")
	}
	if s := mailSealFor(e, "rcv@example.com", msg(part), "snd@example.com"); s.Tone != "ok" {
		t.Errorf("a genuine signature: %+v", s)
	}
	if s := mailSealFor(e, "rcv@example.com", msg(strings.Replace(part, "final", "draft", 1)), "snd@example.com"); s.Tone != "danger" {
		t.Errorf("a signature over changed words: %+v", s)
	}
	if s := mailSealFor(e, "rcv@example.com", msg(part), "someone@else.example"); s.Tone != "" {
		t.Errorf("a sender with no key on file: %+v", s)
	}
}

// Reply all reaches the sender and everyone else on the message, each once,
// and never the mailbox replying.
func TestReplyAllReachesEveryoneButMe(t *testing.T) {
	raw := []byte("From: Priya <priya@halcyon.dev>\r\nTo: me@example.com, Mehul <mehul@shah.example>\r\nCc: Priya <priya@halcyon.dev>, ops@example.com, ME@example.com\r\n\r\nhi\r\n")
	to, cc := mailReplyAll(raw, "me@example.com")
	if to != `"Priya" <priya@halcyon.dev>, "Mehul" <mehul@shah.example>` || cc != "<ops@example.com>" {
		t.Errorf("to %q, cc %q", to, cc)
	}
}
