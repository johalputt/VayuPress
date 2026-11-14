// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	netmail "net/mail"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/i18n"
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

// sealKeys is VayuPGP's checks with the reading mailbox's keys for people
// outside the install bound in, so a sender whose key the holder added is
// checked against it.
type sealKeys struct {
	pgp   *vpgp.Engine
	known vpgp.KnownKeys
}

func (s sealKeys) DecryptAndCheck(ciphertext []byte, recipientEmail, senderEmail string) ([]byte, vpgp.SigVerdict, error) {
	return s.pgp.DecryptAndCheck(ciphertext, recipientEmail, senderEmail, s.known)
}

func (s sealKeys) CheckDetached(data, sig []byte, senderEmail string) vpgp.SigVerdict {
	return s.pgp.CheckDetached(data, sig, senderEmail, s.known)
}

func (s sealKeys) CheckClearSigned(text []byte, senderEmail string) vpgp.SigVerdict {
	return s.pgp.CheckClearSigned(text, senderEmail, s.known)
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
//
// An S/MIME signature (RFC 8551) is judged by vmail.CheckSMIME against the
// certificate authorities in roots; nil, as the reader gives, is the
// system's own, which x509 reads when no pool is named.
func mailSealFor(lang string, roots *x509.CertPool, c sealChecker, account string, stored []byte, fromAddr string) mailSeal {
	if armored, ok := armoredMessage(stored); ok {
		if c == nil {
			return mailSeal{Text: i18n.T(lang, "Encrypted")}
		}
		plain, v, err := c.DecryptAndCheck(armored, account, fromAddr)
		// PGP/MIME is shown as its plaintext alone (rebuildDecryptedMessage,
		// as the display hook does); an inline block, spliced into whatever
		// surrounds it.
		whole := isPGPMIME(stored) && rebuildDecryptedMessage(stored, plain) != nil ||
			showsOnlyBlock(stored, "-----BEGIN PGP MESSAGE-----", "-----END PGP MESSAGE-----")
		switch {
		case err != nil:
			return mailSeal{Text: i18n.T(lang, "Encrypted · this mailbox's key does not open it")}
		case v == vpgp.SigBad:
			return mailSeal{Text: i18n.T(lang, "Encrypted, but the signature does not match {address}'s key", "address", fromAddr), Tone: "danger"}
		case !whole:
			return mailSeal{Text: i18n.T(lang, "Part of this message is encrypted; the rest is not")}
		case v == vpgp.SigVerified:
			return mailSeal{Text: i18n.T(lang, "Signed and encrypted · {address}'s key is verified", "address", fromAddr), Tone: "ok"}
		case v == vpgp.SigUnknownKey:
			return mailSeal{Text: i18n.T(lang, "Encrypted and signed · no key on file for {address} to check it with", "address", fromAddr)}
		}
		return mailSeal{Text: i18n.T(lang, "Encrypted")}
	}
	v, whole := vpgp.SigNone, false
	data, sig, sigType, onlyTwo, ok := signedParts(stored)
	switch {
	case ok && (sigType == "application/pkcs7-signature" || sigType == "application/x-pkcs7-signature"):
		return smimeSeal(lang, vmail.CheckSMIME(data, sig, fromAddr, roots, time.Now()), fromAddr, onlyTwo)
	case ok && strings.Contains(string(sig), "PGP SIGNATURE"):
		v, whole = vpgp.SigUnknownKey, onlyTwo
		if c != nil {
			v = c.CheckDetached(data, sig, fromAddr)
		}
	case bytes.Contains(stored, []byte("-----BEGIN PGP SIGNED MESSAGE-----")):
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
		return mailSeal{Text: i18n.T(lang, "The signature does not match {address}'s key", "address", fromAddr), Tone: "danger"}
	case !whole:
		return mailSeal{Text: i18n.T(lang, "Part of this message is signed; the rest is not")}
	case v == vpgp.SigVerified:
		return mailSeal{Text: i18n.T(lang, "Signed · {address}'s key is verified", "address", fromAddr), Tone: "ok"}
	}
	return mailSeal{Text: i18n.T(lang, "Signed · no key on file for {address} to check it with", "address", fromAddr)}
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

// signedParts is a multipart/signed message (RFC 1847: PGP/MIME's RFC 3156
// and S/MIME's RFC 8551 alike) taken apart: the signed entity exactly as it
// was signed (its own headers and body, CRLF line ends, without the line
// break before the next delimiter), the signature with its transfer encoding
// undone, and the signature part's media type. onlyTwo is false when parts
// follow the signature, which it does not cover and the reader would show.
func signedParts(stored []byte) (data, sig []byte, sigType string, onlyTwo, ok bool) {
	msg, err := netmail.ReadMessage(bytes.NewReader(stored))
	if err != nil {
		return nil, nil, "", false, false
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/signed" || params["boundary"] == "" {
		return nil, nil, "", false, false
	}
	body, err := io.ReadAll(msg.Body)
	if err != nil {
		return nil, nil, "", false, false
	}
	crlf := bytes.ReplaceAll(bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n")), []byte("\n"), []byte("\r\n"))
	delim := []byte("--" + params["boundary"])
	start := bytes.Index(crlf, delim)
	if start < 0 {
		return nil, nil, "", false, false
	}
	rest := crlf[start+len(delim):]
	nl := bytes.Index(rest, []byte("\r\n"))
	if nl < 0 {
		return nil, nil, "", false, false
	}
	rest = rest[nl+2:]
	stop := bytes.Index(rest, append([]byte("\r\n"), delim...))
	if stop < 0 {
		return nil, nil, "", false, false
	}
	data = rest[:stop]
	mr := multipart.NewReader(bytes.NewReader(crlf), params["boundary"])
	if _, err := mr.NextPart(); err != nil {
		return nil, nil, "", false, false
	}
	p, err := mr.NextPart()
	if err != nil {
		return nil, nil, "", false, false
	}
	sigType, _, _ = mime.ParseMediaType(p.Header.Get("Content-Type"))
	var r io.Reader = p
	if strings.EqualFold(strings.TrimSpace(p.Header.Get("Content-Transfer-Encoding")), "base64") {
		r = base64.NewDecoder(base64.StdEncoding, p)
	}
	if sig, err = io.ReadAll(io.LimitReader(r, 1<<20)); err != nil || len(sig) == 0 {
		return nil, nil, "", false, false
	}
	_, err = mr.NextPart()
	return data, sig, strings.ToLower(sigType), err == io.EOF, true
}

// smimeSeal is the seal for an S/MIME signature. Green only for a signature
// that holds, by a certificate for the sender's own address that an
// authority this server trusts issued and that is valid now, over all the
// reader shows; red for one that does not hold or is another address's.
func smimeSeal(lang string, v vmail.SMIMEVerdict, fromAddr string, whole bool) mailSeal {
	switch {
	case v.Status == vmail.SMIMEBad:
		return mailSeal{Text: i18n.T(lang, "The S/MIME signature does not hold for this message"), Tone: "danger"}
	case v.Status == vmail.SMIMEOtherAddress:
		return mailSeal{Text: i18n.T(lang, "Signed with S/MIME by {signer}, not by {address}", "signer", v.Signer, "address", fromAddr), Tone: "danger"}
	case !whole:
		return mailSeal{Text: i18n.T(lang, "Part of this message is signed; the rest is not")}
	case v.Status == vmail.SMIMEVerified:
		return mailSeal{Text: i18n.T(lang, "Signed with S/MIME · {address}'s certificate from {issuer} is valid", "address", fromAddr, "issuer", v.Issuer), Tone: "ok"}
	case v.Status == vmail.SMIMEExpired:
		return mailSeal{Text: i18n.T(lang, "Signed with S/MIME · {address}'s certificate has expired", "address", fromAddr)}
	}
	return mailSeal{Text: i18n.T(lang, "Signed with S/MIME · {address}'s certificate is from {issuer}, which this server does not trust", "address", fromAddr, "issuer", v.Issuer)}
}
