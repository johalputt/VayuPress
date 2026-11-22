-- Migration 136 (down): the hub's reporters, reports and listings go.
DROP TABLE IF EXISTS community_listed;
DROP TABLE IF EXISTS community_reports;
DROP TABLE IF EXISTS community_reporters;
