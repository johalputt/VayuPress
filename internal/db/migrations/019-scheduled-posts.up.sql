CREATE TABLE IF NOT EXISTS scheduled_posts(id TEXT PRIMARY KEY,slug TEXT NOT NULL,title TEXT NOT NULL,content TEXT NOT NULL,tags TEXT NOT NULL DEFAULT '',publish_at DATETIME NOT NULL,status TEXT NOT NULL DEFAULT 'scheduled',error TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,published_at DATETIME);
CREATE INDEX IF NOT EXISTS idx_scheduled_due ON scheduled_posts(status,publish_at);
CREATE INDEX IF NOT EXISTS idx_scheduled_slug ON scheduled_posts(slug);
