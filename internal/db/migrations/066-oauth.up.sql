-- OAuth 2.1 authorization server (ADR-0140, VayuMCP Stage 4) — the one-click
-- "Connect" flow on claude.ai and any MCP OAuth client.
--
-- IMPORTANT: the migration runner executes ONE statement per physical LINE (it
-- splits Up on '\n'), so every statement below MUST stay on a single line.
--
-- Access tokens are NOT stored here. On the /token exchange the server mints a
-- scoped VayuPress API key (internal/apikeys) and returns its token as the OAuth
-- access_token, so the existing capability model, per-key rate budget, audit log,
-- and revocation apply unchanged — and no bearer token is ever persisted at rest.
-- This schema only holds dynamically-registered clients (RFC 7591), single-use
-- PKCE-bound authorization codes carrying the grant to mint, and hashed refresh
-- tokens that rotate the underlying key.
CREATE TABLE IF NOT EXISTS oauth_clients(client_id TEXT PRIMARY KEY, client_name TEXT NOT NULL DEFAULT '', redirect_uris TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE IF NOT EXISTS oauth_codes(code_hash TEXT PRIMARY KEY, client_id TEXT NOT NULL, redirect_uri TEXT NOT NULL, code_challenge TEXT NOT NULL, grant_caps TEXT NOT NULL, owner_user_id TEXT NOT NULL DEFAULT '', label TEXT NOT NULL DEFAULT '', expires_at DATETIME NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS idx_oauth_codes_expires ON oauth_codes(expires_at);
CREATE TABLE IF NOT EXISTS oauth_refresh_tokens(token_hash TEXT PRIMARY KEY, client_id TEXT NOT NULL, api_key_id TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
