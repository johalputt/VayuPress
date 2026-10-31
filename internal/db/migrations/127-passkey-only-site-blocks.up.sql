-- Migration 127: an account or mailbox that signs in by passkey only (its own
-- password is refused everywhere while it is set), and the reach of a sign-in
-- block: 'signin', the sign-in doors alone, or 'site', every request to the
-- install. One statement per line, as the runner requires.
CREATE TABLE IF NOT EXISTS passkey_only (principal TEXT PRIMARY KEY, since INTEGER NOT NULL) WITHOUT ROWID;
ALTER TABLE signin_blocks ADD COLUMN scope TEXT NOT NULL DEFAULT 'signin';
