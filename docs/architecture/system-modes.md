# System Modes — VayuPress

**Status:** Authoritative  
**Date:** 2026-06-13  
**Package:** `internal/mode`

VayuPress operates in one of six system modes. The current mode is held by
`mode.Global` and governs subsystem behaviour at runtime. Transitions are
validated against the allowed graph and logged as structured JSON.

---

## Modes

| Mode | Value | Description |
|------|-------|-------------|
| **Normal** | `normal` | All subsystems fully operational |
| **Degraded** | `degraded` | Partial capability — SLO error budget low; feature work paused |
| **ReadOnly** | `read-only` | Writes refused — WAL corruption or migration drift detected |
| **Recovery** | `recovery` | Active recovery operation in progress |
| **Maintenance** | `maintenance` | Operator-initiated planned downtime |
| **Quarantined** | `quarantined` | Plugin/federation isolation active |

---

## Transition Graph

```
Normal ──→ Degraded ──→ Normal
  │          │
  │          ├──→ ReadOnly ──→ Recovery ──→ Normal
  │          │                     │
  │          ├──→ Quarantined       └──→ ReadOnly
  │          └──→ Maintenance ──→ Normal
  │
  ├──→ ReadOnly
  ├──→ Recovery
  ├──→ Maintenance
  └──→ Quarantined
```

`ForceTransition` bypasses the graph and is reserved for operator CLI use.

---

## Policy-Driven Transitions

`EvaluateFromPolicy` maps policy engine results to mode transitions:

| Policy Condition | Target Mode | Priority |
|-----------------|-------------|----------|
| `migrationDrift == true` | ReadOnly | Highest |
| `pluginsQuarantined == true` | Quarantined | Medium |
| `sloExhausted == true` | Degraded | Lowest |

Priority matters when multiple conditions are true simultaneously.

---

## Subsystem Behaviour by Mode

| Subsystem | Normal | Degraded | ReadOnly | Recovery | Maintenance | Quarantined |
|-----------|--------|----------|----------|----------|-------------|-------------|
| Article writes | ✓ | ✓ | ✗ | ✗ | ✗ | ✓ |
| Federation send | ✓ | throttled | ✗ | ✗ | ✗ | ✗ |
| Plugin invocation | ✓ | ✓ | ✓ | ✗ | ✗ | ✗ |
| Search indexing | ✓ | ✓ | ✓ | ✗ | ✗ | ✓ |
| Migration apply | ✓ | ✗ | ✗ | ✓ | ✓ | ✗ |
| Signing | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |

Signing is **always active** — it is a kernel invariant and cannot be
suspended by any mode.

---

## Transition Hooks

Subsystems register hooks via `mode.Global.OnTransition(func(Transition))`.
Hooks are called synchronously in registration order on the goroutine that
performed the transition. Keep hooks fast — defer expensive work (alerting,
queue flushing) to a background goroutine.

---

## Audit Trail

Every transition (including forced ones) is:
1. Appended to `Manager.history` in memory.
2. Logged via `logging.LogJSON` at `warn` (normal) or `error` (forced) level.
3. Preserved for the lifetime of the process (history is never trimmed).

History is accessible via `mode.Global.History()` and exposed on the
`/internal/health` endpoint.

---

## Usage

```go
// Check mode before a write
if mode.Global.Is(mode.ModeReadOnly, mode.ModeRecovery, mode.ModeMaintenance) {
    return ErrWritesForbidden
}

// React to policy evaluation
mode.Global.EvaluateFromPolicy(
    sloTracker.BudgetExhausted(),
    migrations.DriftCount() > 0,
    sandbox.QuarantinedCount() > 0,
)

// Register a subsystem hook
mode.Global.OnTransition(func(t mode.Transition) {
    if t.To == mode.ModeQuarantined {
        federation.SuspendOutbound()
    }
})
```
