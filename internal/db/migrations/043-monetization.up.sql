-- Monetization v1 (payments + advertising).
--
-- Two new surfaces, both gated behind operator-toggled feature flags so a fresh
-- install never takes payments or shows adverts until switched on:
--
--   * payment_orders — the sovereign order ledger. Every checkout (built-in
--     direct/offline gateway or a connected third-party gateway via signed
--     webhook) records an order here. An order is the single source of truth
--     for what a payer owes, in which currency, for which tier/cadence, and its
--     lifecycle (pending -> paid / canceled / refunded). When an order is paid
--     the member is upgraded and a confirmation email is sent — but that
--     orchestration lives in the app layer; this table only persists state.
--     reference is a short human-quotable code the payer puts on their transfer
--     so the operator can reconcile an offline payment to the right order.
--
--   * ad_slots — operator-defined advertising placements. A slot targets a
--     placement on the public site (header, above/below the post, in-content,
--     sidebar, footer) and renders either a same-origin image+link "house" ad,
--     a sanitized HTML creative, or a Google AdSense unit. enabled gates each
--     slot individually; the whole surface is additionally gated by feature.ads.
--
-- IMPORTANT: the migration runner executes ONE statement per line, so every
-- statement below MUST stay on a single line (see internal/db/db.go).

CREATE TABLE IF NOT EXISTS payment_orders(id TEXT PRIMARY KEY,reference TEXT NOT NULL UNIQUE,email TEXT NOT NULL,name TEXT NOT NULL DEFAULT '',tier_slug TEXT NOT NULL,cadence TEXT NOT NULL DEFAULT 'monthly',amount_cents INTEGER NOT NULL DEFAULT 0,currency TEXT NOT NULL DEFAULT 'USD',gateway TEXT NOT NULL DEFAULT 'direct',status TEXT NOT NULL DEFAULT 'pending',gateway_ref TEXT NOT NULL DEFAULT '',note TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,paid_at DATETIME,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS idx_payment_orders_status ON payment_orders(status, created_at);
CREATE INDEX IF NOT EXISTS idx_payment_orders_email ON payment_orders(email, created_at);
CREATE TABLE IF NOT EXISTS ad_slots(id TEXT PRIMARY KEY,name TEXT NOT NULL,placement TEXT NOT NULL DEFAULT 'below_post',kind TEXT NOT NULL DEFAULT 'image',image_url TEXT NOT NULL DEFAULT '',link_url TEXT NOT NULL DEFAULT '',alt_text TEXT NOT NULL DEFAULT '',html TEXT NOT NULL DEFAULT '',enabled INTEGER NOT NULL DEFAULT 1,sort INTEGER NOT NULL DEFAULT 0,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS idx_ad_slots_placement ON ad_slots(placement, enabled, sort);
