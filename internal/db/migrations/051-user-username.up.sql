-- Human-readable author handles: /author/<username> instead of /author/<id>.
-- Nullable-style empty default; a partial unique index keeps non-empty handles
-- unique while allowing many rows with the empty placeholder before backfill.
ALTER TABLE users ADD COLUMN username TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username ON users(username) WHERE username != '';
