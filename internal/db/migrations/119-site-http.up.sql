-- Migration 119: what the server measured for each site.
--
-- site_http_minutes counts each site's public requests by the status they
-- were answered with, a row per site and minute that had any, and the pages
-- among them with the time the server took to answer them. site_http_latency
-- keeps those pages' server times as a histogram per site and hour, one row
-- per bucket that has any, so a percentile over any range is read from the
-- buckets. domain_id is the site as the analytics tables name it: '' is the
-- primary, else a hosted site's id. Both are purged with the analytics.
--
-- cls_seen marks an engagement row whose layout shift was measured: 0 was
-- both "no shift" and "not measured", so the readers' shift figure left out
-- every page that never shifted. Rows from before stay unmeasured.
CREATE TABLE IF NOT EXISTS site_http_minutes(domain_id TEXT NOT NULL, minute TEXT NOT NULL, requests INTEGER NOT NULL DEFAULT 0, s2xx INTEGER NOT NULL DEFAULT 0, s3xx INTEGER NOT NULL DEFAULT 0, s4xx INTEGER NOT NULL DEFAULT 0, s404 INTEGER NOT NULL DEFAULT 0, s5xx INTEGER NOT NULL DEFAULT 0, pages INTEGER NOT NULL DEFAULT 0, page_ms INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(domain_id, minute)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_site_http_minutes_minute ON site_http_minutes(minute);
CREATE TABLE IF NOT EXISTS site_http_latency(domain_id TEXT NOT NULL, hour TEXT NOT NULL, bucket INTEGER NOT NULL, n INTEGER NOT NULL, PRIMARY KEY(domain_id, hour, bucket)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_site_http_latency_hour ON site_http_latency(hour, bucket);
ALTER TABLE vayuanalytics_sessions ADD COLUMN cls_seen INTEGER NOT NULL DEFAULT 0;
