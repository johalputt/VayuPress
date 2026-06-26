DROP TABLE IF EXISTS member_events;
-- Note: the added member_tiers columns (trial_days, stripe_monthly_price,
-- stripe_yearly_price) and member_subscriptions columns (trial_end,
-- cancel_at_period_end) are intentionally retained; SQLite DROP COLUMN support
-- is version-dependent and these columns are harmless if rolled back.
