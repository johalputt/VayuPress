-- Migration 116: one table for what VayuShield did, in one shape.
-- vayushield_blocked and vayushield_challenges held one event in two places
-- (a block wrote a row to each) and in two shapes, so no report could put a
-- block, a challenge and an operator rule's refusal on one line, and neither
-- recorded which gate or rule acted, the status answered or what deciding
-- cost. shield_events is that line: site, request, layer (the gate), rule,
-- action (block, challenge, tarpit, rate_limit), outcome (for a challenge:
-- issued, solved), reason, classification, path, status and the evaluation's
-- microseconds, beside the hashes the trail always kept. Existing rows move
-- across; the duplicate challenge row a block wrote ('blocked') is not kept,
-- as its block row is. Old rows carry no layer, rule, status or cost: those
-- were never recorded and are left empty rather than guessed.
-- One statement per line, as the runner requires.
CREATE TABLE IF NOT EXISTS shield_events(id INTEGER PRIMARY KEY, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, domain_id TEXT NOT NULL DEFAULT '*', request_id TEXT NOT NULL DEFAULT '', layer TEXT NOT NULL DEFAULT '', rule_id TEXT NOT NULL DEFAULT '', action TEXT NOT NULL, outcome TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL DEFAULT '', classification TEXT NOT NULL DEFAULT '', path TEXT NOT NULL DEFAULT '', status INTEGER NOT NULL DEFAULT 0, eval_us INTEGER NOT NULL DEFAULT 0, bot_score REAL NOT NULL DEFAULT 0, challenge_type TEXT NOT NULL DEFAULT '', time_to_solve_ms INTEGER NOT NULL DEFAULT 0, fingerprint_hash TEXT NOT NULL DEFAULT '', ja3_hash TEXT NOT NULL DEFAULT '', ip_hash TEXT NOT NULL DEFAULT '', ua_family TEXT NOT NULL DEFAULT '', country_code TEXT NOT NULL DEFAULT '');
INSERT INTO shield_events(created_at, domain_id, request_id, action, reason, path, bot_score, fingerprint_hash, ja3_hash, ip_hash, ua_family, country_code) SELECT created_at, domain_id, request_id, 'block', block_reason, request_path, bot_score, fingerprint_hash, ja3_hash, ip_hash, user_agent, country_code FROM vayushield_blocked;
INSERT INTO shield_events(created_at, domain_id, request_id, action, outcome, bot_score, challenge_type, time_to_solve_ms, fingerprint_hash, ip_hash, country_code) SELECT created_at, domain_id, request_id, CASE WHEN challenge_type='tarpit' THEN 'tarpit' ELSE 'challenge' END, outcome, bot_score, challenge_type, time_to_solve_ms, fingerprint_hash, ip_hash, country_code FROM vayushield_challenges WHERE outcome<>'blocked';
CREATE INDEX IF NOT EXISTS idx_shield_events_created ON shield_events(created_at, action, outcome);
CREATE INDEX IF NOT EXISTS idx_shield_events_site ON shield_events(domain_id, created_at, action, outcome);
DROP TABLE IF EXISTS vayushield_blocked;
DROP TABLE IF EXISTS vayushield_challenges;
