package oauth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"testing"

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
		`CREATE TABLE oauth_refresh_tokens(token_hash TEXT PRIMARY KEY, client_id TEXT NOT NULL, api_key_id TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
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
	// Wrong client is refused.
	if _, err := s.ExchangeRefresh(ctx, raw, "vpc_other"); err != ErrMismatch {
		t.Errorf("wrong client: got %v, want ErrMismatch", err)
	}
	// ...and the failed exchange consumed it (rotation semantics), so the right
	// client can no longer use it.
	if _, err := s.ExchangeRefresh(ctx, raw, "vpc_x"); err != ErrNotFound {
		t.Errorf("refresh must be single-use even after a mismatch: got %v", err)
	}
	// A fresh refresh token resolves to its bound key exactly once.
	raw2, _ := s.IssueRefresh(ctx, "vpc_x", "key-1")
	id, err := s.ExchangeRefresh(ctx, raw2, "vpc_x")
	if err != nil || id != "key-1" {
		t.Fatalf("valid refresh: id=%q err=%v", id, err)
	}
	if _, err := s.ExchangeRefresh(ctx, raw2, "vpc_x"); err != ErrNotFound {
		t.Errorf("refresh replay must fail: got %v", err)
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
