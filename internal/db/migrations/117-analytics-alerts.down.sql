-- Migration 117 (down): the alerts and their history go.
DROP INDEX IF EXISTS idx_alert_events_unseen;
DROP INDEX IF EXISTS idx_alert_events_alert;
DROP TABLE IF EXISTS alert_events;
DROP INDEX IF EXISTS idx_analytics_alerts_domain;
DROP TABLE IF EXISTS analytics_alerts;
