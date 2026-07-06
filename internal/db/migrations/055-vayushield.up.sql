-- VayuShield — sovereign bot protection subsystem (Part 7 schema).
-- IMPORTANT: the migration runner executes ONE statement per line, so every
-- statement below MUST stay on a single line (see internal/db/db.go).
--
-- vayushield_signatures is the self-learning bot signature knowledge base. It
-- holds one row per distinct composite fingerprint, carrying the TLS (JA3/JA4),
-- HTTP/2 SETTINGS and header-order sub-hashes, a classification and a confidence
-- score. Rows are either auto_learned (promoted by the background reporter) or
-- operator_verified (reviewed in the VayuOS review queue). No IP is stored here.
CREATE TABLE IF NOT EXISTS vayushield_signatures(id INTEGER PRIMARY KEY AUTOINCREMENT,fingerprint_hash TEXT NOT NULL UNIQUE,ja3_hash TEXT NOT NULL DEFAULT '',ja4_hash TEXT NOT NULL DEFAULT '',http2_settings_hash TEXT NOT NULL DEFAULT '',header_order_hash TEXT NOT NULL DEFAULT '',user_agent_pattern TEXT NOT NULL DEFAULT '',ip_range_hint TEXT NOT NULL DEFAULT '',post_quantum_present INTEGER NOT NULL DEFAULT 0,classification TEXT NOT NULL DEFAULT 'unknown',bot_name TEXT NOT NULL DEFAULT '',confidence REAL NOT NULL DEFAULT 0.5,first_seen DATETIME NOT NULL,last_seen DATETIME NOT NULL,request_count INTEGER NOT NULL DEFAULT 1,false_positive_count INTEGER NOT NULL DEFAULT 0,auto_learned INTEGER NOT NULL DEFAULT 0,operator_verified INTEGER NOT NULL DEFAULT 0,notes TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS idx_signatures_fingerprint ON vayushield_signatures(fingerprint_hash);
CREATE INDEX IF NOT EXISTS idx_signatures_classification ON vayushield_signatures(classification);
CREATE INDEX IF NOT EXISTS idx_signatures_review ON vayushield_signatures(operator_verified,confidence);
-- vayushield_challenges records the outcome of every issued challenge. bot_score
-- and fingerprint are retained for tuning; ip_hash is a salted daily-rotating
-- hash (never a plaintext IP) so the row cannot re-identify a visitor.
CREATE TABLE IF NOT EXISTS vayushield_challenges(id INTEGER PRIMARY KEY AUTOINCREMENT,session_hash TEXT NOT NULL DEFAULT '',challenge_type TEXT NOT NULL,bot_score REAL NOT NULL DEFAULT 0,fingerprint_hash TEXT NOT NULL DEFAULT '',outcome TEXT NOT NULL DEFAULT '',time_to_solve_ms INTEGER NOT NULL DEFAULT 0,ip_hash TEXT NOT NULL DEFAULT '',country_code TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS idx_challenges_created ON vayushield_challenges(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_challenges_type ON vayushield_challenges(challenge_type,created_at);
-- vayushield_blocked records hard blocks for operator review + false-positive
-- whitelisting. user_agent is kept (not PII) but no plaintext IP is stored.
CREATE TABLE IF NOT EXISTS vayushield_blocked(id INTEGER PRIMARY KEY AUTOINCREMENT,fingerprint_hash TEXT NOT NULL DEFAULT '',ja3_hash TEXT NOT NULL DEFAULT '',ip_hash TEXT NOT NULL DEFAULT '',user_agent TEXT NOT NULL DEFAULT '',request_path TEXT NOT NULL DEFAULT '',block_reason TEXT NOT NULL DEFAULT '',bot_score REAL NOT NULL DEFAULT 0,country_code TEXT NOT NULL DEFAULT '',operator_reviewed INTEGER NOT NULL DEFAULT 0,false_positive INTEGER NOT NULL DEFAULT 0,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS idx_blocked_created ON vayushield_blocked(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_blocked_review ON vayushield_blocked(operator_reviewed);
