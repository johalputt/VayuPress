-- Down for 127: passkey only goes with its table, and every block reaches sign-in alone again.
ALTER TABLE signin_blocks DROP COLUMN scope;
DROP TABLE IF EXISTS passkey_only;
