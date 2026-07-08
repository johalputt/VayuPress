# ADR-0122: Network-Hardening Tiers in VayuOS — Explanation, Copy-Command UX, and the Privilege Boundary

**Status**: Accepted  
**Date**: 2026-07-08  
**Author**: @johalputt  
**Relates to**: [ADR-0036](ADR-0036-csp-nonce-centralized.md) (strict admin CSP), [ADR-0056](ADR-0056-process-isolation-runtime-sandboxing.md) (process isolation), [ADR-0112](ADR-0112-vayushield-resilience-tiers.md) (VayuShield resilience tiers), [ADR-0120](ADR-0120-collapsible-htmx-shield-console.md) (collapsible HTMX shield console)

## Context

Operators asked to activate the Bot Shield's **Tier 2** (kernel firewall) and
**Tier 3** (edge/nginx) hardening directly from VayuOS — "no terminal" — and
worried that turning them on might slow VayuOS or the public site. The Network
hardening section previously just listed two commands with little explanation.
Two questions had to be answered honestly: *can* the panel toggle them, and do
they hurt performance.

## Decision

### 1. Explain the tiers and correct the performance concern

The section now states plainly what each tier is and that it **improves**
performance rather than degrading it:

- **Tier 2 · nftables (kernel):** per-IP connection/packet rate limits + SYN
  cookies, enforced in the Linux kernel, so a flood is dropped *before* a packet
  reaches VayuPress. Discarding abuse that early **lowers** CPU/DB load under
  attack; legitimate traffic sees no measurable cost.
- **Tier 3 · nginx (edge):** per-IP request/connection shaping + slow-loris
  timeouts at the reverse proxy — abuse absorbed at the edge, normal requests
  untouched.

So no "new technology" is needed to run them smoothly: they are the
high-performance layer and are net-positive for capacity. Each tier shows its
exact command with a **Copy** button (a small same-origin, nonce-gated script —
strict admin CSP preserved, ADR-0036) so the operator pastes rather than types.

### 2. Do NOT execute Tier 2/3 from the unprivileged web app

VayuPress runs as an unprivileged service (`www-data` + `CAP_NET_BIND_SERVICE`).
It cannot — and must not — run `nftables` or reload nginx. Wiring the web panel
to execute root-level firewall/nginx changes would make a web-app compromise a
direct path to root: it would defeat the very isolation that ADR-0056 and the
unprivileged-service posture exist to provide. Therefore Tier 2/3 application
stays an explicit operator action (copy → paste over SSH; both idempotent and
reversible — `vayushield-firewall.sh remove`, or delete the nginx conf + reload).

### 3. Document the safe path to a real in-panel toggle

A genuine one-click switch is achievable **without** granting the web app root,
via a small **privileged helper**: a root systemd service the operator installs
once that watches an intent flag the (unprivileged) web app writes, and runs only
the fixed, vetted scripts with **no arguments taken from the web app** (so there
is no injection surface). The web app never executes privileged code; it only
expresses intent. This is deferred until an operator opts in, because it requires
a one-time privileged install and changes host firewall/nginx state.

### 4. Console polish (same change set)

- Engagement analytics collapses into a **single** card with its four reports as
  flattened sub-sections, so the section's HTMX Refresh control no longer floats
  outside a card.
- The per-section Refresh control is a subtle rounded pill whose icon spins on
  the `htmx-request` state (pure CSS).

## Consequences

- Operators understand what Tier 2/3 do, that they help performance, and exactly
  how to apply/undo them, with copy-paste convenience.
- The unprivileged-service security boundary is preserved and documented; no
  root-exec path is exposed to the web surface.
- A safe, opt-in route to a true in-panel toggle is on record for when an
  operator wants it.
- No new client JavaScript beyond a tiny nonce-gated clipboard helper; the strict
  admin CSP is intact.
