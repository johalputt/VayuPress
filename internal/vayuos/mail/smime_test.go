// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"math/big"
	"mime"
	"net/mail"
	"strings"
	"testing"
	"time"

	"software.sslmate.com/src/go-pkcs12"
)

// hexCodec seals a field by spelling it in hex, so a test can tell sealed
// from clear without the install's key.
type hexCodec struct{}

func (hexCodec) SealField(p string) (string, error) {
	return "hex:" + hex.EncodeToString([]byte(p)), nil
}
func (hexCodec) OpenField(s string) (string, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(s, "hex:"))
	return string(b), err
}

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestCA(t *testing.T) testCA {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test Mail CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return testCA{cert, key}
}

func (ca testCA) pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.cert)
	return p
}

// issue is a certificate from ca; edit adjusts the template before signing.
func (ca testCA) issue(t *testing.T, email string, edit func(*x509.Certificate)) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: email},
		EmailAddresses: []string{email}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(12 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection}}
	if edit != nil {
		edit(tpl)
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return cert, key
}

func (ca testCA) p12(t *testing.T, cert *x509.Certificate, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	b, err := pkcs12.Modern.Encode(key, cert, []*x509.Certificate{ca.cert}, "pw")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A certificate is kept with its key sealed, and shown by its issuer and
// expiry.
func TestACertificateIsKeptWithItsKeySealed(t *testing.T) {
	s, ctx := newContactStore(t), context.Background()
	s.UseTOTPCodec(hexCodec{})
	ca := newTestCA(t)
	cert, key := ca.issue(t, groupOwner, nil)
	got, err := s.ImportSMIME(ctx, strings.ToUpper(groupOwner), ca.p12(t, cert, key), "pw", time.Now())
	if err != nil || got.Issuer != "Test Mail CA" || !got.NotAfter.Equal(cert.NotAfter) {
		t.Fatalf("%+v, %v", got, err)
	}
	if shown, ok := s.SMIMECertFor(ctx, groupOwner); !ok || shown != got {
		t.Fatalf("shown %+v, %v", shown, ok)
	}
	var stored string
	_ = s.db.QueryRow(`SELECT key FROM vayumail_smime WHERE mailbox=?`, groupOwner).Scan(&stored)
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	if !strings.HasPrefix(stored, "hex:") || strings.Contains(stored, base64.StdEncoding.EncodeToString(der)) {
		t.Fatalf("the key is not sealed: %q", stored[:20])
	}
	if err := s.DeleteSMIME(ctx, groupOwner); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.SMIMECertFor(ctx, groupOwner); ok {
		t.Fatal("a removed certificate is still shown")
	}
}

// What is refused, one rule a seed, by the reason the console shows.
func TestWhatACertificateMustBe(t *testing.T) {
	ctx, ca := context.Background(), newTestCA(t)
	good, goodKey := ca.issue(t, groupOwner, nil)
	other, _ := ca.issue(t, "someone@else.test", nil)
	early, earlyKey := ca.issue(t, groupOwner, func(c *x509.Certificate) { c.NotBefore = time.Now().Add(time.Hour) })
	late, lateKey := ca.issue(t, groupOwner, func(c *x509.Certificate) { c.NotAfter = time.Now().Add(-time.Minute) })
	web, webKey := ca.issue(t, groupOwner, func(c *x509.Certificate) { c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth} })
	enc, encKey := ca.issue(t, groupOwner, func(c *x509.Certificate) { c.KeyUsage = x509.KeyUsageKeyEncipherment })
	_, strayKey := ca.issue(t, groupOwner, nil)
	mismatched, err := pkcs12.Modern.Encode(strayKey, good, nil, "pw")
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		file     []byte
		password string
		noCodec  bool
		want     string
	}{
		"no key to seal with":   {ca.p12(t, good, goodKey), "pw", true, "no key to seal"},
		"too large":             {make([]byte, maxSMIMEFile+1), "pw", false, "too large"},
		"the wrong password":    {ca.p12(t, good, goodKey), "nope", false, "this password opens"},
		"another address":       {ca.p12(t, other, goodKey), "pw", false, "not for " + groupOwner},
		"not valid yet":         {ca.p12(t, early, earlyKey), "pw", false, "not valid now"},
		"expired":               {ca.p12(t, late, lateKey), "pw", false, "not valid now"},
		"for web servers":       {ca.p12(t, web, webKey), "pw", false, "not one for signing mail"},
		"for encryption only":   {ca.p12(t, enc, encKey), "pw", false, "not one for signing mail"},
		"another certificate's": {mismatched, "pw", false, "key is not the certificate's"},
	} {
		s := newContactStore(t)
		if !c.noCodec {
			s.UseTOTPCodec(hexCodec{})
		}
		if _, err := s.ImportSMIME(ctx, groupOwner, c.file, c.password, time.Now()); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// signedEntity takes a multipart/signed message apart: the entity as signed
// and the signature, base64 undone.
func signedEntity(t *testing.T, raw []byte) ([]byte, []byte) {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	media, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if media != "multipart/signed" || params["protocol"] != "application/pkcs7-signature" || params["micalg"] != "sha-256" {
		t.Fatalf("not an S/MIME signed message: %s", msg.Header.Get("Content-Type"))
	}
	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(msg.Body)
	delim := "--" + params["boundary"] + "\r\n"
	parts := strings.Split(body.String(), delim)
	data := strings.TrimSuffix(parts[1], "\r\n")
	sigPart := parts[2][strings.Index(parts[2], "\r\n\r\n")+4:]
	sigPart = sigPart[:strings.Index(sigPart, "--"+params["boundary"]+"--")]
	sig, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(sigPart, "\r\n", ""))
	if err != nil {
		t.Fatal(err)
	}
	return []byte(data), sig
}

// A mailbox that keeps a certificate signs with it when asked to sign, and
// the check reads that signature as verified by the authority that issued
// it, and as nothing better under any other condition.
func TestMailIsSignedWithTheCertificateAndChecked(t *testing.T) {
	e := newLoopbackEngine(t, loopbackBridge{localSet: map[string]bool{"alice@example.com": true}})
	e.accounts.UseTOTPCodec(hexCodec{})
	ctx, ca := context.Background(), newTestCA(t)
	cert, key := ca.issue(t, "alice@example.com", nil)
	if _, err := e.accounts.ImportSMIME(ctx, "alice@example.com", ca.p12(t, cert, key), "pw", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ComposeRich(ctx, ComposeMessage{From: "alice@example.com", To: []string{"far@far.test"}, Subject: "Signed", Body: "Grüße, signed.", Sign: true}); err != nil {
		t.Fatal(err)
	}
	sent, _ := e.ListFolder(ReadAsOwner("alice"), "Sent")
	raw, _ := e.ReadFolderMessage(ReadAsOwner("alice"), "Sent", sent[0].ID)
	data, sig := signedEntity(t, raw)
	now := time.Now()
	for name, c := range map[string]struct {
		data  []byte
		from  string
		roots *x509.CertPool
		at    time.Time
		want  SMIMEStatus
	}{
		"as sent":            {data, "alice@example.com", ca.pool(), now, SMIMEVerified},
		"changed on the way": {bytes.Replace(data, []byte("signed"), []byte("forged"), 1), "alice@example.com", ca.pool(), now, SMIMEBad},
		"from someone else":  {data, "mallory@example.com", ca.pool(), now, SMIMEOtherAddress},
		"an unknown issuer":  {data, "alice@example.com", x509.NewCertPool(), now, SMIMEUntrusted},
		"after it ran out":   {data, "alice@example.com", ca.pool(), cert.NotAfter.Add(time.Hour), SMIMEExpired},
	} {
		v := CheckSMIME(c.data, sig, c.from, c.roots, c.at)
		if v.Status != c.want {
			t.Errorf("%s: %+v", name, v)
		}
		if c.want != SMIMEBad && (v.Signer != "alice@example.com" || v.Issuer != "Test Mail CA") {
			t.Errorf("%s: names %+v", name, v)
		}
	}
	if v := CheckSMIME(data, []byte("not a signature"), "alice@example.com", ca.pool(), now); v.Status != SMIMEBad {
		t.Errorf("garbage read as %+v", v)
	}
}

// Deleting the mailbox deletes its certificate and key.
func TestACertificateGoesWithItsMailbox(t *testing.T) {
	s, ctx := newContactStore(t), context.Background()
	s.UseTOTPCodec(hexCodec{})
	ca := newTestCA(t)
	cert, key := ca.issue(t, groupOwner, nil)
	if err := s.Create(ctx, groupOwner, "x", "A", RoleMailbox); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportSMIME(ctx, groupOwner, ca.p12(t, cert, key), "pw", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, groupOwner); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.SMIMECertFor(ctx, groupOwner); ok {
		t.Fatal("the certificate outlived its mailbox")
	}
}
