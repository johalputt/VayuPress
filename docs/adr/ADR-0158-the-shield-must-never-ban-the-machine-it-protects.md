# ADR-0158 — The shield must never ban the machine it protects

- **Status:** Accepted; shipped in v3.17.15
- **Date:** 2026-08-06
- **Follows** ADR-0155, ADR-0156 and ADR-0157. Each fixed a real defect. This is
  the one that was causing the reported outage.

## 0. The evidence that ended four days of wrong answers

Every earlier attempt reasoned forward from code to a mechanism that could
produce a 502. This one started from the machine, during a failure:

```
$ curl -m 8 http://127.0.0.1:8080/
curl: (28) Failed to connect to 127.0.0.1 port 8080: Connection timed out

$ curl -m 8 -H 'Host: …' http://127.0.0.1/
curl: (28) Failed to connect to 127.0.0.1 port 80: Connection timed out

$ curl https://…
curl: (6) Could not resolve host
```

**A loopback connection cannot time out.** There is no network between the two
ends. It connects, or it is refused, and both are instantaneous. A *timeout* on
127.0.0.1 has exactly one cause: a packet filter is **dropping** the packets. A
`reject` would have said "refused"; a `drop` says nothing and the client waits.

So the machine was filtering its own loopback traffic. And the filter was ours.

## 1. The defect

VayuShield's kernel offload (ADR-0123) exports jailed addresses to a root agent,
which mirrors them into an nftables set. The chain it created:

```
chain input {
    type filter hook input priority -20; policy accept;
    ip saddr @banned4 drop
    ip6 saddr @banned6 drop
}
```

Priority **−20**. The main firewall table is at **−10**, so this chain is
evaluated *first*. The main table opens with `iif "lo" accept`; this one had no
exemption of any kind, and a drop here is final — the accept downstream is never
reached.

**So a single loopback entry in the ban set cut the machine off from itself.**
nginx could not reach the application, so every visitor got 502. A local
resolver stopped answering, so name lookups failed. Both surfaced as timeouts
rather than errors. Minutes later the ban's TTL expired and the site returned by
itself, leaving a running process, a healthy application, an empty error log and
nothing whatsoever to point at.

That last property is why it survived four releases: **every artifact an
operator or a developer would examine was clean.** The application was fine. The
database was fine. nginx was running and had logged no 502 — because nginx never
generated one; its upstream simply never answered.

## 2. How the loopback address gets jailed

Nothing in the jail path excluded it. `jailBadActor` jails whatever key it is
given, and `offload.Ban` exported whatever it was passed.

Two things make that key `127.0.0.1` in practice:

- **A reverse proxy whose real-IP layer is not configured.** Then every visitor
  arrives as the proxy's address, and on a same-host proxy that is loopback. The
  code already knew this: `vayushield.go:1102` notes that in that state "the
  answer would be identical for the whole audience". One bad actor convicts
  everybody — including the machine.
- **Certificate provisioning.** The helper issues a burst of loopback pre-flight
  requests, one per domain, to prove the server answers its own ACME challenge
  before spending a rate-limited validation. A burst of requests from a single
  source is precisely the shape the rate limiter exists to punish.

Which is why the outage correlated with provisioning without provisioning being
the fault.

## 3. What was built — three layers

**The chain exempts loopback, first and unconditionally.** There is no threat
model in which dropping a host's traffic to itself is the desired outcome, so it
is not a policy decision and it is not configurable.

**The agent refuses to install such an entry.** It already revalidates every
line rather than trusting the file; an address that could not possibly be wanted
is now refused there too, and counted.

**The application never exports one.** `offload.Ban` declines loopback and the
unspecified address before the entry reaches the ban file, and counts the
refusal — because a refusal means the shield decided this machine was its own
attacker, which is a misconfiguration worth surfacing rather than swallowing.

Private ranges stay bannable on purpose. An operator behind a LAN-facing proxy
may have a real reason to ban `10.x`, and refusing it would be this product
overriding a decision that belongs to them. Loopback is different in kind.

**The upgrade path is the load-bearing half.** Every install hardened before
this change already has the old table, and the previous code returned early
whenever the table existed. A fix that only wrote correct rules on a clean box
would have reached none of the machines that were actually broken. The agent now
inspects the live chain and rebuilds it if the exemption is absent.

## 4. How it was proven

Five mutations, all killed — including one that moved the exemption *after* the
drops, which is the version that compiles, reads correctly and does nothing.

The shell is **executed** against a stubbed `nft` that captures the ruleset the
function tries to load, rather than read for strings. That mattered: a
source-level check would have passed on a script whose exemption was in the
wrong place.

One test-harness defect worth recording, because it is the same class this repo
keeps paying for: the function extractor ended a shell function at the first
line that was exactly `}` — which, for a function that writes an nftables
ruleset through a heredoc, is the closing brace of the *table*, several lines
inside the heredoc. It returned half a function and bash rejected the harness
rather than the code. Extractors need to understand the syntax they are cutting.

## 5. The rule, and the process lesson

**A control that can deny service must never be able to deny it to the host
itself.** Any gate keyed on a client address — a firewall set, a rate limiter, a
blocklist, a geo rule — needs loopback carved out at the point of enforcement,
not somewhere downstream that a `drop` will never reach.

And the expensive one, restated from ADR-0157 because it took another release to
learn: **a plausible mechanism is not a diagnosis.** Four releases fixed four
real defects while the actual fault went untouched. What found it was one
command run against the failing machine — `curl` to loopback — and the single
observation that its result was impossible.
