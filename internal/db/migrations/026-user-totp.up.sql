-- Admin v3 two-factor authentication (ADR-0068, Phase 5).
-- totp_secret holds the per-user base32 TOTP secret (empty = not enrolled).
-- totp_enabled flips to 1 only after the user verifies a code, so a half-
-- finished enrolment never locks anyone out.
ALTER TABLE users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN totp_enabled INTEGER NOT NULL DEFAULT 0;
