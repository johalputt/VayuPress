-- Migration 097 (down): forget read marks; every recent event reads as new again.
DROP TABLE IF EXISTS notification_seen;
