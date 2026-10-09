// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"math/big"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"software.sslmate.com/src/go-pkcs12"

	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

type hexFieldCodec struct{}

func (hexFieldCodec) SealField(p string) (string, error) { return hex.EncodeToString([]byte(p)), nil }
func (hexFieldCodec) OpenField(s string) (string, error) {
	b, err := hex.DecodeString(s)
	return string(b), err
}

// smimeFixture is a certificate authority and a .p12 it issued for addr.
func smimeFixture(t *testing.T, addr string) (*x509.CertPool, []byte) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test Mail CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	ca, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(2), EmailAddresses: []string{addr}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(12 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	p12, err := pkcs12.Modern.Encode(key, cert, []*x509.Certificate{ca}, "pw")
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return pool, p12
}

// The seal reads an S/MIME signature as what was found: green only for the
// sender's own valid certificate from an authority trusted, over all that is
// shown.
func TestTheSealReadsAnSMIMESignature(t *testing.T) {
	a := appWithMailAccounts(t)
	a.vayuMail.Accounts().UseTOTPCodec(hexFieldCodec{})
	ctx := context.Background()
	roots, p12 := smimeFixture(t, "dana@example.com")
	if _, err := a.vayuMail.Accounts().ImportSMIME(ctx, "dana@example.com", p12, "pw", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.vayuMail.ComposeRich(ctx, vmail.ComposeMessage{From: "dana@example.com", To: []string{"far@far.test"}, Subject: "S", Body: "the words", Sign: true}); err != nil {
		t.Fatal(err)
	}
	sent, _ := a.vayuMail.ListFolder(vmail.ReadAsOwner("dana@example.com"), "Sent")
	raw, err := a.vayuMail.ReadFolderMessage(vmail.ReadAsOwner("dana@example.com"), "Sent", sent[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, _, ok := signedParts(raw)
	if !ok {
		t.Fatalf("not read as signed:\n%s", raw)
	}
	boundary := raw[bytes.LastIndex(raw, []byte("\r\n--"))+4 : bytes.LastIndex(raw, []byte("--\r\n"))]
	extra := bytes.Replace(raw, []byte("--"+string(boundary)+"--"), []byte("--"+string(boundary)+"\r\nContent-Type: text/plain\r\n\r\nadded later\r\n--"+string(boundary)+"--"), 1)
	for name, c := range map[string]struct {
		raw   []byte
		roots *x509.CertPool
		from  string
		tone  string
		words string
	}{
		"as sent":            {raw, roots, "dana@example.com", "ok", "dana@example.com's certificate from Test Mail CA is valid"},
		"changed on the way": {bytes.Replace(raw, []byte("the words"), []byte("new words"), 1), roots, "dana@example.com", "danger", "does not hold"},
		"in another's name":  {raw, roots, "boss@example.com", "danger", "by dana@example.com, not by boss@example.com"},
		"an untrusted CA":    {raw, x509.NewCertPool(), "dana@example.com", "", "which this server does not trust"},
		"with more after":    {extra, roots, "dana@example.com", "", "Part of this message is signed"},
	} {
		s := mailSealFor(c.roots, nil, "dana@example.com", c.raw, c.from)
		if s.Tone != c.tone || !strings.Contains(s.Text, c.words) {
			t.Errorf("%s: %+v", name, s)
		}
	}
}

func smimeUpload(t *testing.T, a *App, u *users.User, p12 []byte, password string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("email", "dana@example.com")
	_ = mw.WriteField("password", password)
	fw, _ := mw.CreateFormFile("p12", "dana.p12")
	_, _ = fw.Write(p12)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/os/vayumail/accounts/smime", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("HX-Target", "vm-mbox-settings")
	rec := httptest.NewRecorder()
	a.handleVayuOSSMIMEUpload(rec, withUser(req, u))
	return rec
}

// A certificate is uploaded and removed on the mailbox's settings page, by
// an administrator, and never for a mailbox handed to its holder.
func TestACertificateIsUploadedFromSettings(t *testing.T) {
	a := appWithMailAccounts(t)
	a.vayuMail.Accounts().UseTOTPCodec(hexFieldCodec{})
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
	_, p12 := smimeFixture(t, "dana@example.com")
	if rec := smimeUpload(t, a, &users.User{ID: "u2", Email: "dana@example.com", Role: users.RoleAuthor}, p12, "pw"); rec.Code != http.StatusForbidden {
		t.Errorf("a non-administrator: %d", rec.Code)
	}
	if rec := smimeUpload(t, a, admin, p12, "wrong"); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Not kept: that is not a PKCS#12 file this password opens") {
		t.Errorf("the wrong password: %d %s", rec.Code, rec.Body)
	}
	rec := smimeUpload(t, a, admin, p12, "pw")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Issued by Test Mail CA") {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	req := httptest.NewRequest(http.MethodPost, "/os/vayumail/accounts/smime/remove", strings.NewReader("email=dana%40example.com"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Target", "vm-mbox-settings")
	rec = httptest.NewRecorder()
	a.handleVayuOSSMIMERemove(rec, withUser(req, admin))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "S/MIME certificate</span> <span class=\"muted text-sm\">None") {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	if err := a.vayuMail.HandOver(context.Background(), "dana@example.com", "op", ""); err != nil {
		t.Fatal(err)
	}
	if rec := smimeUpload(t, a, admin, p12, "pw"); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "handed to its holder") {
		t.Errorf("a handed-over mailbox: %d %s", rec.Code, rec.Body)
	}
}
