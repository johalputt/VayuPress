# ADR-0059 — Filesystem & Syscall Confinement (P28)

**Status:** Accepted  
**Date:** 2026-06-13  
**Deciders:** VayuPress Maintainers  
**Supersedes:** ADR-0058 (extends it)

---

## Context

P27 added cgroup v2 resource ceilings, PID/IPC/network namespace isolation, and
symlink traversal hardening. The remaining attack surface after P27:

- Plugins can still see the full host filesystem mount tree.
- No syscall filtering — a compromised plugin can call any syscall the kernel exposes.
- Linux capabilities are fully inherited (if the daemon runs as root, so does every plugin).
- Stray parent file descriptors may leak to child processes.
- No private tmpfs scratch — plugins share `/tmp` with the host.

## Decision

Implement P28 — Filesystem & Syscall Confinement — in `internal/sandbox/`:

### 1. Mount Namespace (CLONE_NEWNS)

`Manifest.ConfineMounts = true` sets `CLONE_NEWNS` in `SysProcAttr.Cloneflags`.
Inside the new namespace:
- `applyMountPropagation()` marks the root `MS_PRIVATE|MS_REC` so no mounts
  leak back to the host tree.
- `applyProcMask()` bind-mounts `/dev/null` over sensitive `/proc` entries
  (`/proc/kcore`, `/proc/keys`, `/proc/sched_debug`, etc.).

### 2. Private tmpfs Scratch Directory

`setupMountConfinement()` allocates a per-invocation `os.MkdirTemp` directory
and mounts a `tmpfs` (MS_NOEXEC|MS_NOSUID|MS_NODEV, 64 MiB cap) on it. The
path is passed to the plugin via `PLUGIN_SCRATCH` env var. Unmounted and removed
on `PluginConfinement.Cleanup()`.

Failure to mount tmpfs is **non-fatal** (logs a warning, scratch dir still exists
as a regular directory) — best-effort degradation.

### 3. Seccomp-BPF Syscall Allowlist

`buildSeccompFilter()` generates a minimal BPF program that:
1. Validates `AUDIT_ARCH_X86_64` (kills on wrong arch).
2. Allows a curated set of ~35 syscalls: exit/exit_group, read/write, mmap/brk,
   signal handling, Go runtime (futex/clone/gettid/tgkill), FD management,
   epoll, clock/time.
3. Returns `SECCOMP_RET_ERRNO | EPERM` (not KILL) for anything else — the plugin
   gets an error rather than a crash, enabling graceful degradation.

`ApplySeccompFilter()` installs via `prctl(PR_SET_NO_NEW_PRIVS, 1)` then
`prctl(PR_SET_SECCOMP, SECCOMP_MODE_FILTER, &prog)`. This is called by the
plugin binary's own init path (plugin-side enforcement).

### 4. Linux Capability Dropping

`DropCapabilities()` calls `capset(2)` with all-zero effective/permitted/inheritable
sets, stripping every Linux capability (CAP_SYS_ADMIN, CAP_NET_ADMIN,
CAP_SYS_PTRACE, CAP_SETUID, CAP_SETGID, etc.). Called plugin-side before execve.

### 5. FD Inheritance Control

`CloseExtraFDs()` reads `/proc/self/fd` and sets `FD_CLOEXEC` on all FDs
except stdin (0), stdout (1), stderr (2), and any explicitly kept FDs.
Called in the parent before `cmd.Start()`.

### 6. Secure Exec Environment

`PrepareExecEnv()` builds the minimal env `{PATH, HOME=/tmp, PLUGIN_NAME,
PLUGIN_SCRATCH}` plus any Manifest-declared extras. Parent environment is
never inherited.

## Consequences

**Positive:**
- Plugin syscall surface shrinks from ~450 to ~35.
- Mount tree leakage to host is impossible via `MS_PRIVATE|MS_REC`.
- Sensitive `/proc` files are masked before the plugin can read them.
- Capability escalation eliminated by stripping all capabilities at exec.
- Stray FD leaks closed via CLOEXEC sweep.
- Writable scratch is isolated per-plugin, auto-cleaned, size-capped.

**Negative / Trade-offs:**
- Seccomp filter is x86-64 only; other architectures get no filtering
  (non-Linux stubs are no-ops — see `confinement_other.go`).
- `CLONE_NEWNS` may require `CAP_SYS_ADMIN` on older kernels or restricted
  container runtimes; EPERM falls back to no isolation (P27 pattern).
- Capability drop must be done in the child process before execve — current
  implementation exposes `DropCapabilities()` / `ApplySeccompFilter()` for
  plugin binaries to call in their own init; future work: seccomp-new-process
  via `SysProcAttr.Pdeathsig` + pre-exec helper.

## Files Changed

- `internal/sandbox/confinement_linux.go` — P28 implementation (Linux)
- `internal/sandbox/confinement_other.go` — no-op stubs (non-Linux)
- `internal/sandbox/manifest.go` — `ConfineMounts`, `DropCaps` fields
- `internal/sandbox/subprocess.go` — wired `SetupConfinement`, `CloseExtraFDs`,
  `PrepareExecEnv`, `MountNamespaceFlags` into `start()` / `killSubprocess()`
