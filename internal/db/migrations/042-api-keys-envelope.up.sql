-- Envelope encryption keyring + API-key scopes (v1.17.0).
--
-- Two changes make key management fully automatic:
--   * secret_keyring — a single-row keyring holding the Data Encryption Key
--     (DEK) that protects every stored third-party credential. Credentials are
--     encrypted with the DEK, NOT with the API key, so rotating the API key (or
--     any auth credential) never makes a stored secret undecryptable — nothing
--     has to be re-entered. The DEK is stored directly when no dedicated
--     encryption secret is configured (kek_src='none'), or wrapped by a key
--     derived from VAYU_SECRET when one is set (kek_src='env'). See ADR-0088.
--   * vayu_api_keys.scope — distinguishes the auto-provisioned internal/system
--     key (scope='internal'), which VayuPress manages itself and propagates to
--     internal consumers live on rotation, from operator-issued keys
--     (scope='external').
--
-- IMPORTANT: the migration runner executes ONE statement per line, so every
-- statement below MUST stay on a single line (see internal/db/db.go).

CREATE TABLE IF NOT EXISTS secret_keyring(id INTEGER PRIMARY KEY,dek TEXT NOT NULL,kek_src TEXT NOT NULL DEFAULT 'none',kek_check TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,rotated_at DATETIME);
ALTER TABLE vayu_api_keys ADD COLUMN scope TEXT NOT NULL DEFAULT 'external';
