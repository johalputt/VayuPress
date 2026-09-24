// SPDX-License-Identifier: Apache-2.0

package main

// qrcode.go — small helper to render a QR code as a CSP-safe inline data: image.
//
// The admin CSP allows `img-src 'self' data:`, so a base64 PNG data URI embeds
// directly in an <img> with no external request and no extra route. Used for
// TOTP (2FA) enrolment — the otpauth:// URI as a scannable code — and for the
// mail "scan to view settings" convenience on the Connect tab. VayuTalk's share
// panel serves the PNG itself (/os/talk/qr), because its identity can change
// without a page load.

import (
	"encoding/base64"

	"rsc.io/qr"
)

// qrDataURI encodes text as a QR code and returns it as a data:image/png;base64
// URI suitable for an <img src>. Returns "" on any encode error so callers can
// simply omit the image. Medium error correction (qr.M) balances density and
// scan reliability for typical otpauth/settings payloads.
func qrDataURI(text string) string {
	png := qrPNG(text)
	if png == nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
}

// qrPNG encodes text as a QR code PNG, or returns nil for empty text or an
// encode error.
func qrPNG(text string) []byte {
	if text == "" {
		return nil
	}
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return nil
	}
	return code.PNG()
}
