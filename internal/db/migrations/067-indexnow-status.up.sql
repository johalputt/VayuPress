-- IndexNow per-post submission status (search-engine instant-index ping, ADR-0139/SEO).
-- One row per slug, upserted every time the post URL is announced to IndexNow, so the
-- Posts manager can show whether each published post was submitted and offer a manual
-- re-ping when it was not. The migration runner executes ONE statement per physical
-- line, so the whole CREATE TABLE stays on a single line.
CREATE TABLE IF NOT EXISTS indexnow_submissions(slug TEXT PRIMARY KEY, state TEXT NOT NULL, http_code INTEGER NOT NULL DEFAULT 0, detail TEXT NOT NULL DEFAULT '', submitted_at INTEGER NOT NULL DEFAULT 0);
