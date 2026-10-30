-- Migration 124: a site's click map, for the sites that turn it on. Each row
-- is a count, never a click: the clicks on one page, one day, at one width
-- class, on one element of the page (its path of tags and their order among
-- their like, no text, no attribute), in one tenth of that element across
-- and down. A reader's clicks are never kept apart, so they cannot be put
-- back together. Rage counts the bursts of three on one element within a
-- second; dead the clicks on what is no link or control. One statement per
-- line, as the runner requires.
CREATE TABLE IF NOT EXISTS analytics_heat (domain_id TEXT NOT NULL DEFAULT '', path TEXT NOT NULL, day TEXT NOT NULL, width TEXT NOT NULL, element TEXT NOT NULL, x INTEGER NOT NULL, y INTEGER NOT NULL, clicks INTEGER NOT NULL DEFAULT 0, rage INTEGER NOT NULL DEFAULT 0, dead INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (domain_id, path, day, width, element, x, y)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_analytics_heat_day ON analytics_heat(day);
