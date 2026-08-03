-- Migration 082: settings belong to a scope (ADR-0153 Phase 2).
--
-- The migration runner executes ONE statement per physical LINE.
--
-- WHY THIS IS A REBUILD AND NOT AN ALTER. site_settings' primary key was (key).
-- One key, one value, for the whole install — so every hosted domain shared the
-- operator's theme, SEO defaults, newsletter configuration and 320-odd other
-- settings. A domain could differ in exactly six brand fields, which were kept
-- in domains.config_json and overlaid at render time. That is why a hosted
-- domain looked like the operator's own site: it WAS the operator's own site,
-- wearing a different name and three colours.
--
-- A primary key cannot be changed in place in SQLite, so the table is rebuilt.
-- Of the rebuilds this codebase has needed (articles.slug, members.email,
-- analytics_daily, and this) it is the least dangerous: the data is
-- configuration, not business records, and every row survives the copy.
--
-- EXISTING ROWS BACKFILL TO scope='' — the primary — because that is precisely
-- what they are: every setting written before this migration was written by the
-- operator, for the one site this install served. '' is the same primary
-- sentinel already used by articles.domain_id (060), members.domain_id (061)
-- and analytics_daily.domain_id (080), so the convention is unchanged and a
-- single-domain install reads byte-identical before and after.
--
-- THE FALLBACK DIRECTION IS THE PRODUCT DECISION, and it lives in Go rather
-- than here (ADR-0153 D2, settled by the operator): a key with no row for a
-- scope resolves to the compiled-in DEFAULT, never to the primary's stored
-- value. There is deliberately no view, no COALESCE onto the primary and no
-- foreign key that would make inheritance easy to reintroduce by accident.
ALTER TABLE site_settings RENAME TO site_settings_pre082;
CREATE TABLE IF NOT EXISTS site_settings(scope TEXT NOT NULL DEFAULT '',key TEXT NOT NULL,value TEXT NOT NULL DEFAULT '',updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,PRIMARY KEY(scope,key));
INSERT INTO site_settings(scope,key,value,updated_at) SELECT '',key,value,updated_at FROM site_settings_pre082;
DROP TABLE site_settings_pre082;
CREATE INDEX IF NOT EXISTS idx_site_settings_scope ON site_settings(scope);
