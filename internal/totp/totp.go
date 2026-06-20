// Package totp implements TOTP (RFC 6238) and the underlying HOTP (RFC 4226)
// using only the Go standard library — no third-party dependencies, in keeping
// with VayuPress's sovereign single-binary posture.
//
// It is used for optional two-factor authentication on admin accounts. Secrets
// are base32-encoded (the format every authenticator app expects) and codes are
// compared in constant time to avoid leaking match progress via timing.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Period is the TOTP time step in seconds (the RFC-recommended default).
const Period = 30

// Digits is the number of digits in a generated code (authenticator default).
const Digits = 6

// GenerateSecret returns a new random base32 secret (no padding), suitable for
// embedding in an otpauth:// URI and for storage. 20 bytes = 160 bits, the
// RFC 4226 recommended minimum.
func GenerateSecret() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return strings.ToUpper(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf)), nil
}

// hotp computes the RFC 4226 HOTP value for the given counter and secret.
func hotp(secret string, counter uint64, digits int) (string, error) {
	key, err := decodeSecret(secret)
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)

	// Dynamic truncation (RFC 4226 §5.3).
	offset := sum[len(sum)-1] & 0x0f
	bin := (uint32(sum[offset])&0x7f)<<24 |
		(uint32(sum[offset+1])&0xff)<<16 |
		(uint32(sum[offset+2])&0xff)<<8 |
		(uint32(sum[offset+3]) & 0xff)

	mod := uint32(1)
	for i := 0; i < digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, bin%mod), nil
}

// GenerateAt returns the TOTP code for secret at time t (default digits).
func GenerateAt(secret string, t time.Time) (string, error) {
	counter := uint64(t.Unix() / Period)
	return hotp(secret, counter, Digits)
}

// Validate reports whether code is valid for secret at the current time. It
// accepts the current step plus one step either side (±30s) to tolerate clock
// skew, comparing in constant time.
func Validate(secret, code string) bool {
	return ValidateAt(secret, code, time.Now())
}

// ValidateAt is Validate with an explicit reference time (for testing).
func ValidateAt(secret, code string, t time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != Digits {
		return false
	}
	for skew := -1; skew <= 1; skew++ {
		counter := uint64((t.Unix() / Period) + int64(skew))
		want, err := hotp(secret, counter, Digits)
		if err != nil {
			return false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

// ProvisioningURI builds an otpauth://totp/ URI for enrolment in an authenticator
// app. issuer and account are URL-escaped; the label is "issuer:account".
func ProvisioningURI(secret, issuer, account string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprintf("%d", Digits))
	q.Set("period", fmt.Sprintf("%d", Period))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// decodeSecret accepts a base32 secret with or without padding and upper/lower
// case, returning the raw key bytes.
func decodeSecret(secret string) ([]byte, error) {
	s := strings.ToUpper(strings.TrimSpace(secret))
	s = strings.ReplaceAll(s, " ", "")
	s = strings.TrimRight(s, "=")
	return base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
}
