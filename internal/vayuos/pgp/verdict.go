// SPDX-License-Identifier: Apache-2.0

package pgp

import (
	"bytes"
	"errors"
	"io"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
)

// SigVerdict is what a message's OpenPGP signature says about its sender, for
// the Mail reader's seal. Only SigVerified may be shown as good.
type SigVerdict int

const (
	SigNone       SigVerdict = iota // not signed
	SigVerified                     // signed with the claimed sender's key on file
	SigUnknownKey                   // signed, but no key on file for the sender to check it with
	SigBad                          // signed, and it does not hold for the sender's key on file
)

// senderOnFile is the claimed sender's public key as this install already
// holds it. It never fetches: recipientEntity falls back to WKD, and a key
// fetched because a message was opened tells the sender's domain it was read.
func (e *Engine) senderOnFile(email string) *openpgp.Entity {
	if email == "" {
		return nil
	}
	pk, err := e.GetPublicKey(email)
	if err != nil {
		return nil
	}
	ent, err := entityFromArmor(pk.Armor)
	if err != nil {
		return nil
	}
	return ent
}

// judge turns what OpenPGP reported into a verdict. signer is the key the
// signature was made with, when it is one the ring holds.
func judge(signed bool, sender, signer *openpgp.Entity, sigErr error) SigVerdict {
	switch {
	case !signed:
		return SigNone
	case sender == nil:
		return SigUnknownKey
	case sigErr != nil || signer == nil:
		// Made with some other key than the sender's on file, or broken.
		return SigBad
	case bytes.Equal(signer.PrimaryKey.Fingerprint, sender.PrimaryKey.Fingerprint):
		return SigVerified
	default:
		return SigBad
	}
}

// DecryptAndCheck decrypts an armored message for recipientEmail and judges
// its signature, if it has one, against senderEmail's key on file. Unlike
// DecryptAndVerifyForEmail it says whether the message was signed at all, so
// "not signed" and "signed by a key we cannot check" read differently, and it
// never fetches a key.
func (e *Engine) DecryptAndCheck(ciphertext []byte, recipientEmail, senderEmail string) ([]byte, SigVerdict, error) {
	if e.ks == nil {
		return nil, SigNone, errors.New("vayupgp: engine not started")
	}
	userID, ok := e.ks.userIDForEmail(recipientEmail)
	if !ok {
		return nil, SigNone, ErrNotFound
	}
	ring, err := e.decryptionRing(userID)
	if err != nil {
		return nil, SigNone, err
	}
	sender := e.senderOnFile(senderEmail)
	if sender != nil {
		ring = append(ring, sender)
	}
	block, err := armor.Decode(bytes.NewReader(ciphertext))
	if err != nil {
		return nil, SigNone, err
	}
	md, err := openpgp.ReadMessage(block.Body, ring, nil, e.packetConfig())
	if err != nil {
		return nil, SigNone, err
	}
	// The signature is checked only once the whole body has been read.
	body, err := io.ReadAll(md.UnverifiedBody)
	if err != nil {
		return nil, SigNone, err
	}
	var signer *openpgp.Entity
	if md.SignedBy != nil {
		signer = md.SignedBy.Entity
	}
	return body, judge(md.IsSigned, sender, signer, md.SignatureError), nil
}

// CheckDetached judges an armored detached signature over data (a PGP/MIME
// signed part, RFC 3156) against senderEmail's key on file.
func (e *Engine) CheckDetached(data, sig []byte, senderEmail string) SigVerdict {
	sender := e.senderOnFile(senderEmail)
	if sender == nil {
		return SigUnknownKey
	}
	signer, err := openpgp.CheckArmoredDetachedSignature(openpgp.EntityList{sender}, bytes.NewReader(data), bytes.NewReader(sig), e.packetConfig())
	return judge(true, sender, signer, err)
}

// CheckClearSigned judges an inline clear-signed message against senderEmail's
// key on file. A text with no clear-signed block is SigNone.
func (e *Engine) CheckClearSigned(text []byte, senderEmail string) SigVerdict {
	b, _ := clearsign.Decode(text)
	if b == nil {
		return SigNone
	}
	sender := e.senderOnFile(senderEmail)
	if sender == nil {
		return SigUnknownKey
	}
	signer, err := b.VerifySignature(openpgp.EntityList{sender}, e.packetConfig())
	return judge(true, sender, signer, err)
}
