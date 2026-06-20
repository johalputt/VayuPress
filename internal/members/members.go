// Package members implements reader memberships and content paywalls for
// VayuPress.
//
// Readers are distinct from admin authors (internal/users). A member is just an
// email with a tier (free or paid) and a status. Authentication is passwordless:
// a member requests a magic link, receives a one-time token by email, and
// exchanges it for a member session cookie. This keeps the reader experience
// frictionless and stores no reader passwords.
//
// Paywalls are expressed per article via article_access.level (public|members).
// Enforcement (serving a preview + CTA to non-members) lives in the HTTP layer;
// this package owns persistence, tokens, and tier logic only.
//
// Payments are optional and decoupled: tiers can be set manually by an admin, or
// upgraded automatically by the Stripe webhook receiver. VayuPress never embeds a
// payment SDK — it only reacts to a signed webhook.
package members

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// Tiers and access levels.
const (
	TierFree = "free"
	TierPaid = "paid"

	AccessPublic  = "public"  // anyone
	AccessMembers = "members" // any logged-in member (free or paid)
	AccessPaid    = "paid"    // paid members only
)

// LoginTokenTTL bounds how long a magic link is valid.
const LoginTokenTTL = 30 * time.Minute

// SessionTTL bounds a member login session.
const SessionTTL = 30 * 24 * time.Hour

// Member is a reader account.
type Member struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Tier      string    `json:"tier"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// IsPaid reports whether the member has an active paid membership.
func (m *Member) IsPaid() bool { return m != nil && m.Tier == TierPaid && m.Status == "active" }

// Store manages members, magic-link tokens, sessions, and per-article access.
type Store struct{ db *sql.DB }

// New creates a Store.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Upsert ensures a member row exists for email and returns it.
func (s *Store) Upsert(ctx context.Context, email string) (*Member, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if _, err := mail.ParseAddress(email); err != nil {
		return nil, fmt.Errorf("invalid email: %w", err)
	}
	if m, err := s.Get(ctx, email); err == nil {
		return m, nil
	}
	id := randHex(12)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO members(id,email) VALUES(?,?)`, id, email); err != nil {
		return nil, fmt.Errorf("upsert member: %w", err)
	}
	return &Member{ID: id, Email: email, Tier: TierFree, Status: "active", CreatedAt: time.Now().UTC()}, nil
}

// Get returns the member with the given email.
func (s *Store) Get(ctx context.Context, email string) (*Member, error) {
	var m Member
	err := s.db.QueryRowContext(ctx,
		`SELECT id,email,tier,status,created_at FROM members WHERE email=?`,
		strings.TrimSpace(strings.ToLower(email))).
		Scan(&m.ID, &m.Email, &m.Tier, &m.Status, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// GetByID returns the member with the given id.
func (s *Store) GetByID(ctx context.Context, id string) (*Member, error) {
	var m Member
	err := s.db.QueryRowContext(ctx,
		`SELECT id,email,tier,status,created_at FROM members WHERE id=?`, id).
		Scan(&m.ID, &m.Email, &m.Tier, &m.Status, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// List returns members, newest first.
func (s *Store) List(ctx context.Context, limit int) ([]Member, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,email,tier,status,created_at FROM members ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.Email, &m.Tier, &m.Status, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetTier updates a member's tier (free|paid) by email.
func (s *Store) SetTier(ctx context.Context, email, tier string) error {
	if tier != TierFree && tier != TierPaid {
		return fmt.Errorf("invalid tier %q", tier)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE members SET tier=? WHERE email=?`, tier, strings.TrimSpace(strings.ToLower(email)))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no member with that email")
	}
	return nil
}

// LinkStripeCustomer associates a Stripe customer id with a member (by email),
// creating the member if necessary, and sets them to paid.
func (s *Store) UpgradeByEmail(ctx context.Context, email, stripeCustomer string) error {
	m, err := s.Upsert(ctx, email)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE members SET tier=?,stripe_customer=? WHERE id=?`, TierPaid, stripeCustomer, m.ID)
	return err
}

// Count returns the number of members by tier.
func (s *Store) Count(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT tier,COUNT(*) FROM members GROUP BY tier`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var tier string
		var n int
		rows.Scan(&tier, &n)
		out[tier] = n
	}
	return out, rows.Err()
}

// ---- magic-link login tokens ------------------------------------------------

// CreateLoginToken issues a one-time magic-link token for email and returns the
// raw token (to embed in the emailed link). Only its hash is stored.
func (s *Store) CreateLoginToken(ctx context.Context, email string) (string, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if _, err := mail.ParseAddress(email); err != nil {
		return "", fmt.Errorf("invalid email: %w", err)
	}
	token := randHex(32)
	exp := time.Now().UTC().Add(LoginTokenTTL).Format("2006-01-02 15:04:05")
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO member_login_tokens(token_hash,email,expires_at) VALUES(?,?,?)`,
		hashToken(token), email, exp); err != nil {
		return "", err
	}
	return token, nil
}

// ConsumeLoginToken validates and deletes a magic-link token, returning the
// associated email. Single-use: the row is removed on success.
func (s *Store) ConsumeLoginToken(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", fmt.Errorf("missing token")
	}
	h := hashToken(token)
	var email string
	var exp time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT email,expires_at FROM member_login_tokens WHERE token_hash=?`, h).Scan(&email, &exp)
	if err != nil {
		return "", fmt.Errorf("invalid or expired link")
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM member_login_tokens WHERE token_hash=?`, h)
	if time.Now().UTC().After(exp.UTC()) {
		return "", fmt.Errorf("link expired")
	}
	return email, nil
}

// ---- member sessions --------------------------------------------------------

// CreateSession issues a member session and returns the raw token.
func (s *Store) CreateSession(ctx context.Context, memberID string) (string, error) {
	token := randHex(32)
	exp := time.Now().UTC().Add(SessionTTL).Format("2006-01-02 15:04:05")
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO member_sessions(token_hash,member_id,expires_at) VALUES(?,?,?)`,
		hashToken(token), memberID, exp); err != nil {
		return "", err
	}
	return token, nil
}

// ValidateSession resolves a session token to a live member.
func (s *Store) ValidateSession(ctx context.Context, token string) (*Member, error) {
	if token == "" {
		return nil, fmt.Errorf("no session")
	}
	var memberID string
	var exp time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT member_id,expires_at FROM member_sessions WHERE token_hash=?`, hashToken(token)).
		Scan(&memberID, &exp)
	if err != nil {
		return nil, fmt.Errorf("invalid session")
	}
	if time.Now().UTC().After(exp.UTC()) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM member_sessions WHERE token_hash=?`, hashToken(token))
		return nil, fmt.Errorf("session expired")
	}
	return s.GetByID(ctx, memberID)
}

// DestroySession removes a member session.
func (s *Store) DestroySession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM member_sessions WHERE token_hash=?`, hashToken(token))
	return err
}

// PurgeExpired clears expired login tokens and sessions.
func (s *Store) PurgeExpired(ctx context.Context) (int64, error) {
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	var total int64
	for _, tbl := range []string{"member_login_tokens", "member_sessions"} {
		res, err := s.db.ExecContext(ctx, "DELETE FROM "+tbl+" WHERE expires_at<?", now)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

// ---- per-article access -----------------------------------------------------

// SetAccess sets the access level for a slug (public|members).
func (s *Store) SetAccess(ctx context.Context, slug, level string) error {
	if level != AccessPublic && level != AccessMembers && level != AccessPaid {
		return fmt.Errorf("invalid access level %q", level)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO article_access(slug,level) VALUES(?,?)
		 ON CONFLICT(slug) DO UPDATE SET level=excluded.level`, slug, level)
	return err
}

// GetAccess returns the access level for a slug, defaulting to public.
func (s *Store) GetAccess(ctx context.Context, slug string) string {
	var level string
	err := s.db.QueryRowContext(ctx, `SELECT level FROM article_access WHERE slug=?`, slug).Scan(&level)
	if err != nil || level == "" {
		return AccessPublic
	}
	return level
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
