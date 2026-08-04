-- Down for 086: drop the run trail and its indexes.
--
-- This table is owned outright by VayuFlow and read by nothing else, so
-- dropping it returns the schema exactly to where it was. Leaving it behind
-- would make the next up migration's CREATE TABLE IF NOT EXISTS a silent no-op
-- against stale columns — and the column that would silently persist is the
-- UNIQUE idempotency index, which is the one piece of this design that must
-- never be half-present.
DROP INDEX IF EXISTS idx_vayuflow_runs_idem;
DROP INDEX IF EXISTS idx_vayuflow_runs_flow;
DROP INDEX IF EXISTS idx_vayuflow_runs_status;
DROP TABLE IF EXISTS vayuflow_runs;
