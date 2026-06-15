# ADR-0063 — Gated Governance Budget Actuation (Ω12)

**Status:** Accepted  
**Date:** 2026-06-15  
**Deciders:** VayuPress Maintainers

---

## Context

The governance error-budget ledger (`internal/budget`) accounts for severity-classified
events and, when a budget is exhausted, *recommends* an escalation severity. Its scope
boundary was deliberate and documented: it accounts and recommends, but does **not** drive
mode transitions. The package comment named the missing piece explicitly:

> letting a budget actuate the fault→mode engine is a control-loop change that belongs
> behind its own safety design and tests. The recommendation is surfaced … for an
> operator or a future, explicitly-gated actuator.

This ADR records the design of that actuator.

## Decision

Add `internal/budget.Actuator` — a control loop that, **when explicitly enabled**, drives an
exhausted budget's recommended escalation into the mode engine. The default posture is
unchanged: actuation is off unless an operator sets `GOVERNANCE_ACTUATION=true`.

Severity → protective-mode mapping:

| On-exhaust severity | Target mode |
|---------------------|-------------|
| ESCALATION          | degraded    |
| CONTAINMENT         | read-only   |
| CRITICAL            | quarantined |
| NOTICE / below      | none (informational debt — recorded, no transition) |

### Safety properties (enforced in the actuator, not assumed of callers)

1. **Opt-in.** A disabled actuator is a hard no-op; with it off the system behaves exactly
   as the recommend-only design — no mode ever changes from budget pressure.
2. **One-shot / debounced.** Each budget actuates once on the rising edge into `exhausted`
   and will not fire again until it recovers below exhausted and re-exhausts. A budget that
   sits exhausted across many evaluation ticks actuates once, not per tick — no flapping.
3. **Graph-respecting.** Transitions are requested through `mode.Manager.Transition` (never
   `ForceTransition`), so the allowed-transition graph still governs. An escalation that is
   not permitted from the current mode is a logged refusal, never a forced jump.
4. **Audited.** Every actuation and every refusal emits a structured `budget-actuator` log
   line naming the budget, its contributors, and the target mode.

### Surfacing

`GET /api/v1/admin/budgets` runs one evaluation tick and reports `actuation_enabled` plus an
`actuations[]` array describing what the actuator did (or why it declined). Operator
acknowledgement (`POST /api/v1/admin/budgets/ack`) still clears debt and therefore naturally
disarms the actuator for that budget.

## Consequences

- The recommend-only guarantee is preserved by default; turning actuation on is a deliberate,
  logged operator choice.
- Autonomous mode escalation is now possible but bounded: opt-in, one-shot, graph-respecting,
  and fully audited — the four properties that make an automatic control loop safe to ship.
- Future work (not in this ADR): a periodic evaluation ticker so actuation does not depend on
  the budgets endpoint being polled, and per-budget enable granularity.
