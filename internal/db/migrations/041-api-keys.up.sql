-- API key management & encrypted third-party service credentials (v1.17.0).
--
-- Two new tables back the VayuOS "API Keys" console:
--   * vayu_api_keys       — VayuPress's own issued bearer tokens. Only a
--                           SHA-256 hash of each token is stored; the raw token
--                           is shown once at creation/rotation and is never
--                           recoverable. Keys can be labelled, rotated and
--                           revoked at runtime (see internal/apikeys).
--   * service_credentials — third-party secrets (IndexNow, n8n, Ollama,
--                           OpenRouter, custom) sealed with AES-256-GCM under a
--                           key derived from the master secret. The plaintext is
--                           never stored; only the ciphertext + a masked hint
--                           for display (see internal/secrets, ADR-0088).
--
-- IMPORTANT: the migration runner executes ONE statement per line, so every
-- statement below MUST stay on a single line (see internal/db/db.go).

CREATE TABLE IF NOT EXISTS vayu_api_keys(id TEXT PRIMARY KEY,label TEXT NOT NULL DEFAULT '',prefix TEXT NOT NULL DEFAULT '',key_hash TEXT NOT NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,last_used_at DATETIME,revoked INTEGER NOT NULL DEFAULT 0);
CREATE UNIQUE INDEX IF NOT EXISTS idx_vayu_api_keys_hash ON vayu_api_keys(key_hash);
CREATE INDEX IF NOT EXISTS idx_vayu_api_keys_active ON vayu_api_keys(revoked, created_at);

CREATE TABLE IF NOT EXISTS service_credentials(id TEXT PRIMARY KEY,provider TEXT NOT NULL,label TEXT NOT NULL DEFAULT '',endpoint TEXT NOT NULL DEFAULT '',secret_nonce TEXT NOT NULL DEFAULT '',secret_ct TEXT NOT NULL DEFAULT '',hint TEXT NOT NULL DEFAULT '',enabled INTEGER NOT NULL DEFAULT 1,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS idx_service_credentials_provider ON service_credentials(provider, enabled);
