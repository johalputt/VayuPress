CREATE TABLE IF NOT EXISTS articles(id TEXT PRIMARY KEY,title TEXT NOT NULL,slug TEXT UNIQUE NOT NULL,content TEXT NOT NULL,tags TEXT DEFAULT '',created_at DATETIME NOT NULL,updated_at DATETIME NOT NULL);
CREATE INDEX IF NOT EXISTS idx_articles_slug    ON articles(slug);
CREATE INDEX IF NOT EXISTS idx_articles_created ON articles(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_articles_updated ON articles(updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_articles_tags    ON articles(tags);
CREATE TABLE IF NOT EXISTS write_jobs(id INTEGER PRIMARY KEY AUTOINCREMENT,article_json TEXT NOT NULL,op TEXT NOT NULL DEFAULT 'insert',status TEXT NOT NULL DEFAULT 'pending',retries INTEGER NOT NULL DEFAULT 0,retry_at DATETIME,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS idx_jobs_status  ON write_jobs(status,created_at);
CREATE INDEX IF NOT EXISTS idx_jobs_retries ON write_jobs(retries);
