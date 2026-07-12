-- VayuDomains Stage 2 — per-domain content ownership.
-- IMPORTANT: the migration runner executes ONE statement per line, so every
-- statement below MUST stay on a single line (see internal/db/db.go).
--
-- domain_id tags each article with the domain that owns it. It is purely
-- additive: every existing article defaults to '' which means "the primary
-- domain / unassigned", so an existing single-domain install (e.g. johal.in) is
-- byte-identical — the primary domain serves exactly the rows it always did.
-- A secondary domain serves only the rows carrying its own domain id.
--   ''            — owned by the primary domain (all historic content).
--   <domain.id>   — owned by that secondary domain (see migration 059).
-- The global UNIQUE on articles.slug is intentionally kept for now: two domains
-- cannot yet share a slug. Relaxing that to UNIQUE(domain_id,slug) needs an
-- articles-table rebuild (the constraint is inline, not a droppable index) and
-- is deferred to a later sub-stage (ADR-0132).
ALTER TABLE articles ADD COLUMN domain_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_articles_domain ON articles(domain_id);
CREATE INDEX IF NOT EXISTS idx_articles_domain_created ON articles(domain_id,created_at DESC);
