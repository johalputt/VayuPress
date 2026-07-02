# ADR-0105: Operator-Only Encrypted Backups (AES-256-GCM + Argon2id)

**Status**: Accepted
**Date**: 2026-07-02
**Author**: @johalputt
**Owner**: Core
**Relates to**: [ADR-0042](ADR-0042-backup-restore-automation.md), [ADR-0089](ADR-0089-vayuos-one-click-update-and-backup.md), [ADR-0076](ADR-0076-vayupgp-at-rest-keys.md)

## Context

VayuPress could already snapshot and restore the data directory (ADR-0042,
ADR-0089). But those archives were plaintext tarballs: anyone who obtained a
copy — a leaked cloud snapshot, a stolen laptop, a misconfigured bucket — could
read the entire site, including private data that is otherwise protected at
rest: VayuMail maildirs, VayuPGP private keys (themselves encrypted, but the
surrounding mail and metadata are not), member records, and settings.

For a "privacy-first, sovereign" product this is the wrong default. The
requirement: **a copied backup must be useless to anyone but its creator** —
unreadable, unmodifiable, and non-enumerable with any modern tool, unless you
hold the passphrase.

## Decision

Add `internal/backup` and a `vayupress backup` / `vayupress restore` CLI that
produce and consume a **fully encrypted, authenticated archive** of the data
directory (SQLite DB + settings, media, VayuMail maildirs, PGP key store).

### Format

```
magic "VPBK1\n" · salt[16] · frames…
frame = len(uint32 BE) · AES-256-GCM ciphertext   (nonce = frame counter)
```

- The plaintext is a streaming `tar` + `gzip` of the data directory, chunked
  into 1 MiB frames.
- Each frame is sealed with **AES-256-GCM**; the 96-bit nonce is a strictly
  increasing frame counter, safe because the key is unique per backup.
- The key is derived with **Argon2id** (t=3, m=64 MiB, p=2 — the project's
  password-hashing posture) from the operator's passphrase and a random
  per-backup 16-byte salt.

### Properties this gives us

- **Confidential:** without the passphrase the archive is indistinguishable
  from random; filenames and content never appear in the ciphertext (verified
  by a plaintext-leak test).
- **Tamper-evident:** every frame is independently authenticated, so any bit
  flip fails `Open` before a single byte is written on restore. A wrong
  passphrase restores nothing (`ErrBadPassphrase`).
- **Streaming:** frames are sealed and written incrementally, so a multi-GB
  data directory never has to fit in memory.
- **Path-safe restore:** archive entries are cleaned and rejected if they
  escape the destination, so a crafted backup cannot write outside `-dest`.

### Passphrase handling

The passphrase is read from `VAYU_BACKUP_PASSPHRASE` or prompted on stdin —
**never** from argv (which would leak into shell history and process listings).
An empty passphrase is refused: it is the only key to the backup, and losing it
means losing the data, so this is documented plainly in the CLI output.

## Consequences

- A stolen or leaked backup is inert: no modern tool can read, alter, or
  enumerate it without the passphrase, satisfying the sovereignty/privacy goal.
- The passphrase is unrecoverable by design — there is no escrow, no backdoor,
  no "reset". This is the correct trade for a single-operator, sovereign
  product, and the CLI states it explicitly.
- The plaintext-backup path (ADR-0042/0089) is superseded for off-box storage;
  the encrypted archive is the recommended way to move a site between servers or
  keep an off-site copy.
- Round-trip, wrong-passphrase, tamper, not-a-backup and plaintext-leak tests
  guard the format and its guarantees.
