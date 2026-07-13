-- VayuDomains Stage 4 — per-domain member attribution.
--
-- IMPORTANT: the migration runner executes ONE statement per physical LINE
-- (it splits Up on '\n'), so every statement below MUST stay on a single line.
-- This migration is checksummed and immutable once shipped (ADR-0033/0034).
--
-- Additive only: a new domain_id column (default '' = the primary / unassigned
-- bucket) records which domain a member signed up on, so an existing single-
-- domain install is byte-identical (every existing member stays domain_id='').
-- Login stays keyed by the globally-UNIQUE email, so this changes no read path;
-- domain_id is attribution + per-domain counts. Relaxing the global UNIQUE(email)
-- to UNIQUE(domain_id,email) — so the same address can be a member of two domains
-- — needs a members-table rebuild and is a deferred sub-stage (ADR-0132 #2).
ALTER TABLE members ADD COLUMN domain_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_members_domain ON members(domain_id);
