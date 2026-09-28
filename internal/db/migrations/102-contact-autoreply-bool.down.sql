-- Migration 102 (down): the contact auto-reply back to the "on"/"off" the Pages surface stored.
UPDATE site_settings SET value='on' WHERE key='contact.autoreply' AND value='true';
UPDATE site_settings SET value='off' WHERE key='contact.autoreply' AND value='false';
