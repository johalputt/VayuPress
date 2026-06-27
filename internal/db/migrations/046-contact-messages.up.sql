-- Contact-form submissions inbox. Every message sent through a page's contact
-- form is persisted here so operators can read it inside /os even when SMTP
-- delivery is unconfigured or fails. The form still emails the operator (and an
-- optional visitor auto-reply); this table is the durable record + admin inbox.
--
-- IMPORTANT: the migration runner executes ONE statement per line, so every
-- statement below MUST stay on a single line (see internal/db/db.go).
CREATE TABLE IF NOT EXISTS contact_messages(id TEXT PRIMARY KEY,name TEXT NOT NULL,email TEXT NOT NULL,message TEXT NOT NULL,page TEXT NOT NULL DEFAULT '',ip TEXT NOT NULL DEFAULT '',is_read INTEGER NOT NULL DEFAULT 0,created_at DATETIME NOT NULL);
CREATE INDEX IF NOT EXISTS idx_contact_messages_created ON contact_messages(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_contact_messages_read ON contact_messages(is_read);
