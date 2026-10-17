# System Modes — VayuPress

**Status:** Authoritative  
**Date:** 2026-06-13 (reviewed against the code 2026-10-16)  
**Package:** `internal/mode`

VayuPress operates in one of six system modes. The current mode is held by
`mode.Global` and governs subsystem behaviour at runtime. Transitions are
validated against the allowed graph and logged as structured JSON.

---

## Modes

| Mode | Value | Description |
|------|-------|-------------|
| **Normal** | `normal` | All subsystems fully operational |
| **Degraded** | `degraded` | Entered when outbox commits keep failing (`internal/fault`), or when an error budget is exhausted and the operator has turned on the budget actuator (`internal/budget`, off by default) |
| **ReadOnly** | `read-only` | Entered on repeated WAL write faults or any migration fault (`internal/fault`) |
| **Recovery** | `recovery` | An operator-driven recovery in progress |
| **Maintenance** | `maintenance` | Operator-initiated planned downtime |
| **Quarantined** | `quarantined` | Entered when plugin invocations keep faulting; plugins are not run |

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

## What each mode gates

Read from the code that consults the mode; a behaviour not listed here does not
change with the mode.

| Behaviour | Refused in | Where |
|-----------|-----------|-------|
| Writing a post or page: the API, the connector, the editor, scheduled publishing, imports from the console | ReadOnly, Quarantined | `mode.RefusesContentWrites`, read by `queue.Enqueue` (503 `writes_refused`), the worker, `publishDuePosts` and the console's `refuseContentWrites` |
| Applying a job already queued | ReadOnly, Quarantined | The worker leaves it pending and applies it when the mode is back |
| Running a plugin | Quarantined | `internal/sandbox/subprocess.go` |
| Applying an update | ReadOnly, Quarantined, Maintenance | `update.PreflightMode` |
| IndexNow submissions | ReadOnly, Quarantined, Maintenance | `cmd/vayupress/app.go` |
| Importing media, unfurling an embed (editor and the connector) | ReadOnly, Quarantined | `handlers_media_import.go`, `handlers_embed_unfurl.go`, `mcp_server.go` |
| Saving or resetting the theme, changing branding | ReadOnly, Quarantined | `handlers_theme.go`, `handlers_theme_assets.go` |

A refused write is refused, not lost: a scheduled post that falls due stays
scheduled, and a queued job stays queued, until the mode returns. The CLI
importers run in their own process and do not see the server's mode.

The current mode is shown in the console's system bar on every page, and on
Home's *This install*.

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
3. Written to SQLite by the mode journal (`internal/mode/journal.go`), which
   subscribes through `OnTransition`; at start the last journalled mode is
   restored, so a restart does not silently return to Normal.

The history is shown on the System state and Governance pages.

---

## Usage

```go
// Check mode before a write
if mode.Global.Is(mode.ModeReadOnly, mode.ModeRecovery, mode.ModeMaintenance) {
    return ErrWritesForbidden
}

// Subscribe to transitions (the mode journal is the one subscriber today)
mode.Global.OnTransition(func(t mode.Transition) {
    logging.LogInfo("example", string(t.From)+" -> "+string(t.To))
})
```
