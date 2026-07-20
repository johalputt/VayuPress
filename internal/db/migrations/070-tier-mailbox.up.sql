-- Tier mail entitlements (migration 070). A paid tier can include a VayuMail
-- mailbox: mail_enabled=1 entitles the member to a mailbox, and mail_quota_mb
-- caps its storage (0 = unlimited). Consumed by member mailbox provisioning.
-- One statement per line: the migration runner executes newline-separated SQL.
ALTER TABLE member_tiers ADD COLUMN mail_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE member_tiers ADD COLUMN mail_quota_mb INTEGER NOT NULL DEFAULT 0;
