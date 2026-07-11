package main

// vayuos_pgp_decrypt_test.go — the transparent-decryption hook must handle
// BOTH PGP shapes on the wire. Inline PGP (VayuPress's own sends) splices
// the plaintext in place of the armored body. PGP/MIME (RFC 3156, what the
// VayuMail app and third-party clients send) must be REBUILT: splicing
// plaintext into the middle of a multipart/encrypted structure produces a
// corrupt message — an "encrypted" envelope with no ciphertext — which the
// webmail rendered as raw MIME and the app re-fetched forever.

import (
	"context"
	"io"
	"net/mail"
	"strings"
	"testing"

	vpgp "github.com/johalputt/vayupress/internal/vayuos/pgp"
)

// pgpAppForDecrypt builds an App with only a live VayuPGP engine (the hook
// needs nothing else) and a minted keypair for the recipient.
func pgpAppForDecrypt(t *testing.T, email string) *App {
	t.Helper()
	cfg := vpgp.DefaultConfig()
	cfg.Enabled = true
	cfg.StorageDir = t.TempDir()
	cfg.MasterSecret = []byte("test-master-secret")
	e := vpgp.NewEngine(&cfg)
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start pgp engine: %v", err)
	}
	t.Cleanup(func() { _ = e.Stop(context.Background()) })
	if _, err := e.EnsureKeypair(&vpgp.PGPUser{UserID: email, Name: "Test", Email: email}); err != nil {
		t.Fatalf("ensure keypair: %v", err)
	}
	return &App{vayuPGP: e}
}

const decryptTestPlain = "hello from the app composer\nsecond line"

// buildPGPMIME wraps armored ciphertext exactly the way the VayuMail app's
// composer does (RFC 3156: control part first, octet-stream ciphertext next).
func buildPGPMIME(armored string) []byte {
	return []byte("From: sender@johal.test\r\n" +
		"To: rcpt@johal.test\r\n" +
		"Subject: test 2 encryption\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/encrypted; protocol=\"application/pgp-encrypted\"; boundary=\"bnd\"\r\n" +
		"\r\n" +
		"--bnd\r\n" +
		"Content-Type: application/pgp-encrypted\r\n" +
		"\r\n" +
		"Version: 1\r\n" +
		"--bnd\r\n" +
		"Content-Type: application/octet-stream; name=\"encrypted.asc\"\r\n" +
		"\r\n" +
		armored + "\r\n" +
		"--bnd--\r\n")
}

func TestDecryptHookRebuildsPGPMIME(t *testing.T) {
	const rcpt = "rcpt@johal.test"
	a := pgpAppForDecrypt(t, rcpt)
	armored, err := a.vayuPGP.Encrypt([]byte(decryptTestPlain), rcpt)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	out := a.pgpDecryptForAccount(rcpt, buildPGPMIME(string(armored)))
	s := string(out)

	if strings.Contains(s, "BEGIN PGP MESSAGE") {
		t.Fatalf("ciphertext survived decryption:\n%s", s)
	}
	if strings.Contains(s, "multipart/encrypted") {
		t.Fatalf("multipart/encrypted framing survived the rebuild:\n%s", s)
	}
	msg, err := mail.ReadMessage(strings.NewReader(s))
	if err != nil {
		t.Fatalf("rebuilt message unparsable: %v\n%s", err, s)
	}
	bodyBytes, _ := io.ReadAll(msg.Body)
	if strings.Contains(string(bodyBytes), "Version: 1") {
		t.Fatalf("RFC 3156 control part leaked into the rebuilt body:\n%s", bodyBytes)
	}
	if msg.Header.Get("Subject") != "test 2 encryption" {
		t.Errorf("Subject lost: %q", msg.Header.Get("Subject"))
	}
	if msg.Header.Get("X-VayuPGP") != "encrypted" {
		t.Errorf("X-VayuPGP marker missing (badge would vanish)")
	}
	if !strings.Contains(s, "hello from the app composer") {
		t.Fatalf("plaintext missing from rebuilt body:\n%s", s)
	}
}

func TestDecryptHookInlineSpliceStillWorks(t *testing.T) {
	const rcpt = "rcpt@johal.test"
	a := pgpAppForDecrypt(t, rcpt)
	armored, err := a.vayuPGP.Encrypt([]byte(decryptTestPlain), rcpt)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	raw := []byte("From: a@johal.test\r\nTo: " + rcpt + "\r\nSubject: inline\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\nX-VayuPGP: encrypted\r\n\r\n" +
		string(armored))

	out := string(a.pgpDecryptForAccount(rcpt, raw))
	if strings.Contains(out, "BEGIN PGP MESSAGE") {
		t.Fatalf("inline armor survived: %s", out)
	}
	if !strings.Contains(out, "hello from the app composer") {
		t.Fatalf("plaintext missing after inline splice: %s", out)
	}
}

func TestDecryptHookLeavesUnrelatedMailAlone(t *testing.T) {
	a := pgpAppForDecrypt(t, "rcpt@johal.test")
	raw := []byte("From: a@b\r\nSubject: plain\r\n\r\njust text")
	if got := a.pgpDecryptForAccount("rcpt@johal.test", raw); string(got) != string(raw) {
		t.Fatalf("plain mail modified: %q", got)
	}
}

func TestDecryptHookWrongKeyReturnsOriginal(t *testing.T) {
	const rcpt = "rcpt@johal.test"
	a := pgpAppForDecrypt(t, rcpt)
	// Encrypt to a DIFFERENT mailbox's key: decryption for rcpt must fail
	// closed and serve the original bytes untouched.
	if _, err := a.vayuPGP.EnsureKeypair(&vpgp.PGPUser{UserID: "other@johal.test", Name: "O", Email: "other@johal.test"}); err != nil {
		t.Fatalf("ensure other keypair: %v", err)
	}
	armored, err := a.vayuPGP.Encrypt([]byte(decryptTestPlain), "other@johal.test")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	raw := buildPGPMIME(string(armored))
	if got := a.pgpDecryptForAccount(rcpt, raw); string(got) != string(raw) {
		t.Fatalf("undecryptable message was modified")
	}
}
