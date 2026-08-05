-- Down for 087: drop the inbox and its indexes.
--
-- Owned outright by VayuFlow and read by nothing else. Dropping it loses only
-- undrained triggers, which is the correct outcome for a rollback: the flows
-- that would have consumed them are going away too.
DROP INDEX IF EXISTS idx_vayuflow_inbox_pending;
DROP INDEX IF EXISTS idx_vayuflow_inbox_event;
DROP INDEX IF EXISTS idx_vayuflow_inbox_eventid;
DROP TABLE IF EXISTS vayuflow_inbox;
