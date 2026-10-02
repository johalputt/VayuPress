// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	netmail "net/mail"
	"strings"

	vpgp "github.com/johalputt/vayupress/internal/vayuos/pgp"
)

// The reader's security seal (Mail plan §3.3): one pill that states what was
// verified about a message, in words. Its colour is the verdict's, never the
// message's own say-so: a Content-Type of multipart/signed or a body that
// carries an armoured block says only that a signature is present.

// sealChecker is what the seal asks of VayuPGP. Every method judges against
// keys already on file and fetches nothing (vpgp.senderOnFile says why).
type sealChecker interface {
	DecryptAndCheck(ciphertext []byte, recipientEmail, senderEmail string) ([]byte, vpgp.SigVerdict, error)
	CheckDetached(data, sig []byte, senderEmail string) vpgp.SigVerdict
	CheckClearSigned(text []byte, senderEmail string) vpgp.SigVerdict
}

// mailSeal is the pill: its words, and a tone of "ok" (only a verified
// signature), "danger" (a signature that does not hold) or "" (neutral).
type mailSeal struct{ Text, Tone string }

// mailSealFor reads the message as stored for account, from fromAddr (shown
// as who). The zero seal means the message is neither signed nor encrypted.
func mailSealFor(c sealChecker, account string, stored []byte, fromAddr, who string) mailSeal {
	if who == "" {
		who = fromAddr
	}
	if armored, ok := armoredMessage(stored); ok {
		if c == nil {
			return mailSeal{Text: "Encrypted"}
		}
		_, v, err := c.DecryptAndCheck(armored, account, fromAddr)
		switch {
		case err != nil:
			return mailSeal{Text: "Encrypted · this mailbox's key does not open it"}
		case v == vpgp.SigVerified:
			return mailSeal{Text: "Signed and encrypted · " + who + "'s key is verified", Tone: "ok"}
		case v == vpgp.SigUnknownKey:
			return mailSeal{Text: "Encrypted and signed · no key on file for " + fromAddr + " to check it with"}
		case v == vpgp.SigBad:
			return mailSeal{Text: "Encrypted, but the signature does not match " + fromAddr + "'s key", Tone: "danger"}
		}
		return mailSeal{Text: "Encrypted"}
	}
	v := vpgp.SigNone
	if data, sig, ok := signedParts(stored); ok {
		v = vpgp.SigUnknownKey
		if c != nil {
			v = c.CheckDetached(data, sig, fromAddr)
		}
	} else if bytes.Contains(stored, []byte("-----BEGIN PGP SIGNED MESSAGE-----")) {
		v = vpgp.SigUnknownKey
		if c != nil {
			v = c.CheckClearSigned(clearSignedBlock(stored), fromAddr)
		}
	}
	switch v {
	case vpgp.SigVerified:
		return mailSeal{Text: "Signed · " + who + "'s key is verified", Tone: "ok"}
	case vpgp.SigUnknownKey:
		return mailSeal{Text: "Signed · no key on file for " + fromAddr + " to check it with"}
	case vpgp.SigBad:
		return mailSeal{Text: "The signature does not match " + fromAddr + "'s key", Tone: "danger"}
	}
	return mailSeal{}
}

// armoredMessage is the first armoured PGP message in a stored message, inline
// or inside PGP/MIME (RFC 3156) alike.
func armoredMessage(stored []byte) ([]byte, bool) {
	const begin, end = "-----BEGIN PGP MESSAGE-----", "-----END PGP MESSAGE-----"
	i := bytes.Index(stored, []byte(begin))
	if i < 0 {
		return nil, false
	}
	j := bytes.Index(stored[i:], []byte(end))
	if j < 0 {
		return nil, false
	}
	return stored[i : i+j+len(end)], true
}

// clearSignedBlock is the clear-signed block of a message's body, from its
// first line to the end of its signature.
func clearSignedBlock(stored []byte) []byte {
	i := bytes.Index(stored, []byte("-----BEGIN PGP SIGNED MESSAGE-----"))
	block := stored[i:]
	const end = "-----END PGP SIGNATURE-----"
	if j := bytes.Index(block, []byte(end)); j >= 0 {
		block = block[:j+len(end)]
	}
	return block
}

// signedParts is a PGP/MIME signed message (RFC 3156) taken apart: the signed
// entity exactly as it was signed (its own headers and body, CRLF line ends,
// without the line break before the next delimiter), and the signature.
func signedParts(stored []byte) (data, sig []byte, ok bool) {
	msg, err := netmail.ReadMessage(bytes.NewReader(stored))
	if err != nil {
		return nil, nil, false
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/signed" || params["boundary"] == "" {
		return nil, nil, false
	}
	body, err := io.ReadAll(msg.Body)
	if err != nil {
		return nil, nil, false
	}
	crlf := bytes.ReplaceAll(bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n")), []byte("\n"), []byte("\r\n"))
	delim := []byte("--" + params["boundary"])
	start := bytes.Index(crlf, delim)
	if start < 0 {
		return nil, nil, false
	}
	rest := crlf[start+len(delim):]
	nl := bytes.Index(rest, []byte("\r\n"))
	if nl < 0 {
		return nil, nil, false
	}
	rest = rest[nl+2:]
	stop := bytes.Index(rest, append([]byte("\r\n"), delim...))
	if stop < 0 {
		return nil, nil, false
	}
	data = rest[:stop]
	mr := multipart.NewReader(bytes.NewReader(crlf), params["boundary"])
	if _, err := mr.NextPart(); err != nil {
		return nil, nil, false
	}
	p, err := mr.NextPart()
	if err != nil {
		return nil, nil, false
	}
	if sig, err = io.ReadAll(p); err != nil || !strings.Contains(string(sig), "PGP SIGNATURE") {
		return nil, nil, false
	}
	return data, sig, true
}
