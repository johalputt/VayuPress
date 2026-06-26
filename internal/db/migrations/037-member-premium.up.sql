-- Premium memberships: reader profiles, priced subscription tiers, per-member
-- subscription state, and label segmentation. Builds on 023-members (members,
-- member_login_tokens, member_sessions, article_access).
--
-- IMPORTANT: the migration runner executes ONE statement per line, so every
-- statement below MUST stay on a single line (see internal/db/db.go).

-- Reader profile enrichment. New columns are nullable / defaulted so existing
-- rows upgrade losslessly and the passwordless upsert keeps working unchanged.
ALTER TABLE members ADD COLUMN name TEXT NOT NULL DEFAULT '';
ALTER TABLE members ADD COLUMN note TEXT NOT NULL DEFAULT '';
ALTER TABLE members ADD COLUMN newsletter_opt_in INTEGER NOT NULL DEFAULT 1;
ALTER TABLE members ADD COLUMN last_seen_at DATETIME;

-- Priced subscription tiers. "benefits" is a JSON array of short strings shown on the pricing page and member portal; public tiers are listed publicly.
CREATE TABLE IF NOT EXISTS member_tiers(id TEXT PRIMARY KEY,slug TEXT NOT NULL UNIQUE,name TEXT NOT NULL,description TEXT NOT NULL DEFAULT '',monthly_cents INTEGER NOT NULL DEFAULT 0,yearly_cents INTEGER NOT NULL DEFAULT 0,currency TEXT NOT NULL DEFAULT 'USD',benefits TEXT NOT NULL DEFAULT '[]',visibility TEXT NOT NULL DEFAULT 'public',active INTEGER NOT NULL DEFAULT 1,sort INTEGER NOT NULL DEFAULT 0,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS idx_member_tiers_listing ON member_tiers(active, visibility, sort);

-- Per-member subscription state. The newest active row is a member's current plan; canceled / expired rows are retained for history and churn analytics.
CREATE TABLE IF NOT EXISTS member_subscriptions(id TEXT PRIMARY KEY,member_id TEXT NOT NULL,tier_slug TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'active',cadence TEXT NOT NULL DEFAULT 'monthly',amount_cents INTEGER NOT NULL DEFAULT 0,currency TEXT NOT NULL DEFAULT 'USD',stripe_subscription TEXT NOT NULL DEFAULT '',current_period_end DATETIME,started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,canceled_at DATETIME,FOREIGN KEY(member_id) REFERENCES members(id) ON DELETE CASCADE);
CREATE INDEX IF NOT EXISTS idx_member_subs_member ON member_subscriptions(member_id, status);
CREATE INDEX IF NOT EXISTS idx_member_subs_status ON member_subscriptions(status);

-- Label segmentation (e.g. "founding-member", "vip", "trial").
CREATE TABLE IF NOT EXISTS member_labels(id TEXT PRIMARY KEY,name TEXT NOT NULL UNIQUE,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE IF NOT EXISTS member_label_map(member_id TEXT NOT NULL,label_id TEXT NOT NULL,PRIMARY KEY(member_id, label_id),FOREIGN KEY(member_id) REFERENCES members(id) ON DELETE CASCADE,FOREIGN KEY(label_id) REFERENCES member_labels(id) ON DELETE CASCADE);

-- Seed the built-in Free tier and a starter paid tier. The paid tier keeps the slug "paid" so it stays consistent with the article-access level set by the Stripe upgrade path.
INSERT OR IGNORE INTO member_tiers(id,slug,name,description,monthly_cents,yearly_cents,benefits,visibility,sort) VALUES ('tier_free','free','Free','Create a free account to join the community and read free posts.',0,0,'["Access to all free posts","New posts delivered to your inbox","One link to sign in on any device"]','public',0),('tier_paid','paid','Premium','Unlock every premium story and support independent publishing.',500,5000,'["Everything in Free","Full access to premium, members-only posts","Members-only newsletter","Cancel anytime"]','public',1);
