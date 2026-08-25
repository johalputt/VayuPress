-- Migration 092 (down): remove domain-scoped API key support.
ALTER TABLE vayu_api_keys DROP COLUMN domain_id;