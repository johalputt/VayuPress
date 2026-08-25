-- 090: visitor identity for VayuAnalytics engagement rows.
--
-- session_hash alone could not answer "unique VISITORS" or "returning
-- visitors": it is per reading session by design (sliding 30-minute window).
-- visitor_hash is stable for one anonymous visitor for one UTC day (see
-- internal/vayuanalytics/session), enabling:
--   - daily uniques that mean visitors, not sessions;
--   - TRUE new-vs-returning: a visitor whose first-ever row predates the
--     window is returning — not a daily-reset flag;
--   - retention cohorts anchored on MIN(entry_time) per visitor.
-- Privacy posture is unchanged: visitor_hash is the same class of secret-
-- salted, daily-rotating one-way hash as session_hash.

ALTER TABLE vayuanalytics_sessions ADD COLUMN visitor_hash TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_va_visitor_time ON vayuanalytics_sessions(visitor_hash, entry_time);
