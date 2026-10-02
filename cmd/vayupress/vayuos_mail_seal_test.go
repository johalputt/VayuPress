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
		s := mailSealFor(f, "me@example.com", []byte(pgpMIMESigned), "ananya@iyer.example", "Ananya")
		if s.Tone != c.tone || !strings.Contains(s.Text, c.word) {
			t.Errorf("verdict %d: seal %+v, want tone %q and %q", c.v, s, c.tone, c.word)
		}
	}
	// What was checked is the signed part exactly: its own headers and body,
	// without the line break before the next delimiter.
	f := &fakeSeal{v: vpgp.SigVerified}
	mailSealFor(f, "me@example.com", []byte(pgpMIMESigned), "ananya@iyer.example", "")
	if string(f.gotData) != "Content-Type: text/plain\r\n\r\nSigned words." {
		t.Errorf("checked %q", f.gotData)
	}
	// An encrypted message: green only when signed and verified.
	enc := []byte("From: r@x\r\nContent-Type: text/plain\r\n\r\n-----BEGIN PGP MESSAGE-----\r\nhQE\r\n-----END PGP MESSAGE-----\r\n")
	if s := mailSealFor(&fakeSeal{v: vpgp.SigNone}, "me@x", enc, "r@x", ""); s.Tone != "" || s.Text != "Encrypted" {
		t.Errorf("encrypted, unsigned: %+v", s)
	}
	if s := mailSealFor(&fakeSeal{v: vpgp.SigVerified}, "me@x", enc, "r@x", "Rohan"); s.Tone != "ok" || s.Text != "Signed and encrypted · Rohan's key is verified" {
		t.Errorf("encrypted and verified: %+v", s)
	}
	// Plain mail has no seal at all.
	if s := mailSealFor(&fakeSeal{v: vpgp.SigVerified}, "me@x", []byte("From: a@x\r\n\r\nhello\r\n"), "a@x", ""); s != (mailSeal{}) {
		t.Errorf("plain mail: %+v", s)
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
	if s := mailSealFor(e, "rcv@example.com", msg(part), "snd@example.com", "Snd"); s.Tone != "ok" {
		t.Errorf("a genuine signature: %+v", s)
	}
	if s := mailSealFor(e, "rcv@example.com", msg(strings.Replace(part, "final", "draft", 1)), "snd@example.com", "Snd"); s.Tone != "danger" {
		t.Errorf("a signature over changed words: %+v", s)
	}
	if s := mailSealFor(e, "rcv@example.com", msg(part), "someone@else.example", ""); s.Tone != "" {
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
