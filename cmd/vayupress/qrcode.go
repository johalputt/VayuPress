package main

// qrcode.go — small helper to render a QR code as a CSP-safe inline data: image.
//
// The admin CSP allows `img-src 'self' data:`, so a base64 PNG data URI embeds
// directly in an <img> with no external request and no extra route. Used for
// TOTP (2FA) enrolment — the otpauth:// URI as a scannable code — and for the
// mail "scan to view settings" convenience on the Connect tab.

import (
	"encoding/base64"
	"encoding/json"

	"rsc.io/qr"
)

// thunderbirdAccountQR builds the JSON payload that Thunderbird for Android /
// K-9 read from a scanned QR (Add account → Scan QR code) to auto-create an
// account, and returns it as a QR data URI. The password is intentionally
// omitted — VayuMail never stores a plaintext password — so the client prompts
// for it after importing the (IMAP + SMTP) servers.
//
// Payload shape (Thunderbird-android account-QR): a JSON array
//
//	[ version, [ account... ] ]
//	account = [ [incoming, outgoing], [ [email, displayName] ] ]
//	server  = [ protocol, host, port, security, authType, username ]
//	  protocol: "imap" | "pop3" | "smtp"
//	  security: 0=none, 1=STARTTLS, 2=SSL/TLS
//	  authType: 0=automatic, 1=password (PLAIN)
//
// It is isolated here so the exact schema is easy to adjust if Thunderbird
// changes it.
func thunderbirdAccountQR(email, host string, imapsPort, submissionPort int) string {
	if email == "" || host == "" {
		return ""
	}
	payload := []any{
		1,
		[]any{
			[]any{
				[]any{
					[]any{"imap", host, imapsPort, 2, 1, email},
					[]any{"smtp", host, submissionPort, 1, 1, email},
				},
				[]any{
					[]any{email, email},
				},
			},
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return qrDataURI(string(b))
}

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
