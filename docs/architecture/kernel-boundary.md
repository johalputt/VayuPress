# Kernel Boundary — VayuPress

**Status:** Authoritative  
**Date:** 2026-06-13  
**Authority:** VayuPress Maintainers (RFC + 2/3 vote to modify)

This document answers the most important architectural question before ecosystem expansion:
**what is the immutable platform kernel, and what is a replaceable subsystem?**

---

## Immutable Kernel

The following components form the **VayuPress Platform Kernel**. They define invariants
that no plugin, extension, or subsystem can bypass. Changes require an RFC and 2/3 vote.

| Component | Package | Invariant |
|-----------|---------|-----------|
| **Signing** | `internal/signing` | Every published article has a valid Ed25519 signature. Verification is called before serving. No bypass path exists. |
| **Capability Enforcement** | `internal/sandbox/capability_enforcer.go` | Plugin capabilities are checked against the manifest before every Invoke(). No plugin may exceed its declared capabilities. |
| **Migration Integrity** | `internal/migrations` | Migration checksums are verified against embedded SQL. Drift is a hard error. No schema change without a migration. |
| **Identity Model** | `internal/did` | DID:key authentication binds identity to cryptographic key material. No shared-secret fallback. |
| **Event Durability** | `internal/outbox` | Events are written to the outbox in the same transaction as the state change. No fire-and-forget. |
| **Audit Trail** | `internal/migrations/resilience.go` (journal) | Migration journal is append-only. No entry may be deleted or modified. |
| **SLO Error Budget** | `internal/slo` | BudgetExhausted() blocks release gate. No release while any Blocking SLO is at zero. |
| **Policy Engine** | `internal/policy` | All architecture, security, reliability, and release policies are registered here. No ad hoc enforcement bypasses the engine. |

### Kernel Bypass Prohibition

No code path may:
- Serve an article without calling `signing.Verify()`
- Start a plugin subprocess without calling `EnforceCapabilities()`
- Apply a database schema change outside the migration engine
- Emit an event without the transactional outbox
- Delete or modify a migration journal entry
- Release with an exhausted SLO error budget

Violations of the above are **architectural defects**, not bugs. They require an ADR,
RFC, and a migration plan before being merged.

---

## Replaceable Subsystems

The following subsystems are **independently evolvable** with versioned contracts.
They may be replaced, upgraded, or removed as long as the contract is maintained.

| Subsystem | Current Implementation | Contract | Replaceability |
|-----------|----------------------|----------|----------------|
| **Plugin Runtime** | `internal/sandbox` subprocess | Manifest + IPC JSON protocol | Replaceable (e.g., WASM runtime, microVM) |
| **AI Runtime** | `internal/ai` LocalEmbedder | `Embedder` interface, policy.Apply | Replaceable (e.g., remote inference, LLM API) |
| **Storage Backend** | `internal/storage` file + IPFS stubs | `Backend` interface | Replaceable per Backend interface |
| **Federation Transport** | `internal/federation` ActivityPub | ActivityPub + HTTP Signature | Replaceable (e.g., AT Protocol, Nostr) |
| **Search Index** | `internal/search` FTS5 + semantic | Search query + result contract | Replaceable (e.g., Meilisearch, Elasticsearch) |
| **Cluster Coordination** | `internal/cluster` leader election | `Node.IsLeader()` contract | Replaceable (e.g., Raft, etcd) |
| **Metrics Backend** | `internal/metrics` in-process counters | Prometheus exposition format | Replaceable (e.g., OpenTelemetry) |
| **Tracing Backend** | `internal/trace` SQLite store | OpenTelemetry-compatible span format | Replaceable (e.g., Jaeger, Tempo) |

### Replaceability Contract

A subsystem is replaceable only if:
1. Its public interface is defined in Go (`interface` type).
2. The interface is declared **Stable** in `docs/compatibility/stability-matrix.md`.
3. At least one alternative implementation exists (even as a stub).
4. The replacement compiles and passes all existing tests without modification.

---

## Extension Boundary

Third-party code interacts with VayuPress through these defined extension points:

| Extension Point | Mechanism | Kernel Bypass Risk |
|----------------|-----------|-------------------|
| Plugin hooks | Subprocess IPC + Manifest | None — capability enforcer is in kernel |
| ActivityPub federation | HTTP + Inbox/Outbox | Low — content is sanitised before storage |
| REST API | HTTP handlers (L0-L2 trust) | None — handlers cannot access kernel directly |
| Event subscribers | Outbox polling | None — subscribers receive committed events only |
| Search indexing | Article events | None — index is derived, not authoritative |

Extensions **cannot**:
- Access the SQLite database directly (only through the migration-managed schema)
- Call `signing.Sign()` with the host key (key is not exported to extensions)
- Modify the migration journal
- Disable capability enforcement
- Register policies in the Policy Engine (only host process can)

---

## Kernel Evolution Protocol

When a kernel component must change:

1. File an RFC using `docs/rfc-template.md`.
2. RFC must explain: what invariant changes, what the migration path is.
3. Requires 2/3 supermajority vote per `internal/governance`.
4. New ADR required.
5. Golden tests and compatibility matrix updated before merge.
6. Change may not land in a patch release — requires minor or major version bump.
