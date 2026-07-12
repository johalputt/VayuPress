-- VayuDomains — multi-domain registry (Stage 1: foundation).
-- IMPORTANT: the migration runner executes ONE statement per line, so every
-- statement below MUST stay on a single line (see internal/db/db.go).
--
-- domains is the authoritative registry of every hostname this single binary
-- serves. Stage 1 only READS this table for host resolution; content, mail and
-- member scoping arrive in later stages. The primary row (is_primary=1) mirrors
-- the historic single-domain install and is seeded in Go at startup from
-- config.Cfg.Domain, so an existing site (e.g. johal.in) is byte-identical:
-- the registry describes what already exists rather than changing it.
--   host        — the fully-qualified hostname served (unique, lower-cased).
--   site_type   — what "/" serves for this host: 'blog' | 'business' |
--                 'business_subpath' | 'static' | 'mail_only'. Empty is treated
--                 as 'blog' for backward compatibility, matching site.mode.
--   mail_enabled— 1 if this domain accepts branded mail (Stage 3), else 0.
--   tls_state   — certificate lifecycle: 'primary' (managed outside the
--                 registry, e.g. the existing certbot cert), 'pending',
--                 'active', 'failed'. Stage 1 never provisions; it only records.
--   config_json — per-domain overrides (branding, redirects) as a JSON blob;
--                 empty means "inherit the global settings" (johal.in default).
--   is_primary  — exactly one row carries 1: the original install domain.
--   status      — 'active' | 'disabled'; a disabled domain is resolved but its
--                 host middleware treats it as unknown (soft-delete, auditable).
CREATE TABLE IF NOT EXISTS domains(id TEXT PRIMARY KEY,host TEXT NOT NULL UNIQUE,site_type TEXT NOT NULL DEFAULT 'blog',mail_enabled INTEGER NOT NULL DEFAULT 0,tls_state TEXT NOT NULL DEFAULT 'pending',config_json TEXT NOT NULL DEFAULT '',is_primary INTEGER NOT NULL DEFAULT 0,status TEXT NOT NULL DEFAULT 'active',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS idx_domains_host ON domains(host);
CREATE INDEX IF NOT EXISTS idx_domains_primary ON domains(is_primary);
