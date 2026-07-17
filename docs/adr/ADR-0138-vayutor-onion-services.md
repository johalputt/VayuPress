# ADR-0138: VayuTor — one-click Tor onion services for every hosted domain

- **Status:** Accepted
- **Date:** 2026-07-17
- **Deciders:** VayuPress maintainers
- **Supersedes:** —
- **Related:** ADR-0131 (VayuTalk), ADR-0132 (VayuDomains), ADR-0097 (cookieless
  analytics)

## Context

VayuPress is a sovereign, single-VPS system. Even so, a visitor's ISP, the
site's network provider, and any on-path observer can see *that* a given IP
reached the site, and the operator must trust the clearnet transport not to leak
who reads what. Privacy-focused operators asked for a way in that no
intermediary can track — without giving up the normal clearnet site, and with no
loss of speed or quality.

Tor v3 onion services solve exactly this: the connection is end-to-end encrypted
and rendezvous-routed, the server never learns the visitor's IP, and no inbound
port is exposed. The requirement is that **every** hosted domain (VayuDomains,
ADR-0132) gets its own `.onion`, that the clearnet URL and the `.onion` work
**simultaneously**, that activation is **one click** from VayuOS, and that the
only Tor metric shown is a **count** — no identity, no time, nothing else.

## Decision

Add **VayuTor**, a VayuOS subsystem (`internal/vayuos/vayutor`) that drives a
locally-running `tor` daemon over its authenticated control port.

**Control-port, not torrc files.** VayuPress connects to Tor's control port and
issues `ADD_ONION` per domain, mapping virtual port 80 to its own local HTTP
port (`127.0.0.1:8080`). This makes activation truly one-click — no torrc
editing, no tor restart. Onions are created **without** the `Detach` flag, so
their lifetime is bound to VayuPress being up. Each identity's ed25519 key is
persisted (table `tor_onions`) so the **same `.onion` address is stable across
restarts** and survives a DB restore. The deploy/update script installs `tor`,
enables the cookie-authenticated control port on loopback, and adds the
`www-data` service user to the `debian-tor` group so it can read the auth
cookie — no elevated privilege, and **no inbound port is opened**.

**Both URLs, one site.** An early request middleware maps an incoming
`<onion>.onion` Host to the clearnet host it represents and rewrites `r.Host`
before the VayuDomains resolver runs, so the exact same per-domain routing and
content serve over Tor with zero duplication. On clearnet responses for a domain
that has an onion, an `Onion-Location` response header advertises the `.onion`
so Tor Browser can discover/auto-switch to it. The clearnet path is byte-for-byte
unchanged and pays nothing when VayuTor is off.

**One-click + dormant by default.** The env var `VAYUOS_TOR` is a master switch
(default available; `off` hard-disables). The real control is a settings toggle
(`tor.enabled`, default off) flipped on the VayuTor page. Onions are created only
while the toggle is on **and** a control port is reachable; otherwise VayuTor is
completely dormant (no connection attempts). A non-critical subsystem: an onion
outage never affects the clearnet site or boot.

**Count-only analytics.** The single VayuTor metric is an aggregate onion
pageview counter (`tor.visits`). No IP (Tor provides none), no timestamp, no
path, no user agent, no cookie — nothing per-visitor is ever recorded or
derivable. The counter is an in-memory atomic flushed to one settings key on a
timer (bounded DB writes) and on shutdown.

## Consequences

- A sovereign site gains a private, un-trackable entry point for **every** hosted
  domain with one click, alongside the clearnet URL, with **no new table beyond
  the onion-key store, no new inbound port, and no plaintext or PII exposure**.
- Onion keys live in the operator's own database (same custody as everything
  else they own); a restore brings the same `.onion` addresses back. Losing the
  DB means new addresses — an accepted trade for keeping keys under sovereign
  control rather than in Tor's separate `HiddenServiceDir`.
- The control-port coupling means onions require the local `tor` daemon to be
  running with its control port enabled; the deploy/update automation sets this
  up, and the VayuTor page reports clearly when the daemon is unreachable.
- Strictly CSP-safe: the admin page is server-rendered with a nonce'd,
  same-origin island (copy + count poll); `Onion-Location` is a plain response
  header and needs no CSP allowance.
- Tor's exit/rendezvous path is higher-latency than clearnet by nature; VayuTor
  does not slow the clearnet site at all, and onion visitors get the same content
  and features. This is a property of Tor, not of VayuPress.

## Addendum — managed tor daemon (v3.13.61)

The original design coupled VayuTor to a **separately-configured** system `tor`
service (control port on `127.0.0.1:9051`, cookie auth, group membership set up
by the deploy/update script). That worked, but left a gap: the **in-app
one-click self-update replaces only the binary**, so a server that first gained
VayuTor via that path — or was provisioned before the control-port automation
existed — would activate the toggle and sit at "connection refused."

VayuTor now runs its **own tor daemon** when no external control port is
reachable. Because `tor` runs happily as an ordinary user with a user-owned
`DataDirectory`, VayuPress spawns it as a child process under the unprivileged
service user, with a generated torrc (`ControlPort unix:<state>/tor/control.sock`,
`CookieAuthentication 1`, `SocksPort 0`, no relay/exit/dir surface). The engine
prefers a reachable `VAYUOS_TOR_CONTROL_ADDR` (an operator's purpose-built tor)
and falls back to the managed instance. The only remaining one-time root step is
installing the `tor` **binary** (`apt-get install tor`, which the deploy/update
script still does) — everything else is automatic and survives binary-only
updates. Deactivating VayuTor kills the managed child, so onions and the process
both stop; the persisted onion keys still bring the same addresses back on
re-activation. Env knobs: `VAYUOS_TOR_MANAGED` (default on), `VAYUOS_TOR_BINARY`,
`VAYUOS_TOR_DIR`.

## Addendum — bootstrap escalation & bridges (v3.13.67)

Real servers surfaced a ladder of ways a network can stop Tor from bootstrapping
(so onions never publish even though activation "succeeded"). VayuTor now handles
them with a **one-shot escalation ladder** on the managed tor, driven by the
existing stall detector (no forward bootstrap progress for 150 s — a slow but
climbing bootstrap is never disturbed):

1. **direct** — no restriction.
2. **firewall-friendly** (`ReachableAddresses *:80,*:443`) — for hosts that only
   permit outbound 80/443.
3. **bridges** (`UseBridges 1` + `Bridge …` lines, with `ClientTransportPlugin
   obfs4 exec …` when the lines are obfs4) — for hosts that block Tor at the IP
   level (a VPS null-routing public relay IPs, or DPI). When tor's log proves an
   IP-level block (`No route to host`/NOROUTE), or the operator has configured
   bridges, VayuTor **skips straight to bridges** — ports-only cannot help a
   null-routed relay. `escBridges` is terminal; the ladder resets on deactivation.

Bridges are **operator-supplied** — configured entirely **from the VayuTor admin
page** (saved to `settings.tor.bridges`, applied live with no restart and no
server access) or via the `VAYUOS_TOR_BRIDGES` env var; the settings value wins.
obfs4 lines come from bridges.torproject.org; the built-in default set is
intentionally empty — VayuPress does not ship third-party bridge infrastructure.

**Embedded obfs4 transport.** The obfs4 pluggable transport is **built into the
VayuPress binary** (via the lyrebird obfs4 client library, exposed as the hidden
`__run-obfs4` subcommand): the managed tor is told `ClientTransportPlugin obfs4
exec <vayupress> __run-obfs4`, so obfs4 bridges work with **no `obfs4proxy`
package installed** — nothing to apt-install, nothing done outside VayuOS. It is a
client transport only (a loopback SOCKS listener that dials out through obfs4);
its `pt_state` lives under the writable DataDirectory, and it exits when the
managed tor does. A system `obfs4proxy`/`lyrebird` is still preferred when
present. Operators paste **IPv4** obfs4 bridges (the server needs working IPv6 to
use IPv6 bridges — most don't). The obfs4 pluggable
transport (`obfs4proxy`, installed by the deploy/update script) is spawned by the
managed tor as the same unprivileged user, with its `pt_state` under the writable
DataDirectory — no root, no new inbound surface. Because v3 onion services are
end-to-end encrypted and VayuTor's onion↔domain mapping is intentionally public
(co-hosted with the clearnet Let's Encrypt site), routing the server's own
circuits through third-party bridges is anonymity- and confidentiality-neutral,
and the "count-only, nothing tracked" privacy posture is unchanged. Diagnostics:
the VayuTor page reads tor's log tail and shows the exact remedy (allow outbound,
fix the clock, install obfs4proxy, or paste bridges) plus the current transport.

## Addendum (v3.13.72) — VayuPress-managed current Tor

The last remaining VayuTor blocker on a real deployment was **not** VayuPress: a
host running an end-of-life OS (Debian 10 "buster") ships Tor **0.4.2.7 (2020)**,
which today's directory authorities reject ("Consensus not signed by sufficient
number of requested authorities"). No bridge can fix this — bridges route *around*
a network block, but consensus validation is done by the local tor, and an
ancient tor simply cannot validate the current network. Updating a system package
needs root, which an unprivileged web app must never assume.

To honour the "everything from VayuOS, nothing on the server" constraint, the
managed tor now **downloads and runs its own current Tor** when the system tor is
absent or older than 0.4.7 (`internal/vayuos/vayutor/dist.go`):

- **Source.** The Tor Project's official static *expert bundle*, discovered from
  the distribution index (`dist.torproject.org/torbrowser/`), newest stable
  first, with the archive mirror and both filename conventions as fallbacks,
  fetched over CA-validated HTTPS. Only the `tor` binary and its bundled shared
  libraries are extracted (flat, by base name — path-traversal-safe), into
  `<tor-dir>/dist/`; the binary is run with `LD_LIBRARY_PATH` pointed at that dir
  so it needs nothing from the host beyond libc.
- **Trust & validation.** Transport authenticity comes from HTTPS to the official
  host. Before use, the downloaded binary must execute and report a version
  ≥ 0.4.7; anything that fails to run (typically an ancient host libc rejecting a
  modern build) or is too old is discarded.
- **No regression.** Any failure falls back to the system tor exactly as before.
  A failed download is cooled down (15 min) so it isn't retried every reconcile;
  toggling VayuTor off→on clears the cooldown for an immediate retry. Cached
  across restarts (re-validated on load). Disable with `VAYUOS_TOR_DOWNLOAD=off`.
- **Operator-visible fallback.** When even the downloaded build won't run on the
  host, the admin page states the reason and gives a one-time, run-once-as-root
  `apt` command to install a current, self-updating Tor from Tor Project's
  official repository — the guaranteed path for a host too old for a modern static
  build. For an EOL OS, upgrading the OS remains the durable fix; everything else
  in VayuTor already works (bridges + embedded obfs4 defeat the provider's Tor
  block; only consensus validation needs a current tor).

This keeps VayuTor's promise intact: one click in VayuOS, nothing done on the
server — now even on hosts whose own Tor is too old to join the network.
