-- Migration 136: the shared attacker list, on an install that runs it as a hub. A reporter is an install's signing key, with the network it last reported from; removed keeps it refused. A report is one address from one reporter, the latest time it was reported. A listing is an address three reporters on different networks reported within a day, served until its expiry. NOTE: runMigrations executes line-by-line, so keep each statement on ONE line.
CREATE TABLE IF NOT EXISTS community_reporters (key TEXT PRIMARY KEY, net TEXT NOT NULL DEFAULT '', first_seen INTEGER NOT NULL, last_report INTEGER NOT NULL DEFAULT 0, reports INTEGER NOT NULL DEFAULT 0, removed INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS community_reports (addr TEXT NOT NULL, key TEXT NOT NULL, net TEXT NOT NULL, at INTEGER NOT NULL, PRIMARY KEY (addr, key));
CREATE INDEX IF NOT EXISTS idx_community_reports_key ON community_reports(key);
CREATE INDEX IF NOT EXISTS idx_community_reports_at ON community_reports(at);
CREATE TABLE IF NOT EXISTS community_listed (addr TEXT PRIMARY KEY, until INTEGER NOT NULL);
