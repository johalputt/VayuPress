-- Migration 132: how often each ad slot was seen and clicked, per day.
-- Written in batches by ads.Counter; domain_id is the slot's site ('' for
-- the install's own). One statement per line, as the runner requires.
CREATE TABLE IF NOT EXISTS ad_events(slot_id TEXT NOT NULL,domain_id TEXT NOT NULL DEFAULT '',day TEXT NOT NULL,seen INTEGER NOT NULL DEFAULT 0,clicks INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(slot_id,day));
CREATE INDEX IF NOT EXISTS idx_ad_events_site_day ON ad_events(domain_id,day);
