-- Member avatars (reader profile pictures): a real photo (<=100 KB, stored as a
-- blob), a chosen prebuilt cartoon, or a deterministic gender-aware auto avatar.
-- gender is optional (neutral default) and only steers the auto avatar. Kept on
-- the members row so a single lookup by email resolves everything the public
-- comment avatar needs. The blob is never returned by the canonical member SELECT
-- (memberCols) — it is fetched only by the avatar-serve path.
ALTER TABLE members ADD COLUMN gender TEXT NOT NULL DEFAULT '';
ALTER TABLE members ADD COLUMN avatar_choice TEXT NOT NULL DEFAULT '';
ALTER TABLE members ADD COLUMN avatar_mime TEXT NOT NULL DEFAULT '';
ALTER TABLE members ADD COLUMN avatar_blob BLOB;
