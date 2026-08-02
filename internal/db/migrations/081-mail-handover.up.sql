-- Migration 081: mailbox handover and its access record (ADR-0152 Phase 5).
--
-- The migration runner executes ONE statement per physical LINE.
--
-- WHAT HANDOVER IS, AND WHAT IT IS NOT. After a mailbox is handed over, the
-- operator can no longer open it THROUGH THE PRODUCT: not from the panel, not
-- with their own console password over IMAP, not by resetting its password or
-- clearing its second factor, and not by minting a credential for it. What
-- remains is a command-line break-glass that cannot run without writing a
-- permanent, client-visible record.
--
-- It is NOT encryption. The messages are still readable files on a server the
-- operator runs, and anyone with direct access to that machine, its database or
-- a backup can still read them. ADR-0152 section D4 records why the cryptographic
-- version is deliberately not built, and states the exact sentence an agency may
-- put in front of a client. Nothing here licenses a stronger one.
--
-- Handover is ONE-WAY and the break-glass record is PERMANENT, enforced by
-- triggers rather than by application code: a promise that can be quietly undone
-- with an UPDATE is not a promise, and the party who would undo it is the same
-- party who runs the database.
CREATE TABLE IF NOT EXISTS mail_handover(mailbox TEXT PRIMARY KEY,handed_at DATETIME,handed_by TEXT NOT NULL DEFAULT '',recovery_contact TEXT NOT NULL DEFAULT '',break_glass_at DATETIME,break_glass_actor TEXT NOT NULL DEFAULT '',break_glass_reason TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS idx_mail_handover_handed ON mail_handover(handed_at);
CREATE TRIGGER IF NOT EXISTS mail_handover_is_one_way BEFORE UPDATE ON mail_handover FOR EACH ROW WHEN OLD.handed_at IS NOT NULL AND NEW.handed_at IS NULL BEGIN SELECT RAISE(ABORT,'mail_handover: handover cannot be undone'); END;
CREATE TRIGGER IF NOT EXISTS mail_handover_break_glass_is_permanent BEFORE UPDATE ON mail_handover FOR EACH ROW WHEN OLD.break_glass_at IS NOT NULL AND (NEW.break_glass_at IS NULL OR NEW.break_glass_at<>OLD.break_glass_at) BEGIN SELECT RAISE(ABORT,'mail_handover: the break-glass record is permanent'); END;
CREATE TRIGGER IF NOT EXISTS mail_handover_no_delete_after_handover BEFORE DELETE ON mail_handover FOR EACH ROW WHEN OLD.handed_at IS NOT NULL BEGIN SELECT RAISE(ABORT,'mail_handover: a handed-over record cannot be deleted'); END;
CREATE TABLE IF NOT EXISTS mail_access_ledger(seq INTEGER PRIMARY KEY AUTOINCREMENT,ts DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,mailbox TEXT NOT NULL,actor TEXT NOT NULL DEFAULT '',action TEXT NOT NULL,detail TEXT NOT NULL DEFAULT '',prev_hash TEXT NOT NULL DEFAULT '',entry_hash TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS idx_mail_ledger_mailbox ON mail_access_ledger(mailbox,seq DESC);
CREATE TRIGGER IF NOT EXISTS mail_access_ledger_is_append_only BEFORE UPDATE ON mail_access_ledger BEGIN SELECT RAISE(ABORT,'mail_access_ledger is append-only'); END;
CREATE TRIGGER IF NOT EXISTS mail_access_ledger_no_delete BEFORE DELETE ON mail_access_ledger BEGIN SELECT RAISE(ABORT,'mail_access_ledger is append-only'); END;
