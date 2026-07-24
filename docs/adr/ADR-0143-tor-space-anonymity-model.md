# ADR-0143 — Tor Space anonymity model & threat boundary

Status: Accepted (implemented: layered egress defense, DNS-before-resolve guard,
process-wide tripwire, anonymity self-audit; one-click in-app Tor Space inherits
all of it. Live multi-onion behaviour is validated on real deploys, not in CI.)
Date: 2026-07-24
Deciders: VayuPress core

## Context

ADR-0141 (VayuOS Spaces) introduced the **Tor Space** — a whole-install mode
(`VAYUOS_MODE=tor`, `config.Cfg.OnionMode`) served only as a Tor v3 `.onion`,
with its own database, no clearnet domain and no CA-TLS. The point of that world
is **operator anonymity**: someone running VayuPress on a home server or a cheap
VPS should be able to publish a site, run mail and chat, and **not** reveal the
server's real IP address — which, for a home server, is the operator's home.

Anonymity is not a single switch. The asset we protect is the **server's real IP
address** (and anything that correlates to it). The ways software can betray it
are many and easy to add by accident: any outbound HTTP call, a DNS lookup, an
SMTP relay, a webhook, a payment call, an "update check", a hotlinked image the
reader's browser fetches, or simply binding the site to a public interface where
a port scan finds it. Each is a **clearnet callback** that, in a Tor Space, hands
a third party (or a passive observer) the IP the whole exercise is meant to hide.

This ADR records the anonymity model as implemented, the layered defense that
enforces it, and — deliberately and prominently — **what it does not protect
against**. We refuse to claim "100% anonymous": a false guarantee would lead a
real person to take a risk the software cannot actually cover.

## Decision

### 1. Bind nowhere public
In OnionMode the HTTP listener binds `127.0.0.1` only (`onionSafeBindAddr`). The
site is reachable solely through Tor's hidden-service port, so a scan of the
host's public IP serves nothing.

### 2. No clearnet callback — enforced in layers
The invariant is: **in a Tor Space, no connection leaves to a clearnet host from
the real IP.** It is enforced in depth so that a single forgotten call site can
never silently break it:

- **Central kill-switch.** `safefetch.SetBlockClearnetEgress(OnionMode)` makes
  every `safefetch` dial fail closed — closing every current and future
  safefetch call site (remote images, embeds, WKD, IndexNow, webhooks).
- **Per-call-site guards** for the paths that do not use safefetch: AI generation
  routes through the guarded transport; the external SMTP relay is refused before
  any dial (`safefetch.ClearnetBlocked`); plugin-registry downloads and the
  Stripe/PayPal nil-client fallback use the guarded transport. Webmention is
  inbound-only and gravatar serve-only, so neither dials out.
- **DNS before the dial.** The SSRF host guard refuses a clearnet host **before**
  resolving it, so a Tor Space never leaks even the DNS query (and the intent to
  reach that host) to the system resolver.
- **Process-wide tripwire.** `http.DefaultTransport` is replaced with a guarded
  transport in OnionMode, so even a caller that bypasses everything above —
  `http.DefaultClient`, a third-party library, or future code — still cannot
  dial clearnet. Every refused dial increments a counter.
- **Reader-side.** The onion CSP strips every external origin (`img-src 'self'
  data:`, never widened to `http:`/`https:`), so a reader's Tor Browser cannot be
  made to fetch a tracking pixel from the reader's own IP.

### 3. Verifiable, honest posture
`internal/anonaudit` computes a live report shown on `/os/spaces` and logged at
boot: which controls are active, which residual risks remain (a configured
external SMTP relay, a leftover clearnet domain), the tripwire count (0 expected;
a rising count shows the guard catching a leak attempt), and the inherent limits
below. Any control that *should* be active but is not is logged as an error. The
report never uses the words "100% anonymous".

### 4. One-click, no domain, isolated
The Tor Space is one click from `/os/spaces`. On a clearnet install the toggle
spawns a **fully isolated child** VayuPress (`internal/vayuos/torspace`) with
`VAYUOS_MODE=tor`, its own database/media/keys, and its `.onion` as its domain —
so the child inherits every control above. The child's environment whitelists
only `PATH`: no parent secret or clearnet domain crosses, so the two worlds
cannot be correlated through shared credentials. Each keystore DEK falls back to
its own host-bound key file (no shared `VAYU_SECRET` needed).

## Threat model

**Protects against:**
- A passive network observer or the host's ISP learning what the server contacts
  (no clearnet egress, no DNS leak).
- A third-party service (AI provider, payment processor, SMTP relay, update
  server, plugin host) logging the server's real IP.
- Discovery by scanning the host's public IP (loopback-only bind).
- A reader's browser being turned into an IP beacon (external-origin-free CSP).
- Correlation of the Tor and clearnet worlds through shared secrets or database.

**Does NOT protect against (be honest with the operator):**
- **A global passive adversary** who can watch both ends of the Tor circuit —
  Tor itself does not defend against traffic-confirmation at that scale, and
  neither can we.
- **Deanonymisation of Tor** (a Tor vulnerability, a hostile guard/exit for
  clearnet-bound traffic, or the operator misconfiguring Tor).
- **Operator behaviour and content.** Writing style, photos carrying EXIF/GPS,
  reused handles or emails, payment details, or logging into the clearnet and
  Tor worlds from patterns that link them — none of this is transport, and the
  server cannot police it.
- **The bootstrap paradox.** Fetching the Tor binary the first time is a clearnet
  download (you cannot fetch Tor over Tor). It is integrity-pinnable
  (`VAYUTOR_BUNDLE_SHA256`, ADR-0138/L3) but it is a clearnet connection at setup;
  prefer installing Tor from the OS package over a trusted channel.
- **Reaching the onion wrongly.** Opening the `.onion` through a clearnet
  "tor2web" gateway, or over a non-Tor client, defeats the anonymity for whoever
  does so. Use Tor Browser.

## Consequences

- Features that inherently need clearnet (external SMTP, third-party payments,
  social auto-post, update checks) **do not function** in a Tor Space by default;
  they are refused rather than silently leaking. This is the correct default for
  the "no clearnet callback" guarantee. A future opt-in could route specific
  egress **through Tor's SOCKS proxy** instead of blocking it (features work over
  Tor, IP still hidden) — deliberately not the default, and out of scope here.
- The layered defense means adding a new outbound integration in the future
  cannot silently deanonymise a Tor Space: the process-wide tripwire catches it
  and the self-audit surfaces the count.
- We publish the limitations as prominently as the protections. Anonymity is a
  property of the whole system — Tor, the network, and the operator — not a
  checkbox VayuPress can tick on their behalf.
