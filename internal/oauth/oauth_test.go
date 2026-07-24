package oauth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	schema := []string{
		`CREATE TABLE oauth_clients(client_id TEXT PRIMARY KEY, client_name TEXT NOT NULL DEFAULT '', redirect_uris TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE oauth_codes(code_hash TEXT PRIMARY KEY, client_id TEXT NOT NULL, redirect_uri TEXT NOT NULL, code_challenge TEXT NOT NULL, grant_caps TEXT NOT NULL, owner_user_id TEXT NOT NULL DEFAULT '', label TEXT NOT NULL DEFAULT '', expires_at DATETIME NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE oauth_refresh_tokens(token_hash TEXT PRIMARY KEY, client_id TEXT NOT NULL, api_key_id TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, expires_at DATETIME, consumed_at DATETIME)`,
	}
	for _, s := range schema {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	return New(db)
}

func challengeFor(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func TestVerifyPKCE(t *testing.T) {
	v := "the-quick-brown-fox-verifier-string-1234567890"
	ch := challengeFor(v)
	if !VerifyPKCE(ch, v) {
		t.Error("correct verifier must satisfy the S256 challenge")
	}
	if VerifyPKCE(ch, "wrong-verifier") {
		t.Error("wrong verifier must NOT satisfy the challenge")
	}
	if VerifyPKCE("", v) || VerifyPKCE(ch, "") {
		t.Error("empty challenge/verifier must fail (no plain fallback)")
	}
}

func TestRegisterClientAndRedirectMatch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	c, err := s.RegisterClient(ctx, "Claude", []string{"https://claude.ai/api/mcp/auth_callback", "javascript:alert(1)", "https://evil.com/#frag"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if len(c.RedirectURIs) != 1 || c.RedirectURIs[0] != "https://claude.ai/api/mcp/auth_callback" {
		t.Errorf("only valid https redirect should be kept, got %v", c.RedirectURIs)
	}
	got, err := s.GetClient(ctx, c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.RedirectAllowed("https://claude.ai/api/mcp/auth_callback") {
		t.Error("registered redirect must be allowed (exact match)")
	}
	if got.RedirectAllowed("https://claude.ai/api/mcp/auth_callback/") {
		t.Error("a non-exact redirect (trailing slash) must NOT be allowed")
	}
	if _, err := s.RegisterClient(ctx, "bad", []string{"http://evil.com/cb"}); err == nil {
		t.Error("a client with no valid (https/localhost) redirect must be rejected")
	}
}

// TestValidRedirectURIRejectsHostTricks is the regression test for the HIGH
// finding: the http loopback exception must match the real HOST, not a string
// prefix, so subdomain/userinfo tricks cannot register a cleartext off-site URI.
func TestValidRedirectURIRejectsHostTricks(t *testing.T) {
	good := []string{
		"https://claude.ai/api/mcp/auth_callback",
		"https://example.com/cb?x=1",
		"http://localhost:8080/cb",
		"http://127.0.0.1/cb",
		"http://[::1]:9000/cb",
		// Private-use / custom URI schemes for native apps (RFC 8252 §7.1) — a
		// native MCP client that registers a scheme handler must be able to
		// register, or dynamic client registration fails with "couldn't register".
		"claudeai://callback",
		"com.anthropic.claude:/oauth2redirect",
		"com.example.app://cb",
	}
	for _, u := range good {
		if !validRedirectURI(u) {
			t.Errorf("valid redirect %q was rejected", u)
		}
	}
	bad := []string{
		"http://localhost.evil.com/cb",      // subdomain, not loopback
		"http://127.0.0.1x.attacker.com/cb", // prefix trick
		"http://localhost@evil.com/cb",      // userinfo → real host evil.com
		"http://127.0.0.1@evil.com/cb",      // userinfo trick
		"http://evil.com/cb",                // plain cleartext non-loopback
		"https://evil.com/cb#frag",          // fragment
		"https://user:pw@example.com/cb",    // userinfo on https
		"javascript:alert(1)",               // script scheme
		"data:text/html,<script>1</script>", // data scheme
		"vbscript:msgbox(1)",                // script scheme
		"file:///etc/passwd",                // local file scheme
		"blob:https://x/y",                  // blob scheme
	}
	for _, u := range bad {
		if validRedirectURI(u) {
			t.Errorf("dangerous redirect %q was ACCEPTED — must be rejected", u)
		}
	}
}

// TestLoopbackRedirectPortAgnostic locks in RFC 8252 §7.3: a native client that
// registers an http loopback redirect must be allowed to complete the flow even
// though the ephemeral listener port differs on the authorize/token call. The
// port is ignored for loopback; everything else (scheme, host, path, query) must
// still match, and non-loopback hosts get exact matching only.
func TestLoopbackRedirectPortAgnostic(t *testing.T) {
	c := Client{RedirectURIs: []string{"http://127.0.0.1:8976/callback", "https://claude.ai/cb"}}
	if !c.RedirectAllowed("http://127.0.0.1:51234/callback") {
		t.Error("loopback redirect with a different port must be allowed (RFC 8252 §7.3)")
	}
	if !c.RedirectAllowed("http://127.0.0.1:8976/callback") {
		t.Error("the exact registered loopback redirect must be allowed")
	}
	if c.RedirectAllowed("http://127.0.0.1:51234/evil") {
		t.Error("a different PATH must NOT match, even on loopback")
	}
	if c.RedirectAllowed("http://localhost:8976/callback") {
		t.Error("a different loopback HOST (localhost vs 127.0.0.1) must not port-match")
	}
	if c.RedirectAllowed("https://claude.ai/cb/") {
		t.Error("https redirects must still be EXACT-matched (trailing slash differs)")
	}
	if !c.RedirectAllowed("https://claude.ai/cb") {
		t.Error("the exact registered https redirect must be allowed")
	}
}

func TestCodeExchangeSingleUseAndPKCE(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	verifier := "verifier-abcdefghijklmnopqrstuvwxyz-0123456789"
	g := CodeGrant{
		ClientID: "vpc_x", RedirectURI: "https://claude.ai/cb",
		CodeChallenge: challengeFor(verifier), GrantCaps: "*:*", OwnerUserID: "u1", Label: "Claude",
	}
	raw, err := s.IssueCode(ctx, g)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	// Wrong PKCE verifier is refused.
	if _, err := s.ExchangeCode(ctx, raw, g.ClientID, g.RedirectURI, "not-the-verifier"); err != ErrPKCE {
		t.Errorf("wrong verifier: got %v, want ErrPKCE", err)
	}
	// A PKCE failure still consumes the code (single-use), so a retry with the
	// right verifier now finds nothing — proving the code cannot be brute-forced.
	if _, err := s.ExchangeCode(ctx, raw, g.ClientID, g.RedirectURI, verifier); err != ErrNotFound {
		t.Errorf("code must be single-use even after a failed exchange: got %v", err)
	}

	// A fresh code with the correct verifier succeeds exactly once.
	raw2, _ := s.IssueCode(ctx, g)
	got, err := s.ExchangeCode(ctx, raw2, g.ClientID, g.RedirectURI, verifier)
	if err != nil {
		t.Fatalf("valid exchange: %v", err)
	}
	if got.GrantCaps != "*:*" || got.OwnerUserID != "u1" {
		t.Errorf("exchange returned wrong grant: %+v", got)
	}
	if _, err := s.ExchangeCode(ctx, raw2, g.ClientID, g.RedirectURI, verifier); err != ErrNotFound {
		t.Errorf("code replay must fail: got %v", err)
	}
}

func TestCodeExchangeClientAndRedirectBinding(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	verifier := "verifier-abcdefghijklmnopqrstuvwxyz-0123456789"
	g := CodeGrant{ClientID: "vpc_x", RedirectURI: "https://claude.ai/cb", CodeChallenge: challengeFor(verifier), GrantCaps: "posts:write"}

	raw, _ := s.IssueCode(ctx, g)
	if _, err := s.ExchangeCode(ctx, raw, "vpc_other", g.RedirectURI, verifier); err != ErrMismatch {
		t.Errorf("wrong client_id must fail with ErrMismatch, got %v", err)
	}
	raw2, _ := s.IssueCode(ctx, g)
	if _, err := s.ExchangeCode(ctx, raw2, g.ClientID, "https://claude.ai/other", verifier); err != ErrMismatch {
		t.Errorf("wrong redirect_uri must fail with ErrMismatch, got %v", err)
	}
}

func TestRefreshRotation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	raw, err := s.IssueRefresh(ctx, "vpc_x", "key-1")
	if err != nil {
		t.Fatalf("issue refresh: %v", err)
	}
	// Wrong client is refused WITHOUT consuming the token (a wrong client_id must
	// not be able to burn a valid refresh token — that would be a DoS).
	if _, err := s.ExchangeRefresh(ctx, raw, "vpc_other"); err != ErrMismatch {
		t.Errorf("wrong client: got %v, want ErrMismatch", err)
	}
	// The rightful client can still use it (the mismatch did NOT consume it).
	if id, err := s.ExchangeRefresh(ctx, raw, "vpc_x"); err != nil || id != "key-1" {
		t.Errorf("token must survive a wrong-client attempt: id=%q err=%v", id, err)
	}
	// ...and replaying the consumed token now trips reuse detection (a breach
	// signal), returning the bound key id so the caller can revoke the grant.
	if id, err := s.ExchangeRefresh(ctx, raw, "vpc_x"); err != ErrReuse || id != "key-1" {
		t.Errorf("replay of a consumed refresh must be ErrReuse (key-1): id=%q err=%v", id, err)
	}
	// A fresh refresh token resolves to its bound key exactly once.
	raw2, _ := s.IssueRefresh(ctx, "vpc_x", "key-1")
	id, err := s.ExchangeRefresh(ctx, raw2, "vpc_x")
	if err != nil || id != "key-1" {
		t.Fatalf("valid refresh: id=%q err=%v", id, err)
	}
	if _, err := s.ExchangeRefresh(ctx, raw2, "vpc_x"); err != ErrReuse {
		t.Errorf("refresh replay must trip reuse detection: got %v", err)
	}
}

// A refresh token past its expiry is rejected with ErrExpired and removed.
func TestRefreshExpiry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	raw, err := s.IssueRefresh(ctx, "vpc_x", "key-exp")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// Force the token past its lifetime.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE oauth_refresh_tokens SET expires_at=? WHERE api_key_id='key-exp'`,
		time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("age token: %v", err)
	}
	if _, err := s.ExchangeRefresh(ctx, raw, "vpc_x"); err != ErrExpired {
		t.Errorf("expired refresh must be ErrExpired: got %v", err)
	}
}

func TestRevokeRefreshForKey(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	raw, _ := s.IssueRefresh(ctx, "vpc_x", "key-9")
	if err := s.RevokeRefreshForKey(ctx, "key-9"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := s.ExchangeRefresh(ctx, raw, "vpc_x"); err != ErrNotFound {
		t.Error("a refresh token for a revoked key must no longer work")
	}
}
