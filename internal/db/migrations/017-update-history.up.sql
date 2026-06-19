CREATE TABLE IF NOT EXISTS update_history(id INTEGER PRIMARY KEY AUTOINCREMENT,from_version TEXT,to_version TEXT,status TEXT NOT NULL,backup_path TEXT DEFAULT '',detail TEXT DEFAULT '',started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,completed_at DATETIME);
CREATE INDEX IF NOT EXISTS idx_update_history_started ON update_history(started_at DESC);
