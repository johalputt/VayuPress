-- Migration 089 down: drop the consumed-step marker.
ALTER TABLE users DROP COLUMN totp_last_step;
