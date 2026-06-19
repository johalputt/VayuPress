CREATE TABLE IF NOT EXISTS newsletter_subscribers(id TEXT PRIMARY KEY,email TEXT NOT NULL UNIQUE,status TEXT NOT NULL DEFAULT 'active',confirmed INTEGER NOT NULL DEFAULT 0,token TEXT NOT NULL DEFAULT '',subscribed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,unsubscribed_at DATETIME);
CREATE INDEX IF NOT EXISTS idx_newsletter_email ON newsletter_subscribers(email);
CREATE INDEX IF NOT EXISTS idx_newsletter_status ON newsletter_subscribers(status);
