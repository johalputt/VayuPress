-- Migration 120: each site's pages by whether the page cache answered them.
--
-- cached_pages and cached_ms are the pages answered from the page cache and
-- their server time in total; rendered_pages and rendered_ms the pages the
-- cache did not hold, rendered for the request. A page neither counts (a
-- hosted site's file, a page the cache does not keep) is in pages alone.
ALTER TABLE site_http_minutes ADD COLUMN cached_pages INTEGER NOT NULL DEFAULT 0;
ALTER TABLE site_http_minutes ADD COLUMN cached_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE site_http_minutes ADD COLUMN rendered_pages INTEGER NOT NULL DEFAULT 0;
ALTER TABLE site_http_minutes ADD COLUMN rendered_ms INTEGER NOT NULL DEFAULT 0;
