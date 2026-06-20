CREATE TABLE IF NOT EXISTS members(id TEXT PRIMARY KEY,email TEXT NOT NULL UNIQUE,tier TEXT NOT NULL DEFAULT 'free',status TEXT NOT NULL DEFAULT 'active',stripe_customer TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS idx_members_email ON members(email);
CREATE INDEX IF NOT EXISTS idx_members_stripe ON members(stripe_customer);
CREATE TABLE IF NOT EXISTS member_login_tokens(token_hash TEXT PRIMARY KEY,email TEXT NOT NULL,expires_at DATETIME NOT NULL);
CREATE TABLE IF NOT EXISTS member_sessions(token_hash TEXT PRIMARY KEY,member_id TEXT NOT NULL,expires_at DATETIME NOT NULL,FOREIGN KEY(member_id) REFERENCES members(id) ON DELETE CASCADE);
CREATE INDEX IF NOT EXISTS idx_member_sessions_expiry ON member_sessions(expires_at);
CREATE TABLE IF NOT EXISTS article_access(slug TEXT PRIMARY KEY,level TEXT NOT NULL DEFAULT 'public');
