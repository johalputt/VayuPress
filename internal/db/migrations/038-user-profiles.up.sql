-- Author/staff public profiles. Each account (admin/author/editor) gains an
-- optional avatar, a short bio (capped to 250 characters by the application),
-- and a JSON map of social links rendered on the public author profile page.
-- New columns are defaulted so existing accounts upgrade losslessly.
ALTER TABLE users ADD COLUMN avatar_url TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN bio TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN socials TEXT NOT NULL DEFAULT '{}';
