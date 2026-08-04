-- Down for 085: drop the flow table and its indexes.
--
-- Unlike the additive column migrations, this one owns its table outright —
-- nothing else reads vayuflow_flows — so dropping it returns the schema exactly
-- to where it was. A down that leaves an orphan table behind would make the
-- next up migration's CREATE TABLE IF NOT EXISTS a silent no-op against stale
-- columns, which is worse than either state.
DROP INDEX IF EXISTS idx_vayuflow_enabled;
DROP INDEX IF EXISTS idx_vayuflow_owner;
DROP TABLE IF EXISTS vayuflow_flows;
