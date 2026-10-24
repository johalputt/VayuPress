-- Migration 119 (down): the server's own measurements per site go.
DROP INDEX IF EXISTS idx_site_http_latency_hour;
DROP TABLE IF EXISTS site_http_latency;
DROP INDEX IF EXISTS idx_site_http_minutes_minute;
DROP TABLE IF EXISTS site_http_minutes;
