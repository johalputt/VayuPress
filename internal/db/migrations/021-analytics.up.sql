CREATE TABLE IF NOT EXISTS analytics_daily(day TEXT NOT NULL,path TEXT NOT NULL,views INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(day,path));
CREATE INDEX IF NOT EXISTS idx_analytics_day ON analytics_daily(day);
CREATE TABLE IF NOT EXISTS analytics_referrers(day TEXT NOT NULL,host TEXT NOT NULL,hits INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(day,host));
CREATE INDEX IF NOT EXISTS idx_analytics_ref_day ON analytics_referrers(day);
