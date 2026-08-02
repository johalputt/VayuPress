-- Migration 080: attribute traffic to the domain it happened on (ADR-0152).
--
-- The migration runner executes ONE statement per physical LINE.
--
-- WHY THIS IS A REBUILD AND NOT AN ALTER. analytics_daily's primary key was
-- (day, path). Two hosted domains that both publish /about therefore shared one
-- row and their view counts were ADDED TOGETHER. That is not a missing feature,
-- it is a wrong number: showing a client that figure would be showing them
-- another client's visits and calling it theirs. A primary key cannot be changed
-- in place in SQLite, so the table is rebuilt.
--
-- Of the three rebuilds this codebase needs (articles.slug, members.email, and
-- this) it is by far the safest: the data is a derived daily aggregate, not
-- business records. Losing it would cost a chart, not a customer.
--
-- EXISTING ROWS BACKFILL TO '' — the primary domain — because that is precisely
-- what they are: every view counted before this migration happened on the one
-- domain the install served. '' is the same primary sentinel used by
-- articles.domain_id (060) and members.domain_id (061), so the convention is
-- unchanged and a single-domain install reads identically before and after.
ALTER TABLE analytics_daily RENAME TO analytics_daily_pre080;
CREATE TABLE IF NOT EXISTS analytics_daily(day TEXT NOT NULL,domain_id TEXT NOT NULL DEFAULT '',path TEXT NOT NULL,views INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(day,domain_id,path));
INSERT INTO analytics_daily(day,domain_id,path,views) SELECT day,'',path,views FROM analytics_daily_pre080;
DROP TABLE analytics_daily_pre080;
CREATE INDEX IF NOT EXISTS idx_analytics_day ON analytics_daily(day);
CREATE INDEX IF NOT EXISTS idx_analytics_domain_day ON analytics_daily(domain_id,day);
ALTER TABLE analytics_referrers RENAME TO analytics_referrers_pre080;
CREATE TABLE IF NOT EXISTS analytics_referrers(day TEXT NOT NULL,domain_id TEXT NOT NULL DEFAULT '',host TEXT NOT NULL,hits INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(day,domain_id,host));
INSERT INTO analytics_referrers(day,domain_id,host,hits) SELECT day,'',host,hits FROM analytics_referrers_pre080;
DROP TABLE analytics_referrers_pre080;
CREATE INDEX IF NOT EXISTS idx_analytics_ref_day ON analytics_referrers(day);
CREATE INDEX IF NOT EXISTS idx_analytics_ref_domain_day ON analytics_referrers(domain_id,day);
