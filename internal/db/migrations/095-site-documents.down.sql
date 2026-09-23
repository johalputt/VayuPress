-- Migration 095 (down): remove site documents and the contact message's site.
ALTER TABLE contact_messages DROP COLUMN domain_id;
DROP INDEX IF EXISTS idx_site_revisions_domain;
DROP TABLE IF EXISTS site_revisions;
DROP TABLE IF EXISTS site_drafts;
