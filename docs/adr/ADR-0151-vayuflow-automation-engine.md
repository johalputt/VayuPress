# ADR-0151 — VayuFlow: the deterministic automation engine

- **Status:** Accepted
- **Date:** 2026-07-30 (accepted 2026-08-05, after the adversarial pass below)
- **Relates to:** ADR-0139 (VayuMCP), ADR-0141 (VayuOS Spaces), ADR-0146 (Buzz
  connector), ADR-0149 (network intelligence), ADR-0150 (VayuVeil)

## The claim, worded to be defensible

> **Every automated action this install takes was authorised in advance by a
> named operator, is reproducible from its recorded inputs, and could not have
> exceeded the blast radius declared when it was armed — including when a model
> chose its content.**

Note what that does *not* claim. It does not claim the automation is smart, that
it will pick the right moment, or that a model's output will be good. Those are
quality questions. The claim above is an *authority* question, and it is the one
worth making unbreakable, because the failure mode of an automation engine is
never "it wrote a mediocre summary" — it is "it published 400 of them", "it
emailed the list twice", or "a comment told it to and it did".

## Context

### What we already have, and why none of it is an automation engine

The pieces look close enough to be mistaken for it:

| Piece | What it actually does | Why it is not automation |
|---|---|---|
| `internal/scheduler` | Stages *articles* with a `publish_at`, ticker promotes them | One trigger, one action, hardcoded to posts |
| `internal/events` | In-process typed bus, `Subscribe`/`Publish` | No persistence; a crash loses in-flight subscribers |
| `internal/outbox` | Durable relay: DB row → `DispatchFn` | Delivery machinery, not decision machinery |
| `internal/webhooks` | HMAC-signed POST to a subscribed URL | Pushes a fact *out*; someone else decides |
| `/mcp` (ADR-0139) | 20 tools, open-ended, human in the loop | Requires a model and a person at the other end |

The gap is the middle: nothing in this codebase can express *"when X happens, if
Y holds, do Z — and prove afterwards that it did exactly that."* Operators
currently get that by pointing an external agent at `/mcp`, which works, but
makes a per-site automation depend on someone else's uptime, someone else's
token budget, and a session that has to be running.

### Why deterministic-first, and why this is the strategic call

The tempting version of this feature is an in-house agent: give a local model
the MCP toolset, a cron entry and a prompt, and let it work out the rest. That
should not be built, for one architectural reason and one security reason.

**Architectural.** Being the *tool provider* is the durable position. Every
improvement in every model that speaks MCP accrues to this install for free, at
zero maintenance cost. An in-house agent inverts that: it becomes a component
that must be kept competitive with an industry, forever, by one project.

**Security.** The MCP surface deliberately withholds a general write hole. A
scheduled agent with tool access and a context window fed by comments, inbound
mail and fetched pages is an autonomous prompt-injection target with no human in
the loop — and it holds the operator's own authority. "Summarise today's
comments and post the digest" is one hostile comment away from "and also update
the site settings". The industry has not solved this; it is not going to be
solved incidentally, here, as a side feature.

So VayuFlow is a **rules engine, not an agent.** Model output is admitted, but
only as a *value inside a step whose effect was already bounded* — never as the
thing that chooses the step. Section 6 makes that structural rather than
advisory.

## Decision

Build `internal/vayuflow`: a durable, auditable trigger → condition → action
engine, surfaced at `/os/vayuflow`, with capability-bounded actions, a
declared-blast-radius contract enforced by type, and an honest posture report.

### 1. The object model

A **Flow** is a persisted, versioned document:

```go
// Flow is one automation. It is inert until Armed, and every field that
// can cause an effect on the world is bounded before it can run.
type Flow struct {
    ID        string
    Name      string
    Enabled   bool
    Trigger   Trigger
    Condition Condition       // zero value = "always"; explicitly allowed
    Steps     []Step          // executed in order, short-circuit on failure
    Budget    Budget          // REQUIRED — see §3
    Owner     string          // the operator who armed it; authority is theirs
    Mode      RunMode         // runDryRun | runLive — zero value is invalid
    Version   int             // bumps on every edit; runs record the version
}
```

`RunMode`'s zero value is `runUnset` and is **not** a valid answer. This is the
`rule.go` lesson applied directly (`internal/vayushield/rule.go`): the first
version of `OnionPolicy` made the safe option the zero value, which meant a
literal that omitted the field had the type answering its own question. Here the
stakes are higher — a `Flow{}` that defaults to live is an automation that
someone forgot to arm running anyway.

### 2. Triggers — the only three kinds, and why not more

| Kind | Fires on | Source |
|---|---|---|
| `TriggerSchedule` | a cron expression, install timezone | ticker, same shape as `scheduler` |
| `TriggerEvent` | a typed domain event | `internal/events` + a durable spool |
| `TriggerManual` | an operator pressing Run | `/os/vayuflow` |

There is deliberately **no webhook-inbound trigger in v1**. An inbound trigger
is an unauthenticated remote party choosing when this install does work, which
is a denial-of-service surface and a rate-limit problem before it is a feature.
It lands only once it can be metered by the same machinery as `/api`, and it
carries its own ADR.

`TriggerEvent` requires work the current bus cannot do. `internal/events` is
in-process and lossy by design — `Publish` fans out to subscribers with no
persistence, so a crash between the event and the action loses the run silently.
VayuFlow subscribes once, writes an **inbox row** in the same transaction as the
originating change where one exists, and the runner drains it. That is the
`outbox` pattern pointed inward, and it is what makes "did this flow run?" a
question with an answer.

### 3. Budget — the declared blast radius

This is the part that makes the headline claim true, and it has no equivalent
anywhere in the codebase today.

```go
// Budget is what a flow is permitted to spend on ONE run and across a window.
// Every field is a hard ceiling checked before the effect, not after.
type Budget struct {
    MaxStepsPerRun   int           // refuses runaway step expansion
    MaxRunsPerHour   int           // refuses a trigger storm
    MaxWritesPerRun  int           // posts created/updated, mails sent, ...
    MaxEgressPerRun  int           // outbound fetches, via safefetch only
    Timeout          time.Duration // whole-run wall clock
}

// Complete reports whether every ceiling was declared. A Budget with any
// zero field is refused at save time — "unlimited" is not expressible.
func (b Budget) Complete() error
```

"Unlimited" being inexpressible is the design. An operator who genuinely wants a
thousand writes types `1000`, and that number appears in the audit trail beside
what the run actually spent. The interesting column on the runs table is
`writes: 3 / 20` — a flow consistently near its ceiling is one to look at.

Ceilings are checked in the **effect path**, not in the planner, so a bug in
step expansion cannot route around them.

### 4. Actions — a capability registry, not a function map

Every action type registers a contract, and cannot be invoked without one. This
is `rule.go`'s registry pattern, and the reason for it is identical: four
obligations re-implemented by hand at twenty action sites is four obligations
forgotten at the twenty-first, silently.

```go
// Capability is what an action is allowed to touch. A registration missing
// any answer fails a test, not a review.
type Capability struct {
    Kind        Kind        // actContent, actMail, actEgress, actAdmin, actModel
    Writes      WritePolicy // writeNone | writeDraft | writeLive — no zero default
    Onion       OnionPolicy // inert or active under VAYUOS_MODE=tor, with reason
    Reversible  Tri         // can an operator undo it, and how
    MinRole     users.Role  // authority floor; checked against Flow.Owner at RUN
    Rationale   string      // why these answers, in prose, shown in the UI
}
```

Four consequences that fall out of this and are worth stating:

- **`actAdmin` is registered but has no members in v1.** Settings, users, keys,
  domains, VayuShield tiers and payment config are not automatable. The registry
  makes that a visible, testable emptiness rather than an absence nobody
  audited.
- **Authority is re-checked at run time against `Flow.Owner`.** A flow armed by
  an admin who is later demoted stops working. Without this, a flow is a
  permanent capability grant that outlives the grant — the exact bug pattern
  ADR-0149's amnesty walk exists to prevent elsewhere.
- **`Onion` is answered per action.** Under `VAYUOS_MODE=tor` an egress action is
  a clearnet callback, which is precisely what ADR-0141 exists to prevent. Every
  egress action routes through `safefetch` and is therefore *already* closed by
  `SetBlockClearnetEgress` — the registry entry makes it explicit and testable
  instead of relying on the call site.
- **`Reversible` drives the UI, honestly.** An irreversible action gets a
  confirmation and a distinct chip. A "send mail" step cannot be undone and
  should never render like a draft edit.

### 5. Conditions — total, side-effect-free, no expression language

Conditions are a small closed set of typed predicates over the trigger payload
and site state (tag equals, author is, status is, count over window, time
window, and boolean composition). Deliberately **not** a scriptable expression
language.

A general expression evaluator inside a rules engine is a sandbox with a
different name, and this project already carries one real sandbox
(`internal/sandbox`) whose 37 dead entries are a standing reminder of what an
unfinished isolation surface costs. Every condition here is total — it
terminates, allocates nothing unbounded, and cannot call out.

### 6. Model steps — bounded output, never bounded authority

A step may call `internal/aiassist` (which already has provider-error scrubbing,
per-user rate limiting and `quality.Unusable` rejection from v3.14.0). It is
constrained by three structural rules:

1. **A model step produces a value; it never selects a step.** The step graph is
   fixed at save time. There is no branch whose target is model output.
2. **Its output is typed and validated** before it reaches the next step —
   length bounds, `Unusable()` rejection, and HTML sanitisation on the same path
   the editor uses. A failed validation fails the run; it does not pass raw
   text through.
3. **A model step can never raise `Writes` above the flow's declared level.** A
   flow whose capability is `writeDraft` cannot publish, whatever the model
   returns. This is the sentence that makes "a bad generation can't publish
   itself" a property of the type system rather than a hope.

Under `VAYUOS_MODE=tor`, a model step using a remote provider is egress and is
closed by the same kill-switch. A local provider is not, and the posture report
must distinguish those two cases rather than reporting "AI: enabled".

### 7. Execution, and what "durable" costs

One runner goroutine, bounded concurrency, backed by a `flow_runs` table. Each
run records: flow ID **and version**, trigger cause, mode, every step with its
inputs, outputs, duration and error, budget spent against budget declared, and
the resolved owner role at run time.

- **Idempotency.** Every run carries a key derived from
  `(flow, trigger identity)`. The event trigger's identity is the inbox row, so
  redelivery cannot double-execute. This is the single most valuable property in
  the whole design: the failure operators actually fear is the newsletter that
  went out twice.
- **Crash recovery.** A run interrupted mid-flight resumes as `interrupted`, not
  as a silent success and not as an automatic retry. Retry is an operator
  decision, because a step that already sent mail must not be replayed by a
  ticker.
- **Retention.** Runs are pruned on the same policy as the VayuShield trail, and
  the panel states the retention window rather than implying infinite history.

### 8. Dry run is the default, and it is a real execution

`Mode` starts at `runDryRun`, and a dry run executes the *whole* flow —
conditions evaluated against live state, model steps genuinely called, every
effect captured and rendered as a diff — while the effect path refuses at the
capability boundary. A dry run that skips the model, or that stubs the
condition, tells the operator nothing about what the live run will do.

Going live is an explicit, per-flow, logged action.

### 9. `/os/vayuflow` — the panel

House style, per the repository contributor notes: `page-header`, `page-sub`, a `stat-grid` of four
(armed flows, runs 24h, refusals 24h, budget-capped runs 24h — that last tile
takes `stat-card--warn`), `section-head` bands, and a `mon-stack` of `monAcc`
accordions with `mon-chip--on`/`--off` so a flow's state reads while collapsed.
No inline `style=`, one nonce'd script.

Nav key added to `adminAreas` in `osPathMinLevel` (`handlers_auth.go`) and
pinned by test — a flow editor that inherited the permissive author default
would let an author arm an automation that runs with their own authority, which
is a privilege-escalation bug wearing a routing mistake's clothes.

### 10. The posture report

`internal/vayuflow/flowaudit`, following `anonaudit` and `shieldaudit`: a set of
`Check`s with a `Status`, computed from live state and shown on the panel. It
reports what is *not* true as prominently as what is:

- flows armed live vs. dry-run
- any flow whose owner's role no longer satisfies its actions' `MinRole`
- any flow that hit its budget ceiling in the window (a ceiling reached is a
  ceiling doing its job *or* a ceiling set wrong — either wants a human)
- egress-capable flows while `OnionMode` is on, and confirmation they are inert
- model steps, split by local vs. remote provider
- **the honest ceiling:** VayuFlow bounds what an *authorised automation* can do.
  It is not a defence against an operator account that has been taken over, and
  the report says so in those words. A posture panel that implies otherwise is
  the same defect class as the report that told an operator their readers were
  broken on the strength of the operator's own request.

## Phasing

Each phase lands on `main` as its own commit; **one release at the end**, after
the adversarial pass the release discipline requires. Not one release per phase.

| Phase | Content | Gate that proves it |
|---|---|---|
| **P1** | Store, schema, `Flow`/`Budget`/`Capability` types, registry, `Complete()` | a flow with any unset contract field fails to save |
| **P2** | Runner, `flow_runs`, idempotency keys, crash → `interrupted` | redelivering an event twice produces one run |
| **P3** | Schedule + manual triggers; content actions at `writeDraft` | a `writeDraft` flow cannot produce a live post |
| **P4** | `/os/vayuflow` panel, dry-run diff view, arming | arming is logged with actor and prior mode |
| **P5** | Event trigger + durable inbox | a crash between event and run loses no run |
| **P6** | Model steps, output validation, budget accounting | a model returning 50KB of garbage fails the run |
| **P7** | `flowaudit` posture, mail + egress actions, Tor inertness | egress actions inert under `VAYUOS_MODE=tor` |

## The adversarial pass this design already anticipates

Written now, so the pre-release audit starts from attacks rather than features:

1. **Trigger storm.** Publish 10,000 articles; does `MaxRunsPerHour` hold, and
   does the inbox grow without bound while it does?
2. **Budget bypass via step expansion.** Can any step cause more effects than
   `MaxWritesPerRun`, by looping, by fan-out, or by a model returning a list?
3. **Authority outliving the grant.** Arm as admin, demote the owner, fire the
   trigger. The run must refuse.
4. **Injection through content.** A comment or inbound mail containing
   instructions reaches a model step. It must be incapable of changing the step
   graph — verify by construction, then by test.
5. **Tor leak.** `VAYUOS_MODE=tor` with an armed egress flow. Nothing leaves.
6. **Idempotency under redelivery.** Same event twice, crash mid-run, resume.
7. **The dry-run lie.** Does dry run genuinely evaluate everything a live run
   would, or does it skip the expensive parts and under-report?

Each becomes a failing test first, in the attacker's voice, and every fix is
mutation-tested — a test that passes against the broken version proves nothing,
which this project has learned twice already.

## What the adversarial pass actually found

Run before the release rather than after it, over everything the seven phases
accumulated. Four of the seven pre-declared attacks landed. Each was written as
a failing test in the attacker's voice before any code changed, and each fix was
mutation-tested — the fix was re-broken and the test confirmed to go red again.
Six mutations, six kills.

| Attack | Verdict | What it cost |
| --- | --- | --- |
| 1 — trigger storm | **Half found.** The rate ceiling held and wrote no row, as designed. The inbox did not: `PruneDrained` existed, said it was "bounded by policy rather than by hope", and was called by nothing in the binary. | A drain pass now also forgets — 30-day window, hourly interval. Pruning every pass would have swapped an unbounded table for an unbounded scan every five seconds. |
| 2 — budget bypass via step expansion | Nothing found. There is no expansion: `Steps` is a fixed ordered list settled at save time, `chargeStep` runs before every step, `Complete()` refuses a flow whose step count exceeds its own `MaxStepsPerRun`, and a model returning a list returns a *string* that nothing iterates. An action calling `Write` in a loop is stopped by the ledger, not by its own restraint. | Pinned by `TestAnActionCannotOutspendTheWriteCeilingByLooping`. |
| 3 — authority outliving the grant | Nothing found. The owner's role is not stored on the flow at all, and a resolver that errors reads as no role rather than as the last known one. Re-attacked from the demotion side. | Pinned by `TestADemotedOwnerStopsTheFlowEvenThoughItWasArmedByAnAdmin`. |
| 4 — injection through content | Nothing found. There is no edge to take: `Step` has no branch target, `prev` is one value, and substitution replaces only a parameter whose *whole* value is the placeholder — so content cannot append to an operator-written URL, and the fan-out a whole-value substitution would buy is refused by `mail.send` itself. | Pinned by `TestInjectedContentCannotSpliceASecondRecipientOrRedirectAFetch`. |
| 5 — Tor leak | **Found, twice.** `Effects.Fetch` refused only when the *calling* action declared itself inert, and the registry test that keeps egress actions inert covers `KindEgress` and nothing else — so an action of any other kind that reached for the network got the clearnet, with only `safefetch` left downstream. Separately, `Flow.NeedsEgress` keyed on the Onion policy, so the panel told an operator whose model runs on this very host that their flow "reaches a remote host" while the posture report on the same page said it did not. | `Fetch` now refuses whenever clearnet egress is blocked, whatever the action declared — it is called Fetch; being outbound is not something it needs to be told. `NeedsEgress` keys on the kind, and a model step gets its own line naming which provider this install actually has. |
| 6 — idempotency under redelivery | Nothing found. The key is claimed in `Begin`, before any step runs, and derives from the inbox row id. Covered by the P2 and P5 suites. | No change needed. |
| 7 — the dry-run lie | **Found.** `Effects.Model` is deliberately ungated so a dry run calls the model for real. `Effects.Fetch` *was* gated — so a fetch → model → draft flow dry-ran with an empty body into a model that genuinely ran. Under-reporting the read and over-reporting the generation, on the same screen, in the same run. | A fetch is a read that produces a value, already charged and already refused in a Tor Space. It happens, and the capture says `fetched` rather than `would fetch`. |

One of the new assertions was itself wrong on the first attempt, and it is worth
recording because it is the repository's own named failure mode: searching the
whole rendered page for `reaches a remote host` matched the posture report's
sentence *"No armed flow reaches a remote host."* — a substring that passes a
regression and fails a correct fix. The test now extracts the one accordion and
fails loudly if it cannot say which element it read.

## Consequences

**Good.** Automation stops depending on an external session being alive. Every
automated effect gets an owner, a ceiling and a record. The capability registry
makes "what can this install do to itself without me?" an enumerable question.
MCP stays the open-ended path, so the two are complementary rather than
competing.

**Costs, stated plainly.** A new persistent subsystem with its own schema,
runner and panel — real surface. Deterministic conditions will not cover every
case an operator imagines, and some will want the scriptable version we are
declining to build; the honest answer is "use MCP for that, with a human in the
loop." And the engine is only as good as its ceilings: a `Budget` filled in with
large numbers to make a warning go away is a real, and likely, operator failure
mode. The panel should make a near-ceiling run visible rather than pretending
the number was chosen carefully.

**Rejected alternatives.** A general in-house agent (§Context). A scriptable
expression language (§5). Inbound webhook triggers in v1 (§2). Automating
`actAdmin` (§4).

## Appendix — dead-code inventory (evidence, not a deletion order)

Nothing is deleted by this ADR. This records what was measured, so a future
decision starts from evidence rather than a fresh guess.

**Method.** `go list -deps ./cmd/vayupress` gives the packages actually linked
into the shipped binary: **103**. Comparing against `go list ./...` gives the
packages that are not. This is stronger evidence than an import grep, because it
follows the real transitive closure from the real entrypoint.

**Never linked into the shipped binary** (with their `scripts/deadcode-allow.txt`
entry counts):

| Package | Dead entries | Note |
|---|---|---|
| `internal/federation` | 27 | ActivityPub, ahead of its wiring |
| `internal/migrations` | 16 | **schema migrations do not use this** — they run through `internal/db` (`runMigrations`, `verifyMigrationChecksums`, embedded FS). Only bench/fuzz tests import it |
| `internal/storage` | 13 | `arweave_stub.go`, `ipfs_stub.go` — stubs by their own filenames |
| `internal/ai` | 12 | one importer: `internal/search/semantic`, itself unlinked. Only `ai.Embedding` crosses the boundary |
| `internal/slo` | 10 | |
| `internal/graph`, `internal/merkle`, `internal/governance`, `internal/profiling` | 7 each | `/os/governance` **does not import** `internal/governance`; the page is alive, the package is not |
| `internal/registry`, `internal/events/schema` | 6 each | |
| `internal/did` | 5 | |
| `internal/archive`, `internal/search/sharded`, `internal/search/semantic`, `internal/signing` | 4 each | `signing` referenced only by tests and `archcheck` rules |
| `internal/spam` | 3 | comments carry their own `StatusSpam` constant |

Legitimately unlinked and **not** candidates: `cmd/vayudocs` (separate binary),
`docs/plugins/examples/*` (documentation), `internal/archcheck`,
`internal/testutil`, `internal/compat` (test-only tooling),
`scripts/screenshot-proxy`.

**Two findings worth carrying forward.** First, `internal/sandbox` has the
largest dead surface (37) but *is* linked and *does* have production importers —
it is partially wired, which is a different and more interesting state than
unused. Second, the two entries that read as load-bearing from their names —
`migrations` and `governance` — are the two where the live service turned out to
use something else entirely. Name-based intuition was wrong in exactly the cases
where being wrong would have hurt; that is the argument for the linker-based
method over grep.

**If a removal is ever undertaken**, the order that minimises risk is: stubs
first (`storage`), then leaf packages with zero importers of any kind, then
`ai`+`semantic` as a pair, and never a package with production importers. Each
step is its own commit with `scripts/deadcode-gate.sh --update`, full gates, and
a build of the real binary — because the only proof that matters is that the
shipped artifact still links and passes.
