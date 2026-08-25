-- Migration 091 (up): Core Web Vitals RUM columns for VayuAnalytics.
--
-- Real-user performance signals ride the existing engagement beacons — no new
-- endpoint, no third-party script. Values are server-clamped before storage:
-- lcp_ms / inp_ms are milliseconds (0 = not reported), cls_x100 is the
-- cumulative layout shift scaled by 100 so it fits an integer column exactly.
ALTER TABLE vayuanalytics_sessions ADD COLUMN lcp_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE vayuanalytics_sessions ADD COLUMN inp_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE vayuanalytics_sessions ADD COLUMN cls_x100 INTEGER NOT NULL DEFAULT 0;
