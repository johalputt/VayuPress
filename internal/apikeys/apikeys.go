// Package apikeys provides a database-backed, rotatable store for VayuPress's
// own API keys — the bearer tokens that authenticate callers of the VayuPress
// HTTP API. Unlike the single static API_KEY env var (which remains valid as a
// bootstrap/root credential), keys issued here can be created, labelled,
// rotated and revoked at runtime from the VayuOS admin panel.
//
// Security model: only a SHA-256 hash of each token is persisted — the raw
// token is returned exactly once at creation/rotation and is never recoverable
// afterwards (mirroring how login sessions are stored, see internal/auth).
// Verification hashes the presented token and compares against the active set,
// so there is no plaintext credential at rest to leak.
package apikeys

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// TokenPrefix is the human-recognisable scheme marker on every issued token.
// It lets operators (and secret scanners) recognise a VayuPress key at a glance.
const TokenPrefix = "vp_"

// ErrNotFound is returned when no key matches the supplied id.
var ErrNotFound = errors.New("apikeys: key not found")

// Key is the metadata view of an issued API key. The raw token and its hash are
// never exposed through this type — only a short, non-sensitive display prefix.
type Key struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	Prefix     string     `json:"prefix"` // e.g. "vp_a1b2c3" — safe to display
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	Revoked    bool       `json:"revoked"`
}

// Store is a thread-safe API-key store backed by the vayu_api_keys table. It
// keeps an in-memory set of active token hashes so per-request verification is
// an O(1) map lookup rather than a DB round-trip; the set is refreshed lazily
// (30 s TTL) and invalidated immediately on any mutation.
type Store struct {
	db *sql.DB

	mu     sync.RWMutex
	active map[string]string // sha256(token) hex -> key id
	ttl    time.Time
}

// New creates a Store backed by db.
func New(db *sql.DB) *Store {
	return &Store{db: db, active: make(map[string]string)}
}

// hashToken returns the lowercase hex SHA-256 of a raw token.
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// generateToken returns a fresh random token and its stored prefix. The token
// is TokenPrefix + 32 random bytes (64 hex chars); the prefix is the scheme
// marker plus the first 6 hex chars, which uniquely-enough labels a key in a
// list without revealing anything useful.
func generateToken() (raw, prefix string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	body := hex.EncodeToString(buf)
	raw = TokenPrefix + body
	prefix = TokenPrefix + body[:6]
	return raw, prefix, nil
}

// Create issues a new API key with the given label and returns the metadata
// plus the raw token. The raw token is shown to the caller exactly once.
func (s *Store) Create(ctx context.Context, label string) (Key, string, error) {
	raw, prefix, err := generateToken()
	if err != nil {
		return Key{}, "", err
	}
	id := hashToken(raw)[:24] // stable, opaque id derived from the token hash
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO vayu_api_keys(id, label, prefix, key_hash, created_at, revoked)
		 VALUES(?,?,?,?,?,0)`,
		id, label, prefix, hashToken(raw), now,
	); err != nil {
		return Key{}, "", err
	}
	s.invalidate()
	return Key{ID: id, Label: label, Prefix: prefix, CreatedAt: now}, raw, nil
}

// Rotate replaces the secret of an existing (non-revoked) key, returning the new
// raw token. The id and label are preserved so any reference to the key stays
// valid while the old secret is instantly invalidated.
func (s *Store) Rotate(ctx context.Context, id string) (string, error) {
	raw, prefix, err := generateToken()
	if err != nil {
		return "", err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE vayu_api_keys SET key_hash=?, prefix=?, last_used_at=NULL WHERE id=? AND revoked=0`,
		hashToken(raw), prefix, id,
	)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrNotFound
	}
	s.invalidate()
	return raw, nil
}

// Revoke permanently disables a key without deleting its audit row.
func (s *Store) Revoke(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE vayu_api_keys SET revoked=1 WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.invalidate()
	return nil
}

// Delete removes a key row entirely.
func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM vayu_api_keys WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.invalidate()
	return nil
}

// List returns all keys (active and revoked), newest first, metadata only.
func (s *Store) List(ctx context.Context) ([]Key, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, label, prefix, created_at, last_used_at, revoked
		 FROM vayu_api_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Key
	for rows.Next() {
		var k Key
		var last sql.NullTime
		var revoked int
		if err := rows.Scan(&k.ID, &k.Label, &k.Prefix, &k.CreatedAt, &last, &revoked); err != nil {
			return nil, err
		}
		if last.Valid {
			t := last.Time
			k.LastUsedAt = &t
		}
		k.Revoked = revoked != 0
		out = append(out, k)
	}
	return out, rows.Err()
}

// Verify reports whether raw is a currently-valid (issued, non-revoked) API key.
// On a hit it asynchronously records the last-used timestamp. This is the hook
// registered with the auth package so issued keys authenticate API requests
// alongside the static bootstrap key.
func (s *Store) Verify(raw string) bool {
	if raw == "" {
		return false
	}
	h := hashToken(raw)
	s.mu.RLock()
	fresh := time.Now().Before(s.ttl)
	id, ok := s.active[h]
	s.mu.RUnlock()
	if !fresh {
		if err := s.refresh(); err == nil {
			s.mu.RLock()
			id, ok = s.active[h]
			s.mu.RUnlock()
		}
	}
	if ok {
		go s.touch(id)
	}
	return ok
}

// refresh reloads the active token-hash set from the database.
func (s *Store) refresh() error {
	rows, err := s.db.Query(`SELECT key_hash, id FROM vayu_api_keys WHERE revoked=0`)
	if err != nil {
		return err
	}
	defer rows.Close()
	m := make(map[string]string)
	for rows.Next() {
		var h, id string
		if rows.Scan(&h, &id) == nil {
			m[h] = id
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.active = m
	s.ttl = time.Now().Add(30 * time.Second)
	s.mu.Unlock()
	return nil
}

// touch records that a key was used, at most once per minute to avoid write
// amplification under load.
func (s *Store) touch(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = s.db.ExecContext(ctx,
		`UPDATE vayu_api_keys SET last_used_at=? WHERE id=? AND (last_used_at IS NULL OR last_used_at < ?)`,
		time.Now().UTC(), id, time.Now().UTC().Add(-time.Minute),
	)
}

func (s *Store) invalidate() {
	s.mu.Lock()
	s.ttl = time.Time{}
	s.mu.Unlock()
}

// Mask renders a display string for a key prefix, e.g. "vp_a1b2c3…".
func Mask(prefix string) string {
	if prefix == "" {
		return "—"
	}
	return fmt.Sprintf("%s…", prefix)
}
