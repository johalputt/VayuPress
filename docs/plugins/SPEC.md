# VayuPress Plugin Interface Specification

**Status:** Stable · **Spec version:** 1.0 · **Applies to:** VayuPress ≥ 1.5.0

This is the normative interface contract between the VayuPress host and a
plugin. The key words **MUST**, **MUST NOT**, **SHOULD**, **SHOULD NOT**, and
**MAY** are to be interpreted as in RFC 2119.

A conforming plugin and a conforming host that both implement this document will
interoperate regardless of the language the plugin is written in.

---

## 1. Plugin kinds

VayuPress supports two plugin kinds. This spec is normative for both, but §4–§6
(IPC, sandbox, lifecycle) apply only to **out-of-process** plugins.

| Kind | Runs as | Isolation | Use when |
|------|---------|-----------|----------|
| **In-process hook** | A Go `HookFunc` compiled into the host | None (same address space) | First-party, trusted extensions |
| **Out-of-process (sandboxed)** | A separate executable launched by the host | seccomp-BPF, PID/IPC/mount namespaces, cgroup v2 limits, dropped capabilities, path/network allowlists | Third-party or untrusted code (the default for installable plugins) |

Out-of-process plugins **MAY** be written in any language; they only have to
speak the IPC protocol in §4.

---

## 2. Manifest

Every out-of-process plugin is described by a **Manifest** the operator
registers with the host. Unlisted capabilities are **denied by default**.

| Field | Type | Required | Default | Meaning |
|-------|------|----------|---------|---------|
| `Name` | string | **yes** | — | Unique id (used in logs, metrics, the registry UI). |
| `Executable` | string | **yes** | — | Path to the plugin binary. |
| `Args` | []string | no | `[]` | Startup arguments. |
| `AllowedReadPaths` | []string | no | `[]` (none) | Filesystem path **prefixes** the plugin may read. |
| `AllowedWritePaths` | []string | no | `[]` (none) | Filesystem path **prefixes** the plugin may write. |
| `AllowNetwork` | bool | no | `false` | Whether outbound network is permitted. |
| `Timeout` | duration | no | `2s` | Max wall-clock for a single hook invocation. |
| `MaxRestarts` | int | no | `3` | Crashes tolerated before quarantine. |
| `Env` | []string | no | `[]` | Extra `KEY=VALUE` vars. Parent env is **not** inherited. |
| `ResourceLimits` | object | no | unlimited | cgroup v2 ceilings (memory, CPU %, max PIDs). |
| `IsolatePID` | bool | no | `true` | Run in a private PID namespace. |
| `IsolateIPC` | bool | no | `true` | Run in a private IPC namespace. |
| `ExecutableHash` | string | no | `""` | Expected SHA-256 hex; verified before exec when set. |
| `MaxMessageBytes` | int64 | no | `1 MiB` | Max size of one stdout message. |
| `RunAs` | string | no | `""` | `"uid:gid"` to drop to on Linux. |
| `ConfineMounts` | bool | no | `false` | Private mount ns + tmpfs scratch; read-only view of allowed paths. |
| `DropCaps` | bool | no | `true` when root | Drop all Linux capabilities before exec. |

The host **MUST** reject a manifest with an empty `Name` or `Executable`. When
`ExecutableHash` is set the host **MUST** verify the on-disk binary's SHA-256
before launching and refuse to start on mismatch.

---

## 3. Capability model

Capabilities are **allowlists**. The host enforces them independently of the
plugin; a plugin **MUST NOT** assume the host will not also block a denied
operation.

- **Filesystem.** A path is permitted only if it is prefixed by an entry in
  `AllowedReadPaths` (read) or `AllowedWritePaths` (write). Everything else is
  denied. A payload key that names a path outside the allowlist **MUST** cause
  the host to reject the invocation with a capability error.
- **Network.** When `AllowNetwork` is `false`, the host denies invocations whose
  payload implies network access, and (on Linux) the sandbox prevents it.
- **Privilege.** With `DropCaps` the host drops all Linux capabilities and sets
  `NoNewPrivs` before `exec`, so the plugin cannot regain privilege.

The granted set is echoed to the plugin in each request's `capabilities` object
(§4.1) so a well-behaved plugin **MAY** self-check before acting.

---

## 4. IPC protocol (out-of-process)

Transport is **line-oriented JSON over stdio**. The host writes **exactly one
JSON request per line** to the plugin's **stdin** and reads **exactly one JSON
response per line** from the plugin's **stdout**. `stderr` is free-form and
**SHOULD** be avoided in favour of structured `log_lines` (§4.2).

A response line **MUST NOT** exceed `MaxMessageBytes`; the host **MUST** treat
an over-long line as a protocol error and count it as a crash.

### 4.1 Request (host → plugin)

```json
{
  "hook": "articles.write",
  "payload": { "slug": "hello-world", "id": "…", "content": "<p>…</p>" },
  "correlation_id": "…",
  "causation_id": "…",
  "trace_id": "…",
  "capabilities": {
    "allow_network": false,
    "allowed_read_paths": [],
    "allowed_write_paths": []
  }
}
```

| Field | Type | Notes |
|-------|------|-------|
| `hook` | string | The event name (§5). |
| `payload` | object | Event data. Schema is per-hook. |
| `correlation_id` | string | Stable id for the originating operation. |
| `causation_id` | string | Id of the immediate cause. |
| `trace_id` | string | Distributed-trace id. |
| `capabilities` | object | The permissions the host actually granted. |

The plugin **MUST** read one request, process it, then write one response
before reading the next. It **MUST** tolerate being sent multiple requests over
its lifetime (the host keeps the process warm).

### 4.2 Response (plugin → host)

```json
{
  "ok": true,
  "error": "",
  "log_lines": [ { "level": "info", "msg": "stamped seo meta" } ]
}
```

| Field | Type | Notes |
|-------|------|-------|
| `ok` | bool | `true` on success. |
| `error` | string | Human-readable error when `ok` is `false`. |
| `log_lines` | array | Structured logs the host forwards with the request's correlation id. |

A plugin **MUST** set `ok:false` with a non-empty `error` to signal failure; it
**MUST NOT** crash to report an ordinary error.

---

## 5. Hook events

The host fires named hooks. A plugin is registered against one hook event and is
invoked each time that event occurs. Current first-party events:

| Hook | Fired when | Payload (indicative) |
|------|-----------|----------------------|
| `articles.write` | An article is created or updated | `slug`, `id`, `content`, `title`, `tags` |

Payload schemas are **additive**: the host **MAY** add fields; a plugin **MUST**
ignore unknown fields. New hook events are introduced in a backward-compatible
way and documented here.

---

## 6. Lifecycle & failure handling

1. **Start.** The host verifies `ExecutableHash` (if set), applies namespaces,
   cgroup limits, capability drops and `exec`s the binary.
2. **Invoke.** Per event, the host enforces capabilities, writes a request, and
   waits up to `Timeout` for a response.
3. **Timeout.** Exceeding `Timeout` is treated as a crash.
4. **Crash/restart.** A crashed process is restarted up to `MaxRestarts` times.
5. **Quarantine.** Beyond `MaxRestarts`, the plugin is **quarantined** and no
   longer invoked. When the system is in **Quarantined** mode, *all* plugin
   invocations are denied regardless of per-plugin state.
6. **Shutdown.** On host shutdown every subprocess is terminated.

Plugin failures are **fully contained**: a crash, hang, or protocol violation in
a plugin **MUST NOT** affect host correctness or availability.

The live state of every registered subprocess plugin (running/quarantined, PID,
invocation count, crash count) is surfaced in the admin under
**VayuOS → Tools & Plugins → Sandboxed plugins**.

---

## 7. Conformance

A plugin is **conforming** if it:

- speaks the §4 line-oriented JSON IPC protocol;
- emits exactly one response per request, within `Timeout` and `MaxMessageBytes`;
- reports ordinary failures via `ok:false`/`error` rather than crashing;
- operates strictly within its granted capabilities; and
- ignores unknown request fields.

See `docs/plugins/examples/` for conforming reference plugins
(`seo-stamp`, `trace-tap`, `webhook-notify`, `frontmatter-guard`, `wordcount`).

---

## 8. Versioning

This spec is versioned independently of VayuPress. Backward-compatible additions
(new optional manifest fields, new hook events, new payload fields) increment the
**minor** spec version. A breaking change increments the **major** version and is
accompanied by an ADR and a migration note.
