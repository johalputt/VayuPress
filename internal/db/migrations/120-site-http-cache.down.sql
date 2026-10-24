-- Migration 120 (down): the pages by page cache go.
ALTER TABLE site_http_minutes DROP COLUMN rendered_ms;
ALTER TABLE site_http_minutes DROP COLUMN rendered_pages;
ALTER TABLE site_http_minutes DROP COLUMN cached_ms;
ALTER TABLE site_http_minutes DROP COLUMN cached_pages;
