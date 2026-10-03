-- Migration 104 (up): how a person reads mail. '' is beside the list (three columns); 'full' opens a message over the list at full width, with back to the list. A person's choice, kept on the account so it follows them to every browser. NOTE: runMigrations executes line-by-line, so keep each statement on ONE line.
ALTER TABLE users ADD COLUMN mail_layout TEXT NOT NULL DEFAULT '';
