# ADR-0125: VayuMail Per-Account Signatures — Stored Setting, Send-Time Insertion & Self-Service Editing

**Status**: Accepted  
**Date**: 2026-07-08  
**Author**: @johalputt  
**Relates to**: [ADR-0124](ADR-0124-vayumail-outbound-reliability.md) (VayuMail overhaul), the VayuMail engine (`internal/vayuos/mail`), the composer (`cmd/vayupress/vayuos_mail.go`)

## Context

VayuMail had no notion of a sender signature: operators re-typed a sign-off on
every message. This is a Phase 2 slice of the VayuMail enterprise redesign. Three
questions had to be settled: where the signature lives, when it is applied, and
who may edit it.

## Decision

### 1. Storage — a per-account column, not a separate table

A `signature TEXT NOT NULL DEFAULT ''` column is added to `vayumail_accounts` via
the store's existing idempotent `ALTER TABLE ADD COLUMN` migration pattern (the
same used for `role`, `totp_*`, `quota_bytes`). The signature is a 1:1 attribute
of an account, so a column is simpler than a side table and needs no join. It is
bounded to 4 KiB. `AccountStore` gains `SetSignature`/`SignatureFor`, and
`Account.Signature` is returned by `List`.

### 2. Applied at send time, before the quoted history

The signature is **not** baked into the composer textarea or into saved drafts
(which would double it on send and couple the stored draft to a mutable
setting). Instead the send handler appends it server-side, using the RFC 3676
dash-dash-space delimiter, and — reusing `splitQuoted` from the reader — inserts it
**after the freshly-written reply and before any quoted history** rather than at
the very bottom. A per-message "Append signature" toggle (default on) lets the
sender opt out; the composer previews the signature and swaps it live when the
`From` account changes (each `From` option carries its `data-sig`).

### 3. Self-service editing within an admin-only surface

Account management stays admin-only, with **one narrow exception**: a mailbox
holder may update the signature of **their own** address (and nothing else) via
the account-update endpoint. The handler enforces this by requiring, for a
non-admin caller, that the only field present is the signature and that the
target email equals the caller's own mailbox. This keeps signatures self-service
without widening account management.

## Consequences

- Signatures never double (send-time only) and drafts stay setting-independent.
- No new table or route; the change is a column + one endpoint field + composer
  UI. Existing accounts default to no signature (no behaviour change).
- HTML-body signatures and multiple signatures per account are out of scope
  (plain-text only, one per account) and can extend this later.
