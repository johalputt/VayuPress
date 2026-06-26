-- Links a VayuMail mailbox address to a CMS user/account. When an admin assigns
-- a mailbox to a team member, this records the address so the member's VayuOS
-- mail panel scopes to their own mailbox. Empty means no mailbox assigned.
ALTER TABLE users ADD COLUMN mail_address TEXT NOT NULL DEFAULT '';
