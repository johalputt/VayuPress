DROP INDEX IF EXISTS idx_users_client_domain;
ALTER TABLE users DROP COLUMN client_domain_id;
