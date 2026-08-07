-- Migration 088: let the sessions table hold a principal that is not a users row.
--
-- The migration runner executes ONE statement per physical LINE.
--
-- THE DEFECT. Migration 020 declared sessions.user_id as a foreign key into
-- users(id), and every connection opens with PRAGMA foreign_keys=ON. But the
-- column has never held only users ids: a VayuMail mailbox signing in at
-- /os/login is stored as "vmail:<address>" (admin_os_ui.go), and
-- resolveMailSessionUser says so in as many words -- "the synthesized user is
-- never persisted". There is no users row for it, so the INSERT was refused and
-- the mailbox fallback answered "could not start session". A documented sign-in
-- route could not issue a session at all.
--
-- WHY DROPPING THE CONSTRAINT IS THE HONEST FIX rather than minting a shadow
-- users row per mailbox. A users row is an identity with a role, a profile and
-- an author URL; a mailbox that signs in already resolves to one of those when a
-- matching CMS account exists, and deliberately does NOT when it does not. Two
-- rows describing one person, one of them created only to satisfy a constraint,
-- is a data model built around a check rather than the other way round.
--
-- WHAT IS LOST, AND WHERE IT WENT. The old constraint carried ON DELETE CASCADE,
-- so removing a staff account also removed its sessions. That is behaviour worth
-- keeping and it is now explicit: handleUserDelete calls
-- SessionStore.DestroyForUser, which ends every session for a principal and
-- reports how many. Explicit is better here anyway -- the cascade was invisible,
-- untested, and silently absent for the "vmail:" half of the column.
CREATE TABLE IF NOT EXISTS sessions_rebuild(token_hash TEXT PRIMARY KEY,user_id TEXT NOT NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,expires_at DATETIME NOT NULL);
INSERT OR IGNORE INTO sessions_rebuild(token_hash,user_id,created_at,expires_at) SELECT token_hash,user_id,created_at,expires_at FROM sessions;
DROP TABLE sessions;
ALTER TABLE sessions_rebuild RENAME TO sessions;
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expiry ON sessions(expires_at);
