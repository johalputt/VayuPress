-- Migration 102 (up): the contact form's auto-reply moved from the Pages surface, which stored "on"/"off", to a Settings toggle, which stores "true"/"false". Without this an install that had turned it off would show the toggle off and keep replying, since the reader now treats only "false" as off. NOTE: runMigrations executes line-by-line, so keep each statement on ONE line.
UPDATE site_settings SET value='true' WHERE key='contact.autoreply' AND value='on';
UPDATE site_settings SET value='false' WHERE key='contact.autoreply' AND value='off';
