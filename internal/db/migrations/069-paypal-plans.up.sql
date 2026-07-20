-- PayPal billing-plan cache (migration 069). PayPal, unlike Stripe, needs a
-- pre-created product + billing plan before a subscription can be created, so
-- VayuPress lazily creates them on first checkout and caches the ids here, keyed
-- by a fingerprint of (tier, cadence, amount, currency) — a price change yields a
-- new fingerprint (hence a new plan). The shared catalog product id is stored
-- under the reserved fingerprint '__product__'. Statement kept on one line: the
-- migration runner executes newline-separated statements.
CREATE TABLE IF NOT EXISTS paypal_plans(fingerprint TEXT PRIMARY KEY, plan_id TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
