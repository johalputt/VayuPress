package mail

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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

// canonicalizeBodyRelaxed implements RFC 6376 §3.4.4.
func canonicalizeBodyRelaxed(body []byte) []byte {
	text := strings.ReplaceAll(string(body), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	for i, ln := range lines {
		// Reduce WSP runs to a single space and strip trailing WSP.
		ln = reduceWSP(ln)
		lines[i] = strings.TrimRight(ln, " \t")
	}
	out := strings.Join(lines, "\r\n")
	// Ignore all empty lines at the end of the body.
	out = strings.TrimRight(out, "\r\n")
	if len(out) == 0 {
		return []byte{}
	}
	return []byte(out + "\r\n")
}

// canonicalizeHeaderRelaxed implements RFC 6376 §3.4.2 for one header.
func canonicalizeHeaderRelaxed(key, value string) string {
	k := strings.ToLower(strings.TrimSpace(key))
	// Unfold: remove CRLF, then collapse WSP runs.
	v := strings.ReplaceAll(value, "\r\n", "")
	v = strings.ReplaceAll(v, "\n", "")
	v = reduceWSP(v)
	v = strings.TrimSpace(v)
	return k + ":" + v
}

// reduceWSP collapses runs of spaces/tabs into a single space.
func reduceWSP(s string) string {
	var b strings.Builder
	prevWSP := false
	for _, r := range s {
		if r == ' ' || r == '\t' {
			if !prevWSP {
				b.WriteByte(' ')
			}
			prevWSP = true
			continue
		}
		b.WriteRune(r)
		prevWSP = false
	}
	return b.String()
}

// Sign produces a complete "DKIM-Signature: …" header (no trailing CRLF) over
// the given headers and body using relaxed/relaxed canonicalization and
// rsa-sha256, as defined by RFC 6376.
func (d *DKIM) Sign(headers []HeaderField, body []byte) (string, error) {
	if d.priv == nil {
		return "", errors.New("vayumail: DKIM key not initialised")
	}
	bodyHash := sha256.Sum256(canonicalizeBodyRelaxed(body))
	bh := base64.StdEncoding.EncodeToString(bodyHash[:])

	// Header names included in the signature, in signing order.
	var signedNames []string
	for _, h := range headers {
		signedNames = append(signedNames, h.Key)
	}

	sig := fmt.Sprintf(
		"v=1; a=rsa-sha256; c=relaxed/relaxed; d=%s; s=%s; t=%d; h=%s; bh=%s; b=",
		d.Domain, d.Selector, time.Now().UTC().Unix(), strings.Join(signedNames, ":"), bh,
	)

	// Build the data to sign: each signed header canonicalized + CRLF, then the
	// DKIM-Signature header itself canonicalized with an empty b= and NO
	// trailing CRLF.
	var sb strings.Builder
	for _, h := range headers {
		sb.WriteString(canonicalizeHeaderRelaxed(h.Key, h.Value))
		sb.WriteString("\r\n")
	}
	sb.WriteString(canonicalizeHeaderRelaxed("DKIM-Signature", sig))

	digest := sha256.Sum256([]byte(sb.String()))
	signature, err := rsa.SignPKCS1v15(rand.Reader, d.priv, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	b := base64.StdEncoding.EncodeToString(signature)
	return "DKIM-Signature: " + sig + b, nil
}
