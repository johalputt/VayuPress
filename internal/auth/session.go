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
	db     *sql.DB // writer: session create/destroy/purge
	reader *sql.DB // read pool: session validation (runs on every admin request)
	ttl    time.Duration
}

// NewSessionStore creates a SessionStore with the default TTL. Reads default to
// the same handle as writes; call UseReader to point the hot validation path at
// a dedicated read pool.
func NewSessionStore(db *sql.DB) *SessionStore {
	return &SessionStore{db: db, reader: db, ttl: DefaultSessionTTL}
}

// UseReader routes read-only session queries (Validate) at a dedicated read
// pool instead of the single writer connection. Validate runs on EVERY VayuOS
// admin request, so on a saturated writer (busy_timeout waits) it otherwise
// serialises the whole admin panel behind unrelated writes — the cause of
// admin-only 502s after a restart while the public site (read pool + cache)
// stays fast. Writes stay on the writer; WAL gives the reader read-your-writes,
// so a freshly created session validates immediately. A nil reader is ignored.
func (s *SessionStore) UseReader(reader *sql.DB) {
	if reader != nil {
		s.reader = reader
	}
}

func (s *SessionStore) readDB() *sql.DB {
	if s.reader != nil {
		return s.reader
	}
	return s.db
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
	err := s.readDB().QueryRowContext(ctx,
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

// SetSessionCookieRemember writes the session cookie with hardened attributes,
// choosing its lifetime from the operator's "Remember me" choice.
//
// SameSite=Strict (audit F-6): the staff/admin session is only ever used for
// same-site navigation within /os, so Strict gives the strongest CSRF posture
// for these privileged, state-changing surfaces. (The reader/member cookie
// stays Lax because it must survive the cross-site top-level navigation of a
// magic-link click from an email client.)
//
// Lifetime:
//
//   - remember=true  → PERSISTENT: Max-Age = the session TTL, so the sign-in
//     survives browser restarts (the default, seamless posture).
//   - remember=false → a browser-SESSION cookie: no Max-Age/Expires, so the
//     browser drops it the moment it closes. On the next visit there is no cookie
//     to present, so the operator is signed out and must log in again — the right
//     posture for a shared or public machine.
//
// The server-side session record still expires on its own TTL; dropping the
// cookie is what logs the browser out.
func SetSessionCookieRemember(w http.ResponseWriter, token string, remember bool) {
	c := &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   CSRFCookieSecure(),
		SameSite: http.SameSiteStrictMode,
	}
	if remember {
		c.MaxAge = int(DefaultSessionTTL.Seconds())
	}
	http.SetCookie(w, c)
}

// ClearSessionCookie expires the session cookie on the client.
func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   CSRFCookieSecure(),
		SameSite: http.SameSiteStrictMode,
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
