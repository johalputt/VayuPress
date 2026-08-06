# ADR-0156 — The write connection must not be a single point of failure

- **Status:** Accepted; P1–P5 shipped in v3.17.13
- **Date:** 2026-08-06
- **Follows** ADR-0155, which removed three unnecessary restarts from certificate
  provisioning and measured startup at ~1.2s. That work was correct and did not
  stop the outage, which is what led here.

## 0. The report

> After adding a new domain and provisioning a certificate, the site shows 502.
> Two minutes gone and still 502. Then it comes back on its own, with no restart
> command, and the certificate is live. Make this smooth and solid so VayuPress
> does not hang again.

## 1. What was established, and what was not

Facts, each read from the machine rather than inferred:

| Check | Result | What it rules out |
| --- | --- | --- |
| `systemctl status` | `active (running)`, same PID for hours | a crash, a restart loop |
| `systemctl list-jobs` | no jobs queued | the ADR-0155 §1 hypothesis |
| `free -h` | 2.1 GB used, 9.3 GB available | memory pressure |
| OOM killer | no kills | the kernel reaping the process |
| `MemoryCurrent` | 10.8 GB | nothing — cgroup v2 counts page cache |
| Recovery | self-recovered, no intervention | anything requiring a repair |

A live probe during a later occurrence: two consecutive requests to the install's
lightest endpoint timed out at 60 seconds each. The process was up and not
answering.

**The ADR-0155 hypothesis was wrong and is withdrawn.** It proposed that
`systemctl try-restart` issued from inside a provisioning unit was queueing
behind that unit's own transaction. `list-jobs` was empty. That attribution was
made without evidence and the evidence refuted it.

**What is NOT established, and is not claimed anywhere below:** that certificate
provisioning is what first takes the write connection. It is the correlation the
operator reported, and it is plausible — provisioning invokes the CLI several
times per run — but nothing here traced the trigger. This ADR is about the
**amplifier**, which was traced, measured, and is the reason a brief cause became
a multi-minute outage.

## 2. The mechanism, proven

Three properties of the code combine. Each is individually reasonable.

**One.** The writer pool is capped at a single connection, because SQLite has one
writer (`internal/db/db.go:145`, `SetMaxOpenConns(1)`). Correct, not negotiable.

**Two.** Every public page view counted itself like this:

```go
go func() {
    if err := a.analytics.Record(context.Background(), scope, path, ref); err != nil {
        logging.LogError("analytics", "record failed", err.Error())
    }
}()
```

`context.Background()` has a **nil** `Done` channel. `busy_timeout` does not
apply — that governs SQLite once you hold a connection. This is the queue in
front of it, in `database/sql`, and it has no deadline at all.

**Three.** Nothing bounds how many of those goroutines exist. One per view.

Measured directly, against a pool configured exactly like production:

```
A: deadline-free write still blocked after 2s — waits indefinitely
B: deadlined write bailed after 150ms with context deadline exceeded
C: DBStats InUse=1 WaitCount=2 WaitDuration=150ms
```

So: anything holding the write connection turns every arriving view into a
goroutine parked forever in an unbounded queue. When the connection frees, that
backlog drains one statement at a time — and every caller that genuinely needed
to write (a sign-in, an admin save, an MCP tool call) is behind it.

**This explains every reported symptom.** The outage outlasts its own cause, by
however long the backlog takes to drain. It resolves with no restart. It leaves
nothing in the log, because nothing failed. And it is *worse on a busier site*,
because traffic is what fills the queue — which is the opposite of how an
operator expects a fault to behave, and part of why it went unattributed.

Reproduced, and measured as the operator experienced it: 40,000 views arriving
during a held connection delayed the next legitimate write by **644 ms**. Scale
the traffic to a real site over a multi-minute provisioning window and the
minutes are accounted for.

## 3. Why nothing could see it

The queue was always measurable. `database/sql` has counted it since Go 1.11 —
`DBStats.WaitCount` and `DBStats.WaitDuration`, both cumulative, both lock-free.
Nothing in this product read either.

Worse, the endpoint whose job was to report exactly this could not:

```go
func HandleHealthDB(w http.ResponseWriter, r *http.Request) {
    if err := dbpkg.DB.Ping(); err != nil {   // unbounded, on the pool of one
```

`/health/db` queued behind the stall like everything else, so during the incident
it hung rather than reporting. A monitor watching it recorded a timeout — the one
response that carries no information. `/health/ready`, `/health/workers` and
`/health/ethics` had the same shape.

**A health check that fails the same way as the thing it monitors is not a health
check.**

## 4. What was built

**P1 — Counting a view no longer touches the database.**
`internal/analytics/recorder.go`. Views accumulate in an in-memory tally keyed
exactly as the table is keyed, and one goroutine flushes totals on a five-second
tick, in batched transactions, with a deadline. `RecordAsync` takes a mutex,
increments an integer and returns.

The scaling inverts. Write volume is now bounded by the number of **distinct
pages viewed per interval**, not by traffic: a page under heavy load costs one
row update every few seconds however many people read it. Measured, 9,000 views
became 45 statements. The busiest install now writes least per view.

The buffer is bounded (20,000 distinct keys) and **drops rather than growing**,
counting drops for the panel. Losing a view count is a rounding error; losing the
site is an outage. A key already in the buffer is never dropped, so sustained
load on a fixed set of pages never loses anything.

**P2 — The writer is watched.** `internal/db/stall.go` samples `DBStats` every
second. An interval spent essentially entirely waiting is a stall; brief
contention is not, because a panel that cries wolf is a panel nobody reads. Each
event records when it started, how long it lasted, how many callers were delayed
and the total time they spent queued — that last figure summed across callers, so
it exceeds the wall clock precisely when a crowd was affected.

**P3 — Health answers during a stall.** Every `/health` handler that touches the
write pool is bounded at two seconds. `/health/db` gained a third state:

- `ok` — the writer answered
- `contended` — it did not, with the stall detail attached
- `down` — it answered with an error

"The database is fine and the queue in front of it is not" is a different fault
from "the database is down", with a different fix, and the endpoint now says
which.

**P4 — It is on the page.** The Monitoring page carries a *Write connection*
band: stalls since boot, the worst one, total time callers spent queued, and a
history table. Mid-incident it leads with a callout naming the duration and the
number of callers affected — and says that reads and cached pages are unaffected,
because an operator needs to know whether their readers are down.

**P5 — A stall explains itself.** Past five seconds, the watchdog captures a
goroutine snapshot **while the process is still stuck**, which is the only moment
the stacks name what is holding the connection. Last three kept, 0600, pruned —
a recurring stall must not fill a disk and become a second outage. The panel says
the snapshot exists and where.

## 5. What this does NOT claim

- **It does not prove what triggers a stall.** It removes the thing that turned
  a short one into a long one, and it makes the next one self-describing. If
  provisioning is the trigger, the next occurrence will say so in a goroutine
  snapshot rather than in an argument.
- **It does not make stalls impossible.** Something can always take the write
  connection for a while. The claim is bounded: traffic no longer piles onto it,
  writes no longer wait without a deadline, and nothing is invisible.
- **The panel reports no live waiter count**, because `DBStats` does not expose
  one. A number invented for a panel is the same defect as a posture row for a
  control nobody verified.

## 6. How it was proven

Eleven mutations. Ten were killed on the first run. **One survived, and it
changed the shape of the work.**

The first version of the headline test asserted that `RecordAsync` returns
quickly while the write connection is held. It passed — and it passed just as
happily with the original defect pasted back in. Of course it did: the old code
was already `go func() { … }()`, so the page view never waited either. The
visitor whose view triggered the write was fine.

**The property being asserted was the wrong one.** The damage was never to the
visitor; it was to everyone else queued behind them. Rewritten to measure what
actually matters — how many callers had to wait for the write connection, and
whether goroutines accumulate — the same mutation failed loudly:

```
5000 of 5000 page views queued for the write connection
goroutine count grew by 5000 while serving 5000 views
```

This is the second time in two releases that a test agreeing with itself has been
caught only by mutation, after the theme-export tests that re-derived the
exporter's own filter. The lesson generalises past both: **a test must assert the
consequence to a third party, not the behaviour of the thing under test.**

A second test — that a legitimate write is not starved after a stall clears —
initially failed to kill the mutation at 5,000 views because the backlog drained
inside the threshold. It was strengthened to 40,000 rather than kept as
decoration. A test that cannot fail is not a gate.

## 7. What the audit found

The pre-release adversarial pass attacked the new code rather than reviewing it,
and asked one question of each piece: *what would I do to this?* Two findings,
both fixed before the version was cut, both mutation-tested.

**The referrer host was visitor-controlled and unbounded — and buffering changed
what that costs.** `referrerHost` returns whatever `Referer` parses to, and Go's
server accepts roughly a megabyte of headers. Before this work, an absurd value
went straight to SQLite and bloated a table. After it, the value is held in
memory until the next flush, so 20,000 distinct keys each carrying a
header-sized "host" is a memory-exhaustion path **this work would have
introduced**. Capped at the DNS limit of 253 octets and *discarded* past it
rather than truncated, because truncating invents a hostname and attributes real
traffic to it. The page view itself still counts — refusing a bad referrer must
not refuse the visit.

**Goroutine snapshots were being written under the cache directory, which nginx
roots.** Every vhost this product writes contains
`location ^~ /.well-known/acme-challenge/ { root CACHE_DIR; }`. That location is
narrow, so the snapshots were not reachable and this was not exploitable. It was
one edit to a shell script away from being so, and what it would have published
is a dump full of internal paths and function names. They live beside the
database now, in the state directory, which is served by nothing. **A
diagnostic's safety must not depend on the contents of an unrelated file.**

Attacked and found sound: the view buffer's memory ceiling (paths are already
capped at 512 bytes, and a key already present is never re-counted against the
cap, so sustained load on a fixed page set cannot fill it); the flush path
(chunked, so a large batch cannot itself become the stall it prevents); the
snapshot retention (capped and pruned, so a recurring stall cannot fill a disk);
and the panel's escaping (a hostile snapshot path renders as text).

## 8. Risks worth stating

- **View counts are up to five seconds stale**, and up to five seconds of counts
  are lost on an unclean kill. A clean shutdown flushes. This is the trade for
  never queueing traffic on the writer, and it is the right one.
- **The watchdog costs a sample per second.** It copies a struct behind the
  pool's mutex and takes no connection.
- **Goroutine snapshots are operator-visible artefacts.** They name internal
  functions and file paths, so they are written 0600 and the panel publishes only
  the path, never the contents.
