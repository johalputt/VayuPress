-- Migration 107 (down): the pass's record goes; the posts it restored stay restored, each with its pre-update version in its history.
DROP TABLE IF EXISTS text_repair_lost;
DROP TABLE IF EXISTS text_repair;
