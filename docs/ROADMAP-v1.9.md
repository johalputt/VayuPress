# VayuPress v1.9.0 Roadmap — "Stable Private Email"

> Theme: finish the sovereignty story started in v1.8.0 by making **self-hosted
> email genuinely usable**, while staying inside the Operational Simplicity
> Doctrine (one binary, low resource, easy maintenance).

This roadmap is intentionally scoped to the operator's real needs rather than
feature breadth: **a mailbox you can actually receive and read mail in**, lower
resource use at scale, and less maintenance burden.

## 1. Inbound mail (headline)

The receive side of VayuMail, built incrementally so each step ships working:

- [x] **Inbound storage + read access** (landed first on `feat/v1.9.0-inbound-mail`):
      local delivery into Maildir (`Engine.DeliverInbound`), mailbox listing and
      raw message read (`Maildir.List` / `ReadRaw`), per-account inbox summaries
      (`Engine.Mailboxes`), and a `/os/vayuos/mail/inbox` panel view. Fully tested
      (deliver → list → read roundtrip, path-traversal rejection).
- [ ] **SMTP receive (port 25 listener)** — accept inbound mail, run SPF/DKIM/
      DMARC verification, then hand off to `DeliverInbound`. Greylisting + the
      existing rate-limiter to bound abuse. Gated behind an explicit
      `VAYUOS_MAIL_INBOUND=on` switch (Operational Simplicity Doctrine).
- [ ] **IMAP read access (port 993, TLS)** — minimal IMAP4rev1 so standard
      clients (Thunderbird, mobile) can read the Maildir. Auth delegated to
      VayuPress accounts via the existing `Bridge.AuthUser`.
- [ ] **Auto PGP decrypt on read** — transparently decrypt PGP messages for the
      owning account when serving them, reusing VayuPGP.

## 2. Low-resource optimisation

- [ ] Analytics rollups: fold `analytics_pageviews` into daily aggregates beyond
      a configurable window so storage/RAM stay flat on high-traffic sites.
- [ ] Make secwatch + DNS health checks fully off the request path (cached,
      background-refreshed) so the panel never blocks on network.
- [ ] Idle-RAM audit for the mail queue worker and PGP keystore caches.

## 3. Easier maintenance

- [ ] Wire the v1.8.0 security-update watcher into an **admin-confirmed**
      self-update flow (no autonomous action — surfaces the patch, operator
      approves).
- [ ] First-boot wizard at `/os/vayuos/setup`: capture domain + admin email +
      password, then auto-configure TLS, DKIM, DNS guidance, and the admin PGP
      key in under two minutes.
- [ ] One-command DNS verification report (copy-paste records + live status).

## Non-goals for v1.9.0

- No external mail relay dependency (sovereignty: direct MX only).
- No webmail UI beyond the read-only inbox summary (clients use IMAP).
- No telemetry, ever.

## Constitution checkpoints

One binary ✓ · privacy by default ✓ · zero telemetry ✓ · Apache-2.0 chain clean
✓ · a new always-listening daemon is admitted only behind an explicit opt-in and
documented as a governed milestone.
