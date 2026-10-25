-- Migration 121: which pages an ad slot shows on.
-- show_on is '' (every page that has the slot's placement, as before), 'posts',
-- 'pages' or 'chosen'; show_paths holds the chosen addresses, one a line ("/"
-- is the home page). Existing slots keep showing everywhere.
-- One statement per line, as the runner requires.
ALTER TABLE ad_slots ADD COLUMN show_on TEXT NOT NULL DEFAULT '';
ALTER TABLE ad_slots ADD COLUMN show_paths TEXT NOT NULL DEFAULT '';
