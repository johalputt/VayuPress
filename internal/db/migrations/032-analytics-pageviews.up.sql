CREATE TABLE IF NOT EXISTS analytics_pageviews(id TEXT PRIMARY KEY,session_id TEXT NOT NULL,url_path TEXT NOT NULL,url_query TEXT NOT NULL DEFAULT '',page_title TEXT NOT NULL DEFAULT '',referrer TEXT NOT NULL DEFAULT '',hostname TEXT NOT NULL DEFAULT '',utm_source TEXT NOT NULL DEFAULT '',utm_medium TEXT NOT NULL DEFAULT '',utm_campaign TEXT NOT NULL DEFAULT '',utm_content TEXT NOT NULL DEFAULT '',utm_term TEXT NOT NULL DEFAULT '',event_type INTEGER NOT NULL DEFAULT 1,event_name TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS idx_apv_session ON analytics_pageviews(session_id);
CREATE INDEX IF NOT EXISTS idx_apv_path ON analytics_pageviews(url_path);
CREATE INDEX IF NOT EXISTS idx_apv_created ON analytics_pageviews(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_apv_referrer ON analytics_pageviews(referrer);
CREATE INDEX IF NOT EXISTS idx_apv_utm_src ON analytics_pageviews(utm_source);
