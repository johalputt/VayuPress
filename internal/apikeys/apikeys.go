// SPDX-License-Identifier: Apache-2.0

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
	"strings"
	"sync"
	"time"
)

// TokenPrefix is the human-recognisable scheme marker on every issued token.
// It lets operators (and secret scanners) recognise a VayuPress key at a glance.
const TokenPrefix = "vp_"

// Key scopes. An "internal" key is the single, auto-provisioned system key that
// VayuPress manages itself for internal automation (plugins, background jobs).
// It is propagated to internal consumers live on rotation and cannot be
// manually revoked/deleted. "external" keys are issued by the operator.
const (
	ScopeInternal = "internal"
	ScopeExternal = "external"
)

// InternalKeyID is the reserved, stable id of the internal/system key.
const InternalKeyID = "internal-system"

// ErrNotFound is returned when no key matches the supplied id.
var ErrNotFound = errors.New("apikeys: key not found")

// ErrInternalProtected is returned when a destructive op targets the system key.
var ErrInternalProtected = errors.New("apikeys: the internal system key cannot be revoked or deleted")

// Key is the metadata view of an issued API key. The raw token and its hash are
// never exposed through this type — only a short, non-sensitive display prefix.
type Key struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Prefix string `json:"prefix"` // e.g. "vp_a1b2c3" — safe to display
	Scope  string `json:"scope"`  // "internal" or "external"
	// DomainID binds the key to ONE hosted domain (migration 092). Empty means
	// global: the key may act on every domain this install serves. A bound key
	// must never read or mutate another domain's data — enforcement lives at
	// the request layer via auth.APIKeyMayAccessDomain.
	DomainID   string     `json:"domain_id,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	Revoked    bool       `json:"revoked"`
	// Fine-grained access (migration 062). Zero values keep a legacy row behaving
	// exactly as before: empty owner, no expiry, all-actions grant for existing
	// keys (backfilled), Active defaulting true.
	Permissions Permissions `json:"permissions,omitempty"`
	ExpiresAt   *time.Time  `json:"expires_at,omitempty"`
	OwnerUserID string      `json:"owner_user_id,omitempty"`
	RatePerMin  int         `json:"rate_per_min,omitempty"`
	UseCount    int64       `json:"use_count"`
	Active      bool        `json:"active"`
}

// Store is a thread-safe API-key store backed by the vayu_api_keys table. It
// keeps an in-memory set of active token hashes so per-request verification is
// an O(1) map lookup rather than a DB round-trip; the set is refreshed lazily
// (30 s TTL) and invalidated immediately on any mutation.
type Store struct {
	db *sql.DB

	mu     sync.RWMutex
	active map[string]KeyInfo // sha256(token) hex -> resolved key info
	ttl    time.Time

	internalRaw string // in-memory raw value of the system key (read live)
}

// New creates a Store backed by db.
func New(db *sql.DB) *Store {
	return &Store{db: db, active: make(map[string]KeyInfo)}
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

// Create issues a new external API key with the given label and returns the
// metadata plus the raw token. The raw token is shown to the caller exactly once.
//
// For backward compatibility the key is granted SUPERUSER access (the all-or-
// nothing behaviour every issued key had before fine-grained permissions), so
// existing automation that mints a key the classic way is unchanged. The admin UI
// uses CreateWithPermissions to issue scoped keys with an explicit grant set.
//
// Deprecated: minting an unrestricted key by default is exactly how operators
// end up with more FULL-ACCESS credentials than they can account for (audit).
// Call CreateWithPermissions with an explicit grant set instead — deny-all,
// scoped, or Superuser() when full access genuinely is the intent. Any new
// caller of this method now fails the staticcheck gate.
func (s *Store) Create(ctx context.Context, label string) (Key, string, error) {
	return s.CreateWithPermissions(ctx, "", label, Superuser(), nil, 0)
}

// CreateScoped issues a new key with an explicit scope, superuser-granted (see
// Create). Retained for API compatibility; the scope only distinguishes internal
// vs external — internal keys are provisioned by EnsureInternal, not here.
//
// Deprecated: same reasoning as Create — issue scoped keys through
// CreateWithPermissions with an explicit grant set.
func (s *Store) CreateScoped(ctx context.Context, label, scope string) (Key, string, error) {
	return s.CreateWithPermissions(ctx, "", label, Superuser(), nil, 0)
}

// CreateDomainScoped issues a new external key bound to ONE hosted domain
// (migration 092): every request it authenticates must target that domain,
// enforced by auth.APIKeyMayAccessDomain. An empty domainID is rejected — use
// CreateWithPermissions for a global key so the two shapes cannot blur.
func (s *Store) CreateDomainScoped(ctx context.Context, ownerID, label, domainID string, perms Permissions, expiresAt *time.Time, ratePerMin int) (Key, string, error) {
	if strings.TrimSpace(domainID) == "" {
		return Key{}, "", errors.New("apikeys: domain-scoped key requires a non-empty domain id")
	}
	return s.create(ctx, ownerID, label, perms, expiresAt, ratePerMin, strings.TrimSpace(domainID))
}

// CreateWithPermissions issues a new external key owned by ownerID (empty for an
// operator/system-owned key) with exactly the given grant set, optional hard
// expiry, and optional per-key rate budget. The raw token is returned once. This
// is the constructor the scoped admin UI and the VCB provisioning flow use.
func (s *Store) CreateWithPermissions(ctx context.Context, ownerID, label string, perms Permissions, expiresAt *time.Time, ratePerMin int) (Key, string, error) {
	return s.create(ctx, ownerID, label, perms, expiresAt, ratePerMin, "")
}

// create is the single insert path behind every public constructor.
func (s *Store) create(ctx context.Context, ownerID, label string, perms Permissions, expiresAt *time.Time, ratePerMin int, domainID string) (Key, string, error) {
	raw, prefix, err := generateToken()
	if err != nil {
		return Key{}, "", err
	}
	id := hashToken(raw)[:24] // stable, opaque id derived from the token hash
	now := time.Now().UTC()
	if ratePerMin < 0 {
		ratePerMin = 0
	}
	if perms == nil {
		perms = NewPermissions()
	}
	var exp interface{}
	var expPtr *time.Time
	if expiresAt != nil {
		u := expiresAt.UTC()
		exp = u
		expPtr = &u
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO vayu_api_keys(id, label, prefix, key_hash, scope, created_at, revoked, permissions, expires_at, owner_user_id, rate_per_min, use_count, active, domain_id)
		 VALUES(?,?,?,?,?,?,0,?,?,?,?,0,1,?)`,
		id, label, prefix, hashToken(raw), ScopeExternal, now, perms.MarshalString(), exp, ownerID, ratePerMin, domainID,
	); err != nil {
		return Key{}, "", err
	}
	s.invalidate()
	return Key{
		ID: id, Label: label, Prefix: prefix, Scope: ScopeExternal, CreatedAt: now,
		Permissions: perms, ExpiresAt: expPtr, OwnerUserID: ownerID, RatePerMin: ratePerMin, Active: true,
		DomainID: domainID,
	}, raw, nil
}

// EnsureInternal provisions (or refreshes) the single internal/system key and
// caches its raw value in memory for internal consumers. It is called once at
// boot; the raw value is regenerated each start (the system key is never handed
// to external parties, and internal callers always read the live value via
// InternalKey), and the stored hash is updated so the key authenticates.
func (s *Store) EnsureInternal(ctx context.Context) error {
	raw, prefix, err := generateToken()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO vayu_api_keys(id, label, prefix, key_hash, scope, created_at, revoked)
		 VALUES(?,?,?,?,?,?,0)
		 ON CONFLICT(id) DO UPDATE SET key_hash=excluded.key_hash, prefix=excluded.prefix, scope='internal', revoked=0`,
		InternalKeyID, "System (internal)", prefix, hashToken(raw), ScopeInternal, now,
	); err != nil {
		return err
	}
	s.mu.Lock()
	s.internalRaw = raw
	s.mu.Unlock()
	s.invalidate()
	return nil
}

// InternalKey returns the current raw value of the internal/system key. Internal
// consumers should call this at use time so a rotation propagates automatically.
func (s *Store) InternalKey() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.internalRaw
}

// Rotate replaces the secret of an existing (non-revoked) key, returning the new
// raw token. The id and label are preserved so any reference to the key stays
// valid while the old secret is instantly invalidated.
func (s *Store) Rotate(ctx context.Context, id string) (string, error) {
	// The internal/system key is provisioned ONLY by EnsureInternal at boot and
	// must never be rotated through the operator/API surface: a rotate returns
	// the fresh secret, so rotating the internal key would hand the caller an
	// unconditional superuser token. Refuse it exactly like Revoke/Delete/
	// SetActive do (audit C2).
	if id == InternalKeyID {
		return "", ErrInternalProtected
	}
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

// RotateWithExpiry rotates the key secret AND resets its expiry in one update.
// The OAuth refresh flow uses it so a short-lived access token is renewed with a
// fresh bounded lifetime on every refresh (audit L9). expiresAt nil clears any
// expiry. Like Rotate, it refuses the internal/system key.
func (s *Store) RotateWithExpiry(ctx context.Context, id string, expiresAt *time.Time) (string, error) {
	if id == InternalKeyID {
		return "", ErrInternalProtected
	}
	raw, prefix, err := generateToken()
	if err != nil {
		return "", err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE vayu_api_keys SET key_hash=?, prefix=?, last_used_at=NULL, expires_at=? WHERE id=? AND revoked=0`,
		hashToken(raw), prefix, expiresAt, id,
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
	if id == InternalKeyID {
		return ErrInternalProtected
	}
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

// RevokeOwnedBy revokes every key owned by ownerUserID and returns how many were
// revoked.
//
// AUDIT FINDING (Section 3). Deleting a VayuPress user left the keys they owned
// authenticating. owner_user_id carries no foreign key, users.Delete is a
// single-table DELETE, and key resolution filters on revoked/active/expiry with
// no join to users — so an administrator who connected an MCP client with full
// control kept that control after their account was deleted, renewed
// indefinitely by the rotating OAuth refresh token. The account vanished from
// every screen the operator has; the access did not.
//
// An EMPTY ownerUserID is refused rather than treated as a wildcard. ” is the
// documented value for operator/system-owned keys (migration 062), so a caller
// that passed a blank id through would revoke the install's entire provisioned
// API surface in one statement. Refusing it here means that mistake cannot be
// made from any call site.
//
// The internal key is exempt for the same reason Revoke exempts it.
func (s *Store) RevokeOwnedBy(ctx context.Context, ownerUserID string) (int, error) {
	if strings.TrimSpace(ownerUserID) == "" {
		return 0, errors.New("apikeys: refusing to revoke by an empty owner id")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE vayu_api_keys SET revoked=1 WHERE owner_user_id=? AND revoked=0 AND id<>?`,
		ownerUserID, InternalKeyID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		// Same invalidation Revoke performs: without it the cached key keeps
		// authenticating until the 30s TTL lapses.
		s.invalidate()
	}
	return int(n), nil
}

// Delete removes a key row entirely.
func (s *Store) Delete(ctx context.Context, id string) error {
	if id == InternalKeyID {
		return ErrInternalProtected
	}
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
	return s.list(ctx, "", "")
}

// ListByOwner returns only the keys owned by ownerUserID (plus never the internal
// key), for the per-user API-keys view. An empty ownerUserID returns operator/
// system-owned keys only.
func (s *Store) ListByOwner(ctx context.Context, ownerUserID string) ([]Key, error) {
	return s.list(ctx, "AND owner_user_id=? AND scope<>'internal'", ownerUserID)
}

// list is the shared query behind List/ListByOwner. whereExtra (if non-empty) is
// appended to the WHERE clause and arg is bound when whereExtra references a "?".
func (s *Store) list(ctx context.Context, whereExtra, arg string) ([]Key, error) {
	q := `SELECT id, label, prefix, scope, created_at, last_used_at, revoked, permissions, expires_at, owner_user_id, rate_per_min, use_count, active, domain_id
	      FROM vayu_api_keys WHERE 1=1 ` + whereExtra + ` ORDER BY (scope='internal') DESC, created_at DESC`
	var rows *sql.Rows
	var err error
	if whereExtra != "" {
		rows, err = s.db.QueryContext(ctx, q, arg)
	} else {
		rows, err = s.db.QueryContext(ctx, q)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Key
	for rows.Next() {
		var k Key
		var last, exp sql.NullTime
		var revoked, active int
		var perms string
		if err := rows.Scan(&k.ID, &k.Label, &k.Prefix, &k.Scope, &k.CreatedAt, &last, &revoked, &perms, &exp, &k.OwnerUserID, &k.RatePerMin, &k.UseCount, &active, &k.DomainID); err != nil {
			return nil, err
		}
		if last.Valid {
			t := last.Time
			k.LastUsedAt = &t
		}
		if exp.Valid {
			t := exp.Time
			k.ExpiresAt = &t
		}
		k.Revoked = revoked != 0
		k.Active = active != 0
		k.Permissions = ParsePermissions(perms)
		out = append(out, k)
	}
	return out, rows.Err()
}

// Verify reports whether raw is a currently-valid (issued, non-revoked,
// active, unexpired) API key. It is the boolean hook registered with the auth
// package; enforcement middleware uses Resolve for the full grant set.
func (s *Store) Verify(raw string) bool {
	_, ok := s.Resolve(raw)
	return ok
}

// Resolve returns the KeyInfo for a currently-valid key (issued, non-revoked,
// active and unexpired), or ok=false. On a hit it asynchronously bumps the
// usage counter. This is the enforcement-time entry point: the returned KeyInfo
// carries the grant set, owner, expiry and per-key rate budget.
func (s *Store) Resolve(raw string) (KeyInfo, bool) {
	if raw == "" {
		return KeyInfo{}, false
	}
	h := hashToken(raw)
	s.mu.RLock()
	fresh := time.Now().Before(s.ttl)
	ki, ok := s.active[h]
	s.mu.RUnlock()
	if !fresh {
		if err := s.refresh(); err == nil {
			s.mu.RLock()
			ki, ok = s.active[h]
			s.mu.RUnlock()
		}
	}
	// Hard expiry is exact, not TTL-lagged: refresh() filters expired keys at
	// load time, but a key whose expires_at passes WHILE cached would otherwise
	// keep authenticating until the 30s TTL elapses (revoke/deactivate invalidate
	// immediately; the clock does not). Re-check here so an expired credential is
	// refused on the very next request.
	if ok && ki.ExpiresAt != nil && !time.Now().Before(*ki.ExpiresAt) {
		ki, ok = KeyInfo{}, false
	}
	if ok {
		go s.touch(ki.ID)
	}
	return ki, ok
}

// refresh reloads the active token-hash → KeyInfo set from the database, keeping
// only keys that are non-revoked, active, and not past their expiry. An expired
// or deactivated key therefore drops out of the cache within the TTL (and
// immediately on any mutation, which invalidates the TTL).
func (s *Store) refresh() error {
	rows, err := s.db.Query(
		`SELECT key_hash, id, label, scope, owner_user_id, permissions, expires_at, rate_per_min, domain_id
		 FROM vayu_api_keys
		 WHERE revoked=0 AND active=1 AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	m := make(map[string]KeyInfo)
	for rows.Next() {
		var h, id, label, scope, owner, perms string
		var exp sql.NullTime
		var rate int
		var domainID string
		if rows.Scan(&h, &id, &label, &scope, &owner, &perms, &exp, &rate, &domainID) != nil {
			continue
		}
		ki := KeyInfo{
			ID: id, Label: label, Scope: scope, Owner: owner,
			Perms: ParsePermissions(perms), RatePerMin: rate,
			DomainID: domainID,
		}
		if exp.Valid {
			t := exp.Time
			ki.ExpiresAt = &t
		}
		m[h] = ki
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

// touch records that a key was used: it stamps last_used_at (at most once per
// minute to avoid write amplification) and increments the monotonic use counter
// on every call so the admin usage view is accurate.
func (s *Store) touch(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	now := time.Now().UTC()
	_, _ = s.db.ExecContext(ctx,
		`UPDATE vayu_api_keys SET use_count=use_count+1, last_used_at=CASE WHEN last_used_at IS NULL OR last_used_at < ? THEN ? ELSE last_used_at END WHERE id=?`,
		now.Add(-time.Minute), now, id,
	)
}

// SetActive toggles a key's active flag (deactivate/reactivate) without rotating
// its secret. A deactivated key stops authenticating within the cache TTL (and
// immediately, since the mutation invalidates the cache). The internal key cannot
// be deactivated.
func (s *Store) SetActive(ctx context.Context, id string, active bool) error {
	if id == InternalKeyID {
		return ErrInternalProtected
	}
	v := 0
	if active {
		v = 1
	}
	res, err := s.db.ExecContext(ctx, `UPDATE vayu_api_keys SET active=? WHERE id=?`, v, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.invalidate()
	return nil
}

// UpdatePermissions replaces a key's grant set. The internal key's effective
// access is always superuser regardless of what is stored, so its grants are not
// editable here.
func (s *Store) UpdatePermissions(ctx context.Context, id string, perms Permissions) error {
	if id == InternalKeyID {
		return ErrInternalProtected
	}
	if perms == nil {
		perms = NewPermissions()
	}
	res, err := s.db.ExecContext(ctx, `UPDATE vayu_api_keys SET permissions=? WHERE id=?`, perms.MarshalString(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.invalidate()
	return nil
}

// cleanupGraceDays is how long an expired key's row stays visible (badged
// "expired" in the admin list) before the sweeper hard-deletes it. The grace
// window keeps the audit trail inspectable; expiry itself is enforced the
// moment it passes (Resolve re-checks the clock).
const cleanupGraceDays = 30

// Cleanup hard-deletes external keys whose expiry passed more than the grace
// window ago. The internal system key is never touched (its expiry is NULL and
// the scope filter excludes it anyway — both guards on purpose).
func (s *Store) Cleanup(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM vayu_api_keys
		 WHERE id <> ? AND scope <> 'internal'
		   AND expires_at IS NOT NULL
		   AND expires_at < datetime('now', ?)`,
		InternalKeyID, fmt.Sprintf("-%d days", cleanupGraceDays))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		s.invalidate()
	}
	return n, nil
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
