# ADR-0178 — A private mailbox, sealed to its holder's key

- **Status:** Accepted
- **Date:** 2026-11-07
- **Amends:** ADR-0152 §D4, which recorded sealing as designed and not built
- **Code:** `internal/vayuos/pgp/holder.go`, `internal/vayuos/mail/sealing.go`, `cmd/vayupress/vayuos_mail_private.go`, `cmd/vayupress/vayuos_mail_privatekey.go`.

## 0. The question

A private mailbox (3.18.30) refuses its administrators: they cannot open it,
reset it, mint a credential for it or change its settings, and each attempt
is written to the holder's record. Its files were still plaintext, and its
key was sealed under the install's key, so whoever ran the server could read
the disk. ADR-0152 §D4 named what sealing needed first: a way out for the
mail (an export the app can use), a way in (an importer that holds up at
scale), a phone app that does not depend on the server holding the key, and
a rule for shared addresses. Those now exist.

## 1. Decision

**The key goes to the holder.** Making a mailbox private locks its PGP key,
and every key rotated out before it, with OpenPGP's own passphrase protection
under the mailbox password (Argon2 with AEAD, RFC 9580), and a second copy
under a recovery code shown once and kept nowhere. The install's key still
seals both copies on disk, so a key file reads like any other; opened, it
yields only locked keys. A test opens every key file with the install's key
alone and fails on any usable private key.

**The mail is sealed to it.** Every message written to a private mailbox
(arrival, Sent, Drafts, IMAP APPEND, the importer) is encrypted to the
mailbox's public key before it reaches the disk, and the mail stored before is
sealed in place, each file keeping its name (flags, IMAP identity) and its
time (retention). Encryption needs only the public key, so mail is sealed
whether or not the holder is signed in. A seal that fails refuses the write:
storing the message in the clear is the one outcome the holder asked never to
happen.

**A sealed file is a standard PGP/MIME message.** The whole original message
is the encrypted payload; outside it are only sender, recipients, date and the
message ids, which delivery and threading need. Any OpenPGP client with the
key reads it, so the export of a private mailbox is still the holder's mail,
and the VayuMail app, which takes the locked key and unlocks it on the phone,
reads it over IMAP whether or not the server holds the key open.

**The server reads it only while a sign-in holds the key open.** A sign-in
that proves the mailbox password (IMAP, POP3, the console's mail sign-in, or
the password typed into the sealed notice in the inbox) unlocks the key into
memory for eight hours, never onto disk. Sign-out, any password reset, or the
holder's Close my mail now ends it. Every read of a message file goes through
the Maildir, which opens a sealed message while the key is held open and hands
out the sealed one otherwise, so search, previews, threads and IMAP all work
inside that time and none of them can read past it.

**Passwords and recovery.** The holder's password change relocks the key
under the new password, and back if the change is refused. A password reset
by any other road leaves the key under the old password: the reset gets the
holder signed in, and the recovery code gets their mail back, under the
password they use now, with a new code replacing the one used. An
administrator's emergency override can still reset the password, as ADR-0152
records; it opens the account and not the mail.

**Shared addresses.** A mailbox assigned to more than one person cannot be
made private by one of them.

## 2. What it does not do

- Whoever controls the server's software could change it to catch the
  password at the holder's next sign-in. The guarantee is against the
  administrator role and against stored data, not against a rewritten server.
  The page says so.
- While the key is held open, everything that reads the mailbox through the
  product reads it opened: the holder's own sessions and devices. Who may read
  it at all is still decided by `readAuthorised`, which refuses operators.
- Mail being sent, and scheduled sends, are plaintext until they leave, as
  any server must hold them to send them.
- There is no back door, by the operator's decision of 2026-10-18: lose the
  password and the recovery code and the mail is gone. The holder is told so
  before the switch, in those words.

## 3. Proof

Each rule has a test that fails when the rule is removed, and each was seen
to fail (`holder_test.go`, `sealing_test.go`,
`private_mailbox_sealed_test.go`): the install's key opens no usable key; the
password, and nothing else, holds it open, and the hold ends; a recovery code
works once; a password change relocks and a refused one does not; rotated-out
keys are locked too; the app is handed the same key, locked; every write is
sealed, a failed seal stores nothing, and a forged sealed mark is sealed
again and stripped from inbound; stored mail is sealed in place; the listing
does not keep showing subjects once the key closes; sign-in opens, sign-out
and reset close; a shared mailbox is refused.
