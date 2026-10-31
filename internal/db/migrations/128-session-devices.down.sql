-- Down for 128: sessions no longer say what they were opened from.
ALTER TABLE sessions DROP COLUMN device;
