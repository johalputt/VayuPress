# ADR-0062 — Phase Ω: Consolidation Before Expansion

**Status:** Accepted  
**Date:** 2026-06-13  
**Deciders:** VayuPress Maintainers

---

## Context

After implementing P1–P28 (40 platform improvements spanning kernel isolation, event durability,
distributed tracing, governance, plugin containment, AI runtime, federation, immutable archives,
signed publishing, distributed search, and operational observability), VayuPress has crossed
from prototype to sovereign runtime platform.

The primary risks shift fundamentally at this stage:

| Risk | Severity |
|------|----------|
| Architectural entropy | Critical |
| Hidden coupling between bounded contexts | Critical |
| Security regression from rapid expansion | Critical |
| Unmaintainable abstractions | Critical |
| Governance drift (what is stable vs experimental) | Critical |

The standard engineering failure mode at this stage is continuing to add features while
the existing foundation becomes fragile. Linux, PostgreSQL, Git, and Kubernetes all passed
through consolidation phases before ecosystem expansion.

## Decision

Adopt **Phase Ω — Consolidation** as a mandatory gate before P29+.

### Phase Ω1 — Consolidation (current)

1. **Security audit**: Complete `docs/security/` with threat model, attack surfaces,
   sandbox boundaries, signing model, federation threats, and incident response playbooks.

2. **Compatibility contracts**: Complete `docs/compatibility/` with stability matrix
   (what is Stable / Beta / Experimental / Internal) and API contracts. No breaking change
   to a Stable interface without RFC + supermajority vote.

3. **Architecture governance**: Complete `docs/architecture/bounded-contexts.md` defining
   9 bounded contexts with strict layering rules. Layer violations are treated as CI failures.

4. **Profiling infrastructure**: `internal/profiling` package with rate-limited pprof handler
   and `Snapshot()` for continuous memory/GC telemetry.

5. **Operations runbooks**: `docs/operations/` expanded with WAL corruption recovery,
   backup/restore procedures with verification checklists.

### Phase Ω2 — Hardening (next)

- Fuzzing expansion (corpus-based, not just smoke)
- ActivityPub HTTP Signature verification
- Per-actor federation rate limits
- Seccomp filter for arm64
- Chaos testing: sandbox crash storms, WAL corruption injection, federation abuse
- Fault injection in plugin IPC path

### Phase Ω3 — Ecosystem Readiness (after Ω2)

- Plugin SDK (external developers)
- Public API stabilisation
- Registry ecosystem
- External contributor documentation
- Repository decomposition (ADR-0044 plan)

## Consequences

**Positive:**
- Clear stability guarantees enable external adoption.
- Bounded context isolation prevents cross-context coupling regression.
- Security documentation enables formal review.
- Operations runbooks reduce MTTR during incidents.

**Negative:**
- Feature velocity deliberately slowed during Ω1.
- Some experimental systems (federation, AI, graph) must be clearly marked to manage expectations.

## Success Criteria for Ω1

- [ ] `docs/security/` complete with all 6 required documents.
- [ ] `docs/compatibility/stability-matrix.md` covers all 30+ packages.
- [ ] `docs/architecture/bounded-contexts.md` defines all 9 bounded contexts.
- [ ] `internal/profiling` builds and pprof handler is wired into router.
- [ ] `docs/operations/` has WAL recovery and backup/restore runbooks.
- [ ] CI remains fully green.
- [ ] Zero new Stable-level interfaces broken.
