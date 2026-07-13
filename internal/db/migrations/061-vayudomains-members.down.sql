-- Reverse 061-vayudomains-members. One statement per physical line.
DROP INDEX IF EXISTS idx_members_domain;
ALTER TABLE members DROP COLUMN domain_id;
