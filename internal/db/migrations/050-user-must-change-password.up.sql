-- Forced password change on first login. A bootstrapped default admin (created
-- automatically on a fresh install) carries must_change_password=1; the console
-- redirects it to the change-password page until a new password is set, which
-- clears the flag. Existing accounts default to 0 (no forced change).
ALTER TABLE users ADD COLUMN must_change_password INTEGER NOT NULL DEFAULT 0;
