-- Reverse 041-api-keys: drop the API key + service credential tables.
DROP INDEX IF EXISTS idx_service_credentials_provider;
DROP TABLE IF EXISTS service_credentials;
DROP INDEX IF EXISTS idx_vayu_api_keys_active;
DROP INDEX IF EXISTS idx_vayu_api_keys_hash;
DROP TABLE IF EXISTS vayu_api_keys;
