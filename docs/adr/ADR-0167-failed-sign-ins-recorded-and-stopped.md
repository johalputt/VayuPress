# ADR-0167 — Failed sign-ins are recorded, seen and stopped

- **Status:** Accepted
- **Date:** 2026-10-31
- **Code:** `internal/signin`, `cmd/vayupress/signin.go`, `cmd/vayupress/admin_os_signin.go`; migrations 125 and 127.

## 0. The question

A mailbox's address is public: it is on every message its holder sends. So
it is what a guesser tries, at every door that takes a password: the site's
mail sign-in, the console, the app, IMAP, POP3, SMTP, contacts and calendar
sync, VayuTalk and the recovery code. The install refused wrong passwords
and locked an account after repeated failures, but it kept no record of who
was trying, could not tell one person mistyping from one source trying many
accounts, and had no way to stop a source rather than an account.

## 1. Decision

**Every failed sign-in is recorded, at every door, by account, door and
source.** What is recorded is the outcome: a wrong password, a right
password with a wrong second factor, or a try at a paused account.

**An address is never kept whole.** It is stored twice: masked for people
(`203.0.x.x`) and as a keyed fingerprint for matching. The fingerprint key
is derived by the secrets store and is not in the database, so a block on a
source holds exactly while nobody, the database's reader included, can read
the address back. Attempts are kept 30 days (`signin.Retention`).

**Password spraying is blocked by itself.** A source failing on three or more
accounts within an hour (`SprayAccounts`, `SprayWindow`) is refused sign-in
for six hours (`SprayFirst`); caught again within a day of that ending, for
twice as long, up to a day (`SprayMax`). An address on the install's own
network is never blocked automatically: it may be the proxy every visitor
comes through. Sessions already signed in are not touched by any block.

**A right password with a wrong second factor raises an alarm**: the holder
is told at once, at their address and their recovery address, where the
attempt came from and through which door. That is the attempt that means
the password is known.

**A blocked source, or one outside an account's countries, is refused before
its password is checked**, so a block costs the attacker the answer too. A
block or a country fence that would stop the person setting it is refused.

**Blocks can cover the whole site** (migration 127): an address, or its
network, refused on every page for 30 days, matched by its fingerprint
before anything else is done for the request, and costing nothing while no
such block exists. An address on the install's own network can be blocked
from sign-in but never from the whole site.

**Shield › Sign-in attempts** shows the record over 24 hours, 7 or 30 days,
with a Block menu on each source and "It was one of us", and lists each
block in force with Lift. Every action is in the audit log. Each mailbox's
settings show its own attempts to its holder and to an administrator.

## 2. Consequences

- A guess at an address is one row, not a log line, and is gone after 30
  days.
- The fingerprint key is part of what a restore needs for existing blocks to
  keep matching; a restore without it starts with no blocks, never with
  wrong ones.
- The same three-account rule applies at every door, because they share one
  recorder (`signin.go`), not one copy per protocol.
