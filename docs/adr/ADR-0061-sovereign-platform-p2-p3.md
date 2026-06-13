# ADR-0061 — Sovereign Platform: P2/P3 Systems

**Status:** Accepted  
**Date:** 2026-06-13

## Context

Following P0 (deploy/migrations/SQLite) and P1 (event schemas/tracing/fuzz/supply-chain),
the remaining sovereign platform capabilities span plugin registry, content integrity,
real-time streaming, governance voting, immutable archives, and search sharding.

## Decisions

### P2/16 — Plugin Registry (`internal/registry/`)
Centralized `Registry` with `PluginMeta` (name, version, SHA-256, download URL).
`Install()` downloads, verifies SHA-256 before writing to destPath. HTTP handler
exposes `/plugins` JSON endpoint. Atomic write via os.Rename after hash check.

### P2/17-18 — Immutable Bundles & Signed Manifests (`deploy/bundle/`)
`pack.sh` creates `.vayu` tar.gz bundles with per-file SHA256SUMS + outer checksum.
`verify.sh` validates both. `release.yml` GitHub Actions workflow: cosign keyless
signing of binaries on tag push, `sbom.yml` generates CycloneDX SBOM.

### P3/21 — Signed Articles (`internal/signing/`)
Ed25519 sign/verify over canonical JSON `(id, title, body, author_id, published_at)`.
`GenerateKeyPair()`, `Sign()`, `Verify()`. Public key embedded in `SignedArticle`
for self-contained verification without PKI.

### P3/22 — Merkle Trees (`internal/merkle/`)
SHA-256 binary Merkle tree. `New([][]byte)` → `Tree`. `Root()` hex string.
`Proof(index)` → sibling hashes. `Verify(leaf, index, proof, root)` for
inclusion proofs. Deterministic — same inputs always produce same root.

### P3/25 — WebSocket Event Streaming (`internal/ws/`)
SSE hub (no external deps). `Hub.Broadcast(Message)` → all connected clients.
`Hub.ServeHTTP` streams `text/event-stream`. Non-blocking broadcast: slow
clients are dropped (buffered channel with select/default). `ConnectedCount()`.

### P3/27 — Local Search Sharding (`internal/search/sharded/`)
`ShardedIndex` distributes posts across N SQLite FTS5 shards by FNV-32 hash
of post ID. `Search()` queries all shards concurrently via goroutines, merges
results. Gracefully skips if FTS5 unavailable.

### P3/33 — Immutable Archives (`internal/archive/`)
`Manager.Create(id, paths)` → tar.gz with embedded `manifest.json` (SHA-256 per file).
`Manager.List()` enumerates snapshots. Suitable for database point-in-time backups
and historical replay.

### P3/35 — Constitutional Voting (`internal/governance/`)
`RFC` + `Store` + `CastVote()`. 2/3 majority threshold (`AcceptanceThreshold = 2/3`).
Auto-resolves when quorum met. Duplicate vote prevention. RFC lifecycle: open →
accepted/rejected/withdrawn.

## Consequences
- Plugin integrity enforced at install time (SHA-256, no implicit trust)
- Content is tamper-evident via Ed25519 signatures and Merkle proofs  
- Real-time observability via SSE without WebSocket library dependency
- Search scales horizontally by adding shards (no re-indexing needed)
- Community governance is constitutionally encoded with auditable vote trails
