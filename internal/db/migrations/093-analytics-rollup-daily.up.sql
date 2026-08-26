-- Migration 093 (up): daily rollup ladder with HLL uniques (2025 plan Wave 4).
--
-- One row per (day, domain): precomputed counters plus a HyperLogLog sketch
-- of that day's distinct visitors. Long-range dashboards union N sketches
-- instead of scanning N days of raw pageviews.
CREATE TABLE IF NOT EXISTS analytics_rollup_daily(
	day       TEXT NOT NULL,
	domain_id TEXT NOT NULL DEFAULT '',
	views     INTEGER NOT NULL DEFAULT 0,
	sessions  INTEGER NOT NULL DEFAULT 0,
	uniques_hll BLOB NOT NULL,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY(day, domain_id)
);