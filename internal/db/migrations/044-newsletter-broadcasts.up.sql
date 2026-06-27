-- Newsletter console: persisted broadcast history for delivery insight.
--
-- The existing newsletter feature sent broadcasts fire-and-forget — counts were
-- only written to the log and never surfaced in the UI. This table records each
-- broadcast (subject, audience size, sent/failed tallies, status, timing) so the
-- Newsletter console can show a real delivery history and success rates.
--
-- IMPORTANT: the migration runner executes ONE statement per line, so every
-- statement below MUST stay on a single line (see internal/db/db.go).

CREATE TABLE IF NOT EXISTS newsletter_broadcasts(id TEXT PRIMARY KEY,subject TEXT NOT NULL,recipients INTEGER NOT NULL DEFAULT 0,sent INTEGER NOT NULL DEFAULT 0,failed INTEGER NOT NULL DEFAULT 0,status TEXT NOT NULL DEFAULT 'sending',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,completed_at DATETIME);
CREATE INDEX IF NOT EXISTS idx_newsletter_broadcasts_created ON newsletter_broadcasts(created_at);
