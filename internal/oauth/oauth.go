// Package oauth implements the storage and protocol primitives for VayuPress's
// OAuth 2.1 authorization server (ADR-0140) — the mechanism behind the one-click
// "Connect" flow on claude.ai and any MCP OAuth client.
//
// It is deliberately host-agnostic: it owns dynamically-registered clients,
// single-use PKCE-bound authorization codes, and hashed refresh tokens, plus the
// PKCE (S256) check and secure token generation. It does NOT mint access tokens —
// the host (cmd/vayupress) mints a scoped VayuPress API key at token-exchange time
// and returns that as the OAuth access_token, so the existing scoped-key model
// (capabilities, rate budget, audit, revocation) is reused verbatim and no bearer
// token is ever stored at rest.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
)

// CodeTTL is how long an authorization code is valid before it must be exchanged.
// Short, per OAuth 2.1 guidance — codes are single-use and consumed within seconds.
const CodeTTL = 5 * time.Minute

var (
	// ErrNotFound is returned when a client/code/refresh token does not exist.
	ErrNotFound = errors.New("oauth: not found")
	// ErrPKCE is returned when the PKCE verifier does not match the challenge.
	ErrPKCE = errors.New("oauth: PKCE verification failed")
	// ErrExpired is returned when an authorization code has expired.
	ErrExpired = errors.New("oauth: authorization code expired")
	// ErrMismatch is returned when the client_id or redirect_uri on exchange does
	// not match the values bound to the code.
	ErrMismatch = errors.New("oauth: client_id or redirect_uri mismatch")
)

// Store is the OAuth authorization-server persistence layer.
type Store struct{ db *sql.DB }

// New returns a Store backed by db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Client is a registered OAuth client.
type Client struct {
	ID           string
	Name         string
	RedirectURIs []string
	CreatedAt    time.Time
}

// CodeGrant is the data carried by an authorization code: everything needed at
// token-exchange time to mint the scoped key the operator approved.
type CodeGrant struct {
	ClientID      string
	RedirectURI   string
	CodeChallenge string // PKCE S256 challenge (base64url, no padding)
	GrantCaps     string // comma-separated "section:action" caps, or "*:*"
	OwnerUserID   string
	Label         string
}

// ── tokens ────────────────────────────────────────────────────────────────────

// randToken returns a URL-safe, unguessable token of nbytes of entropy.
func randToken(nbytes int) (string, error) {
	b := make([]byte, nbytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// ── clients (RFC 7591 dynamic registration) ────────────────────────────────────

// RegisterClient stores a new public client with the given name and redirect URIs
// and returns it (with a freshly generated client_id). At least one redirect URI
// is required; each must be an absolute https URL (or http://localhost for local
// development).
func (s *Store) RegisterClient(ctx context.Context, name string, redirectURIs []string) (Client, error) {
	clean := make([]string, 0, len(redirectURIs))
	for _, u := range redirectURIs {
		u = strings.TrimSpace(u)
		if u != "" && validRedirectURI(u) {
			clean = append(clean, u)
		}
	}
	if len(clean) == 0 {
		return Client{}, errors.New("oauth: at least one valid redirect_uri is required")
	}
	id, err := randToken(18)
	if err != nil {
		return Client{}, err
	}
	id = "vpc_" + id
	urisJSON, _ := json.Marshal(clean)
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO oauth_clients(client_id, client_name, redirect_uris, created_at) VALUES(?,?,?,?)`,
		id, name, string(urisJSON), now); err != nil {
		return Client{}, err
	}
	return Client{ID: id, Name: name, RedirectURIs: clean, CreatedAt: now}, nil
}

// GetClient returns the registered client, or ErrNotFound.
func (s *Store) GetClient(ctx context.Context, clientID string) (Client, error) {
	var c Client
	var uris string
	err := s.db.QueryRowContext(ctx,
		`SELECT client_id, client_name, redirect_uris, created_at FROM oauth_clients WHERE client_id=?`, clientID).
		Scan(&c.ID, &c.Name, &uris, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return Client{}, ErrNotFound
	}
	if err != nil {
		return Client{}, err
	}
	_ = json.Unmarshal([]byte(uris), &c.RedirectURIs)
	return c, nil
}

// RedirectAllowed reports whether uri exactly matches one of the client's
// registered redirect URIs (exact string match, per OAuth 2.1 — no prefix/pattern).
func (c Client) RedirectAllowed(uri string) bool {
	for _, u := range c.RedirectURIs {
		if subtle.ConstantTimeCompare([]byte(u), []byte(uri)) == 1 {
			return true
		}
	}
	return false
}

// ── authorization codes ─────────────────────────────────────────────────────────

// IssueCode stores a single-use authorization code for the grant and returns the
// raw code (only its hash is persisted). Callers put the raw code in the redirect.
func (s *Store) IssueCode(ctx context.Context, g CodeGrant) (string, error) {
	// Opportunistic housekeeping: drop any expired codes so the table stays small
	// without a dedicated sweeper (codes are also deleted on exchange).
	_, _ = s.CleanupExpiredCodes(ctx)
	raw, err := randToken(32)
	if err != nil {
		return "", err
	}
	exp := time.Now().UTC().Add(CodeTTL)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO oauth_codes(code_hash, client_id, redirect_uri, code_challenge, grant_caps, owner_user_id, label, expires_at)
		 VALUES(?,?,?,?,?,?,?,?)`,
		hashToken(raw), g.ClientID, g.RedirectURI, g.CodeChallenge, g.GrantCaps, g.OwnerUserID, g.Label, exp); err != nil {
		return "", err
	}
	return raw, nil
}

// ExchangeCode atomically consumes an authorization code: it verifies the code
// exists, has not expired, matches the presented client_id and redirect_uri, and
// that the PKCE verifier satisfies the stored S256 challenge; then it deletes the
// code (single-use) and returns its grant. Any failure returns a typed error and
// the code is NOT consumed (except a successful exchange).
func (s *Store) ExchangeCode(ctx context.Context, rawCode, clientID, redirectURI, codeVerifier string) (CodeGrant, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CodeGrant{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	var g CodeGrant
	var exp time.Time
	err = tx.QueryRowContext(ctx,
		`SELECT client_id, redirect_uri, code_challenge, grant_caps, owner_user_id, label, expires_at
		 FROM oauth_codes WHERE code_hash=?`, hashToken(rawCode)).
		Scan(&g.ClientID, &g.RedirectURI, &g.CodeChallenge, &g.GrantCaps, &g.OwnerUserID, &g.Label, &exp)
	if err == sql.ErrNoRows {
		return CodeGrant{}, ErrNotFound
	}
	if err != nil {
		return CodeGrant{}, err
	}
	// Delete first so a replay (even concurrent) can consume the code at most once.
	if _, err := tx.ExecContext(ctx, `DELETE FROM oauth_codes WHERE code_hash=?`, hashToken(rawCode)); err != nil {
		return CodeGrant{}, err
	}
	if time.Now().After(exp) {
		_ = tx.Commit() // still remove the expired code
		return CodeGrant{}, ErrExpired
	}
	if subtle.ConstantTimeCompare([]byte(g.ClientID), []byte(clientID)) != 1 ||
		subtle.ConstantTimeCompare([]byte(g.RedirectURI), []byte(redirectURI)) != 1 {
		_ = tx.Commit()
		return CodeGrant{}, ErrMismatch
	}
	if !VerifyPKCE(g.CodeChallenge, codeVerifier) {
		_ = tx.Commit()
		return CodeGrant{}, ErrPKCE
	}
	if err := tx.Commit(); err != nil {
		return CodeGrant{}, err
	}
	return g, nil
}

// ── refresh tokens ──────────────────────────────────────────────────────────────

// IssueRefresh stores a hashed refresh token bound to a client and the minted key
// id, returning the raw refresh token.
func (s *Store) IssueRefresh(ctx context.Context, clientID, apiKeyID string) (string, error) {
	raw, err := randToken(32)
	if err != nil {
		return "", err
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO oauth_refresh_tokens(token_hash, client_id, api_key_id) VALUES(?,?,?)`,
		hashToken(raw), clientID, apiKeyID); err != nil {
		return "", err
	}
	return raw, nil
}

// ExchangeRefresh consumes a refresh token (single-use, rotating): it verifies the
// token exists and matches the client, deletes it, and returns the bound key id so
// the host can rotate that key and issue a fresh refresh token. ErrNotFound/
// ErrMismatch on failure.
func (s *Store) ExchangeRefresh(ctx context.Context, rawRefresh, clientID string) (apiKeyID string, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback() //nolint:errcheck
	var boundClient string
	err = tx.QueryRowContext(ctx,
		`SELECT client_id, api_key_id FROM oauth_refresh_tokens WHERE token_hash=?`, hashToken(rawRefresh)).
		Scan(&boundClient, &apiKeyID)
	if err == sql.ErrNoRows {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	// Verify the presenting client BEFORE consuming the token: a wrong client_id
	// must be a harmless rejection, not destroy a valid token (a DoS otherwise, as
	// the legitimate client's next refresh would then fail). The deferred Rollback
	// leaves the token intact on mismatch.
	if subtle.ConstantTimeCompare([]byte(boundClient), []byte(clientID)) != 1 {
		return "", ErrMismatch
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM oauth_refresh_tokens WHERE token_hash=?`, hashToken(rawRefresh)); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return apiKeyID, nil
}

// RevokeRefreshForKey deletes any refresh tokens bound to an API key id (used when
// a connector is revoked so its refresh token cannot mint a new access token).
func (s *Store) RevokeRefreshForKey(ctx context.Context, apiKeyID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM oauth_refresh_tokens WHERE api_key_id=?`, apiKeyID)
	return err
}

// CleanupExpiredCodes deletes authorization codes past their expiry (housekeeping).
func (s *Store) CleanupExpiredCodes(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM oauth_codes WHERE expires_at < ?`, time.Now().UTC())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ── PKCE ─────────────────────────────────────────────────────────────────────────

// VerifyPKCE reports whether verifier satisfies the S256 challenge, i.e.
// base64url(sha256(verifier)) == challenge, in constant time. Only S256 is
// supported (OAuth 2.1 forbids the "plain" method).
func VerifyPKCE(challenge, verifier string) bool {
	if challenge == "" || verifier == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

// loopbackHosts are the only hosts permitted to use cleartext http as a redirect
// URI (local-development clients). Everything else must be https.
var loopbackHosts = map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}

// validRedirectURI accepts absolute https URLs, and http only for a genuine
// loopback host (local development). It PARSES the URL and compares the real host
// — never a string prefix — so tricks like http://localhost.evil.com,
// http://127.0.0.1x.attacker.com, or the userinfo form http://localhost@evil.com
// (whose real host is evil.com) are rejected. Any fragment or userinfo component
// is rejected outright (OAuth 2.1).
func validRedirectURI(raw string) bool {
	if strings.Contains(raw, "#") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Fragment != "" || u.User != nil {
		return false
	}
	host := u.Hostname() // strips port and IPv6 brackets; excludes userinfo
	if host == "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		return loopbackHosts[host]
	default:
		return false
	}
}
