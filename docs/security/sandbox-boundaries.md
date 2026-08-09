# Sandbox Boundaries — VayuPress Plugin Confinement

**Status:** Authoritative  
**Applies to:** `internal/sandbox/`  
**Last reviewed:** 2026-06-13

---

## Confinement Layers (P27 + P28)

```
┌─────────────────────────────────────────────────────────────────┐
│  Host Process (vayupress binary)                                │
│  UID: non-root (www-data or dedicated uid)                      │
│                                                                  │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │  cgroup v2 slice  (P27 — ADR-0058)                       │  │
│  │  memory.max = Manifest.MemoryLimitMB                      │  │
│  │  cpu.max    = Manifest.CPUQuota                           │  │
│  │  pids.max   = 32                                          │  │
│  │                                                           │  │
│  │  ┌─────────────────────────────────────────────────────┐  │  │
│  │  │  Linux Namespaces (P27)                            │  │  │
│  │  │  CLONE_NEWPID  — private PID tree                  │  │  │
│  │  │  CLONE_NEWIPC  — private SysV IPC / shared memory  │  │  │
│  │  │  CLONE_NEWNET  — no network access                 │  │  │
│  │  │  CLONE_NEWNS   — private mount tree (P28)          │  │  │
│  │  │                                                    │  │  │
│  │  │  ┌───────────────────────────────────────────────┐ │  │  │
│  │  │  │  tmpfs scratch (P28)                         │ │  │  │
│  │  │  │  MS_NOEXEC | MS_NOSUID | MS_NODEV            │ │  │  │
│  │  │  │  size=64m                                    │ │  │  │
│  │  │  │  path: PLUGIN_SCRATCH env var                │ │  │  │
│  │  │  └───────────────────────────────────────────────┘ │  │  │
│  │  │                                                    │  │  │
│  │  │  /proc masked: kcore, keys, sched_debug, scsi,    │  │  │
│  │  │                firmware, latency_stats, timer_*   │  │  │
│  │  └─────────────────────────────────────────────────────┘  │  │
│  │                                                           │  │
│  │  Seccomp-BPF allowlist (P28 — ~35 syscalls)              │  │
│  │  All others → EPERM (graceful, not SIGKILL)              │  │
│  │                                                           │  │
│  │  capset(2) → all capabilities zeroed                     │  │
│  │                                                           │  │
│  │  FD CLOEXEC sweep before exec                            │  │
│  └───────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

---

## IPC Protocol

Plugins communicate via **line-delimited JSON on stdin/stdout**:

```
parent → child:  {"hook":"...", "payload":{...}, "correlation_id":"..."}\n
child  → parent: {"ok":true, "log_lines":[...]}\n
```

- stdin: `bufio.Writer` in parent, `bufio.Scanner` in child
- stdout: `io.LimitReader(stdout, maxBytes)` prevents flooding
- stderr: `cmd.Stderr = nil` — discarded, never forwarded
- All other FDs: CLOEXEC before `cmd.Start()`

---

## Allowed Syscalls (seccomp allowlist)

| Category | Syscalls |
|----------|---------|
| Process lifecycle | `exit`, `exit_group` |
| I/O | `read`, `write`, `readv`, `writev` |
| Memory | `brk`, `mmap`, `munmap`, `mprotect`, `mremap` |
| Signals | `rt_sigaction`, `rt_sigprocmask`, `rt_sigreturn`, `sigaltstack` |
| Go runtime | `futex`, `clone`, `gettid`, `getpid`, `tgkill`, `sched_yield`, `nanosleep` |
| FD management | `close`, `fcntl`, `fstat`, `epoll_create1`, `epoll_ctl`, `epoll_pwait`, `pipe2`, `ppoll` |
| Time | `clock_gettime`, `gettimeofday` |
| Legacy polling (ABIs that define them) | `epoll_wait`, `poll` |

All other syscalls → `SECCOMP_RET_ERRNO | EPERM`.

`epoll_pwait` and `ppoll` are the spellings every supported ABI carries, and
`epoll_pwait` is what the Go runtime's netpoller actually issues — on x86-64 as
much as on arm64. `epoll_wait` and `poll` are kept for plugins that are not Go
binaries, on the ABIs that still define them; arm64 and riscv64 never did.

---

## Degradation Behaviour

| Condition | Behaviour |
|-----------|-----------|
| `CLONE_NEWNS` rejected (EPERM, unprivileged runner) | Retry without namespace flags; log warning |
| `mount tmpfs` fails | Scratch dir exists as regular directory; log warning; no abort |
| `applyProcMask` bind-mount fails | Logged as warn; sandbox continues |
| `capset` fails | Returns error; plugin does not start |
| `ApplySeccompFilter` fails (plugin-side) | Plugin init returns error; parent sees crash → restart budget applies |
| Architecture has no `AUDIT_ARCH_*` entry | `ApplySeccompFilter` refuses; plugin does not start. It never installs an unguarded filter — a guard that cannot match kills on the first syscall |
| Syscall arrives under a different ABI than the filter was built for | `SECCOMP_RET_KILL_PROCESS` — the whole process, not just the offending thread |

---

## What Plugins Cannot Do

- Access the network (CLONE_NEWNET; no `connect`, `socket` syscalls not in allowlist)
- Read host filesystem beyond explicitly allowed paths
- Execute arbitrary binaries (MS_NOEXEC on scratch)
- Escalate privileges (all caps dropped; NO_NEW_PRIVS set)
- See host PID tree (CLONE_NEWPID)
- Communicate via SysV IPC (CLONE_NEWIPC)
- Fork unboundedly (pids.max = 32)
- Consume unbounded memory (memory.max cgroup)
- Run indefinitely (Manifest.Timeout; parent kills after deadline)
- Inherit parent file descriptors (CLOEXEC sweep)

---

## Trust Boundary: Plugin Binary Verification

Before `cmd.Start()`, if `Manifest.ExecutableHash != ""`:
1. SHA-256 of the plugin binary is computed.
2. Must match `Manifest.ExecutableHash` exactly.
3. Mismatch → `start()` returns error; plugin never executes.

This prevents supply-chain substitution of a plugin binary after registration.
