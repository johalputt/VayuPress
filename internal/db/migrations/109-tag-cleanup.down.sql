-- Migration 109 (down): the pass's record and the original tags go; the posts keep their cleaned tags.
DROP TABLE IF EXISTS tag_originals;
DROP TABLE IF EXISTS tag_cleanup;
