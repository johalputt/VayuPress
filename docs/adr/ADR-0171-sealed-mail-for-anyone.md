# ADR-0171 — Mail to anyone, sealed with a passphrase

- **Status:** Accepted
- **Date:** 2026-10-31
- **Code:** `internal/vayuos/mail/sealed.go`, `cmd/vayupress/vayuos_mail_sealed.go`.

## 0. The question

Mail is end-to-end encrypted only between people with keys. Most
correspondents have none, and the confidential-mode schemes of the large
providers keep the message readable by the provider. A holder needed to send
something private to anyone at all.

## 1. Decision

**A sealed message is not sent.** It stays on this server, sealed with a
passphrase the sender tells the recipient another way, and the recipient is
mailed a link (`/sealed/{id}`). On that page the passphrase opens it, for a
day, a week or thirty days as the sender chose (`SealedMaxTTL`), and the
recipient can answer it there; the answer lands in the sender's Inbox from
the address it was sealed for.

**The server keeps only a salt, a nonce and the sealed box.** The key is
Argon2id over the passphrase (three passes, 64 MiB, two lanes), the box is
AES-GCM, and the message's id is the associated data, so a box cannot be
moved under another id. A passphrase is at least ten characters
(`SealedMinPassphrase`).

**Guessing is bounded three ways.** Ten wrong passphrases close the message
for good (`sealedMaxFailures`); wrong passphrases count against the address
as wrong passwords do (ADR-0167); and at most four Argon2 derivations run at
once, so the memory-hard step cannot be used to exhaust the server. The
sender can close the message at any time, and it is gone when its time ends.

**Only a mailbox's holder seals**, from their own mailbox: an administrator
reading someone else's mailbox is not offered it, and the page refuses it.

## 2. Consequences

- A stolen database holds no readable sealed message; a stolen passphrase
  opens one message until it closes.
- The passphrase never reaches the server's storage or logs; it exists in
  the request that opens the message and nowhere else.
