# ADR-0058 — Kernel-Level Isolation & Resource Domains

**Status:** Accepted
**Date:** 2026-06-13

## Context

P26 (ADR-0057) hardened the sandbox with capability enforcement, binary hash
verification, IPC flood protection, and process attribute hardening (Pdeathsig,
NoNewPrivs). However, plugins still share the host's kernel namespaces and have
no enforced resource ceilings beyond execution time. A misbehaving plugin can
still exhaust memory, fork-bomb the system, or use IPC primitives to interfere
with host processes.

## Decision

### 1. cgroup v2 resource ceilings

Per-plugin cgroup slices are created under `/sys/fs/cgroup/vayupress/plugins/`
after subprocess start. Three controllers are supported:

- `memory.max` — resident memory ceiling (OOM-kills the plugin, not the host)
- `cpu.max` — CPU quota in microseconds per 100ms period
- `pids.max` — maximum process/thread count within the cgroup

Limits are declared in `Manifest.ResourceLimits`. All limits are optional and
default to unlimited. cgroup setup is best-effort: if the host lacks cgroup v2
or the process lacks permission, a warning is logged and execution continues.
cgroup directories are cleaned up in `killSubprocess()`.

### 2. Linux namespace isolation

`SysProcAttr.Cloneflags` is set with:

- `CLONE_NEWPID` (when `IsolatePID: true`, default true) — plugin gets its own
  PID namespace; it cannot see or send signals to host processes; if the plugin
  forks, all descendants are contained.
- `CLONE_NEWIPC` (when `IsolateIPC: true`, default true) — own IPC namespace;
  no access to host shared memory segments, semaphores, or message queues.
- `CLONE_NEWNET` (when `AllowNetwork: false`) — own network namespace with no
  interfaces; all outbound and inbound network calls fail immediately without
  reaching the host network stack.

Namespace flags are applied via build-tag-isolated `namespace_linux.go`;
non-Linux builds use a no-op stub.

### 3. Symlink traversal hardening

`EnforceCapabilities` now calls `ResolveAndCheckPath` after the basic prefix
check. `ResolveAndCheckPath` resolves symlink targets and verifies the resolved
path is still within the declared allowed prefixes, preventing plugins from
bypassing path restrictions via symlink indirection.

## Consequences

- **Memory isolation**: an OOM-triggering plugin kills only its own cgroup, not
  the host process or other plugins.
- **CPU fairness**: runaway CPU consumption by one plugin cannot starve other
  goroutines or the HTTP serving path.
- **PID confinement**: fork-bomb attempts are blocked at the kernel level.
- **Network isolation**: `AllowNetwork: false` is now kernel-enforced via
  network namespace, not just host-side policy.
- **IPC isolation**: plugins cannot use shared memory or semaphores to
  interfere with the host or other plugins.
- **Symlink safety**: path traversal via symlinks is detected and rejected
  before IPC dispatch.
- **Trade-offs**: namespace creation adds ~1ms to subprocess startup (amortised
  by pooling). cgroup setup requires write access to `/sys/fs/cgroup/` —
  absent on non-root deployments unless cgroupfs is delegated. All features
  degrade gracefully on unsupported kernels or restricted environments.
