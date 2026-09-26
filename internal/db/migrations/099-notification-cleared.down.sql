-- Migration 099 (down): no Clear all; the bell shows every event of the last day and every condition again. notification_seen.cleared_at is retained: SQLite DROP COLUMN support is version-dependent, and nothing reads the column once this is down.
DROP TABLE IF EXISTS notification_dismissed;
