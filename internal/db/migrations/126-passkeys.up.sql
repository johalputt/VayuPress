-- Migration 126: passkeys. Each row is one passkey of one principal: the
-- install account that registered it ("user:" and its id) or the mailbox
-- ("vmail:" and its address); it signs in as exactly that. handle is the
-- WebAuthn user handle, random and the same for every passkey of a
-- principal, so a passkey found by the browser names no one. credential is
-- the verified credential (its public key, sign count and flags) as JSON;
-- no secret is stored. One statement per line, as the runner requires.
CREATE TABLE IF NOT EXISTS passkeys (id INTEGER PRIMARY KEY, principal TEXT NOT NULL, account TEXT NOT NULL, handle BLOB NOT NULL, credential_id BLOB NOT NULL UNIQUE, credential TEXT NOT NULL, name TEXT NOT NULL, created INTEGER NOT NULL, last_used INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS idx_passkeys_principal ON passkeys(principal);
CREATE INDEX IF NOT EXISTS idx_passkeys_handle ON passkeys(handle);
