-- Down for 088: restore the users foreign key on sessions.
--
-- Rolling back re-imposes a constraint the column's contents can violate, so any
-- "vmail:" session is dropped on the way rather than left to fail a later
-- integrity check. The holders are signed out and sign in again; the alternative
-- is a table that cannot be written to.
CREATE TABLE IF NOT EXISTS sessions_rollback(token_hash TEXT PRIMARY KEY,user_id TEXT NOT NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,expires_at DATETIME NOT NULL,FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE);
INSERT OR IGNORE INTO sessions_rollback(token_hash,user_id,created_at,expires_at) SELECT token_hash,user_id,created_at,expires_at FROM sessions WHERE user_id IN (SELECT id FROM users);
DROP TABLE sessions;
ALTER TABLE sessions_rollback RENAME TO sessions;
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expiry ON sessions(expires_at);
