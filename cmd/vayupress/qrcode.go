package main

// qrcode.go — small helper to render a QR code as a CSP-safe inline data: image.
//
// The admin CSP allows `img-src 'self' data:`, so a base64 PNG data URI embeds
// directly in an <img> with no external request and no extra route. Used for
// TOTP (2FA) enrolment — the otpauth:// URI as a scannable code — and for the
// mail "scan to view settings" convenience on the Connect tab.

import (
	"encoding/base64"

	"rsc.io/qr"
)

// qrDataURI encodes text as a QR code and returns it as a data:image/png;base64
// URI suitable for an <img src>. Returns "" on any encode error so callers can
// simply omit the image. Medium error correction (qr.M) balances density and
// scan reliability for typical otpauth/settings payloads.
func qrDataURI(text string) string {
	if text == "" {
		return ""
	}
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG())
}
