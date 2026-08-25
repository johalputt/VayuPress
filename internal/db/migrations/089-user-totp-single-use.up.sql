-- Migration 089: single-use TOTP (audit finding).
--
-- A captured code stayed valid for its whole ±30s acceptance window and could
-- be replayed within it. totp_last_step records the last time step whose code
-- was consumed for this account; verification refuses any step that is not
-- strictly newer, so each code dies at first use.
ALTER TABLE users ADD COLUMN totp_last_step INTEGER NOT NULL DEFAULT 0;
