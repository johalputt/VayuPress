-- Migration 103 (down): drop the listing cards, their triggers and the feed indexes.
DROP INDEX IF EXISTS idx_articles_feed_domain;
DROP INDEX IF EXISTS idx_articles_feed;
DROP TRIGGER IF EXISTS article_cards_on_delete;
DROP TRIGGER IF EXISTS article_cards_on_update;
DROP TABLE IF EXISTS article_cards;
