-- Per-member preference: email me when someone replies to my comment. Defaults
-- to 1 (on) so existing members are notified unless they opt out in the portal.
ALTER TABLE members ADD COLUMN reply_notify INTEGER NOT NULL DEFAULT 1;
