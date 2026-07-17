-- VayuShield Aegis — background-maintenance indexes (ADR-0137, v3.13.52).
--
-- IMPORTANT: the migration runner executes ONE statement per physical LINE
-- (it splits Up on '\n'), so every statement below MUST stay on a single line.
-- This migration is checksummed and immutable once shipped (ADR-0033/0034).
--
-- The self-maintaining detection cycle prunes stale auto-learned candidates and
-- promotes recurring ones. Without these composite indexes those queries full-
-- scan vayushield_signatures, holding the single SQLite writer for the whole
-- scan while telemetry writes stall — under a large swarm the signature table is
-- big, so the scan is exactly when the shield is busiest. These make the prune
-- and promote passes index-driven; additive, no data change.
CREATE INDEX IF NOT EXISTS idx_signatures_prune ON vayushield_signatures(operator_verified,last_seen,request_count);
CREATE INDEX IF NOT EXISTS idx_signatures_promote ON vayushield_signatures(auto_learned,operator_verified,classification,request_count);
