-- Audit L9: give rotating refresh tokens a finite lifetime and enable reuse
-- detection. expires_at bounds the token; consumed_at marks a token as spent so
-- a later replay of the same (rotated-out) token is detectable as a breach.
-- NULL in either column means "legacy token" — treated as still valid, so
-- connectors issued before this migration keep working until their next refresh.
ALTER TABLE oauth_refresh_tokens ADD COLUMN expires_at DATETIME;
ALTER TABLE oauth_refresh_tokens ADD COLUMN consumed_at DATETIME;
