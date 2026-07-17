# ADR-0137: VayuShield Aegis — sovereign surge defense, swarm-scale classification, and self-maintaining detection

- **Status**: Accepted
- **Date**: 2026-07-16
- **Deciders**: BDFL / Owner
- **Relates to**: [ADR-0111](ADR-0111-vayushield-bot-protection-and-analytics.md) (VayuShield core), [ADR-0112](ADR-0112-vayushield-resilience-tiers.md) (resilience tiers), [ADR-0118](ADR-0118-vayushield-auto-block-and-learning.md) (auto-block & detection-driven learning — this ADR extends and, for the admin "recent blocks list", supersedes it), [ADR-0120](ADR-0120-collapsible-htmx-shield-console.md) (shield console), [ADR-0123](ADR-0123-vayushield-privileged-agent.md) (kernel offload)

## Context

VayuShield already fields a strong sovereign stack: a lock-free admin
sovereignty lane (L0), a 256 KiB Count-Min-Sketch fair-shed pre-filter (L2), a
sharded reputation brain (L5), a self-calibrating challenge ladder (L4), an
HMAC-stateless proof-of-work challenge, and a per-IP TTL blocklist with an
optional in-kernel offload. Yet three real gaps stop it short of
"handle a 1-million-bot swarm without slowing the site, with no CDN":

1. **A distributed swarm reaches the expensive path.** The cheap gates
   (blocklist, reputation jail, rate-limit, fair-shed) all key on *repeat*
   volume from a source. A swarm of ~1M **distinct** IPs each sending only a
   handful of requests stays under every per-source budget, so each request
   flows through to `Classify()` — a per-request TLS/HTTP fingerprint **plus a
   SQLite `SELECT`** on the adaptive signature DB (`internal/vayushield/vayushield.go`
   `Classify` → `botdb.Store.Lookup`). Under a distributed swarm this is
   millions of fingerprint computations and DB reads competing with real
   readers for the same L0 public-concurrency budget. The process stays up (L0
   sheds the overflow with a 503) but *new* human visitors are shed alongside
   the bots — the site "slows down" for real users. There is no cheap way to
   separate an unproven human from a bot **before** the expensive work.

2. **The learning loop is partly dead and slow to close.** `jailBadActor`
   correctly folds a blocked fingerprint into `vayushield_signatures` as a
   `bad_bot` candidate (ADR-0118), so the block→learn path works. But
   `maybeLearn` — the path meant to record a *suspicious-but-not-yet-blocked*
   fingerprint — is unreachable: it fires only when `BotScore > 0.75 AND
   ClientType == Unknown`, while the scorer classifies any `score >= 0.75` as
   `BadBot` and only labels `Unknown` for `0.4 ≤ score < 0.75`. The two
   conditions are mutually exclusive, so that learning path never runs. And an
   auto-learned `bad_bot` candidate only begins to influence live verdicts once
   its confidence reaches `0.8` (scorer step 2), which the `+0.05`/sighting
   upsert reaches only after ~7 recurrences — so learning is slow to *act*.

3. **The blocked-IP admin list is stale and misleading.** The Bot Shield panel
   renders the last 50 rows of `vayushield_blocked`, but the live enforcement
   state is the in-memory jail (blocklist + reputation), which the list does not
   reflect; and nothing prunes the table, so it drifts. Operators read the list
   as "the current block list" when it is really a historical log. The owner's
   directive: stop showing a per-IP list; maintain detection state efficiently
   in a background job and surface only live, trustworthy aggregates.

All fixes must hold the constitution: pure Go, zero CGo, **zero third-party
runtime services (no CDN, no Turnstile/hCaptcha)**, strict CSP (`script-src
'self' 'nonce-…'`, no `unsafe-inline`/`unsafe-eval`), single binary, one SQLite
writer, fail-open (protection never takes the site down), GDPR posture (no
plaintext IP at rest), and no measurable slowdown for legitimate traffic.

## Decision

### 1. Sovereign Surge Mode (Aegis L3) — a stateless "under attack" front gate

Add a surge gate that runs **before** classification, fingerprinting or the
signature `SELECT`. Surge is a deliberate escalation ("challenge every unproven
visitor"), so it engages only when the shield is genuinely under attack:

- the operator's explicit "Under Attack" switch is on (auto-expiring); or
- the global attack-RPS meter is tripped (adaptive under-attack mode); or
- the L0 sovereignty lane is **critically** saturated (≈90% of its concurrency
  cap) — the site is being overwhelmed. This is the zero-configuration trigger
  that catches a **low-and-slow, high-cardinality swarm**: a million distinct
  IPs each sending a little fills the lane without ever spiking per-second RPS,
  so the RPS meter alone would miss it, but the lane occupancy will not.

It deliberately does **not** engage on *mild* L0 pressure (≈75%): that gentler
signal drives the graceful L2 fair-shed, which only sheds heavy hitters and
never touches a client within its fair budget, so a merely-busy site never
challenges everyone. When surge is engaged, any request that is **not** already
trusted is answered with the existing HMAC-stateless proof-of-work interstitial
*up front*:

- **Bypass (no challenge):** a valid shield session cookie (proven human), a
  trusted operator session, a bypassed prefix (`/os`, `/api`, feeds, health,
  the challenge endpoints), or a cheap in-memory static **good-bot / AI-agent**
  User-Agent match (so Google, Bing and sanctioned AI crawlers are never
  challenged). All of these are O(1), allocation-free checks already present.
- **Challenge (cost shifts to the client):** everyone else gets a signed PoW the
  browser solves once in a Web Worker and echoes back for a session cookie; from
  then on they ride the verified/priority lane and are never challenged,
  classified, or shed again. A bot that will not run JS / solve the PoW never
  obtains a session, so it **never reaches fingerprinting, the signature
  `SELECT`, template rendering or SQLite** — it bounces off one HMAC issue
  (microseconds, zero DB, zero render, fixed memory). This is Cloudflare-style
  "Under Attack Mode", implemented sovereignly in-binary: 1M unsolved bots cost
  ~1M HMACs, not 1M renders.

Hardening (from the adversarial design review):

- **Client-bound clearance.** The session/clearance token is cryptographically
  bound to the solver's client key (its IP, folded into the token MAC, never
  stored — no new PII at rest). A cookie solved once by one source is therefore
  worthless when replayed from another, defeating "solve once, replay the cookie
  across the whole botnet." A roaming client whose IP changes simply re-proves.
- **Real cost, not identity theatre.** Surge issues the harder PoW tier (not the
  trivial default), since the adversary includes headless browsers that *can*
  run JS; PoW is a cost-imposition + client-binding tool, not proof of humanity.
- **Never breaks feeds or crawlers.** Feed/sitemap/robots endpoints are bypassed
  by shape (any `*.xml/.atom/.rss`, a `/feed` or `/rss` segment), so JS-less
  legitimate machine clients are never handed an unsolvable challenge. The 503
  carries `Retry-After`, so a crawler challenged during a genuine attack treats
  it as transient (no deindex).
- **No stuck switch.** Auto-engage (RPS flood) relaxes when the flood passes;
  an operator-*forced* surge auto-expires after a bounded window (12h) so a
  forgotten toggle can never challenge visitors indefinitely.
- **Calibration isolation.** Surge challenges every unproven visitor regardless
  of score, so they are excluded from the L4 pass-rate calibrator — they cannot
  skew the score-based ladder's self-tuning.

Surge is **automatic** (engages on a detected flood, disengages when it clears)
or operator-forced. It is off when there is no attack, so normal traffic sees
exactly today's behaviour. Fail-open throughout: a nil signer or an issue error
serves the request rather than an empty response.

### 2. Swarm-scale classification — an in-memory verdict cache (L6)

Front the adaptive-DB `Lookup` with a fixed-size, sharded, lock-cheap
fingerprint→classification cache so steady-state classification does **not** hit
SQLite per request. Fixed memory (hard-capped entries, evicted by age), so a
1M-distinct-fingerprint swarm cannot grow it unbounded; a miss falls through to
the existing read-pool `SELECT` exactly as today. This removes the per-request
DB read that a distributed swarm would otherwise multiply.

### 3. Close the learning loop

- **Fix `maybeLearn`** to record a genuinely suspicious fingerprint (a
  high-but-sub-block heuristic score, or a headless/transport-contradiction
  tell) as an auto-learned candidate — so the knowledge base grows from
  near-misses, not only from hard blocks.
- **Faster, safe auto-promotion:** a recurring auto-learned `bad_bot` candidate
  reaches an actionable confidence sooner (tunable), guarded by the existing
  false-positive counter so a mis-fingerprinted real client decays back out.
  The scorer already acts on `bad_bot` at confidence ≥ 0.8; promotion makes the
  loop *close* on real repeat offenders in a few sightings instead of many.

### 4. Self-maintaining detection — background job, not a stale list

- A single background maintenance job (one goroutine, ticking on a slow
  interval, shutdown-aware) prunes stale `vayushield_signatures` candidates
  (`PurgeStale`), ages out the historical `vayushield_blocked` log, and computes
  the live aggregates the panel shows. The live blocklist/reputation jails are
  already self-expiring in memory and need no sweeper.
- **Remove the per-IP "recent blocks" list from the admin UI.** Replace it with
  trustworthy live aggregates (currently-jailed count, sources under suspicion,
  fair-shed/blocked totals, signatures learned) plus a clear "detection is
  maintained automatically in the background" statement. No per-row IP/UA table.

## Consequences

- **A 1M-bot swarm is absorbed at O(1)/request with no CDN.** Unproven traffic
  under surge is bounced by a stateless HMAC challenge before any expensive
  work; proven humans solve once and are never bothered again. The site stays
  fast for real readers *during* an attack, not just alive.
- **Faster steady state.** The verdict cache removes a per-request SQLite read
  from the classification path; the surge gate removes fingerprinting entirely
  for unproven traffic during an attack.
- **Learning actually learns and acts.** Suspicious near-misses are recorded and
  recurring offenders auto-promote to an actionable signature quickly, with the
  false-positive guard intact.
- **Honest, self-maintaining UI.** The panel shows live, background-maintained
  aggregates instead of a stale historical IP list; the detection tables prune
  themselves; memory stays bounded under any flood.
- **Constitution held.** Pure Go, zero third-party/CDN, strict CSP (the
  interstitial's solver is the same nonce-free same-origin `/__vayushield/
  challenge.js`), single binary, fail-open, GDPR posture unchanged (no new
  plaintext IP at rest; the verdict cache keys on fingerprint hashes, never IPs).
- **Supersedes** the ADR-0118 "operator-visible recent-blocks list" UI decision;
  the auto-block + detection-driven-learning engine it introduced is kept and
  extended.
