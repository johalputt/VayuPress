-- Reverse 064: drop the manual-sync gate column (additive-only migration).
ALTER TABLE domains DROP COLUMN sync_state;
