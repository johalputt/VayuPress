# ADR-0057 — Security Sandboxing & Capability Enforcement

**Status:** Accepted  
**Date:** 2026-06-13  
**Deciders:** VayuPress core team  
**Supersedes:** —  
**Related:** ADR-0056 (process isolation & runtime sandboxing)

---

## Context

ADR-0056 established the process-isolation model: plugins run as child subprocesses communicating via a JSON-over-stdio IPC protocol. That foundation provided crash containment but left several attack surfaces open:

1. **Binary substitution** — an attacker who can replace the plugin binary on disk gets arbitrary code execution inside the sandbox user.
2. **IPC flood** — a malicious or buggy plugin could write an unbounded stdout stream, causing the host to allocate unbounded memory while scanning.
3. **Privilege escalation** — without `PR_SET_NO_NEW_PRIVS`, a plugin binary with a setuid bit could escalate privileges after exec.
4. **Capability bypass** — the host had no host-side enforcement of the declared `Manifest` capabilities before dispatching to the subprocess.
5. **Orphan processes** — if the host process died unexpectedly, child subprocesses could continue running under a zombie parent.

---

## Decision

P26 adds five defence-in-depth layers to `internal/sandbox/` with no new external dependencies.

### 1. Binary Hash Integrity (`manifest.go`)

`Manifest.ExecutableHash` (SHA-256 hex string) is verified by `verifyExecutableHash()` at the top of `start()` before `exec.Command` is called. A mismatch aborts the launch and returns an error — the plugin is not started. This prevents binary substitution attacks where an attacker writes a different executable to the declared path.

### 2. IPC Stdout Flood Protection (`subprocess.go`)

The stdout pipe is wrapped with `io.LimitReader` capped at `Manifest.effectiveMaxMessageBytes()` (default 1 MiB). The `bufio.Scanner` buffer is sized to the same limit. Any single JSON line exceeding the limit causes scanner EOF, which is treated as a crash and triggers the normal restart/quarantine path. This bounds host memory allocation to `O(MaxMessageBytes)` per plugin regardless of what the subprocess writes.

### 3. Process Hardening via `SysProcAttr` (`subprocess_linux.go`)

On Linux, `applyProcAttr` sets:

- `Pdeathsig: SIGKILL` — the child receives SIGKILL when the parent thread dies, preventing orphaned processes.
- `Setpgid: true` — the child gets its own process group, enabling group-kill on timeout.
- `NoNewPrivs: true` — equivalent to `PR_SET_NO_NEW_PRIVS`; the subprocess and all its descendants cannot gain privileges via setuid/setgid/file capabilities, even if the binary has those bits set.

`applyRunAs` parses an optional `Manifest.RunAs` (`"uid:gid"`) and sets `SysProcAttr.Credential`, allowing plugins to run as an unprivileged dedicated user.

On non-Linux platforms (`subprocess_other.go`), `applyProcAttr` and `applyRunAs` are no-ops.

### 4. Host-Side Capability Enforcement (`capability_enforcer.go`)

`EnforceCapabilities(manifest, hook, payload)` is called in `SubprocessPlugin.Invoke` after the quarantine check and before marshaling the request. It inspects the payload for keys that imply capabilities not declared in the manifest:

- If `AllowNetwork: false`, any payload key named `url`, `endpoint`, or `host` (case-insensitive) is rejected with `ErrCapabilityDenied`.
- If a `path` key is present, it must fall under `AllowedReadPaths` or `AllowedWritePaths`.

This is a defence-in-depth layer — the subprocess self-enforces via the `Capabilities` field echoed in each `Request`, but host-side enforcement ensures a compromised plugin cannot even receive a request that exceeds its declared permissions.

### 5. Future BPF Seccomp Plumbing (`Manifest.SeccompProfile`)

`Manifest.SeccompProfile` is a string field reserved for a future BPF seccomp profile path. Writing real BPF bytecode requires kernel-version-specific care and was deferred to avoid introducing fragility. The field is present so operators can document intent and tooling can gate on it without a manifest schema change.

---

## Implementation Files

| File | Change |
|---|---|
| `internal/sandbox/manifest.go` | Added `ExecutableHash`, `MaxMessageBytes`, `SeccompProfile`, `RunAs` fields; `verifyExecutableHash()`, `effectiveMaxMessageBytes()` |
| `internal/sandbox/subprocess.go` | Hash check at top of `start()`; LimitReader + Scanner buffer; `EnforceCapabilities` call in `Invoke` |
| `internal/sandbox/subprocess_linux.go` | `applyProcAttr` with `Pdeathsig`, `Setpgid`, `NoNewPrivs`; `applyRunAs` |
| `internal/sandbox/subprocess_other.go` | No-op stubs for non-Linux |
| `internal/sandbox/capability_enforcer.go` | `EnforceCapabilities` function |
| `internal/sandbox/sandbox_test.go` | Tests for all new behaviours |

---

## Consequences

**Positive:**
- Binary substitution is caught before any code from the replacement binary runs.
- Host memory is bounded regardless of plugin behaviour.
- Privilege escalation via setuid is blocked on Linux.
- Capability violations are rejected before touching the subprocess, reducing attack surface.
- `RunAs` allows operators to run plugins as dedicated low-privilege OS users.

**Negative / Trade-offs:**
- `ExecutableHash` must be updated whenever the plugin binary is redeployed. CI pipelines should compute and embed the hash automatically.
- `LimitReader` means a plugin that legitimately wants to stream large responses must chunk them or increase `MaxMessageBytes`.
- `NoNewPrivs` is irreversible for the child's process tree; plugins that need to invoke setuid helpers will need a different approach.
- Real seccomp BPF enforcement (syscall allow-listing) is deferred — `SeccompProfile` is currently non-functional.
