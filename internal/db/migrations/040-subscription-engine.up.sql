-- Subscription Engine v2: free trials, scheduled cancellations, optional Stripe
-- price wiring, and a per-member activity log. Builds on 037-member-premium
-- (member_tiers, member_subscriptions, member_labels). These additions power
-- richer lifecycle handling (trials / cancel-at-period-end) and the deeper
-- membership analytics (churn, conversion, ARPU, LTV, MRR movement) and the
-- activity feed surfaced in the Members console.
--
-- IMPORTANT: the migration runner executes ONE statement per line, so every
-- statement below MUST stay on a single line (see internal/db/db.go).

-- Tiers gain an optional free-trial length and optional Stripe price ids so a
-- tier can be wired straight to a hosted Stripe checkout without code changes.
ALTER TABLE member_tiers ADD COLUMN trial_days INTEGER NOT NULL DEFAULT 0;
ALTER TABLE member_tiers ADD COLUMN stripe_monthly_price TEXT NOT NULL DEFAULT '';
ALTER TABLE member_tiers ADD COLUMN stripe_yearly_price TEXT NOT NULL DEFAULT '';

-- Subscriptions gain a trial end and a "cancel at period end" flag so a member
-- can keep access until the paid period elapses instead of losing it instantly.
ALTER TABLE member_subscriptions ADD COLUMN trial_end DATETIME;
ALTER TABLE member_subscriptions ADD COLUMN cancel_at_period_end INTEGER NOT NULL DEFAULT 0;

-- Per-member activity log: signups, subscription starts, upgrades/downgrades,
-- trials, renewals, cancellations, comps, and failed payments. amount_cents is
-- the monthly value at the time of the event (0 for non-revenue events).
CREATE TABLE IF NOT EXISTS member_events(id TEXT PRIMARY KEY,member_id TEXT NOT NULL,type TEXT NOT NULL,detail TEXT NOT NULL DEFAULT '',amount_cents INTEGER NOT NULL DEFAULT 0,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,FOREIGN KEY(member_id) REFERENCES members(id) ON DELETE CASCADE);
CREATE INDEX IF NOT EXISTS idx_member_events_member ON member_events(member_id, created_at);
CREATE INDEX IF NOT EXISTS idx_member_events_type ON member_events(type, created_at);
