// SPDX-License-Identifier: Apache-2.0

package pgp

// holder.go — a mailbox key its holder keeps.
//
// Every mailbox key is sealed at rest under the install's key (keystore.go),
// so whoever runs the server can use it. A holder who makes their mailbox
// private takes it out of that reach: the key, and every key rotated out
// before it, is locked with OpenPGP's own passphrase protection under their
// password (Argon2 with AEAD, RFC 9580), and a second copy under a recovery
// code shown to them once. The install's key still seals both copies, so a
// key file looks like any other, but opening it yields only locked keys.
//
// The password unlocks the key into memory for a while after a sign-in that
// proved it (HoldOpenFor), never onto disk; while it is not held open,
// decryption and signing with it answer ErrHolderLocked. Locking it under
// OpenPGP's format, rather than a wrapping of our own, is what lets the
// VayuMail app take the same locked key over its key endpoint and unlock it
// on the phone with the same password.
//
// There is no other way in. The operator decided on 2026-10-18 that a
// private mailbox has no back door: lose the password and the recovery code
// and the key, and the mail sealed to it, is gone.

import (
	"bytes"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/ProtonMail/go-crypto/openpgp/s2k"
)

var (
	// ErrHolderLocked is a holder's key that is not held open: nothing can be
	// decrypted or signed with it until they sign in with their password.
	ErrHolderLocked = errors.New("vayupgp: this mailbox's key is locked with its holder's password")
	// ErrNotUnlocked is a password or recovery code that does not open the key.
	ErrNotUnlocked = errors.New("vayupgp: that does not unlock this mailbox's key")
	// ErrHolderKey refuses what needs the server to hold the key, such as a
	// rotation, for a key its holder keeps.
	ErrHolderKey = errors.New("vayupgp: the holder of this mailbox keeps its key")
)

// HoldOpenFor is how long a sign-in that proved the password keeps the key
// in memory. Each such sign-in starts the time again; signing out ends it.
const HoldOpenFor = 8 * time.Hour

type heldRing struct {
	ring  openpgp.EntityList // the current key first, then rotated-out keys
	until time.Time
}

// lockConfig locks a key the way the VayuMail app unlocks it (3eo): Argon2,
// which OpenPGP allows only with AEAD key protection.
func lockConfig() *packet.Config {
	return &packet.Config{S2KConfig: &s2k.Config{S2KMode: s2k.Argon2S2K}, AEADConfig: &packet.AEADConfig{}}
}

// lockArmor returns the keys in plainArmor locked under secret.
func lockArmor(plainArmor, secret []byte) ([]byte, error) {
	list, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(plainArmor))
	if err != nil || len(list) == 0 {
		return nil, fmt.Errorf("vayupgp: read key to lock: %w", err)
	}
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PrivateKeyType, nil)
	if err != nil {
		return nil, err
	}
	for _, ent := range list {
		if err := ent.EncryptPrivateKeys(secret, lockConfig()); err != nil {
			return nil, fmt.Errorf("vayupgp: lock key: %w", err)
		}
		if err := ent.SerializePrivateWithoutSigning(w, nil); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// openArmor unlocks every key in lockedArmor with secret.
func openArmor(lockedArmor, secret []byte) (openpgp.EntityList, error) {
	list, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(lockedArmor))
	if err != nil || len(list) == 0 {
		return nil, fmt.Errorf("vayupgp: read locked key: %w", err)
	}
	for _, ent := range list {
		if ent.PrivateKey == nil || ent.DecryptPrivateKeys(secret) != nil {
			return nil, ErrNotUnlocked
		}
	}
	return list, nil
}

// newRecoveryCode is 160 random bits in base32, in groups of four.
func newRecoveryCode() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	s := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	var groups []string
	for len(s) > 4 {
		groups = append(groups, s[:4])
		s = s[4:]
	}
	return strings.Join(append(groups, s), "-"), nil
}

// normalCode reads a recovery code as typed: case, spaces and dashes aside.
func normalCode(code string) []byte {
	return []byte(strings.Map(func(r rune) rune {
		switch {
		case r == '-' || r == ' ' || r == '\t' || r == '\n' || r == '\r':
			return -1
		case r >= 'a' && r <= 'z':
			return r - 'a' + 'A'
		}
		return r
	}, code))
}

// ── Key store: a holder's files ─────────────────────────────────────────────

func (k *keyStore) setHolderLocked(userID string, on bool) {
	if k.holder == nil {
		k.holder = make(map[string]bool)
	}
	if on {
		k.holder[userID] = true
	} else {
		delete(k.holder, userID)
	}
}

func (k *keyStore) isHolder(userID string) bool {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.holder[userID]
}

// holderFiles is userID's archived key files and then its key file: locked
// in that order, so a crash part-way never leaves the current key locked
// while an older one is still open to the install.
func (k *keyStore) holderFiles(userID string) []string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	out := append([]string(nil), k.archByID[userID]...)
	if base := k.fileByID[userID]; base != "" {
		out = append(out, base)
	}
	return out
}

func (k *keyStore) readNamed(name string) (storedKey, error) {
	data, err := os.ReadFile(filepath.Join(k.dir, name))
	if err != nil {
		return storedKey{}, err
	}
	var rec storedKey
	err = json.Unmarshal(data, &rec)
	return rec, err
}

// writeHolder seals locked and recovery into rec and replaces the file name.
func (k *keyStore) writeHolder(name string, rec storedKey, locked, recovery []byte) error {
	var err error
	if rec.PrivateNonce, rec.PrivateCT, err = k.seal(locked); err != nil {
		return err
	}
	if rec.RecoveryNonce, rec.RecoveryCT, err = k.seal(recovery); err != nil {
		return err
	}
	rec.Holder = true
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	dst := filepath.Join(k.dir, name)
	if err := os.WriteFile(dst+".tmp", data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(dst+".tmp", dst); err != nil {
		return err
	}
	if !strings.Contains(name, ".arch.") {
		k.mu.Lock()
		k.setHolderLocked(rec.UserID, true)
		k.mu.Unlock()
	}
	return nil
}

// ── Engine ───────────────────────────────────────────────────────────────────

func (e *Engine) holderID(email string) (string, error) {
	if e.ks == nil {
		return "", errors.New("vayupgp: engine not started")
	}
	userID, ok := e.ks.userIDForEmail(email)
	if !ok {
		return "", ErrNotFound
	}
	return userID, nil
}

// HolderKeeps reports whether email's holder keeps its key.
func (e *Engine) HolderKeeps(email string) bool {
	userID, err := e.holderID(email)
	return err == nil && e.ks.isHolder(userID)
}

// HeldOpen reports whether email's holder key is unlocked in memory now.
func (e *Engine) HeldOpen(email string) bool {
	userID, err := e.holderID(email)
	if err != nil {
		return false
	}
	_, ok := e.heldRing(userID)
	return ok
}

func (e *Engine) heldRing(userID string) (openpgp.EntityList, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	h, ok := e.held[userID]
	if !ok {
		return nil, false
	}
	if time.Now().After(h.until) {
		delete(e.held, userID)
		return nil, false
	}
	return h.ring, true
}

func (e *Engine) hold(userID string, ring openpgp.EntityList) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.unlocked, userID)
	if e.held == nil {
		e.held = make(map[string]heldRing)
	}
	e.held[userID] = heldRing{ring: ring, until: time.Now().Add(HoldOpenFor)}
}

// CloseHeld drops email's unlocked key from memory: a sign-out.
func (e *Engine) CloseHeld(email string) {
	if userID, err := e.holderID(email); err == nil {
		e.mu.Lock()
		delete(e.held, userID)
		e.mu.Unlock()
	}
}

// openHolderFiles unlocks every key file of userID with secret, from the
// password copy or, with fromRecovery, the recovery copy, and returns each
// file's record and unlocked key in holderFiles order.
func (e *Engine) openHolderFiles(userID string, secret []byte, fromRecovery bool) ([]string, []storedKey, []openpgp.EntityList, error) {
	names := e.ks.holderFiles(userID)
	recs := make([]storedKey, len(names))
	rings := make([]openpgp.EntityList, len(names))
	for i, name := range names {
		rec, err := e.ks.readNamed(name)
		if err != nil {
			return nil, nil, nil, err
		}
		nonce, ct := rec.PrivateNonce, rec.PrivateCT
		if fromRecovery {
			nonce, ct = rec.RecoveryNonce, rec.RecoveryCT
		}
		armored, err := e.ks.open(nonce, ct)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("vayupgp: open key file: %w", err)
		}
		var ring openpgp.EntityList
		if rec.Holder {
			ring, err = openArmor(armored, secret)
		} else {
			// Not yet locked (a lock that stopped part-way): the install's key
			// is all that seals it.
			ring, err = openpgp.ReadArmoredKeyRing(bytes.NewReader(armored))
		}
		if err != nil {
			return nil, nil, nil, err
		}
		recs[i], rings[i] = rec, ring
	}
	return names, recs, rings, nil
}

// relock writes each file locked under password and code.
func (e *Engine) relock(names []string, recs []storedKey, rings []openpgp.EntityList, password, code []byte) error {
	for i, name := range names {
		plain, err := armorRing(rings[i])
		if err != nil {
			return err
		}
		locked, err := lockArmor(plain, password)
		if err != nil {
			return err
		}
		recovery, err := lockArmor(plain, code)
		if err != nil {
			return err
		}
		if err := e.ks.writeHolder(name, recs[i], locked, recovery); err != nil {
			return err
		}
	}
	return nil
}

// LockToHolder hands email's key to its holder: it and every rotated-out key
// are locked under password and under a new recovery code, which is returned
// once and kept nowhere. The key stays held open, the holder having just
// proved the password. Run again after a lock that stopped part-way, it
// finishes the job (with a new code).
func (e *Engine) LockToHolder(email, password string) (string, error) {
	userID, err := e.holderID(email)
	if err != nil {
		return "", err
	}
	if password == "" {
		return "", ErrNotUnlocked
	}
	code, err := newRecoveryCode()
	if err != nil {
		return "", err
	}
	names, recs, rings, err := e.openHolderFiles(userID, []byte(password), false)
	if err != nil {
		return "", err
	}
	if err := e.relock(names, recs, rings, []byte(password), normalCode(code)); err != nil {
		return "", err
	}
	e.hold(userID, flatten(rings))
	return code, nil
}

// HoldOpen unlocks email's holder key with password and keeps it in memory
// for HoldOpenFor. A key already held open only has its time started again.
// For a key the install still holds it does nothing.
func (e *Engine) HoldOpen(email, password string) error {
	userID, err := e.holderID(email)
	if err != nil || !e.ks.isHolder(userID) {
		return nil
	}
	if ring, ok := e.heldRing(userID); ok {
		e.hold(userID, ring)
		return nil
	}
	_, _, rings, err := e.openHolderFiles(userID, []byte(password), false)
	if err != nil {
		return err
	}
	e.hold(userID, flatten(rings))
	return nil
}

// ChangeHolderPassword relocks email's holder key from oldPassword to
// newPassword. The recovery code is not changed.
func (e *Engine) ChangeHolderPassword(email, oldPassword, newPassword string) error {
	userID, err := e.holderID(email)
	if err != nil || !e.ks.isHolder(userID) {
		return nil
	}
	names, recs, rings, err := e.openHolderFiles(userID, []byte(oldPassword), false)
	if err != nil {
		return err
	}
	for i, name := range names {
		plain, err := armorRing(rings[i])
		if err != nil {
			return err
		}
		locked, err := lockArmor(plain, []byte(newPassword))
		if err != nil {
			return err
		}
		recovery, err := e.ks.open(recs[i].RecoveryNonce, recs[i].RecoveryCT)
		if err != nil {
			return err
		}
		if err := e.ks.writeHolder(name, recs[i], locked, recovery); err != nil {
			return err
		}
	}
	e.hold(userID, flatten(rings))
	return nil
}

// RecoverHolder opens email's holder key with its recovery code and locks it
// under password, the mailbox's password now, and a new recovery code, which
// it returns: a code is used once.
func (e *Engine) RecoverHolder(email, code, password string) (string, error) {
	userID, err := e.holderID(email)
	if err != nil {
		return "", err
	}
	if !e.ks.isHolder(userID) || password == "" {
		return "", ErrNotUnlocked
	}
	names, recs, rings, err := e.openHolderFiles(userID, normalCode(code), true)
	if err != nil {
		return "", err
	}
	next, err := newRecoveryCode()
	if err != nil {
		return "", err
	}
	if err := e.relock(names, recs, rings, []byte(password), normalCode(next)); err != nil {
		return "", err
	}
	e.hold(userID, flatten(rings))
	return next, nil
}

// RetireHolderKey takes a deleted mailbox's holder key, and its rotated-out
// keys, out of use: the files are renamed out of the key store's index, kept
// on disk beside the retired mail they open, so the address can be given to
// someone new without their mail being sealed to a key they do not hold, and a
// mailbox deleted by mistake can still be restored with it. A key the install
// holds is left as it is.
func (e *Engine) RetireHolderKey(email string) error {
	userID, err := e.holderID(email)
	if err != nil || !e.ks.isHolder(userID) {
		return nil
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	for _, name := range e.ks.holderFiles(userID) {
		src := filepath.Join(e.ks.dir, name)
		if err := os.Rename(src, src+".retired-"+stamp); err != nil {
			return fmt.Errorf("vayupgp: retire key file: %w", err)
		}
	}
	e.ks.mu.Lock()
	delete(e.ks.fileByID, userID)
	delete(e.ks.archByID, userID)
	delete(e.ks.holder, userID)
	if cur, ok := e.ks.byEmail[normalizeEmail(email)]; ok && cur == userID {
		delete(e.ks.byEmail, normalizeEmail(email))
		delete(e.ks.emailCreated, normalizeEmail(email))
	}
	e.ks.mu.Unlock()
	e.mu.Lock()
	delete(e.held, userID)
	delete(e.unlocked, userID)
	e.mu.Unlock()
	return nil
}

// SealTo encrypts plain to email's public key: no private key is needed, so
// mail for a private mailbox is sealed whether or not its holder is signed in.
func (e *Engine) SealTo(email string, plain []byte) ([]byte, error) {
	userID, err := e.holderID(email)
	if err != nil {
		return nil, err
	}
	rec, err := e.ks.loadMeta(userID)
	if err != nil {
		return nil, err
	}
	pub, err := entityFromArmor(rec.PublicArmor)
	if err != nil {
		return nil, err
	}
	return encryptTo(plain, []*openpgp.Entity{pub}, nil)
}

func flatten(rings []openpgp.EntityList) openpgp.EntityList {
	var out openpgp.EntityList
	// holderFiles puts the current key last; the ring puts it first, which is
	// the key entity() hands out for signing.
	for i := len(rings) - 1; i >= 0; i-- {
		out = append(out, rings[i]...)
	}
	return out
}

func armorRing(ring openpgp.EntityList) ([]byte, error) {
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PrivateKeyType, nil)
	if err != nil {
		return nil, err
	}
	for _, ent := range ring {
		if err := ent.SerializePrivateWithoutSigning(w, nil); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
