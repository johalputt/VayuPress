-- Migration 091 (down): remove the Core Web Vitals RUM columns.
ALTER TABLE vayuanalytics_sessions DROP COLUMN lcp_ms;
ALTER TABLE vayuanalytics_sessions DROP COLUMN inp_ms;
ALTER TABLE vayuanalytics_sessions DROP COLUMN cls_x100;
