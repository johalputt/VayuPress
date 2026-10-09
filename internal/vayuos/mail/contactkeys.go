// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"errors"
	"strings"
	"time"
)

// Outside keys: the OpenPGP public keys a mailbox holds for people outside
// this install, so it can encrypt to them and check their signatures.
//
// They are the mailbox's own, never the install's: one holder must not be
// able to plant a key under someone else's address and so read what every
// other mailbox sends them. For the same reason a key is never taken for an
// address this install serves; its own key is the one that counts.

// maxContactKeysPerMailbox bounds the list; a test lowers it.
var maxContactKeysPerMailbox = 500

// ContactKey is one outside key as its mailbox holds it.
type ContactKey struct {
	Email       string    `json:"email"`
	Fingerprint string    `json:"fingerprint"`
	Armor       string    `json:"-"`
	Added       time.Time `json:"added"`
}

// ErrOwnAddressKey refuses a key for an address this install serves.
var ErrOwnAddressKey = errors.New("this key is for an address this server serves, whose own key is the one used")

func (s *AccountStore) ensureContactKeysTable() error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_contact_keys(
		mailbox TEXT NOT NULL,
		email TEXT NOT NULL,
		fingerprint TEXT NOT NULL,
		armor TEXT NOT NULL,
		added_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY(mailbox, email));`)
	return err
}

// AddContactKey reads an armored public key and keeps it in rd's mailbox for
// each address it names, replacing a key held before for that address. It
// returns the addresses the key was kept for.
func (e *Engine) AddContactKey(rd Reader, armored []byte) ([]string, error) {
	if err := e.writeAuthorised(rd); err != nil {
		return nil, err
	}
	if e.accounts == nil || e.bridge == nil || e.accounts.ensureContactKeysTable() != nil {
		return nil, errors.New("vayumail: not started")
	}
	fingerprint, emails, err := e.bridge.DescribePublicKey(armored)
	if err != nil {
		return nil, err
	}
	var outside []string
	for _, a := range emails {
		if !e.bridge.IsLocalRecipient(a) {
			outside = append(outside, a)
		}
	}
	if len(outside) == 0 {
		return nil, ErrOwnAddressKey
	}
	mbox := e.mailboxAddr(rd)
	// Only addresses not already held add to the list; one held is replaced.
	var n int
	_ = e.db.QueryRow(`SELECT COUNT(1) FROM vayumail_contact_keys WHERE mailbox=?`, mbox).Scan(&n)
	for _, a := range outside {
		var held int
		if e.db.QueryRow(`SELECT COUNT(1) FROM vayumail_contact_keys WHERE mailbox=? AND email=?`, mbox, a).Scan(&held) == nil && held == 0 {
			n++
		}
	}
	if n > maxContactKeysPerMailbox {
		return nil, errors.New("this mailbox holds as many keys as it can; remove one to add another")
	}
	for _, a := range outside {
		if _, err := e.db.Exec(`INSERT INTO vayumail_contact_keys(mailbox, email, fingerprint, armor) VALUES(?,?,?,?)
			ON CONFLICT(mailbox, email) DO UPDATE SET fingerprint=excluded.fingerprint, armor=excluded.armor, added_at=CURRENT_TIMESTAMP`,
			mbox, a, fingerprint, string(armored)); err != nil {
			return nil, err
		}
	}
	return outside, nil
}

// RemoveContactKey forgets rd's key for email.
func (e *Engine) RemoveContactKey(rd Reader, email string) error {
	if err := e.writeAuthorised(rd); err != nil {
		return err
	}
	if e.accounts == nil || e.accounts.ensureContactKeysTable() != nil {
		return errors.New("vayumail: not started")
	}
	_, err := e.db.Exec(`DELETE FROM vayumail_contact_keys WHERE mailbox=? AND email=?`, e.mailboxAddr(rd), normEmail(email))
	return err
}

// ContactKeys lists rd's outside keys by address.
func (e *Engine) ContactKeys(rd Reader) ([]ContactKey, error) {
	if err := e.readAuthorised(rd); err != nil {
		return nil, err
	}
	return e.contactKeys(e.mailboxAddr(rd))
}

func (e *Engine) contactKeys(mbox string) ([]ContactKey, error) {
	if e.accounts == nil || e.accounts.ensureContactKeysTable() != nil {
		return nil, errors.New("vayumail: not started")
	}
	rows, err := e.db.Query(`SELECT email, fingerprint, armor, added_at FROM vayumail_contact_keys WHERE mailbox=? ORDER BY email`, normEmail(mbox))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ContactKey
	for rows.Next() {
		var k ContactKey
		if err := rows.Scan(&k.Email, &k.Fingerprint, &k.Armor, &k.Added); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// KnownKeys is rd's outside keys as encryption and the reader's seal use
// them: armored, by address.
func (e *Engine) KnownKeys(rd Reader) map[string]string {
	if e.readAuthorised(rd) != nil {
		return nil
	}
	return e.knownKeysOf(e.mailboxAddr(rd))
}

func (e *Engine) knownKeysOf(mbox string) map[string]string {
	keys, err := e.contactKeys(mbox)
	if err != nil || len(keys) == 0 {
		return nil
	}
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		out[strings.ToLower(k.Email)] = k.Armor
	}
	return out
}
