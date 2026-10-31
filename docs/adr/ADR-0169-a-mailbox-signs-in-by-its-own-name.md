# ADR-0169 — A mailbox signs in by a name of its own; its sessions and checklist

- **Status:** Accepted
- **Date:** 2026-10-31
- **Code:** `cmd/vayupress/vayuos_mail_signin.go`, `cmd/vayupress/sessions_mine.go`, `internal/auth/session.go`; migration 128.

## 0. The question

The address people write to is public, so it is what guessers try at every
door. A holder had no way to make the address alone useless for signing in,
no list of where they were signed in, and no single place that said what
was left to make their account safe.

## 1. Decision

**A sign-in name per mailbox.** With one set, the address no longer signs in
with the mailbox's own password at any door (the site, the console, the
app, IMAP, POP3, SMTP, contacts and calendars, VayuTalk); the name does. The
refusal reads and takes as long as a wrong password, so it says nothing
about whether a name is set. App passwords keep working with the address,
so mail apps already set up are not broken.

**Where you are signed in.** A mailbox's Settings, and a staff member's My
profile, list the console sessions open in their name, each with the
browser and system it was opened from (migration 128 adds the device to
each session) and when, "This one" marked, a Sign out on each other one and
Sign out everywhere else. A session is ended only among its own principal's.

**A security checklist with a score**, at the top of Settings › Signing in:
two-factor, a passkey (ADR-0168), a way back in, and no app password left
unused (never used a month after it was made, or not for three months),
each with where to put it right.

## 2. Consequences

- A guesser who knows only the address cannot even reach the password check
  for it, at no cost to existing mail apps.
- The checklist reads the same state the doors enforce; it holds no state of
  its own.
