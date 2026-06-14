# Event Retention Doctrine

VayuPress emits many operational and governance signals. As the timeline becomes
central to system cognition, *where* each signal lives — and for how long — must be
explicit, not incidental. This doctrine classifies every event store along two axes:

- **Retention class** — how long it lives and what guarantees it carries.
- **Purpose** — what role it serves in the operational model.

The governing rule: **a signal's retention class must match its purpose.** Operator
cognition may be ephemeral; audit and replay must be durable. Never rely on an
ephemeral surface for a durable obligation.

## Retention classes

| Class | Definition | Guarantees |
|-------|------------|------------|
| **Ephemeral** | In-memory, bounded, process-local. Lost on restart. | None across restarts. Cheap, fast, cognition-only. |
| **Durable** | Persisted (SQLite / shipped logs). Survives restart. | Survives process lifecycle; backed up. |
| **Replayable** | Durable *and* designed to be re-processed deterministically. | Can be re-driven to reconstruct state/effects. |
| **Audit-grade** | Durable, tamper-evident or append-only, retained for review. | Trustworthy for after-the-fact investigation. |
| **Operator-cognition** | Exists to inform a human/automation *now*; not a system of record. | Best-effort; may be synthesized or sampled. |

These compose: the outbox is *durable + replayable + audit-grade*; the CSP ring is
*ephemeral + operator-cognition*.

## Classification of current stores

| Store | Class(es) | Source of truth? | Notes |
|-------|-----------|------------------|-------|
| **Outbox events** (`delivered_events`, outbox tables) | durable · replayable · audit-grade | **Yes** for side effects | Dedup + dead-letter + replay. The canonical record of what happened to articles. |
| **Mode journal** (`*.modes`) | durable · replayable · audit-grade | **Yes** for mode history | Every transition with cause; reloaded at boot. |
| **Policy evaluations** (`policy_evaluations`) | durable · audit-grade | **Yes** for policy verdicts | Provenance log of every policy run (Ω11). |
| **Structured logs** (`logging.LogJSON`) | durable · audit-grade | **Yes** for diagnostics | Shipped to the log pipeline; secret-redacted; carries `severity`. |
| **Metrics** (`/metrics`) | durable (scraped) · operator-cognition | No — aggregates | Counters/histograms; retention lives in the scraper (Prometheus). |
| **Unified Operational Timeline** | operator-cognition (synthesized) | **No** — a *view* | Re-derived per request from the stores above + the CSP ring. Carries provenance + causal lineage for traversal, but is **not itself a store**. |
| **CSP violation ring** | ephemeral · operator-cognition | No | Last 10, process-local. The durable record is the CSP log line + `vayupress_csp_violations_total`. |

## Rules

1. **The timeline is a projection, not a ledger.** It may synthesize, sample, and
   bound. Anything that must survive a restart or support investigation lives in a
   durable store, and the timeline *references* it (e.g. via provenance
   `correlation_id` once wired), never replaces it.
2. **Ephemeral surfaces declare themselves.** The CSP ring is explicitly bounded
   and process-local; its durable shadow is the log + metric. No alert, audit, or
   replay may depend on the ring.
3. **Audit-grade stores are append-only / tamper-evident.** Mode journal, policy
   evaluations, and the WORM audit log are never mutated in place.
4. **Replay is reserved for durable + replayable stores.** Only the outbox and
   mode journal are designed to be re-driven; operator-cognition surfaces are not
   replay sources.
5. **Severity travels with the event** (`internal/severity`), independent of
   retention class — a `CRITICAL` event must reach a durable, audit-grade store
   regardless of which cognition surface also shows it.

## Open arc

Per-event **correlation IDs** are present in the schema (`provenance.correlation_id`)
but empty on synthesized timeline entries today: the timeline re-derives from
mode/fault/CSP state rather than consuming traced outbox events directly. Wiring the
timeline to read traced, durable sources — so each cognition entry links back to its
audit-grade record — is the next step that fully unifies *cognition* (ephemeral view)
with *record* (durable store) under one provenance graph.
