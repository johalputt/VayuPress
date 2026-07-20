-- Member mailbox link (migration 071). When a paid member on a mail-enabled tier
-- claims their included VayuMail mailbox, the chosen address is recorded here so
-- the portal can show it and a member is limited to one included mailbox. Empty
-- means the member has not claimed a mailbox. One statement per line.
ALTER TABLE members ADD COLUMN mail_address TEXT NOT NULL DEFAULT '';
