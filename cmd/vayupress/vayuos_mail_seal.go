// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	netmail "net/mail"
	"strings"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
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

// mailSealFor reads the message as stored for account, from fromAddr. The
// zero seal means the message is neither signed nor encrypted.
//
// A seal names the address, never the display name: a signature proves which
// key made it, and the name beside the address is whatever the sender typed.
//
// It vouches only for a message the reader shows nothing more of than the
// signed or encrypted block. Anyone holding one genuine signed message could
// otherwise wrap their own words round it and send the whole in its sender's
// name, under a green seal. A signature that fails is red however much of the
// message it covers.
func mailSealFor(c sealChecker, account string, stored []byte, fromAddr string) mailSeal {
	if armored, ok := armoredMessage(stored); ok {
		if c == nil {
			return mailSeal{Text: "Encrypted"}
		}
		plain, v, err := c.DecryptAndCheck(armored, account, fromAddr)
		// PGP/MIME is shown as its plaintext alone (rebuildDecryptedMessage,
		// as the display hook does); an inline block, spliced into whatever
		// surrounds it.
		whole := isPGPMIME(stored) && rebuildDecryptedMessage(stored, plain) != nil ||
			showsOnlyBlock(stored, "-----BEGIN PGP MESSAGE-----", "-----END PGP MESSAGE-----")
		switch {
		case err != nil:
			return mailSeal{Text: "Encrypted · this mailbox's key does not open it"}
		case v == vpgp.SigBad:
			return mailSeal{Text: "Encrypted, but the signature does not match " + fromAddr + "'s key", Tone: "danger"}
		case !whole:
			return mailSeal{Text: "Part of this message is encrypted; the rest is not"}
		case v == vpgp.SigVerified:
			return mailSeal{Text: "Signed and encrypted · " + fromAddr + "'s key is verified", Tone: "ok"}
		case v == vpgp.SigUnknownKey:
			return mailSeal{Text: "Encrypted and signed · no key on file for " + fromAddr + " to check it with"}
		}
		return mailSeal{Text: "Encrypted"}
	}
	v, whole := vpgp.SigNone, false
	if data, sig, onlyTwo, ok := signedParts(stored); ok {
		v, whole = vpgp.SigUnknownKey, onlyTwo
		if c != nil {
			v = c.CheckDetached(data, sig, fromAddr)
		}
	} else if bytes.Contains(stored, []byte("-----BEGIN PGP SIGNED MESSAGE-----")) {
		v = vpgp.SigUnknownKey
		whole = showsOnlyBlock(stored, "-----BEGIN PGP SIGNED MESSAGE-----", "-----END PGP SIGNATURE-----")
		if c != nil {
			v = c.CheckClearSigned(clearSignedBlock(stored), fromAddr)
		}
	}
	switch {
	case v == vpgp.SigNone:
		return mailSeal{}
	case v == vpgp.SigBad:
		return mailSeal{Text: "The signature does not match " + fromAddr + "'s key", Tone: "danger"}
	case !whole:
		return mailSeal{Text: "Part of this message is signed; the rest is not"}
	case v == vpgp.SigVerified:
		return mailSeal{Text: "Signed · " + fromAddr + "'s key is verified", Tone: "ok"}
	}
	return mailSeal{Text: "Signed · no key on file for " + fromAddr + " to check it with"}
}

// showsOnlyBlock reports whether the reader shows nothing of stored but one
// armoured block, from begin to end: no other words in its text, no HTML
// part beside it, no file.
func showsOnlyBlock(stored []byte, begin, end string) bool {
	pm := vmail.ParseMessage(stored)
	text := strings.TrimSpace(pm.Text)
	return strings.TrimSpace(pm.HTML) == "" && len(pm.Attachments) == 0 &&
		strings.HasPrefix(text, begin) && strings.HasSuffix(text, end) && strings.Count(text, begin) == 1
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
// onlyTwo is false when parts follow the signature, which it does not cover
// and the reader would show.
func signedParts(stored []byte) (data, sig []byte, onlyTwo, ok bool) {
	msg, err := netmail.ReadMessage(bytes.NewReader(stored))
	if err != nil {
		return nil, nil, false, false
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/signed" || params["boundary"] == "" {
		return nil, nil, false, false
	}
	body, err := io.ReadAll(msg.Body)
	if err != nil {
		return nil, nil, false, false
	}
	crlf := bytes.ReplaceAll(bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n")), []byte("\n"), []byte("\r\n"))
	delim := []byte("--" + params["boundary"])
	start := bytes.Index(crlf, delim)
	if start < 0 {
		return nil, nil, false, false
	}
	rest := crlf[start+len(delim):]
	nl := bytes.Index(rest, []byte("\r\n"))
	if nl < 0 {
		return nil, nil, false, false
	}
	rest = rest[nl+2:]
	stop := bytes.Index(rest, append([]byte("\r\n"), delim...))
	if stop < 0 {
		return nil, nil, false, false
	}
	data = rest[:stop]
	mr := multipart.NewReader(bytes.NewReader(crlf), params["boundary"])
	if _, err := mr.NextPart(); err != nil {
		return nil, nil, false, false
	}
	p, err := mr.NextPart()
	if err != nil {
		return nil, nil, false, false
	}
	if sig, err = io.ReadAll(p); err != nil || !strings.Contains(string(sig), "PGP SIGNATURE") {
		return nil, nil, false, false
	}
	_, err = mr.NextPart()
	return data, sig, err == io.EOF, true
}
