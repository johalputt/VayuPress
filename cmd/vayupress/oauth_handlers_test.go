package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/oauth"
)

// pinDomain fixes the canonical domain for a test's duration so the OAuth issuer
// — which prefers the configured domain when the request Host is not a
// registered served domain (audit I1) — is deterministic regardless of the
// ambient global config other tests may have set.
func pinDomain(t *testing.T, d string) {
	t.Helper()
	old := config.Cfg.Domain
	config.Cfg.Domain = d
	t.Cleanup(func() { config.Cfg.Domain = old })
}

func oauthTestApp(t *testing.T) *App {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	for _, s := range []string{
		`CREATE TABLE oauth_clients(client_id TEXT PRIMARY KEY, client_name TEXT NOT NULL DEFAULT '', redirect_uris TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE oauth_codes(code_hash TEXT PRIMARY KEY, client_id TEXT NOT NULL, redirect_uri TEXT NOT NULL, code_challenge TEXT NOT NULL, grant_caps TEXT NOT NULL, owner_user_id TEXT NOT NULL DEFAULT '', label TEXT NOT NULL DEFAULT '', expires_at DATETIME NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE oauth_refresh_tokens(token_hash TEXT PRIMARY KEY, client_id TEXT NOT NULL, api_key_id TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, expires_at DATETIME, consumed_at DATETIME)`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	return &App{oauth: oauth.New(db)}
}

func TestOAuthASMetadata(t *testing.T) {
	pinDomain(t, "blog.example.com")
	a := &App{}
	req := httptest.NewRequest(http.MethodGet, "http://blog.example.com/.well-known/oauth-authorization-server", nil)
	rec := httptest.NewRecorder()
	a.handleOAuthASMetadata(rec, req)
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	// The issuer scheme is derived from the host (seo.Origin), not the request's
	// transport: a clearnet domain is always https (VayuPress serves behind a
	// TLS-terminating proxy), and only a .onion would be http (audit I1).
	if m["issuer"] != "https://blog.example.com" {
		t.Errorf("issuer = %v", m["issuer"])
	}
	if m["authorization_endpoint"] != "https://blog.example.com/oauth/authorize" || m["token_endpoint"] != "https://blog.example.com/oauth/token" {
		t.Errorf("wrong endpoints: %v", m)
	}
	methods, _ := m["code_challenge_methods_supported"].([]any)
	if len(methods) != 1 || methods[0] != "S256" {
		t.Errorf("must advertise S256 PKCE only, got %v", m["code_challenge_methods_supported"])
	}
}

func TestOAuthResourceMetadata(t *testing.T) {
	pinDomain(t, "blog.example.com")
	a := &App{}
	req := httptest.NewRequest(http.MethodGet, "https://blog.example.com/.well-known/oauth-protected-resource", nil)
	rec := httptest.NewRecorder()
	a.handleOAuthResourceMetadata(rec, req)
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	if m["resource"] != "https://blog.example.com/mcp" {
		t.Errorf("resource = %v, want the /mcp URL", m["resource"])
	}
	as, _ := m["authorization_servers"].([]any)
	if len(as) != 1 || as[0] != "https://blog.example.com" {
		t.Errorf("authorization_servers = %v", m["authorization_servers"])
	}
}

// TestMCPChallenge proves an unauthenticated /mcp request gets a 401 with the RFC
// 9728 WWW-Authenticate challenge pointing MCP clients at the resource metadata.
func TestMCPChallenge(t *testing.T) {
	pinDomain(t, "blog.example.com")
	a := &App{}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { t.Error("must not reach handler without a key") })
	h := a.requireMCPAuth(next)
	req := httptest.NewRequest(http.MethodPost, "https://blog.example.com/mcp", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	wa := rec.Header().Get("WWW-Authenticate")
	if !strings.Contains(wa, "Bearer") || !strings.Contains(wa, "https://blog.example.com/.well-known/oauth-protected-resource") {
		t.Errorf("WWW-Authenticate must point at the resource metadata, got %q", wa)
	}
}

func TestOAuthAuthorizeRejectsUnknownClient(t *testing.T) {
	a := oauthTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "https://x.example.com/oauth/authorize?client_id=nope&redirect_uri=https://claude.ai/cb&response_type=code&code_challenge=abc&code_challenge_method=S256", nil)
	rec := httptest.NewRecorder()
	a.handleOAuthAuthorize(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown client status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not registered") {
		t.Errorf("expected a local error page (never a redirect) for an unknown client")
	}
	if rec.Header().Get("Location") != "" {
		t.Error("must NOT redirect for an unknown/unvalidated client (open-redirect guard)")
	}
}

func TestOAuthAuthorizeRejectsBadRedirect(t *testing.T) {
	a := oauthTestApp(t)
	c, err := a.oauth.RegisterClient(req(t).Context(), "Claude", []string{"https://claude.ai/cb"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	// A redirect_uri not registered for the client must be refused locally, never
	// redirected to.
	u := "https://x.example.com/oauth/authorize?client_id=" + c.ID + "&redirect_uri=https://evil.example.com/cb&response_type=code&code_challenge=abc&code_challenge_method=S256"
	r := httptest.NewRequest(http.MethodGet, u, nil)
	rec := httptest.NewRecorder()
	a.handleOAuthAuthorize(rec, r)
	if rec.Code != http.StatusBadRequest || rec.Header().Get("Location") != "" {
		t.Errorf("unregistered redirect must be refused locally (got status %d, location %q)", rec.Code, rec.Header().Get("Location"))
	}
}

// req returns a throwaway request for its context in table tests.
func req(t *testing.T) *http.Request {
	t.Helper()
	return httptest.NewRequest(http.MethodGet, "http://x/", nil)
}
