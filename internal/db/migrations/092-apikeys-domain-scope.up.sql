-- Migration 092 (up): domain-scoped API keys (2025 plan Wave 4).
--
-- Adds a domain_id column so an issued key can be bound to a single
-- hosted domain. The operator's admin panel will offer "create key for
-- this domain" and the admin API will enforce the boundary.
--
-- The column is nullable-by-default so existing keys and the bootstrap
-- config key continue to work globally (domain_id = '' means global).
ALTER TABLE vayu_api_keys ADD COLUMN domain_id TEXT NOT NULL DEFAULT '';