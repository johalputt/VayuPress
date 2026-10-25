# Sandbox Boundaries — VayuPress Plugin Confinement

**Status:** Authoritative  
**Applies to:** `internal/sandbox/`  
**Last reviewed:** 2026-10-17, against VayuPress 3.18.7

---

## Confinement Layers (P27 + P28)

```
┌─────────────────────────────────────────────────────────────────┐
│  Host Process (vayupress binary)                                │
│  UID: non-root (www-data or dedicated uid)                      │
│                                                                  │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │  cgroup v2 slice  (P27 — ADR-0058)                       │  │
│  │  memory.max = ResourceLimits.MemoryMaxBytes               │  │
│  │  cpu.max    = ResourceLimits.CPUQuotaPercent              │  │
│  │  pids.max   = ResourceLimits.MaxPIDs (0 = unset)          │  │
│  │                                                           │  │
│  │  ┌─────────────────────────────────────────────────────┐  │  │
│  │  │  Linux Namespaces (P27)                            │  │  │
│  │  │  CLONE_NEWPID  — private PID tree  (IsolatePID)    │  │  │
│  │  │  CLONE_NEWIPC  — private SysV IPC  (IsolateIPC)    │  │  │
│  │  │  CLONE_NEWNET  — no network  (unless AllowNetwork) │  │  │
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
│  │  Seccomp-BPF allowlist: written, NOT applied (see below) │  │
│  │                                                           │  │
│  │  capset(2) capability drop: written, NOT applied         │  │
│  │                                                           │  │
│  │  FD CLOEXEC sweep before exec                            │  │
│  └───────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

---

**Not yet enforced (checked 2026-10-25).** The seccomp filter
(`buildSeccompFilter`, `ApplySeccompFilter`), `NoNewPrivs` and the capability
drop are in `internal/sandbox/confinement_linux.go`, but nothing calls them for
a plugin process: they must run in the child between fork and `exec`, which
the launcher does not have yet. The namespaces, cgroup limits, `RunAs`, the
binary hash and the allowlists above are applied.

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
| The kernel refuses the namespaces (EPERM, an unprivileged runner) | The plugin does not start (`subprocess.go`). Only `VAYUSANDBOX_ALLOW_NO_NAMESPACES=1` makes it retry without namespaces, logged as a warning: an operator's explicit choice, never a silent fallback |
| `mount tmpfs` fails | Scratch dir exists as regular directory; log warning; no abort |
| `applyProcMask` bind-mount fails | Logged as warn; sandbox continues |
| `capset` fails | Returns error; plugin does not start |
| `ApplySeccompFilter` fails (plugin-side) | Plugin init returns error; parent sees crash → restart budget applies |
| Architecture has no `AUDIT_ARCH_*` entry | `ApplySeccompFilter` refuses; plugin does not start. It never installs an unguarded filter — a guard that cannot match kills on the first syscall |
| Syscall arrives under a different ABI than the filter was built for | `SECCOMP_RET_KILL_PROCESS` — the whole process, not just the offending thread |

---

## What Plugins Cannot Do

- Access the network, unless its manifest sets `AllowNetwork` (CLONE_NEWNET; and
  `socket` and `connect` are not in the seccomp allowlist either way)
- Read host filesystem beyond explicitly allowed paths
- Execute arbitrary binaries (MS_NOEXEC on scratch)
- Escalate privileges (all caps dropped; NO_NEW_PRIVS set)
- See the host PID tree, when the manifest sets `IsolatePID` (CLONE_NEWPID)
- Communicate via SysV IPC, when the manifest sets `IsolateIPC` (CLONE_NEWIPC)
- Fork beyond its manifest's `MaxPIDs`, or use more memory than
  `MemoryMaxBytes` (cgroup v2), when those are set
- Run indefinitely (Manifest.Timeout; parent kills after deadline)
- Inherit parent file descriptors (CLOEXEC sweep)

---

## Trust Boundary: Plugin Binary Verification

Before `cmd.Start()`, if `Manifest.ExecutableHash != ""`:
1. SHA-256 of the plugin binary is computed.
2. Must match `Manifest.ExecutableHash` exactly.
3. Mismatch → `start()` returns error; plugin never executes.

This prevents supply-chain substitution of a plugin binary after registration.
