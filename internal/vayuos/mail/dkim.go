package mail

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"

	"github.com/emersion/go-msgauth/dkim"
)

// HeaderField is one RFC 5322 header used as DKIM signing input.
type HeaderField struct {
	Key   string
	Value string
}

// DKIM signs messages per RFC 6376 (relaxed/relaxed canonicalization,
// rsa-sha256) and publishes the corresponding DNS public key record.
type DKIM struct {
	priv     *rsa.PrivateKey
	Selector string
	Domain   string
}

// LoadOrCreateDKIM loads an RSA-2048 DKIM key from dir, generating and
// persisting one (0600) on first use.
func LoadOrCreateDKIM(dir, selector, domain string) (*DKIM, error) {
	if selector == "" {
		selector = "vayu"
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(dir, "dkim_"+selector+".pem")
	if data, err := os.ReadFile(keyPath); err == nil {
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, errors.New("vayumail: invalid DKIM PEM")
		}
		priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		return &DKIM{priv: priv, Selector: selector, Domain: domain}, nil
	}
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	if err := os.WriteFile(keyPath, pemBytes, 0o600); err != nil {
		return nil, err
	}
	return &DKIM{priv: priv, Selector: selector, Domain: domain}, nil
}

// PublicTXT returns the value of the DKIM DNS TXT record to publish at
// <selector>._domainkey.<domain>.
func (d *DKIM) PublicTXT() string {
	der, _ := x509.MarshalPKIXPublicKey(&d.priv.PublicKey)
	return "v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString(der)
}

// RecordName returns the DNS name for the DKIM TXT record.
func (d *DKIM) RecordName() string {
	return d.Selector + "._domainkey." + d.Domain
}

// SignMessage DKIM-signs a complete RFC 5322 message (header block, a blank
// line, then the body, all CRLF-terminated) and returns the message with a
// freshly prepended "DKIM-Signature:" header.
//
// Signing is delegated to the battle-tested github.com/emersion/go-msgauth/dkim
// implementation (relaxed/relaxed canonicalization, rsa-sha256) rather than a
// bespoke canonicalizer: a subtle canonicalization bug is one of the most
// common reasons a message that "looks" signed still fails verification at
// Gmail/Outlook and is filed as spam. Using the vetted library removes that
// entire class of risk. All present headers are signed (HeaderKeys nil).
func (d *DKIM) SignMessage(raw []byte) ([]byte, error) {
	if d.priv == nil {
		return nil, errors.New("vayumail: DKIM key not initialised")
	}
	opts := &dkim.SignOptions{
		Domain:                 d.Domain,
		Selector:               d.Selector,
		Signer:                 d.priv,
		Hash:                   crypto.SHA256,
		HeaderCanonicalization: dkim.CanonicalizationRelaxed,
		BodyCanonicalization:   dkim.CanonicalizationRelaxed,
	}
	var out bytes.Buffer
	if err := dkim.Sign(&out, bytes.NewReader(raw), opts); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
