-- Migration 135 (up): the language a person reads the console in, chosen from the account menu. '' is their browser's. Kept on the account so a new browser, signed in, is in it at once. NOTE: runMigrations executes line-by-line, so keep each statement on ONE line.
ALTER TABLE users ADD COLUMN console_lang TEXT NOT NULL DEFAULT '';
