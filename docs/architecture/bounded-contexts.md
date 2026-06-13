# Bounded Contexts — VayuPress Internal Architecture

**Status:** Authoritative  
**Last reviewed:** 2026-06-13

---

## Layering Rule

```
api → services → domain → infrastructure
```

**Strict rule:** Higher layers may import lower layers. Lower layers MUST NOT import higher layers.  
**Violation = architecture failure.** Run `go mod graph | grep cycle` and `depcycle ./...` in CI.

```
┌─────────────────────────────────────────────────────┐
│  api          (handlers, middleware, routing)         │
├─────────────────────────────────────────────────────┤
│  services     (use-case orchestration)               │
├─────────────────────────────────────────────────────┤
│  domain       (business logic, entities, events)     │
├─────────────────────────────────────────────────────┤
│  infrastructure (DB, IPC, network, storage, tracing) │
└─────────────────────────────────────────────────────┘
```

---

## Bounded Contexts

### 1. Publishing Context

**Owns:** Articles, content lifecycle, versioning, signing  
**Packages:** `internal/signing`, `internal/merkle`, `internal/archive`  
**Publishes events:** `article.published`, `article.updated`, `article.deleted`  
**Consumes events:** none (source of truth)

**Invariants:**
- Every published article has a valid Ed25519 signature.
- `version` is monotonically increasing per article.
- Archived articles are immutable (content-addressed storage).

---

### 2. Governance Context

**Owns:** RFCs, ADRs, votes, constitutional rules  
**Packages:** `internal/governance`  
**Publishes events:** `governance.vote.cast`, `governance.rfc.accepted`, `governance.rfc.rejected`  
**Consumes events:** none

**Invariants:**
- RFC acceptance requires ≥ 2/3 supermajority.
- Votes are append-only; no vote can be changed after cast.
- Each voter may vote once per RFC.

---

### 3. Plugin Sandbox Context

**Owns:** Plugin lifecycle, confinement, IPC, capability enforcement  
**Packages:** `internal/sandbox`, `internal/plugins`, `internal/registry`  
**Publishes events:** `plugin.started`, `plugin.crashed`, `plugin.quarantined`  
**Consumes events:** `article.published` (to invoke hooks)

**Invariants:**
- Plugin binary hash verified before every cold start.
- Plugin may not exceed `Manifest.MaxRestarts` before quarantine.
- Quarantined plugins require operator action to restore.
- Plugin environment contains only `PrepareExecEnv` output (no host env).

---

### 4. Observability Context

**Owns:** Structured logs, distributed traces, metrics  
**Packages:** `internal/logging`, `internal/trace`, `internal/metrics`  
**Publishes events:** none (passive)  
**Consumes events:** all (correlation ID threading)

**Invariants:**
- Every request has a `correlation_id` from ingress to storage.
- Traces are persisted to SQLite with 30-day retention.
- No secrets appear in log fields (enforced by `logging.LogFields` struct).

---

### 5. Federation Context

**Owns:** ActivityPub inbox/outbox, WebFinger, actor management  
**Packages:** `internal/federation`  
**Publishes events:** `federation.received`, `federation.sent`  
**Consumes events:** `article.published` (to create ActivityPub `Create` activities)

**Invariants:**
- Incoming content is always HTML-sanitised before storage.
- Remote actor public keys are cached with TTL.
- Blocked actors are rejected at queue entry, not at processing.

**Stability:** Experimental — HTTP Signature verification pending.

---

### 6. AI Runtime Context

**Owns:** Embeddings, agent execution, governance policies  
**Packages:** `internal/ai`  
**Publishes events:** none  
**Consumes events:** none (invoked synchronously)

**Invariants:**
- All inference calls have context deadlines.
- PII redaction (`policy.Apply`) runs before any text enters embedding pipeline.
- AI agents cannot make outbound network calls (no network access in their execution context).

**Stability:** Experimental.

---

### 7. Search Context

**Owns:** FTS5 sharded search, semantic search index  
**Packages:** `internal/search`  
**Publishes events:** none  
**Consumes events:** `article.published`, `article.updated`, `article.deleted` (index maintenance)

**Invariants:**
- FTS5 queries use parameterised match expressions (no injection).
- Semantic search uses cosine similarity; results are ranked, not filtered.
- Search index is eventually consistent with publishing context.

---

### 8. Identity Context

**Owns:** DID:key authentication, challenge/response  
**Packages:** `internal/did`, `internal/auth`  
**Publishes events:** none  
**Consumes events:** none

**Invariants:**
- Challenges expire in 5 minutes.
- Each challenge is single-use.
- DID public key is cryptographically derived from the DID string itself.

---

### 9. Infrastructure Context (shared)

**Owns:** DB connection, event queue, cluster node, config, lifecycle  
**Packages:** `internal/db`, `internal/queue`, `internal/outbox`, `internal/cluster`, `internal/config`, `internal/lifecycle`, `internal/storage`

**Rules:**
- Infrastructure packages may be imported by any context.
- Infrastructure packages MUST NOT import domain or service packages.
- `internal/db` is the only package allowed to hold a `*sql.DB`.

---

## Prohibited Coupling Patterns

| Pattern | Why forbidden |
|---------|--------------|
| Handler imports `*sql.DB` directly | Bypasses service layer; breaks testability |
| `internal/sandbox` imports `internal/signing` | Cross-context; sandbox must not know about article signing |
| `internal/federation` imports `internal/ai` | Cross-context; federation must not depend on AI runtime |
| `internal/logging` imports any business package | Logging is infrastructure; must have zero business dependencies |
| Any package imports `internal/api` | `api` is the outermost layer; nothing may depend on it |

---

## Dependency Health Checks

Run before every release:

```bash
# Check for import cycles
go build ./...   # cycles cause build failure

# Check layer violations (manual for now; tool planned for Ω2)
grep -r '"github.com/johalputt/vayupress/internal/api' internal/ \
  --include="*.go" | grep -v "internal/api/"
# Should return nothing.

# Check that infrastructure has no business imports
grep -r '"github.com/johalputt/vayupress/internal/signing\|internal/governance\|internal/federation' \
  internal/db/ internal/logging/ internal/queue/ internal/cluster/
# Should return nothing.
```
