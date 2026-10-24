# VayuShield

VayuShield is the install's own request filter. It decides, for each request
to a public page, whether to serve it, ask the visitor to prove it is a
browser, slow it, or refuse it, and records what it decided. It runs in the
same process as the site, before any page is drawn, and needs no outside
service.

This guide is for the operator: what the shield does, in what order, what you
control and where, what it records, what it cannot do, and what it costs. The
design decisions behind it are in ADR-0111, 0112, 0118, 0137 and 0158.

## Where it sits

A request passes, in order:

1. The install's own middleware: request id, panic recovery, the client's
   address (forwarding headers are trusted only from configured proxies),
   logging, security headers, compression.
2. Maintenance mode and the crawler block, when switched on.
3. The site the request is for, from its host.
4. Redirects.
5. The admission lane (Aegis L0), which caps public concurrency so the
   console always has room under a flood.
6. **VayuShield.**
7. Routing, and the page.

Some paths are never challenged or refused by the shield's inferred gates,
because the clients that call them cannot solve a challenge: the console
(`/os`), the API (`/api`), health and metrics, static assets, `/.well-known`,
`/mcp`, `/oauth`, `/dav`, the shield's and the beacon's own endpoints, and
anything shaped like a feed, a sitemap or `robots.txt`. A path is exempt only
when it is under one of these both as it arrived and as the server will
serve it: `/api/../page.html` is not exempt, because a handler that cleans the
path serves it as `/page.html`.

Your address and country refusals apply before the exemptions, to every
path.

## What it decides with

In order, each step able to end the request:

- **Your address lists.** An address or network you refuse is never served;
  one you allow is never challenged or jailed. Entries may end
  (`198.51.100.0/24 until 2026-11-01`), and an entry that has ended is listed
  as such rather than silently dropped.
- **Your country lists.** Refuse, challenge, or serve only a list of
  countries. With an allow list, refusing visitors whose country cannot be
  resolved is a separate switch, off by default.
- **The jail and reputation.** An address that kept breaching its limit is
  refused for the jail time; a known-bad network likewise.
- **Shedding and the per-address rate limit** under load.
- **Surge**, when switched on or when a flood is detected: every visitor not
  yet proven is asked for a proof of work up front.
- **Classification**: the client's signals give a bot score and a client
  type (human, good bot, bad bot, AI agent, headless, unknown). A score at
  the proof-of-work, script or block threshold is answered so. A crawler is a
  good bot only when its network confirms it.
- **Your rules** (below), which see the shield's verdict.

## Your rules

One rule per line, in **Shield › Request rules** for the install and in
each site's own Security page for that site. A rule is an action and conditions that must
all hold:

```
block path=/wp-login.php
challenge country=CN,RU path^=/comments
limit 5/hour method=POST path=/contact
log client=badbot
test block score>=0.8 header:X-Probe
```

Actions: `block` refuses the request (that one; the visitor's other pages are
still served), `challenge` asks for a proof of work, `allow` serves it past
the shield's own gates, `log` counts it, `limit N/min` or `N/hour` gives each
address that many of the matching requests. `test` in front counts what the
rule would do without doing it.

Conditions: `path=`, `path^=` (starts with), `path*=` (contains),
`method=`, `country=`, `client=`, `score>=`, `score<`, `header:Name`,
`header:Name=value`.

How they are read:

- The first rule that matches and acts decides. A `log` rule and a `test`
  rule are counted and evaluation goes on, so adding one never changes what
  the rules after it do. Each rule shows its count and its last match.
- A `limit` gives each address its own allowance on the requests it matches,
  refilling evenly over the period; within it, the request goes on to the
  next rule, past it, it is refused with a 429 and when to come back. The
  allowance is held in memory and starts afresh when the rules are saved.
- The install's rules run first, then the site's: a site can add a rule and
  never override the install's.
- A path is read as it arrived and as the server serves it (`//p`, `/./p`,
  `/x/../p` and `/p/` are all served as `/p`). A refusal holds if either
  reading matches; an allow only if both do.
- At most 100 rules, of at most 16 conditions each. The line past the
  hundredth rule is named, and it and every line after it are ignored; a
  rule of more than 16 conditions, or a line that cannot be read, is named
  with its number and skipped, and the rest of the set stands.
- Rules run after the shield's own checks, so a verified search engine and a
  signed-in reader never reach them.

**Trying a change first.** A site's Security page replays the site's
recorded reader views against the rules and lists as you have typed them,
and says what would have been refused or challenged, without saving.

## Per site

Each hosted site's Security page sets its own thresholds, address and
country lists, rules and response headers (HSTS, Referrer-Policy, framing,
and which device features its pages may use). Every field says whether it is
the site's own or inherits the install's. A site's lists add to the
install's; they never remove an install entry.

## What it records

Every refusal, challenge, tarpit and rule match is a row in `shield_events`:
the site, the request id (the same one as the request's log line), the layer
and rule, the action and outcome, the path, the client class and score, the
country, and how long the decision took. Addresses are stored only as a
salted hash.

The rows are written in batches by a bounded queue, never on the request.
When the queue is full, events are dropped and counted; enforcement goes on.
A batch the database refuses is kept and tried again, and counted as lost
only after three refusals in a row. In read-only mode (entered when the disk
is failing) the queue holds its events and writes them when the mode
returns. **System › Monitoring › Telemetry** shows what the queue holds, has
dropped and has lost.

The site's Security page reads these rows: refusals and challenges over
time, the top paths, networks and countries, the rules that matched, the
live feed, and the same range of the site's traffic beside them.

## What it cannot do

- It sees one request at a time, with what the request and its own memory
  of the address carry. It does not see your server's other ports or your
  network.
- A determined attacker with many addresses and a real browser can pass as
  many readers. The proof of work makes each one cost CPU; it does not make
  it impossible.
- Countries come from the proxy's headers when it is trusted, or from an
  embedded table; a country rule is only as good as that answer.
- It does not filter what your pages do once served, and it does not scan
  uploads.
- In a Tor Space every visitor arrives from 127.0.0.1 and a country lookup
  answers with the server's own location, so the rules are not given either:
  a country rule matches nobody rather than everybody, and a limit never
  holds the whole audience to one allowance. Path, method, client, score and
  header conditions still apply.

## When things fail

- **The database refuses writes**: enforcement is unaffected (decisions are
  made in memory); events are kept, retried, and counted as lost after three
  refusals.
- **A flood**: the admission lane sheds public requests first, the
  per-address limit and the jail take the heaviest sources, and surge can
  challenge every unproven visitor.
- **A rule set that grows**: bounded at 100 rules of 16 conditions, the most
  any request pays for the full set is about 15 µs.
- **A bad entry**: named on save and skipped; the rest of the list stands.
  An entry cannot crash the compiler (fuzzed: `FuzzCompile`, `FuzzParse`).

## What it costs

Measured on four cores (`internal/vayushield/load_test.go`, run with
`VP_SHIELD_LOAD=1`), with recording on, rate limits, the jail and twenty
rules, over a mix of readers on two thousand addresses, crawlers, scripts,
scanners and a few flooding addresses, the time being the middleware's own:

| Rate | Sent | Late | p50 | p95 | p99 | CPU | Heap growth | Events dropped / lost |
|------|-----:|-----:|----:|----:|----:|----:|------------:|----------------------:|
| 1,000 req/s for 20 s | 20,000 | 0 | 26 µs | 56 µs | 80 µs | 8% of a core | 13 MiB | 0 / 0 |
| 5,000 req/s for 20 s | 100,000 | 0 | 17 µs | 51 µs | 73 µs | 19% of a core | 63 MiB | 0 / 0 |
| 20,000 req/s for 5 s | 100,000 | 0 | 14 µs | 40 µs | 61 µs | 41% of a core | 64 MiB | 0 / 0 |

A full rule set of 100 rules of 16 conditions, every condition tested, costs
15 µs a request (`BenchmarkMatchTheCostliestSetThatMisses`).
