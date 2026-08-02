-- Migration 079: an agency CLIENT owns exactly one hosted domain (ADR-0152).
--
-- The migration runner executes ONE statement per physical LINE, so every
-- statement below stays on a single line.
--
-- Additive with a '' default, matching the convention migrations 060 and 061
-- established for articles.domain_id and members.domain_id: every existing row
-- keeps '', which means "not a client" (operator, staff, or any historic
-- account), so an existing install is byte-identical.
--
-- ZERO VALUE IS NOT A VALID ANSWER for a client. '' is already the primary
-- domain's sentinel elsewhere, and a client is NEVER bound to the primary --
-- that is the agency's own install. So role='client' together with
-- client_domain_id='' is an INVALID identity and is refused at every request
-- rather than defaulting to the primary. There is no value here that
-- accidentally grants anything.
ALTER TABLE users ADD COLUMN client_domain_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_users_client_domain ON users(client_domain_id);
