package federation

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// HTTP Signature verification for inbound ActivityPub requests.
//
// This implements the widely-deployed "draft-cavage" HTTP Signatures scheme as
// used across the Fediverse (Mastodon, et al.): the sender signs a canonical
// string built from a chosen set of (pseudo-)headers with RSA-SHA256, and the
// receiver re-derives that string and verifies it against the actor's public
// key. We additionally require a body Digest and bound the Date header to a
// clock-skew window so a captured signature cannot be replayed indefinitely.

var (
	// ErrNoSignature is returned when the request carries no Signature header.
	ErrNoSignature = errors.New("federation: missing Signature header")
	// ErrMalformedSignature is returned when the Signature header cannot be parsed.
	ErrMalformedSignature = errors.New("federation: malformed Signature header")
	// ErrSignatureInvalid is returned when the cryptographic check fails.
	ErrSignatureInvalid = errors.New("federation: signature verification failed")
	// ErrDigestMismatch is returned when the body does not match the Digest header.
	ErrDigestMismatch = errors.New("federation: body digest mismatch")
	// ErrClockSkew is returned when the Date header is outside the allowed window.
	ErrClockSkew = errors.New("federation: request date outside acceptable skew")
)

// maxClockSkew bounds how far a request's Date header may drift from local time.
const maxClockSkew = 5 * time.Minute

// parseSignatureHeader parses an HTTP Signature header value of the form
//
//	keyId="...",algorithm="rsa-sha256",headers="(request-target) host date digest",signature="base64..."
//
// into its component key/value pairs. Values may optionally be quoted.
func parseSignatureHeader(v string) (map[string]string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, ErrNoSignature
	}
	out := make(map[string]string)
	for _, part := range splitTopLevelCommas(v) {
		part = strings.TrimSpace(part)
		eq := strings.IndexByte(part, '=')
		if eq <= 0 {
			return nil, ErrMalformedSignature
		}
		key := strings.TrimSpace(part[:eq])
		val := strings.TrimSpace(part[eq+1:])
		val = strings.Trim(val, `"`)
		out[strings.ToLower(key)] = val
	}
	if out["keyid"] == "" || out["signature"] == "" {
		return nil, ErrMalformedSignature
	}
	return out, nil
}

// splitTopLevelCommas splits on commas that are not inside double quotes, so a
// base64 signature value (which never contains a comma) and quoted header lists
// are handled uniformly without a full grammar.
func splitTopLevelCommas(s string) []string {
	var parts []string
	var b strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			inQuote = !inQuote
			b.WriteByte(c)
		case ',':
			if inQuote {
				b.WriteByte(c)
			} else {
				parts = append(parts, b.String())
				b.Reset()
			}
		default:
			b.WriteByte(c)
		}
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	return parts
}

// signedHeaderList returns the ordered headers covered by the signature. Per the
// spec, an absent "headers" parameter defaults to just the Date header.
func signedHeaderList(params map[string]string) []string {
	h := strings.TrimSpace(params["headers"])
	if h == "" {
		return []string{"date"}
	}
	return strings.Fields(strings.ToLower(h))
}

// buildSigningString reconstructs the canonical signing string from the request
// and the ordered list of signed headers. The synthetic "(request-target)"
// header is the lowercased method followed by the request path.
func buildSigningString(r *http.Request, headers []string) (string, error) {
	var lines []string
	for _, h := range headers {
		switch h {
		case "(request-target)":
			lines = append(lines, fmt.Sprintf("(request-target): %s %s",
				strings.ToLower(r.Method), r.URL.RequestURI()))
		case "host":
			host := r.Host
			if host == "" {
				host = r.Header.Get("Host")
			}
			lines = append(lines, "host: "+host)
		default:
			val := r.Header.Get(h)
			if val == "" {
				return "", fmt.Errorf("%w: signed header %q absent from request", ErrMalformedSignature, h)
			}
			lines = append(lines, h+": "+val)
		}
	}
	return strings.Join(lines, "\n"), nil
}

// ParseRSAPublicKeyPEM decodes a PEM-encoded RSA public key (PKIX or PKCS1).
func ParseRSAPublicKeyPEM(pemStr string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(pemStr)))
	if block == nil {
		return nil, errors.New("federation: invalid PEM public key")
	}
	if pub, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if rsaPub, ok := pub.(*rsa.PublicKey); ok {
			return rsaPub, nil
		}
		return nil, errors.New("federation: public key is not RSA")
	}
	// Fall back to the legacy PKCS#1 encoding some peers still emit.
	return x509.ParsePKCS1PublicKey(block.Bytes)
}

// verifyDigest checks that the SHA-256 of body matches a "SHA-256=base64" Digest
// header value. Only SHA-256 is accepted.
func verifyDigest(digestHeader string, body []byte) error {
	digestHeader = strings.TrimSpace(digestHeader)
	if digestHeader == "" {
		return fmt.Errorf("%w: no Digest header", ErrDigestMismatch)
	}
	const prefix = "SHA-256="
	idx := strings.Index(digestHeader, prefix)
	if idx < 0 {
		return fmt.Errorf("%w: unsupported digest algorithm", ErrDigestMismatch)
	}
	want := strings.TrimSpace(digestHeader[idx+len(prefix):])
	sum := sha256.Sum256(body)
	got := base64.StdEncoding.EncodeToString(sum[:])
	if got != want {
		return ErrDigestMismatch
	}
	return nil
}

// checkDateSkew validates the Date header is within maxClockSkew of now.
func checkDateSkew(dateHeader string, now time.Time) error {
	dateHeader = strings.TrimSpace(dateHeader)
	if dateHeader == "" {
		return fmt.Errorf("%w: missing Date header", ErrClockSkew)
	}
	t, err := http.ParseTime(dateHeader)
	if err != nil {
		return fmt.Errorf("%w: unparseable Date header", ErrClockSkew)
	}
	diff := now.Sub(t)
	if diff < 0 {
		diff = -diff
	}
	if diff > maxClockSkew {
		return ErrClockSkew
	}
	return nil
}

// VerifyRequest verifies an inbound ActivityPub request's HTTP signature against
// the provided RSA public key. It enforces, in order: a Date header within the
// clock-skew window, a matching body Digest (when the signature covers digest),
// and a valid RSA-SHA256 signature over the canonical signing string.
//
// body must be the exact request body bytes (already read by the caller, since
// http.Request.Body is single-use).
func VerifyRequest(r *http.Request, body []byte, pub *rsa.PublicKey) error {
	if pub == nil {
		return ErrSignatureInvalid
	}
	params, err := parseSignatureHeader(r.Header.Get("Signature"))
	if err != nil {
		return err
	}
	headers := signedHeaderList(params)

	if err := checkDateSkew(r.Header.Get("Date"), time.Now()); err != nil {
		return err
	}
	// If the signature covers the body digest, the digest must match the body.
	for _, h := range headers {
		if h == "digest" {
			if err := verifyDigest(r.Header.Get("Digest"), body); err != nil {
				return err
			}
			break
		}
	}

	signingString, err := buildSigningString(r, headers)
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(params["signature"])
	if err != nil {
		return fmt.Errorf("%w: signature not valid base64", ErrMalformedSignature)
	}
	hashed := sha256.Sum256([]byte(signingString))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, hashed[:], sig); err != nil {
		return ErrSignatureInvalid
	}
	return nil
}
