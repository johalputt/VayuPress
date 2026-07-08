# ADR-0124: VayuMail Outbound Reliability — Resend, Retry-Until-Sent & One-Click Dependency Updates

**Status**: Accepted  
**Date**: 2026-07-08  
**Author**: @johalputt  
**Relates to**: [ADR-0120](ADR-0120-collapsible-htmx-shield-console.md) (HTMX console pattern), [ADR-0064](ADR-0064-sovereign-self-update.md) (self-update), the VayuMail engine (`internal/vayuos/mail`)

## Context

VayuMail's outbound queue (`internal/vayuos/mail/queue.go`, table
`vayumail_queue`) is durable with exponential-backoff retry, but the Outbox tab
was **read-only**: an operator could see "failed" but could not resend, retry, or
clear a message, and the panel only refreshed on a full reload. Two concrete
operator needs: (1) the Outbox should keep trying until a message sends, and give
a one-click Resend when it eventually gives up; (2) a one-click way to apply
dependency/security updates. This is the first slice of a broader VayuMail
"enterprise-grade" overhaul.

## Decision

### 1. Durable requeue operations (no data model change)

Add three operations to the existing queue: `Requeue(id)` (reset a message to
`state='pending'`, `attempts=0`, fresh `max_attempts`, `next_attempt=now`),
`RequeueAllFailed()`, and `Delete(id)`. Requeue works on both `failed` and
`pending` rows, so a message that exhausted its retries during a transient outage
can be sent again with one click. Engine wrappers (`ResendQueued`,
`RetryAllFailed`, `DeleteQueued`) also kick an **immediate** off-request delivery
pass (`ProcessDue`) so a Resend acts at once instead of waiting for the 30s
worker tick.

### 2. Retry-until-sent (generous auto-retry, then one-click Resend)

Rather than literally-infinite retries (which would hammer a permanently-dead
address and look like spam), the default `QueueMaxAttempts` is raised 12 → **20**.
With the existing backoff (2m doubling, capped at 6h) that auto-retries a
transient failure for ~4+ days before marking it `failed` — at which point the
operator gets a one-click **Resend** (and **Retry all failed**). This is the
pragmatic, deliverability-safe reading of "keep trying until it sends".

### 3. Live Outbox via HTMX

The Outbox body is an HTMX fragment (`GET /os/vayumail/outbox/fragment`,
`hx-trigger="every 15s"`) that also re-renders in place after a Resend/Delete/
Retry-all (`POST /os/vayumail/outbox/action` → returns the refreshed body). Same
CSP-safe pattern (self-hosted HTMX, nonce-gated glue, `vp_csrf` cookie mirrored to
the header) already used by the Bot Shield console — no new JS, strict admin CSP
intact.

### 4. Dependency updates are applied by installing a signed release

VayuPress is a single, statically-linked binary with its dependencies compiled
in. There is therefore **no server-side `go get`/rebuild** to run, and the
unprivileged web app must not attempt one. "Updating dependencies" = installing
the latest **signed release**, which is built with the patched dependencies. The
Security-updates tab now surfaces a one-click **Update VayuPress** action that
routes to the existing verified self-updater (ADR-0064: SHA-256 + Ed25519,
atomic swap, auto-rollback), and states this plainly. The `secwatch` monitor
stays monitor-only.

## Consequences

- Outbound mail is operator-recoverable: nothing is stuck with no recourse, and
  ordinary transient failures self-heal for days without any action.
- The Outbox is live and consistent with the rest of the modern VayuOS console.
- Applying security patches is one click, with the full verified-update safety
  net, and honestly framed for a single-binary deployment.
- Data model unchanged (same `vayumail_queue` table); the new operations are
  additive and covered by unit tests.

## Not in this release (VayuMail overhaul roadmap)

Compose autosave + HTMX send, mailbox actions via HTMX (read/move/delete without
reload), threaded conversations, richer search, server-side filters, and a design
refresh across the mail tabs — to be delivered as the remaining screens are
reviewed.
