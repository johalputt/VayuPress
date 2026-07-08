# ADR-0123: In-Panel Tier 2/3 Activation via an Unprivileged Intent + Root Reconcile Agent

**Status**: Accepted  
**Date**: 2026-07-08  
**Author**: @johalputt  
**Relates to**: [ADR-0122](ADR-0122-network-hardening-tiers-ui.md) (Tier 2/3 UI + privilege boundary), [ADR-0056](ADR-0056-process-isolation-runtime-sandboxing.md) (process isolation), [ADR-0112](ADR-0112-vayushield-resilience-tiers.md) (VayuShield resilience tiers)

## Context

Operators want to turn VayuShield's **Tier 2** (kernel nftables) and **Tier 3**
(nginx edge) hardening on and off from VayuOS — no terminal. But applying those
needs root (`nftables`, `systemctl reload nginx`), and VayuPress deliberately
runs as an **unprivileged** service (`ProtectSystem=strict`, no `CAP_NET_ADMIN`,
`SystemCallFilter` blocks `@privileged`). Giving the web app the ability to run
root-level firewall/nginx commands would make a web-app compromise a direct path
to root — defeating the very isolation the design depends on (ADR-0056, ADR-0122).
So a naïve "run the script from the handler" is not acceptable.

## Decision

Separate **intent** (unprivileged) from **action** (privileged) with a one-way,
input-free channel.

### The web app expresses intent only
The VayuOS toggle writes or removes an **empty flag file** in a control directory
the service user owns (`<state>/vayushield-control/tier{2,3}.want`). That is the
only thing the panel does — no privileged call, no parameters, just create/delete
an empty file it already has permission to write. (`handleOSShieldTier`,
`shieldSetTierWant`.)

### A separate root agent performs the action
`deploy/vayushield-agent.sh` runs as its own **root** systemd service
(`vayushield-agent.service`), completely separate from the `vayupress` service.
It polls the control dir and, on a change, runs **only the fixed, vetted,
root-owned scripts** installed at `/usr/local/lib/vayushield`
(`vayushield-firewall.sh apply|remove`; copy/validate/reload the nginx conf). It
**never reads any argument or content** from anything the web app writes — the
mere *presence* of a flag file is the entire signal — so there is **no
command-injection surface**. It writes back a status word (`tierN.state`) and a
heartbeat (`agent.alive`) that the panel only *displays* / uses to detect that
the helper is installed.

### Ownership / trust
The control dir is owned by the **service user** (created by the installer /
lazily by the app), so the unprivileged app writes flags there and the root agent
only reads them (root can also write status into an app-owned dir). The agent
never trusts flag *contents*; a bad nginx conf is validated with `nginx -t` and
**rolled back before reload**, so a toggle can never leave the site down.

### Zero-extra-terminal install
The agent is installed/refreshed automatically by `scripts/update-vayupress.sh`
(which already runs with the required privileges during a normal update) — copying
the scripts to `/usr/local/lib/vayushield`, installing the unit, and enabling it.
So the operator gets in-panel toggles after their next normal update with **no
extra terminal step**; the one-time privileged bootstrap rides the update they
already run. (`VAYUSHIELD_AGENT=0` opts out.) The in-app updater cannot do this
(it is unprivileged), which is correct — only a genuinely privileged path may
install a root service.

## Consequences

- Tier 2/3 become real on/off switches in VayuOS, with live status and full
  reversibility, and **no terminal** for day-to-day use.
- The unprivileged-service security boundary is fully preserved: the web app
  gains nothing; all privileged work is isolated in a root agent that trusts none
  of the web app's input.
- Until the agent is installed (i.e. before the next update), the panel says so
  and falls back to the documented copy-paste commands (ADR-0122).
- Because per-IP kernel limits act on whatever IP reaches the origin, Tier 2 is
  fully effective for a **direct-to-origin** deployment; behind a CDN it mainly
  guards direct-to-origin attacks (the panel notes this). Tier-1 in-binary
  defenses and Tier-3 edge shaping are unaffected by that distinction.
