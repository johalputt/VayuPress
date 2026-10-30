-- Migration 123: a visit row carries its city's coordinate when the city
-- table placed it (geoip.Place): the city's centre to a tenth of a degree,
-- never a reader's position, so a map can draw where readers are. NULL for a
-- visit the table did not place, and for every visit before this; nothing is
-- backfilled, as the coordinate was never recorded. One statement per line,
-- as the runner requires.
ALTER TABLE analytics_sessions ADD COLUMN lat REAL;
ALTER TABLE analytics_sessions ADD COLUMN lon REAL;
