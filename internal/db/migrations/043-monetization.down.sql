-- Reverses 043-monetization. One statement per line (see internal/db/db.go).
DROP INDEX IF EXISTS idx_ad_slots_placement;
DROP TABLE IF EXISTS ad_slots;
DROP INDEX IF EXISTS idx_payment_orders_email;
DROP INDEX IF EXISTS idx_payment_orders_status;
DROP TABLE IF EXISTS payment_orders;
