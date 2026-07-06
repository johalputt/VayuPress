-- VayuAnalytics Enterprise — engagement analytics session store (Part 7 schema).
-- IMPORTANT: the migration runner executes ONE statement per line, so every
-- statement below MUST stay on a single line (see internal/db/db.go).
--
-- vayuanalytics_sessions stores one row per (session, page) engagement window.
-- session_hash is a daily-rotating salted hash (SHA-256 of ip+ua+lang+day-bucket)
-- that identifies a visitor only within a single day and can never be linked to
-- PII or across days (GDPR Article 25, data protection by design). No cookie is
-- used. source_category classifies traffic (organic / ai_assisted / direct /
-- social / corporate / email / newsletter / referral / bot).
CREATE TABLE IF NOT EXISTS vayuanalytics_sessions(id INTEGER PRIMARY KEY AUTOINCREMENT,session_hash TEXT NOT NULL,page_path TEXT NOT NULL,source_category TEXT NOT NULL DEFAULT 'direct',source_detail TEXT NOT NULL DEFAULT '',referrer_domain TEXT NOT NULL DEFAULT '',referrer_path TEXT NOT NULL DEFAULT '',entry_time DATETIME NOT NULL,exit_time DATETIME,time_on_page_seconds INTEGER NOT NULL DEFAULT 0,scroll_depth_percent INTEGER NOT NULL DEFAULT 0,engaged INTEGER NOT NULL DEFAULT 0,bounce INTEGER NOT NULL DEFAULT 0,interaction_count INTEGER NOT NULL DEFAULT 0,country_code TEXT NOT NULL DEFAULT '',client_type TEXT NOT NULL DEFAULT 'human',bot_score REAL NOT NULL DEFAULT 0,is_new_session INTEGER NOT NULL DEFAULT 1,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS idx_va_sessions_entry_time ON vayuanalytics_sessions(entry_time);
CREATE INDEX IF NOT EXISTS idx_va_sessions_source ON vayuanalytics_sessions(source_category,entry_time);
CREATE INDEX IF NOT EXISTS idx_va_sessions_page ON vayuanalytics_sessions(page_path,entry_time);
CREATE INDEX IF NOT EXISTS idx_va_sessions_hash ON vayuanalytics_sessions(session_hash,page_path,entry_time);
