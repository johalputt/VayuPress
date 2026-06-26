package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
)

func trimBearer(v string) string { return strings.TrimPrefix(v, "Bearer ") }

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// SessionCookie is the cookie name carrying the opaque session token.
const SessionCookie = "vp_session"

// DefaultSessionTTL is how long a login session remains valid.
const DefaultSessionTTL = 7 * 24 * time.Hour

// SessionStore persists login sessions in SQLite. Only the SHA-256 of each
// token is stored, so a database leak cannot be replayed as a live session —
// the raw token exists only in the user's cookie.
type SessionStore struct {
	db  *sql.DB
	ttl time.Duration
}

// NewSessionStore creates a SessionStore with the default TTL.
func NewSessionStore(db *sql.DB) *SessionStore {
	return &SessionStore{db: db, ttl: DefaultSessionTTL}
}

// Create issues a new session for userID and returns the raw token to set in the
// client cookie.
func (s *SessionStore) Create(ctx context.Context, userID string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	expires := time.Now().UTC().Add(s.ttl)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions(token_hash,user_id,expires_at) VALUES(?,?,?)`,
		hashToken(token), userID, expires.Format("2006-01-02 15:04:05"))
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return token, nil
}

// Validate returns the user id for a live (unexpired) session token, or an
// error if the token is unknown or expired.
func (s *SessionStore) Validate(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", fmt.Errorf("no session")
	}
	var userID string
	var expires time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id,expires_at FROM sessions WHERE token_hash=?`, hashToken(token)).
		Scan(&userID, &expires)
	if err != nil {
		return "", fmt.Errorf("invalid session")
	}
	if time.Now().UTC().After(expires.UTC()) {
		_ = s.Destroy(ctx, token)
		return "", fmt.Errorf("session expired")
	}
	return userID, nil
}

// Destroy deletes the session for the given raw token.
func (s *SessionStore) Destroy(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, hashToken(token))
	return err
}

// PurgeExpired removes all sessions whose expiry has passed. Intended to run on
// a periodic sweep.
func (s *SessionStore) PurgeExpired(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at < ?`, time.Now().UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// SetSessionCookie writes the session cookie with hardened attributes.
func SetSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   csrfCookieSecure(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(DefaultSessionTTL.Seconds()),
	})
}

// ClearSessionCookie expires the session cookie on the client.
func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   csrfCookieSecure(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// SessionTokenFromRequest extracts the raw session token from the request cookie.
func SessionTokenFromRequest(r *http.Request) string {
	if c, err := r.Cookie(SessionCookie); err == nil {
		return c.Value
	}
	return ""
}

// hashToken returns the hex SHA-256 of a raw token for at-rest storage.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// HasValidAPIKey reports whether the request carries the configured API key.
// Used so a single guard can accept either an API key or a login session.
func HasValidAPIKey(r *http.Request) bool {
	key := r.Header.Get("X-API-Key")
	if key == "" {
		key = trimBearer(r.Header.Get("Authorization"))
	}
	return config.Cfg.APIKey != "" && constantTimeEqual(key, config.Cfg.APIKey) || verifyExtraAPIKey(key)
}
