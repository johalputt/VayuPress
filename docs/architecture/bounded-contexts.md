# Bounded Contexts — VayuPress Internal Architecture

**Status:** Authoritative  
**Last reviewed:** 2026-09-25

Every rule below names the test that enforces it. A rule without one is not
listed here.

---

## Layering Rule

```
api → services → domain → infrastructure
```

Higher layers may import lower layers; lower layers must not import higher
ones. Go refuses import cycles at build time. The directions a cycle check
cannot see are pinned in `internal/archcheck/archcheck_test.go`:

| Rule | Enforced by |
|------|-------------|
| Nothing below `internal/api` imports it (`sandbox`, `logging`, `db`) | `TestLayerViolations` |
| Infrastructure (`logging`, `metrics`, `db`, `queue`, `config`, `lifecycle`) imports no domain package (`plugins`, `sandbox`, `search`) | `TestInfraHasNoDomainImports` |
| No `util`, `common`, `shared`, `types`, `dto`, `model` or `helpers` package under `internal/` | `TestNoSharedDTOPackages` (`creep_test.go`) |

A package a rule names that no longer loads fails the test, so a rule cannot
outlive the package it guards.

---

## Contexts

### Plugin Sandbox

**Packages:** `internal/sandbox`, `internal/plugins`

- A plugin whose manifest carries `ExecutableHash` is refused before start when
  the binary's SHA-256 differs (`sandbox/subprocess.go`, `verifyExecutableHash`).
- A crashed plugin is restarted at most `Manifest.MaxRestarts` times (default
  3) and then quarantined (`sandbox/manifest.go`).
- The plugin's environment is built by `PrepareExecEnv`, not inherited from the
  host.
- `internal/sandbox` does not import `reflect` (`TestNoReflectionInCriticalPaths`).
- The IPC request and response shapes are frozen by
  `internal/compat/golden_test.go` against `testdata/golden/plugin-request-schema.json`.

### Observability

**Packages:** `internal/logging`, `internal/trace`, `internal/metrics`,
`internal/severity`, `internal/budget`, `internal/provenance`

The severity taxonomy, the budgets that track it and confidence propagation are
pinned by `internal/archcheck/ontology_contract_test.go` against
[`docs/governance/operational-ontology.md`](../governance/operational-ontology.md).

### Search

**Packages:** `internal/search` (SQLite FTS5)

### Identity

**Packages:** `internal/auth`, `internal/apikeys`, `internal/oauth`

### Infrastructure

**Packages:** `internal/db`, `internal/queue`, `internal/outbox`,
`internal/config`, `internal/lifecycle`

Infrastructure may be imported by any context and imports none of them
(`TestInfraHasNoDomainImports`).
