// SPDX-License-Identifier: Apache-2.0

// Package passkey keeps the passkeys of the install's accounts and mailboxes,
// and the WebAuthn ceremonies that add them and sign in with them.
//
// A passkey belongs to a principal: the install account that registered it
// ("user:" and its id) or the mailbox ("vmail:" and its address). It signs in
// as exactly that principal, never as another one at the same address. Each
// principal has one user handle, random, which is what the browser returns;
// it names no one, so a passkey shown by the browser leaks no address.
package passkey

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

// ErrNoPasskey is a credential or handle this install does not hold.
var ErrNoPasskey = errors.New("passkey: not registered here")

// User is a principal as WebAuthn sees it: its handle, its address as the
// name the browser shows, and its passkeys.
type User struct {
	Principal string
	Account   string
	handle    []byte
	creds     []webauthn.Credential
}

func (u *User) WebAuthnID() []byte                         { return u.handle }
func (u *User) WebAuthnName() string                       { return u.Account }
func (u *User) WebAuthnDisplayName() string                { return u.Account }
func (u *User) WebAuthnCredentials() []webauthn.Credential { return u.creds }

// Adopt keeps handle as u's when u has no passkey yet: the handle a ceremony
// started with, which a fresh lookup would otherwise draw again at random.
// A principal with passkeys keeps the handle they carry.
func (u *User) Adopt(handle []byte) {
	if len(u.creds) == 0 && len(handle) > 0 {
		u.handle = append([]byte(nil), handle...)
	}
}

// Entry is one passkey as a page lists it.
type Entry struct {
	ID       int64
	Name     string
	Created  time.Time
	LastUsed time.Time
}

// Store is the passkeys and the ceremonies under way.
type Store struct {
	db  *sql.DB
	now func() time.Time

	mu         sync.Mutex
	ceremonies map[string]ceremony
}

type ceremony struct {
	principal string // "" for a sign-in, which names no one until it ends
	data      webauthn.SessionData
	until     time.Time
}

const (
	// ceremonyTTL is how long a started ceremony waits for its answer.
	ceremonyTTL = 5 * time.Minute
	// ceremonyCap bounds the ceremonies waiting: anyone can start a sign-in,
	// so the table must not grow with the requests. Past it the oldest
	// expired ones go first and, failing that, a new one is refused.
	ceremonyCap = 10000
)

// ErrBusy is a ceremony refused because too many are waiting.
var ErrBusy = errors.New("passkey: too many sign-ins under way; try again shortly")

// New is the store on db.
func New(db *sql.DB) *Store {
	return &Store{db: db, now: time.Now, ceremonies: map[string]ceremony{}}
}

// UserFor is principal's WebAuthn user, with its passkeys, created with a
// fresh handle when it has none yet.
func (s *Store) UserFor(ctx context.Context, principal, account string) (*User, error) {
	u := &User{Principal: principal, Account: account}
	rows, err := s.db.QueryContext(ctx, `SELECT handle, credential FROM passkeys WHERE principal = ? ORDER BY id`, principal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var h []byte
		var raw string
		if err := rows.Scan(&h, &raw); err != nil {
			return nil, err
		}
		var c webauthn.Credential
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			return nil, err
		}
		u.handle = h
		u.creds = append(u.creds, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if u.handle == nil {
		u.handle = make([]byte, 32)
		if _, err := rand.Read(u.handle); err != nil {
			return nil, err
		}
	}
	return u, nil
}

// ByHandle is the user whose handle is handle, for a sign-in that named no
// one.
func (s *Store) ByHandle(ctx context.Context, handle []byte) (*User, error) {
	var principal, account string
	err := s.db.QueryRowContext(ctx, `SELECT principal, account FROM passkeys WHERE handle = ? LIMIT 1`, handle).Scan(&principal, &account)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoPasskey
	}
	if err != nil {
		return nil, err
	}
	u, err := s.UserFor(ctx, principal, account)
	if err != nil {
		return nil, err
	}
	// The handle is the principal's; a row naming the principal with another
	// handle would be a store that lies, and signs in no one.
	if subtle.ConstantTimeCompare(u.handle, handle) != 1 {
		return nil, ErrNoPasskey
	}
	return u, nil
}

// Add keeps cred as u's passkey called name.
func (s *Store) Add(ctx context.Context, u *User, cred *webauthn.Credential, name string) error {
	raw, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Passkey"
	}
	if len(name) > 64 {
		name = name[:64]
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO passkeys (principal, account, handle, credential_id, credential, name, created) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.Principal, u.Account, u.handle, cred.ID, string(raw), name, s.now().Unix())
	return err
}

// Used keeps cred's new sign count and marks when it signed in.
func (s *Store) Used(ctx context.Context, cred *webauthn.Credential) error {
	raw, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE passkeys SET credential = ?, last_used = ? WHERE credential_id = ?`, string(raw), s.now().Unix(), cred.ID)
	return err
}

// List is principal's passkeys, oldest first.
func (s *Store) List(ctx context.Context, principal string) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, created, last_used FROM passkeys WHERE principal = ? ORDER BY id`, principal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		var created, used int64
		if err := rows.Scan(&e.ID, &e.Name, &created, &used); err != nil {
			return nil, err
		}
		e.Created = time.Unix(created, 0)
		if used > 0 {
			e.LastUsed = time.Unix(used, 0)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Delete removes principal's passkey id; another principal's is left alone.
func (s *Store) Delete(ctx context.Context, principal string, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM passkeys WHERE id = ? AND principal = ?`, id, principal)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoPasskey
	}
	// Passkey only needs a spare: with fewer than two left, one lost device
	// would shut the principal out, so the password comes back.
	if s.count(ctx, principal) < OnlyNeeds {
		_, err = s.db.ExecContext(ctx, `DELETE FROM passkey_only WHERE principal = ?`, principal)
	}
	return err
}

// ── Passkey only ─────────────────────────────────────────────────────────────

// OnlyNeeds is how many passkeys a principal must hold to sign in by passkey
// only: one to use and one spare, so losing a device does not shut them out.
const OnlyNeeds = 2

// ErrNeedsSpare refuses passkey only to a principal without a spare passkey.
var ErrNeedsSpare = errors.New("passkey: add a second passkey, on another device, before turning off the password")

func (s *Store) count(ctx context.Context, principal string) int {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM passkeys WHERE principal = ?`, principal).Scan(&n)
	return n
}

// SetOnly turns passkey only on or off for principal. On needs OnlyNeeds
// passkeys.
func (s *Store) SetOnly(ctx context.Context, principal string, on bool) error {
	if !on {
		_, err := s.db.ExecContext(ctx, `DELETE FROM passkey_only WHERE principal = ?`, principal)
		return err
	}
	if s.count(ctx, principal) < OnlyNeeds {
		return ErrNeedsSpare
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO passkey_only (principal, since) VALUES (?, ?) ON CONFLICT (principal) DO NOTHING`, principal, s.now().Unix())
	return err
}

// Only reports whether any of principals signs in by passkey only.
func (s *Store) Only(ctx context.Context, principals ...string) bool {
	if s == nil {
		return false
	}
	for _, p := range principals {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM passkey_only WHERE principal = ?`, p).Scan(&n); err == nil && n > 0 {
			return true
		}
	}
	return false
}

// OnlyEntry is a principal signing in by passkey only, as the administrator's
// page lists it.
type OnlyEntry struct {
	Principal, Account string
	Since              time.Time
}

// OnlyList is every principal signing in by passkey only.
func (s *Store) OnlyList(ctx context.Context) ([]OnlyEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT o.principal, COALESCE((SELECT account FROM passkeys p WHERE p.principal = o.principal LIMIT 1), ''), o.since FROM passkey_only o ORDER BY o.since`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OnlyEntry
	for rows.Next() {
		var e OnlyEntry
		var since int64
		if err := rows.Scan(&e.Principal, &e.Account, &since); err != nil {
			return nil, err
		}
		e.Since = time.Unix(since, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ── Ceremonies ───────────────────────────────────────────────────────────────

// Hold keeps a started ceremony's data for principal ("" for a sign-in) and
// gives the token its answer must carry.
func (s *Store) Hold(principal string, data *webauthn.SessionData) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.ceremonies) >= ceremonyCap {
		for k, c := range s.ceremonies {
			if now.After(c.until) {
				delete(s.ceremonies, k)
			}
		}
		if len(s.ceremonies) >= ceremonyCap {
			return "", ErrBusy
		}
	}
	s.ceremonies[token] = ceremony{principal: principal, data: *data, until: now.Add(ceremonyTTL)}
	return token, nil
}

// Take gives back, once, the ceremony token names, if it was started for
// principal and has not expired.
func (s *Store) Take(token, principal string) (webauthn.SessionData, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.ceremonies[token]
	delete(s.ceremonies, token)
	if !ok || c.principal != principal || s.now().After(c.until) {
		return webauthn.SessionData{}, false
	}
	return c.data, true
}
