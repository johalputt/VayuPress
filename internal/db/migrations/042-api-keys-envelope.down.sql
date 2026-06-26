-- Reverse 042: drop the keyring. (SQLite cannot drop a column, so vayu_api_keys.scope is left in place; it is harmless.)
DROP TABLE IF EXISTS secret_keyring;
