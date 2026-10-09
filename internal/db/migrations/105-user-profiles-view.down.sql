-- Migration 105 (down): the profile view goes; the users table is untouched by it.
DROP VIEW IF EXISTS user_profiles;
