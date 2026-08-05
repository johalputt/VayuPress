# VayuFlow — running automations on your own install

VayuFlow is the part of VayuOS that does things while you are not watching. It
lives at **`/os/vayuflow`** and it exists to make one claim true:

> Every automated action this install takes was authorised in advance by a named
> operator, is reproducible from its recorded inputs, and could not have exceeded
> the blast radius declared when it was armed — including when a model chose its
> content.

That is a claim about **authority**, not about quality. VayuFlow does not promise
your automation is clever or that it picks the right moment. It promises that
when something happens on its own, you can say who authorised it, what it was
allowed to touch, and what it actually spent.

The design record is [ADR-0151](../adr/ADR-0151-vayuflow-automation-engine.md).

## The shape of a flow

A flow is five things, and none of them may be left blank:

| Part | What it answers | If it is missing |
| --- | --- | --- |
| **Trigger** | What starts it — a schedule, a domain event, or your press | Refused at save |
| **Condition** | Which subjects it applies to (an empty condition means "always", which is an answer) | Accepted as "always" |
| **Steps** | What it does, in order | Refused at save |
| **Budget** | What one run may spend | Refused at save |
| **Owner** | Whose authority it borrows | Refused at save |

There is deliberately **no way to write "unlimited"** in a budget. An operator
who genuinely wants a thousand writes types `1000`, and that number appears in
the run trail beside what the run actually spent. The interesting column on the
runs table is `writes: 3 / 20` — a flow sitting near its ceiling every time is
one to look at.

## Dry-run is the default, and it means something specific

A new flow is in **dry-run** until you arm it. Dry-run is not "test mode" in the
usual sense of a stubbed-out rehearsal. The whole flow executes:

- Conditions are evaluated against live state.
- Model steps are **genuinely called** — you see the real generated text.
- Fetch steps **genuinely fetch** — the next step gets the real body.
- Every ceiling is charged exactly as a live run would charge it.
- The effect path refuses the **writes**, and captures each one as a diff line.

The two "genuinely" bullets are the reason a dry run is worth reading. A rehearsal
that stubbed the generation, or skipped the read and let the model run on nothing,
would show you a plausible draft produced from data the live run will never see.

What it costs you: a dry run of a flow with a fetch step **does make that outbound
request**, and a dry run of a flow with a remote model step **does spend whatever
that provider charges**. Both are metered against the same ceilings as a live run,
and both are refused in a Tor Space.

**Arming is its own action**, on its own button, and it is logged with who did it
and what the mode was before. "Armed" without a from-state cannot tell a first
arming from a re-arming, and that difference matters when you are reading back
why something started happening last Tuesday.

## What an action is allowed to touch

Every action carries a registered capability. It is not a description written by
whoever added the action — it is a contract with five answers, and an action
missing any of them fails a test rather than a code review.

| Action | Writes | In a Tor Space | Undo | Owner must be |
| --- | --- | --- | --- | --- |
| `content.draft.create` | draft | active | reversible | editor |
| `content.draft.update` | draft | active | reversible | editor |
| `model.draft.generate` | **none** | inert when the provider is remote | reversible | editor |
| `egress.fetch` | none | inert | reversible | editor |
| `mail.send` | **live** | active (built-in sender) | **irreversible** | admin |

Two things are worth reading off that table.

**A bad generation cannot publish itself.** `model.draft.generate` writes
*nothing* — it produces a value. The only step that can write it down is capped
at `draft`. That is a property of two registrations rather than a promise about
how good your prompt is.

**Mail is the one irreversible row, and it is admin-only.** A step sends to
**one** recipient. A comma-separated list is refused, because a list would spend
a single write from the budget and deliver many messages — the ceiling would be
telling you the truth about writes and nothing at all about what happened.

Administrative actions — settings, users, keys, domains, VayuShield tiers,
payment configuration — are **not automatable**. That is a decision on record,
pinned by a test, not a gap.

## Authority is checked when it runs, not when you save

The owner's role is deliberately **not stored on the flow**. It is resolved
against the live account on every single run.

Arm a flow as an admin, demote that account, and the next run **refuses** — and
the refusal is in the trail with the role that was actually resolved, so the page
tells you why rather than leaving you to guess. If the role cannot be resolved at
all, that reads as *no authority*, never as *the last one we saw*.

Without this, an armed flow would be a permanent capability grant that outlives
the grant.

## In a Tor Space

If the install runs with `VAYUOS_MODE=tor` (see
[ADR-0141](../adr/ADR-0141-vayuos-spaces-clearnet-tor.md)), any step that reaches
outside is refused in the effect path — not by the action remembering to check,
and not conditioned on how the action described itself. The refusal is driven by
the same central kill-switch that closes every other outbound path in the binary,
so VayuFlow cannot disagree with the rest of the install about whether the
clearnet is reachable.

A **local** model provider is not an outbound call and keeps working. The flow's
card on the panel says which of the two this install has, rather than warning you
about a generation that never leaves the machine.

## Reading the panel

Four tiles answer "what is the state of this?" at a glance, then a posture report,
then the flows, then the recent runs.

The posture report is written to be **honest rather than reassuring**. A row that
states a fact is toned as a fact, not as a success — a green tick beside "no armed
flow reaches a remote host" on an install with no flows at all would be reading
emptiness as safety.

Things worth watching:

- **A pending inbox backlog that is not shrinking.** The drainer has stopped, or
  it is failing on every pass. The trail records what went wrong.
- **Runs capped by their budget.** Either the ceiling is too low for the work, or
  the flow is doing more than you think. The panel cannot tell you which, and
  says so instead of picking.
- **Flows that refuse on authority.** Somebody's role changed.

## How much this keeps

- **Run trail** — kept. It is the record of what your install did on its own, and
  it is bounded per flow per hour by the runs-per-hour ceiling.
- **Event inbox** — drained rows are kept **30 days**, then dropped. Pruning runs
  at most once an hour, as part of a drain pass.

A trigger storm cannot grow the run trail: the runs-per-hour ceiling is checked
*first* and writes no row when it refuses. Every other refusal does get a row and
does count toward the ceiling, so the table is bounded by construction rather
than by a cleanup job that has to keep up.

## What VayuFlow deliberately is not

- **Not a scripting language.** Conditions are a closed set of typed predicates,
  bounded in depth and breadth. If you need arbitrary logic, use VayuMCP with a
  human in the loop — that is the open-ended path, and it is open-ended on
  purpose.
- **Not webhook-triggered.** An inbound webhook is an unauthenticated remote party
  choosing when your install does work. That is a denial-of-service surface before
  it is a feature, and it lands only when it can be metered by the same machinery
  as `/api`.
- **Not a step graph a model can steer.** A step has no branch target. Model
  output is a *value* handed to the next step, never a *choice* of which step runs
  next — so text injected through a comment or an inbound mail has no edge to
  take.
