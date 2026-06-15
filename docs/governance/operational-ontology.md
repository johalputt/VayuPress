# Operational Ontology

VayuPress models its runtime as more than a stream of log lines. Every operational
signal is described along four orthogonal semantic axes. As the number of signals
grows, these axes are what keep the model unambiguous — an operator (or automation)
can always answer *how bad is it*, *how sure are we*, *where did it come from*, and
*how long does it live* for any event.

This document is the canonical statement of those axes and the rules that bind them.
It is enforced, not aspirational: a cross-package contract test
(`internal/governance/ontology_contract_test.go`) fails the build if the code drifts
from what is written here.

## The four axes

| Axis | Package | Vocabulary | Answers |
|------|---------|------------|---------|
| **Severity** | `internal/severity` | OBSERVE · NOTICE · WARN · VIOLATION · ESCALATION · CONTAINMENT · CRITICAL | *How bad is it?* |
| **Confidence** | `internal/provenance` | inferred · derived · canonical | *How sure are we it's true?* |
| **Provenance** | `tlProvenance` (timeline) / outbox | source · actor · cause · lineage | *Where did it come from?* |
| **Retention** | `docs/governance/event-retention.md` | ephemeral · durable · replayable · audit-grade · operator-cognition | *How long does it live, and is it truth?* |

The axes are independent by design. A signal can be CRITICAL severity yet only
*inferred* confidence (a reconstructed alarm); it can be canonical yet OBSERVE
(a boringly true fact). Conflating the axes — treating severity as certainty, or a
cognition surface as a system of record — is the failure mode this ontology exists
to prevent.

## Severity (how bad)

A fixed, **totally ordered** taxonomy (`OBSERVE < … < CRITICAL`). Order is load-bearing:
budgets and "at least this severe" thresholds rely on it. Each level carries explicit
operator expectation, escalation behaviour, timeline class, and policy interaction.
See `severity.All()` for the authoritative, self-documenting registry.

**Invariant.** `registry[i].Level == i` and `registry[i].Rank == i`. The taxonomy is
append-only at the high end; reordering or inserting a level is a breaking change.

## Confidence (how sure)

An ordered epistemic vocabulary (`inferred < derived < canonical`) answering how much
an event should be trusted as ground truth:

- **canonical** — directly observed, durably backed (a recorded mode transition, an
  ingested CSP report). The strongest claim.
- **derived** — computed deterministically from canonical inputs (a budget posture).
  True, but a function of other facts, not an observation.
- **inferred** — a synthesized narrative with no durable record of its own (a
  reconstructed boot sequence). Honest, but never ground truth.

**Propagation rule** (`provenance.Combine`): trust cannot be manufactured by
derivation. Any inferred or unknown input makes a conclusion inferred; a conclusion
drawn purely from canonical observations is *derived*, never itself canonical. The
unset zero value is not a valid level — it fails safe toward zero trust.

## Provenance (where from)

Every synthesized timeline entry carries structured provenance — `source`, `actor`,
`cause`, a deterministic `id`, and a causal `parent_id` — so the timeline is a
traversable graph, not a flat log. Governance budgets additionally attribute their
debt to the contributing sources, so an exhausted budget can name *what* consumed it.

Provenance is populated only where genuinely known. Synthesized governance entries
have no `correlation_id` (that flows through the outbox/trace subsystem) and leave it
empty rather than fabricating one. **Honest gaps over invented attribution.**

## Retention (how long / is it truth)

Defined in full by the [Event Retention Doctrine](./event-retention.md). The single
rule: *a signal's retention class must match its purpose.* The Unified Operational
Timeline is **operator-cognition** — a synthesized view, re-derived per request, and
explicitly **not a system of record**. The outbox, mode journal, and policy
evaluations are the durable, replayable, audit-grade sources of truth.

## How the axes compose: governance error budgets

Budgets (`internal/budget`) are where the axes meet. A budget tracks events of one
*severity* within a rolling window; its charges carry *provenance* (contributing
sources); its posture is a *derived* confidence over those canonical charges; and the
budget itself is **accounting, not a system of record** (operator-cognition + the
durable metric/log behind it).

Crucially, a budget **recommends** an escalation severity at exhaustion but does not
**actuate** it. Recommendation ≠ authority: driving the mode engine from a budget is
a control-loop change reserved for its own safety design. An operator may *acknowledge*
a budget to clear its debt window — that records responsibility; it does not change
the system mode.

## Deferred (deliberately not yet built)

These are named so the ontology's boundaries are explicit:

- **Canonical event substrate** — a single append-only store unifying timeline
  cognition with durable records, giving every event a real `correlation_id` and
  letting confidence propagate along genuine data-derivation edges (today the
  structural lineage parent is ownership, not derivation, so `Combine` is applied
  only where inputs are truly known).
- **Budget actuation** — the gated control loop letting a recommendation drive a mode
  transition, with hysteresis, rollback, and override doctrine.

Both touch all four axes at once; that is exactly why they deserve deliberate design
rather than a bolt-on.

## The governing rule

> Keep the axes separate, keep each honest about what it does not know, and let no
> surface claim an authority (truth, certainty, or actuation) it was not given.
