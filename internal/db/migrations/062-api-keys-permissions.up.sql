-- VayuAPI — fine-grained per-key permissions, ownership, expiry and usage (v3.13.37).
--
-- IMPORTANT: the migration runner executes ONE statement per physical LINE
-- (it splits Up on '\n'), so every statement below MUST stay on a single line.
-- This migration is checksummed and immutable once shipped (ADR-0033/0034).
--
-- Additive only, so an existing install is byte-identical for existing keys:
--   * permissions   — JSON grant blob {"v":1,"grants":{"posts":["read","write"]}};
--                     '{}' (the default) means deny-all for a scoped external key.
--   * expires_at    — optional hard expiry; NULL = never expires.
--   * owner_user_id — the users.id that owns this key ('' = operator/system-owned,
--                     e.g. the legacy static and internal keys).
--   * rate_per_min  — per-key request budget; 0 = fall back to the global default.
--   * use_count     — monotonic call counter for the admin usage view.
--   * active        — soft on/off toggle, DISTINCT from revoked (which is terminal):
--                     a key can be deactivated and reactivated without rotating it.
-- The internal/system key and the static bootstrap key resolve to implicit
-- superuser in code, so this migration never changes their effective access.
ALTER TABLE vayu_api_keys ADD COLUMN permissions TEXT NOT NULL DEFAULT '{}';
ALTER TABLE vayu_api_keys ADD COLUMN expires_at DATETIME;
ALTER TABLE vayu_api_keys ADD COLUMN owner_user_id TEXT NOT NULL DEFAULT '';
ALTER TABLE vayu_api_keys ADD COLUMN rate_per_min INTEGER NOT NULL DEFAULT 0;
ALTER TABLE vayu_api_keys ADD COLUMN use_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE vayu_api_keys ADD COLUMN active INTEGER NOT NULL DEFAULT 1;
-- One-time backfill: every key that existed BEFORE fine-grained permissions had full API access. Stamp those keys (permissions still '{}') with a superuser grant so enforcement (a later release) never silently breaks live automation. Runs exactly once (migrations are checksummed/immutable); keys minted afterwards carry their own explicit grants and are untouched.
UPDATE vayu_api_keys SET permissions='{"v":1,"grants":{"*":["*"]}}' WHERE permissions='{}';
CREATE INDEX IF NOT EXISTS idx_vayu_api_keys_owner ON vayu_api_keys(owner_user_id,revoked);
