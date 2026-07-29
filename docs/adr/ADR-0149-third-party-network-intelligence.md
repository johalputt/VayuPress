# ADR-0149 — Third-party network intelligence

- **Status:** Accepted
- **Date:** 2026-07-29
- **Relates to:** ADR-0141 (VayuOS Spaces), ADR-0148 (multi-node verdict sharing)

## Context

VayuShield decides everything about a visitor from what it can observe itself:
the request shape, the User-Agent, how the client behaves, and a compiled-in
signature set. That is a deliberate and mostly correct posture — it needs no
account with anyone and leaks nothing about the audience.

It is also blind to one thing that is a matter of public record. Whether an
address belongs to a datacenter is not an inference; the network operator
publishes it. Whether a netblock is under criminal control is likewise something
several publishers state openly. A shield that ignores both is discarding
information an operator can get for free.

ADR-0141 previously declined managed threat intelligence, and the objection it
raised was the right one:

> a hijacked vendor endpoint could inject CIDRs into an always-allow fast path

That is not an argument against using published lists. It is an argument against
one specific capability — and this ADR is the answer to it rather than a
disagreement with it.

## Decision

Ship third-party lists, all disabled by default, under four constraints.

### 1. A feed can never produce an ALLOW, and that is the type

`intel.Kind` has exactly two values, `KindDatacenter` and `KindHostile`, and
neither grants anything. There is no `KindAllow` to reach for.

The asymmetry is the whole design. The realistic compromise of a feed is not
that it goes offline; it is that somebody edits what it serves. A hijacked feed
able to add entries to an always-allow set hands an attacker a silent bypass of
every gate in the shield, with no local misconfiguration for an operator to
find. A hijacked feed that can only add suspicion or a refusal causes
over-blocking — bad, visible, recoverable. Those two failures are not
comparable, and no amount of care at the call sites is worth as much as the
value simply not existing.

### 2. Integrity without signatures, stated as such

Most of these feeds are plain text over HTTPS with nothing to verify. The
honest position is not "we check the signature" but three weaker controls named
for what they are:

- A refresh that changes a list by more than **35%** is refused and the
  last-good copy is kept, so an attacker must inject slowly enough to span days
  of visible churn. This catches a wholesale swap. It does **not** catch a
  patient attacker adding ten entries at a time, and the posture report says so.
- Every fetch records a checksum, so "this list changed a lot today" is
  something an operator can see rather than infer.
- Entry counts and response sizes are bounded at build time, so a feed cannot
  become memory exhaustion.

The delta is measured on **published entries**, never on merged ranges.
Adjacent prefixes collapse: a list of 200 contiguous `/16`s compresses to one
range, and a bound computed from that compares 1 against 1 and notices nothing.
This was found by a test whose own fixture had that shape.

### 3. The two tiers have different bars, and different effects

**Datacenter** feeds must be **first-party** — published by the network operator
about their own address space. AWS saying which ranges are AWS is a fact from the
only party who knows. There is deliberately no aggregated "known VPN and hosting
IPs" list: those are compiled by third parties from inference, and inference
about someone's connection must not silently cost a reader access. A match adds
`intel.DatacenterDelta` (0.15) to the score, sharing one clamped budget with the
other heuristics — enough to reach a solvable check, never a block.

**Hostile** feeds must be **conservative by construction**. Spamhaus DROP
qualifies because of what it is: netblocks hijacked or under criminal control,
whose publisher's own guidance is not to route to them. Community abuse-report
aggregations do not qualify, however useful — they carry a real false-positive
rate and this tier can refuse.

Each feed has **its own parser**, and the DROP parser fails the entire document
on a line that is neither a comment nor a prefix. A parser loose enough to read
all four shapes would also read a hijacked response resembling none of them. An
empty parse is refused outright: a 200 that parses to nothing would otherwise be
applied as "this list is now empty", disarming the layer while every indicator
still read healthy.

### 4. Where the deny gate sits is the rest of the design

The hostile gate runs:

- **after** the operator's own rules, so their ALLOW beats a third party's list.
  A human who wrote down "serve this network" made a decision; a feed made a
  claim, and the human wins. This is also how an operator recovers from a bad
  listing without waiting on a publisher.
- **after** the verified-crawler fast path, so no feed can de-index the site.
  A DROP entry covering a search engine's published range should be impossible,
  and "should be impossible" is exactly the assumption this package refuses to
  build on.
- **before** the challenge ladder, and **not** gated on a solved session. The
  proof of work proves a browser, and being a browser is not the objection a
  hostile listing raises.

A valid operator session is the one exemption — the same never-locked-out
guarantee `TrustedFn` carries everywhere else.

The refusal is a flat 403 that **names the publisher**, not the challenge page
every other jail serves. Offering work that cannot change the answer would be
asking for it dishonestly, and a visitor refused over someone else's file can
only act on it if they are told whose file it was.

The gate is registered in the enforcement-contract registry (`rule.go`) as
`GateIntelDeny`, which refused the change until all four obligations were
declared.

### Inert in a Tor Space

Every peer there is `127.0.0.1`, so a lookup describes the Tor daemon rather
than the visitor. Worse, a feed that ever grew an entry covering loopback would
refuse the whole audience at once from a file nobody on that machine controls.
Fetching is likewise blocked — `safefetch` closes the egress and the block is
recorded per feed, because a layer that silently makes no requests looks exactly
like one that is working.

### The operator opts in, per feed

Every feed ships disabled and the panel names the publisher. These are
third-party lists under third-party terms, some restricting commercial use, and
accepting those terms is the operator's decision to make — not something to
inherit from a URL somebody embedded. The feed's `Kind` carries what enabling it
means, so there is no second "and actually enforce it" switch: two decisions
where the operator made one is how a list ends up enabled, fetching, displayed
as healthy, and connected to nothing.

## Consequences

- An operator who enables a datacenter list will see more challenges served to
  VPN users. That is the intended trade and it is reversible with one toggle.
- An operator who enables a hostile list is accepting a third party's judgement
  about their visitors. The panel and the posture report both say so.
- A feed's outage is not this site's vulnerability: refresh fails soft per feed
  and the last-good set is kept.
- Nothing is stored against a source, so there is no sentence for amnesty to
  lift — a listing ends when the publisher delists the network or the operator
  switches the feed off.
- Volumetric absorption is unchanged. Nothing here runs before packets have
  crossed the uplink.
