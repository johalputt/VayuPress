-- Migration 128: the device a console session was opened from, as a short
-- label ("Firefox on Linux"), so a holder's list of sessions says which is
-- which. One statement per line, as the runner requires.
ALTER TABLE sessions ADD COLUMN device TEXT NOT NULL DEFAULT '';
