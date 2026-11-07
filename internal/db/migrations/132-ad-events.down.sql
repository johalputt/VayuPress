-- Down for 132: no record of how often ads were seen or clicked.
DROP INDEX IF EXISTS idx_ad_events_site_day;
DROP TABLE IF EXISTS ad_events;
