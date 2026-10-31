-- Migration 125: failed sign-ins, kept so the operator and each holder can see
-- who tries their accounts, and blocks that stop them. An address is never
-- written down whole: ip_masked hides its last part (103.21.x.x), and ip_fp and
-- net_fp are keyed fingerprints of the address and of its network (/24, or /48
-- for IPv6), made with a key that is not in this database, so a block holds
-- exactly while nobody can read the address back. Attempts are counted per
-- minute, account, door, stage and source, so a flood adds to a count rather
-- than a row; they are kept 30 days. One statement per line, as the runner
-- requires.
CREATE TABLE IF NOT EXISTS signin_attempts (minute INTEGER NOT NULL, account TEXT NOT NULL, door TEXT NOT NULL, stage TEXT NOT NULL, ip_fp TEXT NOT NULL, net_fp TEXT NOT NULL, ip_masked TEXT NOT NULL, country TEXT NOT NULL DEFAULT '', n INTEGER NOT NULL DEFAULT 1, PRIMARY KEY (minute, account, door, stage, ip_fp)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_signin_attempts_fp ON signin_attempts(ip_fp, minute);
CREATE INDEX IF NOT EXISTS idx_signin_attempts_account ON signin_attempts(account, minute);
CREATE TABLE IF NOT EXISTS signin_blocks (id INTEGER PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('address','network','country')), key TEXT NOT NULL, label TEXT NOT NULL, account TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL, created INTEGER NOT NULL, until INTEGER NOT NULL DEFAULT 0, hits INTEGER NOT NULL DEFAULT 0, lifted INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS idx_signin_blocks_key ON signin_blocks(kind, key);
CREATE TABLE IF NOT EXISTS signin_fences (account TEXT PRIMARY KEY, countries TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS signin_known (account TEXT NOT NULL, ip_fp TEXT NOT NULL, at INTEGER NOT NULL, PRIMARY KEY (account, ip_fp)) WITHOUT ROWID;
