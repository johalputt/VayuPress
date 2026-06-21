# ADR-0074 — Formal Plugin Interface Specification

**Status:** Accepted
**Date:** 2026-06-21
**Deciders:** VayuPress Maintainers
**Relates to:** ADR-0056 (sandboxed subprocess plugins), ADR-0072 (Tools & Plugins panel)

---

## Context

VayuPress has shipped sandboxed, out-of-process plugins since ADR-0056: a host
launches a plugin executable inside seccomp/namespace/cgroup confinement and
exchanges line-oriented JSON over stdio. The contract was, until now, expressed
only as Go types (`sandbox.Manifest`, `sandbox.Request`, `sandbox.Response`) plus
a prose `docs/plugins/README.md`.

For a plugin ecosystem to exist, third-party authors — writing in any language —
need a **normative**, versioned contract they can implement against without
reading the Go source, and a stable promise about compatibility. The VayuOS
"Tools & Plugins" surface (ADR-0072) also needs to present the live plugin
registry, which presumes a named, documented interface.

## Decision

Publish a formal **Plugin Interface Specification** at `docs/plugins/SPEC.md`,
versioned independently of VayuPress (spec v1.0), using RFC 2119 language. It
normatively defines:

- the two plugin **kinds** (in-process hook, out-of-process sandboxed);
- the **Manifest** schema (every field, requiredness, default, meaning);
- the **capability model** (deny-by-default allowlists for filesystem and
  network; privilege dropping);
- the **IPC protocol** (one JSON request/response per line over stdio, with the
  exact Request/Response/Capabilities field tables and size/timeout limits);
- the **hook events** and their additive payload-evolution rule;
- the **lifecycle** (start → invoke → timeout → crash/restart → quarantine →
  shutdown) and the containment guarantee;
- a **conformance** checklist; and
- a **versioning** policy (minor = backward-compatible additions, major =
  breaking + migration note).

The spec is descriptive of the *existing* implementation — it introduces no code
changes to the sandbox — so it is immediately accurate.

In parallel, the VayuOS **Tools & Plugins** page gains a "Sandboxed plugins"
registry section that surfaces every registered subprocess plugin's live health
(running/quarantined, PID, invocation count, crash count) from
`plugins.SubprocessStats()`, with a pointer to this spec.

## Consequences

- Third parties can author conforming plugins in any language against a stable,
  testable contract.
- The registry UI gives operators visibility into installed out-of-process
  plugins and their runtime health.
- Future protocol changes are governed: additive changes bump the spec minor
  version; breaking changes require a new ADR and a migration note.
- The Go types remain the implementation, but `SPEC.md` is now the source of
  truth for the *interface*; the two must be kept in sync (a divergence is a bug
  in whichever drifted).

## Alternatives considered

- **Leave the contract as Go types + README** — rejected: not language-neutral,
  not normative, and offers no compatibility promise, which blocks a third-party
  ecosystem.
- **Adopt an external plugin standard (e.g. WASM component model)** — rejected
  for now: heavier, and the existing subprocess+seccomp model already delivers
  strong isolation with zero new dependencies, consistent with the sovereign,
  single-binary posture. Revisitable in a future major spec version.
