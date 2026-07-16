# vayu-compat

Validate a VCB extension package — a plugin (`plugin.json`) or a theme
(`theme.json`) — against the **Vayu Compatibility Bible** (VCB, ADR-0135), the
machine-checkable contract VayuPress enforces at install/apply time. An
extension that passes here installs and runs without compatibility surprises;
one that fails would misbehave in exactly the way each finding describes.

Unlike the other `tools/*` utilities, vayu-compat imports the host's own
contract packages (`internal/vcb`, `internal/theme`, `internal/apikeys`,
`internal/sandbox`) via a `replace ../..` directive, so the rules it applies
are — by construction — the same code the running VayuPress applies. It can
never drift from the host.

## What it checks

| Rule family | Severity | Description |
|-------------|----------|-------------|
| `manifest.vcb.unsupported` | ERROR | Manifest schema version the host does not understand (fail-closed) |
| `plugin.name.*` / `plugin.version.*` | ERROR | Identity required by the sandbox and the plugin registry |
| `plugin.hook.unknown` | ERROR | Hook not in the enumerated catalogue — it would never fire |
| `plugin.apiperm.unknown` / `.wildcard` | ERROR | API permission not a valid `section:action`, or a wildcard (least privilege is the contract) |
| `plugin.executable.*` / `plugin.sha256.*` | ERROR | Executable must be relative + traversal-free; SHA-256 must match (the sandbox refuses a mismatch) |
| `plugin.sandbox.*` | ERROR/WARN | Capability requests: absolute paths, sane limits, numeric `uid:gid`; network/write access surfaces as warnings |
| `plugin.download.*` | ERROR | Distribution metadata: HTTPS-only URL, valid artifact SHA-256 |
| `host.range*` | ERROR | `min_host`/`max_host` must be valid versions and include the running host |
| `theme.color.invalid` | ERROR | Non-hex colour — the compiler refuses the whole theme at apply time |
| `theme.dimension.invalid` / `theme.font.unsafe` | ERROR | Values the compiler would silently drop or mangle |
| `theme.option.unknown` / `.value` | ERROR | Option keys/values outside the schema — silent no-ops at apply time |
| `theme.css.*` | ERROR/WARN | CustomCSS: no `@import`, no external `url()` (CSP `style-src 'self'`), size caps |
| `theme.meta.*` / `theme.name.collision` | ERROR | Catalogue integrity: meta name matches, category in the fixed vocabulary, no preset shadowing |

Exits with code **1** if any ERROR is found (useful in CI pipelines).

## Installation

```bash
cd tools/vayu-compat
go build -o vayu-compat ./cmd/vayu-compat
```

(No CGO required.)

## Usage

```bash
# Validate a plugin package against the running host version
vayu-compat check --manifest ./my-plugin/plugin.json --host 3.13.41

# Also verify the executable exists and matches executable_sha256
vayu-compat check --manifest ./my-plugin/plugin.json --host 3.13.41 --files

# Validate a theme package
vayu-compat check --manifest ./my-theme/theme.json

# Print the enumerated hook catalogue / capability vocabulary
vayu-compat hooks
vayu-compat capabilities
```

## Example output

```text
✗ [plugin.hook.unknown] hooks: hook "article.created.v1" is not in the VCB hook catalogue — it would never fire; see docs/compatibility/vcb
⚠ [plugin.sandbox.network] sandbox.allow_network: plugin declares outbound network access — operators should review why
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
plugin "my-plugin" — 1 error(s), 1 warning(s)
✗ NOT compatible — fix the errors above
```

## CI integration

```bash
vayu-compat check --manifest plugin.json --host "$(cat .release-version)" --files || exit 1
```

The full contract is documented at
[/docs/compatibility/vcb](https://vayupress.com/docs/compatibility/vcb) on any
VayuPress site (and in `docs/compatibility/vcb.md` in this repository).
