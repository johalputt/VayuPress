-- Migration 117: alerts on a site's readers and on what its shield refused.
--
-- An alert counts one figure over a window of minutes (views, visitors,
-- searches that found nothing, refusals, challenges) for one hosted site or,
-- with '*', for the whole install, and fires when the count is above or below
-- its threshold. After firing it waits out its cooldown before it may fire
-- again. Each firing is a row of alert_events, which the bell shows until it
-- is marked seen and which the alert's page lists as its history.
CREATE TABLE IF NOT EXISTS analytics_alerts(id TEXT PRIMARY KEY, domain_id TEXT NOT NULL DEFAULT '*', name TEXT NOT NULL, metric TEXT NOT NULL, op TEXT NOT NULL CHECK(op IN ('above','below')), threshold INTEGER NOT NULL, window_min INTEGER NOT NULL, cooldown_min INTEGER NOT NULL, severity TEXT NOT NULL CHECK(severity IN ('info','warn','danger')), mail_to TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, checked_at DATETIME, last_value INTEGER, last_fired_at DATETIME);
CREATE INDEX IF NOT EXISTS idx_analytics_alerts_domain ON analytics_alerts(domain_id);
CREATE TABLE IF NOT EXISTS alert_events(id INTEGER PRIMARY KEY, alert_id TEXT NOT NULL, domain_id TEXT NOT NULL, fired_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, name TEXT NOT NULL, metric TEXT NOT NULL, op TEXT NOT NULL, threshold INTEGER NOT NULL, window_min INTEGER NOT NULL, value INTEGER NOT NULL, severity TEXT NOT NULL, mailed TEXT NOT NULL DEFAULT '', seen INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS idx_alert_events_alert ON alert_events(alert_id, fired_at);
CREATE INDEX IF NOT EXISTS idx_alert_events_unseen ON alert_events(seen, domain_id, severity);
