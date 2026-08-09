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

1. Validates the `AUDIT_ARCH_*` value for the architecture the binary was built
   for, taken from the `seccompAuditArch` table, and returns
   `SECCOMP_RET_KILL_PROCESS` on a mismatch. The guard is not decoration: a task
   can enter the kernel under a second ABI at runtime (the `int 0x80` compat
   gate on x86-64), where the same syscall numbers name different calls.
2. Allows a curated set of ~35 syscalls: exit/exit_group, read/write, mmap/brk,
   signal handling, Go runtime (futex/clone/gettid/tgkill), FD management,
   epoll, clock/time. The list is architecture-neutral apart from
   `legacyPollSyscalls`, which carries `epoll_wait`/`poll` on the ABIs that
   define them — arm64 and riscv64 do not, and only ever had
   `epoll_pwait`/`ppoll`.
3. Returns `SECCOMP_RET_ERRNO | EPERM` (not KILL) for anything else — the plugin
   gets an error rather than a crash, enabling graceful degradation.

The table was verified empirically, not from memory: a trivial binary was
cross-compiled for each of the seven targets and its ELF header read directly,
comparing `e_machine`, `EI_CLASS` and `EI_DATA` against the machine number and
the two flag bits each table entry decomposes to. All seven matched, s390x
included — it is the one entry with no little-endian bit. Re-run that check
rather than eyeballing the constants if an architecture is ever added; a test
that re-derives a value from a number typed by the same person who typed the
value proves only that they were consistent.

`SECCOMP_RET_KILL_PROCESS` needs Linux 4.14 (2017). Older kernels mask an
unrecognised action down to `SECCOMP_RET_KILL_THREAD`, which is what this code
did on every kernel before the fix — so the floor is the previous behaviour, not
a new failure mode.

Where `auditArch()` has no entry the filter is **not built and not installed**;
`ApplySeccompFilter()` returns an error and the plugin does not start. Installing
a filter whose guard cannot match is strictly worse than installing none, because
the guard's own failure action kills.

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
- The filter is built for seven Linux ABIs — amd64, arm64, arm, 386, riscv64,
  ppc64le, s390x. Any other Linux architecture gets no filter and
  `ApplySeccompFilter()` refuses; non-Linux stubs are no-ops (see
  `confinement_other.go`).
- Enforcement is exercised end to end on the CI runner's architecture only
  (`TestSeccompFilterActuallyEnforces` installs the real filter in a child
  process and probes an allowed and a denied syscall). The other six are covered
  by compilation and by re-deriving every `AUDIT_ARCH_*` value from its ELF
  machine number, not by running a filter on that hardware.
- `legacyPollSyscalls` exists so a non-Go plugin's libc `poll()` keeps working.
  Nothing in the suite is a non-Go plugin, so those two entries are the one part
  of the allowlist no test covers — removing them does not fail any test.

**This section previously read "Seccomp filter is x86-64 only; other
architectures get no filtering."** That was wrong in the direction that hurts.
The filter's arch guard is a kill, not a bypass: built with a hardcoded
`AUDIT_ARCH_X86_64` and run on arm or 386 — both of which compiled fine — the
guard could not match and every sandboxed plugin died on its first syscall. On
arm64 and riscv64 the package did not compile at all. A trade-off written as
"less protection" concealed "no plugin runs, on architectures nobody built for".
Read a documented limitation as a claim to check, not as a measurement.
- `CLONE_NEWNS` may require `CAP_SYS_ADMIN` on older kernels or restricted
  container runtimes; EPERM falls back to no isolation (P27 pattern).
- Capability drop must be done in the child process before execve — current
  implementation exposes `DropCapabilities()` / `ApplySeccompFilter()` for
  plugin binaries to call in their own init; future work: seccomp-new-process
  via `SysProcAttr.Pdeathsig` + pre-exec helper.

## Files Changed

- `internal/sandbox/confinement_linux.go` — P28 implementation (Linux)
- `internal/sandbox/seccomp_arch_linux.go` — `AUDIT_ARCH_*` table, `auditArch()`
- `internal/sandbox/seccomp_poll_{legacy,modern,other}_linux.go` — per-ABI
  `epoll_wait`/`poll` entries
- `internal/sandbox/seccomp_linux_test.go` — enforcement probe, audit-arch
  derivation, allowlist assertions
- `internal/sandbox/confinement_other.go` — no-op stubs (non-Linux)
- `internal/sandbox/manifest.go` — `ConfineMounts`, `DropCaps` fields
- `internal/sandbox/subprocess.go` — wired `SetupConfinement`, `CloseExtraFDs`,
  `PrepareExecEnv`, `MountNamespaceFlags` into `start()` / `killSubprocess()`
