// SPDX-License-Identifier: Apache-2.0

package mail

// drivekeys.go — the key each Drive file is sealed with (driveblob.go), as
// it is kept in the file's row: wrapped by the install's Drive key, or in a
// private mailbox sealed to its holder's OpenPGP key.
//
//	i1:<base64 nonce|sealed key>  AES-256-GCM under the Drive key, the
//	                              mailbox as additional data, so a row
//	                              moved to another mailbox does not open
//	p1:<base64 OpenPGP message>   sealed to the holder; opened only while
//	                              the holder's key is held open
//
// A private mailbox's file is sealed to the holder alone: the server keeps
// no key that opens it. Making a mailbox private seals each file's key to
// the holder (32 bytes each), not the file again.

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
)

// ErrDriveNoKey is Drive asked to keep a file on an install with no Drive
// key: it keeps none rather than keep one in the clear.
var ErrDriveNoKey = errors.New("vayumail: files are kept sealed, and this install has no key to seal them with")

// UseDriveKey gives the engine the install's Drive key (from its secrets
// store), which wraps each file's own key.
func (e *Engine) UseDriveKey(key []byte) { e.driveKey = append([]byte(nil), key...) }

// newFileKey is a file's own key, and that key as its row keeps it.
func (e *Engine) newFileKey(mbox string, private bool) (key []byte, wrapped string, err error) {
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, "", err
	}
	wrapped, err = e.wrapFileKey(mbox, key, private)
	return key, wrapped, err
}

func (e *Engine) wrapFileKey(mbox string, key []byte, private bool) (string, error) {
	if private {
		if e.maildir == nil || e.maildir.keys == nil {
			return "", ErrDriveLocked
		}
		sealed, err := e.maildir.keys.SealTo(mbox, key)
		if err != nil {
			return "", err
		}
		return "p1:" + base64.StdEncoding.EncodeToString(sealed), nil
	}
	aead, err := blobAEAD(e.driveKey)
	if err != nil {
		return "", ErrDriveNoKey
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return "i1:" + base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, key, keyAD(mbox))), nil
}

// openFileKey is the key a row keeps, opened: ErrDriveLocked for a private
// file whose holder's key is not held open.
func (e *Engine) openFileKey(mbox, wrapped string) ([]byte, error) {
	kind, body, ok := strings.Cut(wrapped, ":")
	raw, err := base64.StdEncoding.DecodeString(body)
	if !ok || err != nil {
		return nil, errBlobDamaged
	}
	switch kind {
	case "p1":
		if e.maildir == nil || e.maildir.keys == nil {
			return nil, ErrDriveLocked
		}
		key, err := e.maildir.keys.DecryptForEmail(raw, mbox)
		if err != nil || len(key) != 32 {
			return nil, ErrDriveLocked
		}
		return key, nil
	case "i1":
		aead, err := blobAEAD(e.driveKey)
		if err != nil {
			return nil, ErrDriveNoKey
		}
		if len(raw) < aead.NonceSize() {
			return nil, errBlobDamaged
		}
		key, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], keyAD(mbox))
		if err != nil {
			return nil, errBlobDamaged
		}
		return key, nil
	}
	return nil, errBlobDamaged
}

// keyAD binds a wrapped key to its mailbox: a row moved to another
// mailbox's files does not open there.
func keyAD(mbox string) []byte { return []byte("vayumail drive key:" + mbox) }
