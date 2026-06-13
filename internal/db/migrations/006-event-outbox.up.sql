-- Migration 006: durable event outbox for transactional event delivery (ADR-0051).
CREATE TABLE IF NOT EXISTS event_outbox (id INTEGER PRIMARY KEY AUTOINCREMENT, event_type TEXT NOT NULL, payload TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','delivered','dead_letter')), retry_at DATETIME, retries INTEGER NOT NULL DEFAULT 0, dead_reason TEXT, created_at DATETIME NOT NULL DEFAULT (datetime('now')), delivered_at DATETIME);
CREATE INDEX IF NOT EXISTS idx_outbox_pending ON event_outbox(id) WHERE status = 'pending';
