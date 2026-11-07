# VayuPress Plugins

> **Normative contract:** [`SPEC.md`](SPEC.md) is the formal, versioned plugin
> interface specification (manifest schema, capability model, IPC protocol, hook
> events, lifecycle, conformance). This README is the friendly companion — where
> they differ, `SPEC.md` wins.

VayuPress plugins are **out-of-process** programs the host launches inside a
sandbox: its own PID, IPC and network namespaces (no network unless granted),
an optional private mount tree, cgroup v2 limits, an optional `uid:gid` to run
as, a verified binary hash, a parent environment it never inherits, and
explicit path/network allowlists (ADR-0056). A seccomp syscall filter and a
capability drop are written (`internal/sandbox/confinement_linux.go`) but not
yet applied to the plugin process, so neither is a control today.
A plugin can be written in any language; it only has to speak the line-oriented
JSON IPC protocol below.

## IPC protocol

The host writes **one JSON request per line** to the plugin's stdin and reads
**one JSON response per line** from stdout.

### Request (host → plugin)

```json
{
  "hook": "article.create",
  "payload": { "slug": "hello-world", "id": "…", "content": "<p>…</p>" },
  "correlation_id": "…",
  "causation_id": "…",
  "trace_id": "…",
  "capabilities": { "allow_network": false, "allowed_read_paths": [], "allowed_write_paths": [] }
}
```

`capabilities` echoes what the host actually granted. The host enforces the
sandbox regardless, but well-behaved plugins also self-check (see the
`webhook-notify` example).

### Response (plugin → host)

```json
{ "ok": true, "log_lines": [ { "level": "info", "msg": "…" } ] }
```

On failure, return `{ "ok": false, "error": "…" }`. `log_lines` are forwarded
through the host's structured logging pipeline tagged with the request's
correlation ID.

## Manifest

Plugins are registered in host code with a `sandbox.Manifest`. Grant the least
privilege the plugin needs — every field defaults to "denied".

| Field | Meaning | Default |
|-------|---------|---------|
| `Name` | Identifier used in logs/metrics | — |
| `Executable` | Absolute path to the plugin binary | — |
| `Args` | Startup arguments | none |
| `AllowedReadPaths` | Readable path prefixes | none (no FS read) |
| `AllowedWritePaths` | Writable path prefixes | none (no FS write) |
| `AllowNetwork` | Permit outbound network | `false` |
| `Timeout` | Max duration per invocation | `DefaultPluginTimeout` |

### Installing

A plugin is a directory in the plugins directory (`PLUGINS_DIR`, by default
`/var/lib/vayupress/plugins`) holding its binary and a `plugin.json`:

```sh
CGO_ENABLED=0 go build -o /var/lib/vayupress/plugins/wordcount/wordcount ./docs/plugins/examples/wordcount
sha256sum /var/lib/vayupress/plugins/wordcount/wordcount
```

```json
{
  "vcb": 1,
  "name": "wordcount",
  "version": "1.0.0",
  "description": "Counts the words of each new post.",
  "license": "Apache-2.0",
  "hooks": ["article.create"],
  "executable": "wordcount",
  "executable_sha256": "<the digest sha256sum printed>"
}
```

The server loads every plugin there when it starts with
`VAYU_PLUGINS_ENABLED=true`. Before one starts it must pass all of:

- the checks `vayu-compat check --files` runs (`tools/vayu-compat`), with the binary's hash compared
  to `executable_sha256`;
- `executable_sha256` present (the sandbox checks it again before every
  start, so a binary changed after review does not run);
- at least one hook;
- the plugins directory, the plugin's directory, its `plugin.json` and its
  binary owned by root or by the server's own user, not writable by any
  group or other user, and none of them a symbolic link: whoever can write
  them runs code as the server.

A plugin that fails is not started, and Tools and plugins says why. Build
plugins static (`CGO_ENABLED=0` for Go): a plugin that declares no path may
not open files, so a dynamically linked one cannot load its libraries.

## Examples

| Example | Capabilities | Demonstrates |
|---------|--------------|--------------|
| [`wordcount`](examples/wordcount) | none (fully isolated) | A pure, side-effect-free transform that reads the payload and emits a log line. |
| [`webhook-notify`](examples/webhook-notify) | `AllowNetwork: true` | An outbound integration that self-checks its granted capability before calling out. |
| [`seo-stamp`](examples/seo-stamp) | `AllowedReadPaths` | Settings access: reads an exported `vayupress-theme.json` bundle from an allowlisted read path (self-checking the grant first) and stamps SEO from `site.author` / `head.keywords`. |
| [`frontmatter-guard`](examples/frontmatter-guard) | none (fully isolated) | A pure governance check: validates editorial invariants and signals a failure via `{"ok": false}`, which the host records and quarantines on. |
| [`trace-tap`](examples/trace-tap) | none (fully isolated) | An observability tap: reads the `correlation_id` / `causation_id` / `trace_id` the host passes on every call and echoes them in its log line, so plugin work stitches into the host's trace waterfall (`GET /api/v1/admin/trace/{correlation_id}`). |

Build an example:

```sh
CGO_ENABLED=0 go build -o wordcount ./docs/plugins/examples/wordcount
```

## Security model

- **Least privilege:** grant only the paths/network a plugin proves it needs.
- **Host-enforced:** the sandbox is enforced by the host kernel mechanisms, not
  by plugin cooperation. Capability self-checks are defense in depth, not the
  primary control.
- **Resource-bounded:** each invocation is time-boxed (`Timeout`) and the pool
  size caps concurrency; crashes are counted and the worker is restarted up to
  `MaxRestarts`.
- **Quarantine:** when the system enters `quarantined` mode, plugins are
  suspended (see the mode state machine, Ω5/Ω6).
