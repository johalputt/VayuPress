# ADR-0135: Vayu Compatibility Bible — enforceable extension contracts

- **Status**: Accepted
- **Date**: 2026-07-16
- **Deciders**: BDFL / Owner
- **Relates to**: ADR-0134 (VayuAPI fine-grained keys), ADR-0056 (plugin sandbox)

## Context

VayuPress wants an ecosystem: developers and AI agents building themes,
plugins, and tools that work on the first try. What stood in the way was not
missing machinery but missing *contracts*. The sandbox, the theme compiler,
the registry, and the API permission model all enforce real rules — yet none
of them was declared anywhere a third party could check before shipping.
Concretely: plugin manifests existed only as Go structs constructed in host
code, never as a file an author could write; hook names were free-form strings
where a typo silently never fired; the plugin SPEC and all five example
plugins documented hook names (`articles.write`, `article.created.v1`) that
the host has never fired; the compat golden test froze a hand-written field
list containing a wire key (`hook_name`) that never existed (the real tag is
`hook`); themes failed late (a bad colour aborts compile at apply time) or
silently (bad dimensions dropped, unknown option keys ignored); and nothing
told an extension which API permissions it could declare.

## Decision

1. **Contracts live in code, docs describe them.** A new `internal/vcb`
   package is the single source of truth: an enumerated hook catalogue
   (exactly the names the host fires: `article.create`, `article.update`,
   `article.delete`, `cache.purge`), a versioned on-disk plugin manifest
   (`plugin.json`, strict parsing — unknown fields are errors), a versioned
   theme manifest (`theme.json` wrapping the real `theme.Tokens`), and the
   capability vocabulary re-exported from `internal/apikeys`. Manifests
   convert 1:1 into the runtime `sandbox.Manifest`, so nothing validated ever
   differs from what runs.
2. **Validation is the same code the host runs.** `internal/vcb/validate`
   lifts every rule from its enforcement point — the compiler's hex/dimension/
   font primitives (now exported from `internal/theme` and shared with
   `CompileCSS` itself), the option schema, the fixed category vocabulary, the
   sandbox's hash check, the registry's HTTPS+SHA-256 requirements, the
   API-permission enum (wildcards refused in manifests: least privilege).
   `tools/vayu-compat` is a standalone CLI over that package — the first tool
   module allowed to import parent internals (module path
   `github.com/johalputt/vayupress/tools/vayu-compat` + `replace ../..`),
   precisely so it can never drift from the host.
3. **Fail closed, fail early.** Unknown manifest schema versions are refused
   outright; unknown fields are parse errors; unknown hooks, capabilities,
   option keys/values, and categories are validation errors. Silence is never
   the failure mode.
4. **The ecosystem docs now match reality.** The golden test derives the
   frozen IPC field list from the real `sandbox.Request`/`Response` via
   `encoding/json` (deliberate golden update: `hook_name` → `hook`, plus the
   capabilities and response shapes are now frozen too); SPEC.md, the example
   plugins, api-contracts.md and sandbox-boundaries.md were corrected to the
   fired hook names and the real wire key. Built-in presets are tested against
   the published theme contract, so the contract and the shipped themes cannot
   disagree.
5. **CI gates the contract.** `scripts/preflight-ci.sh` gains a named VCB
   section: `go test ./internal/vcb/...` plus an explicit build+test of the
   `tools/vayu-compat` module (nested modules are invisible to the root
   `./...`, so the gate exercises it directly).
6. **Deferred, documented, not promised**: unifying the six event namespaces
   (plugin hooks, webhook events, outbox envelope types, SSE names, two typed
   buses) is a deep refactor; the schema registry's type checker stays shallow
   (strings/integers/booleans) and unwired; in-process `HookFunc`s keep full
   process privileges — third-party code must ship as a sandboxed subprocess.
   All three are recorded as known limitations in the Bible itself.

## Consequences

- A developer or AI agent can write `plugin.json`/`theme.json`, run
  `vayu-compat check`, and know the package will install and behave — the
  validator's findings name the exact enforcement that would otherwise bite.
- The provisioning loop closes: a manifest's declared `api_permissions` are
  the boxes the operator ticks in the VayuOS key manager (ADR-0134), so an
  extension runs with exactly the access it declared.
- The hook catalogue is now load-bearing: adding a host hook means adding it
  to `internal/vcb/hooks.go` (and docs) or extensions cannot subscribe to it.
- The plugin IPC golden finally freezes the real wire contract; any future
  change to `sandbox.Request`/`Response` fails the golden and demands a
  deliberate update per the stability matrix.
- Public documentation ships at `/docs/compatibility/vcb` and
  `/docs/compatibility/vayuapi` (the URL the API's 403/429 errors have linked
  to since v3.13.38 — previously a 404, now real).
