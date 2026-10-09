// SPDX-License-Identifier: Apache-2.0

package mail

// smime.go — S/MIME (RFC 8551) signatures: a mailbox signs its mail with a
// certificate a certificate authority issued it, and the reader checks the
// signatures on mail that comes in. OpenPGP (VayuPGP) is the install's own
// way to sign and encrypt; S/MIME is for correspondents whose organisations
// issue certificates instead, which is why only signing is here: a person
// who needs S/MIME encryption needs it with someone who already reads
// signed mail.
//
// A mailbox's certificate arrives as the PKCS#12 file (.p12, .pfx) its
// authority or browser exports. Its private key is kept sealed under the
// install's at-rest key (the store's SecretCodec, as TOTP seeds are), and is
// refused outright where no such key is installed: a signing key is never
// written to the database in the clear.

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/asn1"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/digitorus/pkcs7"
	"software.sslmate.com/src/go-pkcs12"
)

// ErrNoSMIMECert refuses a message asked to be signed with S/MIME whose
// sender keeps no certificate: such a message is never sent unsigned.
var ErrNoSMIMECert = errors.New("this mailbox has no S/MIME certificate to sign with")

// SMIMECert is what the console shows of a mailbox's certificate.
type SMIMECert struct {
	Email    string
	Issuer   string
	NotAfter time.Time
}

// maxSMIMEFile bounds an uploaded PKCS#12 file. A certificate, its chain and
// its key are a few kilobytes.
const maxSMIMEFile = 64 << 10

// oidEmailAddress is the emailAddress attribute some authorities put in a
// certificate's subject instead of, or as well as, its alternative names.
var oidEmailAddress = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 1}

// certEmails is every address a certificate names.
func certEmails(c *x509.Certificate) []string {
	out := append([]string(nil), c.EmailAddresses...)
	for _, n := range c.Subject.Names {
		if v, ok := n.Value.(string); ok && n.Type.Equal(oidEmailAddress) {
			out = append(out, v)
		}
	}
	return out
}

func certNames(c *x509.Certificate, email string) bool {
	for _, e := range certEmails(c) {
		if strings.EqualFold(strings.TrimSpace(e), email) {
			return true
		}
	}
	return false
}

func issuerName(c *x509.Certificate) string {
	if c.Issuer.CommonName != "" {
		return c.Issuer.CommonName
	}
	if len(c.Issuer.Organization) > 0 {
		return c.Issuer.Organization[0]
	}
	return c.Issuer.String()
}

// usableForMail reports whether a certificate's own key usages allow it to
// sign mail: no extended usage at all, or email protection.
func usableForMail(c *x509.Certificate) bool {
	if c.KeyUsage != 0 && c.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return false
	}
	if len(c.ExtKeyUsage) == 0 {
		return true
	}
	for _, u := range c.ExtKeyUsage {
		if u == x509.ExtKeyUsageEmailProtection || u == x509.ExtKeyUsageAny {
			return true
		}
	}
	return false
}

// keyMatches reports whether key is the private half of the certificate's
// public key, in a kind S/MIME signs with.
func keyMatches(key any, c *x509.Certificate) bool {
	switch k := key.(type) {
	case *rsa.PrivateKey:
		return k.PublicKey.Equal(c.PublicKey)
	case *ecdsa.PrivateKey:
		return k.PublicKey.Equal(c.PublicKey)
	}
	return false
}

// ImportSMIME keeps the certificate and key in a PKCS#12 file as mailbox's,
// replacing any it kept. The certificate must name the mailbox, be valid
// now, allow signing mail, and come with its key.
func (s *AccountStore) ImportSMIME(ctx context.Context, mailbox string, p12 []byte, password string, now time.Time) (SMIMECert, error) {
	mailbox = normEmail(mailbox)
	switch {
	case s.db == nil:
		return SMIMECert{}, errors.New("vayumail: no storage")
	case s.totpCodec == nil:
		return SMIMECert{}, errors.New("this server has no key to seal a private key with, so it keeps none")
	case len(p12) > maxSMIMEFile:
		return SMIMECert{}, errors.New("that file is too large to be a certificate")
	}
	key, leaf, chain, err := pkcs12.DecodeChain(p12, password)
	switch {
	case err != nil:
		return SMIMECert{}, errors.New("that is not a PKCS#12 file this password opens")
	case !certNames(leaf, mailbox):
		return SMIMECert{}, errors.New("the certificate is not for " + mailbox)
	case now.Before(leaf.NotBefore) || now.After(leaf.NotAfter):
		return SMIMECert{}, errors.New("the certificate is not valid now")
	case !usableForMail(leaf):
		return SMIMECert{}, errors.New("the certificate is not one for signing mail")
	case !keyMatches(key, leaf):
		return SMIMECert{}, errors.New("the file's key is not the certificate's, or is of a kind S/MIME does not sign with")
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return SMIMECert{}, err
	}
	sealed, err := s.totpCodec.SealField(base64.StdEncoding.EncodeToString(der))
	if err != nil {
		return SMIMECert{}, err
	}
	var certs bytes.Buffer
	for _, c := range append([]*x509.Certificate{leaf}, chain...) {
		certs.Write(c.Raw)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO vayumail_smime(mailbox,certs,key) VALUES(?,?,?)
		ON CONFLICT(mailbox) DO UPDATE SET certs=excluded.certs, key=excluded.key`, mailbox, certs.Bytes(), sealed); err != nil {
		return SMIMECert{}, err
	}
	return SMIMECert{Email: mailbox, Issuer: issuerName(leaf), NotAfter: leaf.NotAfter}, nil
}

// smimeOf is mailbox's certificate, its chain, and the sealed key.
func (s *AccountStore) smimeOf(ctx context.Context, mailbox string) (*x509.Certificate, []*x509.Certificate, string, error) {
	var der []byte
	var sealed string
	if err := s.db.QueryRowContext(ctx, `SELECT certs,key FROM vayumail_smime WHERE mailbox=?`, normEmail(mailbox)).Scan(&der, &sealed); err != nil {
		return nil, nil, "", err
	}
	certs, err := x509.ParseCertificates(der)
	if err != nil || len(certs) == 0 {
		return nil, nil, "", errors.New("the stored certificate does not read")
	}
	return certs[0], certs[1:], sealed, nil
}

// SMIMECertFor is mailbox's certificate, if it keeps one.
func (s *AccountStore) SMIMECertFor(ctx context.Context, mailbox string) (SMIMECert, bool) {
	if s.db == nil {
		return SMIMECert{}, false
	}
	leaf, _, _, err := s.smimeOf(ctx, mailbox)
	if err != nil {
		return SMIMECert{}, false
	}
	return SMIMECert{Email: normEmail(mailbox), Issuer: issuerName(leaf), NotAfter: leaf.NotAfter}, true
}

// keepsSMIME reports whether mailbox keeps a certificate to sign with.
func (s *AccountStore) keepsSMIME(ctx context.Context, mailbox string) bool {
	_, ok := s.SMIMECertFor(ctx, mailbox)
	return ok
}

// DeleteSMIME forgets mailbox's certificate and key.
func (s *AccountStore) DeleteSMIME(ctx context.Context, mailbox string) error {
	if s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM vayumail_smime WHERE mailbox=?`, normEmail(mailbox))
	return err
}

// signSMIME is a detached S/MIME signature by mailbox over data, carrying
// the certificate and its chain so a reader can check it.
func (s *AccountStore) signSMIME(ctx context.Context, mailbox string, data []byte) ([]byte, error) {
	if s.db == nil || s.totpCodec == nil {
		return nil, ErrNoSMIMECert
	}
	leaf, chain, sealed, err := s.smimeOf(ctx, mailbox)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoSMIMECert
	} else if err != nil {
		return nil, err
	}
	b64, err := s.totpCodec.OpenField(sealed)
	if err != nil {
		return nil, errors.New("the S/MIME key cannot be opened on this server")
	}
	der, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, errors.New("the S/MIME key cannot be opened on this server")
	}
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	sd, err := pkcs7.NewSignedData(data)
	if err != nil {
		return nil, err
	}
	sd.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
	if err := sd.AddSigner(leaf, key, pkcs7.SignerInfoConfig{}); err != nil {
		return nil, err
	}
	for _, c := range chain {
		sd.AddCertificate(c)
	}
	sd.Detach()
	return sd.Finish()
}

// SMIMEStatus is what a check of an S/MIME signature found.
type SMIMEStatus int

const (
	// SMIMEBad: the signature does not hold over the message.
	SMIMEBad SMIMEStatus = iota
	// SMIMEOtherAddress: it holds, made with a certificate for another address.
	SMIMEOtherAddress
	// SMIMEUntrusted: it holds, by the sender's certificate, from an
	// authority this server does not trust.
	SMIMEUntrusted
	// SMIMEExpired: it holds, by the sender's certificate, no longer valid.
	SMIMEExpired
	// SMIMEVerified: it holds, by the sender's certificate, which a trusted
	// authority issued for mail and which is valid now.
	SMIMEVerified
)

// SMIMEVerdict is a check's finding, with the signing certificate's first
// address and its issuer.
type SMIMEVerdict struct {
	Status SMIMEStatus
	Signer string
	Issuer string
}

// CheckSMIME checks a detached S/MIME signature (DER) over data, the signed
// entity exactly as it was signed, from fromAddr, against the authorities in
// roots, at now. The time a signature claims to have been made is the
// signer's to write, so the certificate is judged valid or not at now.
func CheckSMIME(data, sig []byte, fromAddr string, roots *x509.CertPool, now time.Time) SMIMEVerdict {
	p7, err := pkcs7.Parse(sig)
	if err != nil {
		return SMIMEVerdict{Status: SMIMEBad}
	}
	signer := p7.GetOnlySigner()
	if signer == nil {
		return SMIMEVerdict{Status: SMIMEBad}
	}
	p7.Content = data
	if err := p7.Verify(); err != nil {
		return SMIMEVerdict{Status: SMIMEBad}
	}
	v := SMIMEVerdict{Issuer: issuerName(signer)}
	if e := certEmails(signer); len(e) > 0 {
		v.Signer = normEmail(e[0])
	}
	if !certNames(signer, fromAddr) {
		v.Status = SMIMEOtherAddress
		return v
	}
	inter := x509.NewCertPool()
	for _, c := range p7.Certificates {
		inter.AddCert(c)
	}
	_, err = signer.Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection}, CurrentTime: now})
	switch {
	case err == nil:
		v.Status = SMIMEVerified
	case now.After(signer.NotAfter) || now.Before(signer.NotBefore):
		v.Status = SMIMEExpired
	default:
		v.Status = SMIMEUntrusted
	}
	return v
}
