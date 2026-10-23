-- Migration 116 (down): the two trail tables return, empty, in the shape the
-- code before 116 writes (055, with 115's site and request columns), and
-- shield_events goes. The events recorded since are not copied back: the
-- older code would count them under the wrong shape, and the trail is a
-- report, not a record anything depends on.
CREATE TABLE IF NOT EXISTS vayushield_challenges(id INTEGER PRIMARY KEY AUTOINCREMENT,session_hash TEXT NOT NULL DEFAULT '',challenge_type TEXT NOT NULL,bot_score REAL NOT NULL DEFAULT 0,fingerprint_hash TEXT NOT NULL DEFAULT '',outcome TEXT NOT NULL DEFAULT '',time_to_solve_ms INTEGER NOT NULL DEFAULT 0,ip_hash TEXT NOT NULL DEFAULT '',country_code TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,domain_id TEXT NOT NULL DEFAULT '*',request_id TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS idx_challenges_type ON vayushield_challenges(challenge_type,created_at);
CREATE INDEX IF NOT EXISTS idx_challenges_created_outcome ON vayushield_challenges(created_at,outcome);
CREATE INDEX IF NOT EXISTS idx_challenges_domain_created_outcome ON vayushield_challenges(domain_id, created_at, outcome);
CREATE TABLE IF NOT EXISTS vayushield_blocked(id INTEGER PRIMARY KEY AUTOINCREMENT,fingerprint_hash TEXT NOT NULL DEFAULT '',ja3_hash TEXT NOT NULL DEFAULT '',ip_hash TEXT NOT NULL DEFAULT '',user_agent TEXT NOT NULL DEFAULT '',request_path TEXT NOT NULL DEFAULT '',block_reason TEXT NOT NULL DEFAULT '',bot_score REAL NOT NULL DEFAULT 0,country_code TEXT NOT NULL DEFAULT '',operator_reviewed INTEGER NOT NULL DEFAULT 0,false_positive INTEGER NOT NULL DEFAULT 0,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,domain_id TEXT NOT NULL DEFAULT '*',request_id TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS idx_blocked_created ON vayushield_blocked(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_blocked_review ON vayushield_blocked(operator_reviewed);
CREATE INDEX IF NOT EXISTS idx_blocked_domain_created ON vayushield_blocked(domain_id, created_at);
DROP INDEX IF EXISTS idx_shield_events_site;
DROP INDEX IF EXISTS idx_shield_events_created;
DROP TABLE IF EXISTS shield_events;
