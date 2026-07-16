# Vayu Compatibility Bible (VCB) — VayuPress

**Status:** Authoritative  
**Last reviewed:** 2026-07-16

---

## What this is

The Vayu Compatibility Bible is the complete, machine-checkable contract for
building **plugins**, **themes**, and **tools** that work with VayuPress —
first time, every time, whether the author is a developer or an AI agent. It
exists because "compatibility" must not depend on tribal knowledge: every rule
in this document is enforced by code that ships inside VayuPress
(`internal/vcb`), and the `vayu-compat` CLI applies exactly the same code, so
a package that validates cleanly cannot drift from what the host enforces.

Three principles govern everything below:

1. **Declare everything, receive nothing implicitly.** An extension states its
   identity, host range, hooks, API permissions, and sandbox capabilities in a
   manifest. Anything not declared is denied.
2. **Fail closed, fail early.** Unknown manifest fields are parse errors, not
   silent no-ops. Unknown hooks, capabilities, option keys, and categories are
   validation errors — before install, not surprises after.
3. **One source of truth.** Vocabularies (hooks, capabilities, theme options,
   categories) are exported from the code that enforces them and re-checked in
   CI. This document describes them; it never overrides them.

Related: [/docs/compatibility/vayuapi](/docs/compatibility/vayuapi) (API keys,
permissions, rate limits) · [/docs/compatibility/stability-matrix](/docs/compatibility/stability-matrix)
· [/docs/compatibility/api-contracts](/docs/compatibility/api-contracts) ·
[/docs/adr/ADR-0135-vayu-compatibility-bible](/docs/adr/ADR-0135-vayu-compatibility-bible)

---

## Extension kinds

| Kind | Ships as | Runtime contract | Manifest |
|------|----------|------------------|----------|
| **Plugin** | a sandboxed executable + `plugin.json` | JSON-per-line IPC over stdin/stdout; capability-scoped subprocess | `plugin.json` (this doc, §Plugin manifest) |
| **Theme** | design tokens + `theme.json` | compiled to CSS variables by the host theme compiler | `theme.json` (this doc, §Theme manifest) |
| **Tool** | a standalone binary (see `tools/*`) | reads/writes the VayuPress SQLite database and file formats, or calls the HTTP API with a scoped key | none required; API tools declare permissions like plugins |

---

## Plugin manifest (`plugin.json`)

A plugin package carries a `plugin.json` at its root. Unknown fields are
**rejected** (a typo can never silently drop a declaration). Maximum size
256 KiB.

```json
{
  "vcb": 1,
  "name": "seo-stamp",
  "version": "1.2.0",
  "description": "Stamps SEO metadata after publish.",
  "author": "Example Dev",
  "license": "MIT",
  "min_host": "3.13.40",
  "max_host": "",
  "hooks": ["article.create", "article.update"],
  "api_permissions": ["posts:read", "posts:write"],
  "executable": "bin/seo-stamp",
  "executable_sha256": "<64-hex sha-256 of the binary>",
  "sandbox": {
    "allow_network": false,
    "allowed_read_paths": ["/data/public"],
    "allowed_write_paths": [],
    "timeout_ms": 2000,
    "max_restarts": 3,
    "max_message_bytes": 1048576,
    "memory_max_bytes": 134217728,
    "cpu_quota_percent": 50,
    "max_pids": 16,
    "run_as": "1000:1000"
  },
  "distribution": {
    "download_url": "https://example.com/seo-stamp-1.2.0.bin",
    "sha256": "<64-hex sha-256 of the artifact>"
  }
}
```

Field rules (each is checked by `vayu-compat` and the host):

| Field | Rule |
|-------|------|
| `vcb` | Must equal the host's manifest schema version (currently **1**). A higher version is refused outright — never partially interpreted. |
| `name` | Required. ≤ 100 chars, no path separators. The registry keys packages by `name@version`. |
| `version` | Required. Dotted numeric (`1.2.0`). |
| `min_host` / `max_host` | Optional VayuPress version bounds. The validator refuses a package whose range excludes the running host. |
| `hooks` | Every entry must be in the hook catalogue below. A name outside it would never fire. |
| `api_permissions` | Every entry must be a valid, **non-wildcard** `section:action` (see [/docs/compatibility/vayuapi](/docs/compatibility/vayuapi)). `*:*` and `section:*` are refused — least privilege is the contract. |
| `executable` | Required. Relative path inside the package; absolute paths and `..` traversal are refused. |
| `executable_sha256` | Optional but strongly recommended. 64-hex SHA-256; the sandbox verifies the binary before every launch and refuses a mismatch. |
| `sandbox.*` | Deny-by-default capability requests, mapped 1:1 onto the runtime sandbox: read/write paths must be absolute prefixes; `run_as` must be numeric `uid:gid`; limits must be in sane bounds. Network access and write paths are surfaced to the operator as warnings. |
| `distribution` | Optional. `download_url` must be HTTPS; `sha256` is verified on install. |

Sandbox defaults when omitted: no filesystem access, no network, 2 s hook
timeout, 3 restarts before quarantine, 1 MiB IPC message cap, own PID and IPC
namespaces, parent environment never inherited.

---

## Hook catalogue

The host fires exactly these hooks through the plugin manager. This is a
**closed, enumerated set** (`internal/vcb/hooks.go`); the validator refuses a
manifest subscribing to anything else, because an uncatalogued name silently
never fires.

| Hook | Fired when | Payload keys |
|------|-----------|--------------|
| `article.create` | An article was created | `slug`, `id` |
| `article.update` | An article was updated | `slug` |
| `article.delete` | An article was deleted | `slug`, `id` |
| `cache.purge` | The page cache was purged from the admin | `purge_type`, `slug`, `purged_count` |

> **Historical note.** Older drafts of the plugin SPEC and examples referred to
> `articles.write` and `article.created.v1` as hook names. Neither has ever
> been fired by the host; both were corrected when VCB formalised the
> catalogue. If you built against those names, resubscribe to the table above.

Payload schemas are **additive**: the host may add keys; a plugin must ignore
unknown keys. Delivery is asynchronous and lossy under overload (a full queue
drops the event); a hook that fails five times in a row is auto-disabled.

The IPC wire contract (one JSON `Request` per line on stdin, one `Response`
per line on stdout, with `hook`, `payload`, `correlation_id`, `causation_id`,
`trace_id`, `capabilities` request keys) is a **Stable** contract, frozen by
golden test and documented in
[/docs/compatibility/api-contracts](/docs/compatibility/api-contracts).

Separate vocabularies that are **not** plugin hooks: outbound **webhook**
events (`article.created.v1`, `article.updated.v1`, `article.deleted.v1`,
`payment.order_created.v1`, `payment.completed.v1`, or `*`) delivered as
HMAC-signed HTTP POSTs, and the internal outbox event types. They share domain
facts with hooks but keep their own versioned spellings.

---

## Theme manifest (`theme.json`)

A theme package carries a `theme.json` wrapping the exact token struct the
host compiler consumes — what validates is literally what runs.

```json
{
  "vcb": 1,
  "tokens": {
    "Name": "Nightfall",
    "BgDark": "#0b1020",   "SurfaceDark": "#141a2e", "TextDark": "#e2e8f0",
    "MutedDark": "#8ba4c4", "AccentDark": "#0ea5e9",  "Accent2Dark": "#a78bfa",
    "HiDark": "#fbbf24",    "GreenDark": "#34d399",
    "BgLight": "#ffffff",   "SurfaceLight": "#f1f5f9", "TextLight": "#0f172a",
    "MutedLight": "#475569", "AccentLight": "#0284c7", "Accent2Light": "#7c3aed",
    "HiLight": "#b45309",
    "FontSans": "Inter, sans-serif",
    "FontMono": "JetBrains Mono, monospace",
    "FontSizeBase": "1rem", "LineHeight": "1.6",
    "MaxWidth": "72rem", "RadiusSm": "6px", "RadiusLg": "14px",
    "custom_css": ".hero { letter-spacing: 0.02em; }",
    "options": { "width": "wide", "corners": "soft" }
  },
  "meta": {
    "name": "Nightfall",
    "tagline": "A deep-blue night theme.",
    "description": "…",
    "tags": ["dark", "blue"],
    "category": "Dark"
  }
}
```

Theme rules — each one lifted from what the compiler actually does at apply
time:

| Rule | Why |
|------|-----|
| The 15 colour tokens must be empty or `#hex` (`#rgb` … `#rrggbbaa`). | A bad colour **aborts the whole compile** — the theme simply fails to apply. |
| `FontSizeBase`, `LineHeight`, `MaxWidth`, `RadiusSm`, `RadiusLg` must be a number with an optional unit (`rem em px ch % vw vh`). | Invalid dimensions are **silently dropped** by the compiler; the validator turns that silence into an error. |
| Font stacks must avoid `\ ; : ( ) { } < >` and non-ASCII. Quotes are allowed — the compiler strips them harmlessly (unquoted multi-word family names are valid CSS). | Other stripped characters change what renders. |
| Every `options` key must be in the option schema, every value in that option's choices. | Unknown keys/values are **silent no-ops** at apply time. Keys: `archetype, fontpair, scheme, width, corners, feedlayout, headeralign, headingcase, accentfill, herostyle, herobg, heroheight, navstyle, cardstyle, linkstyle, articlealign, articlemeta, relatedposts, trendingposts, authorbox, cardimage` (+ per-theme extras). Run `vayu-compat capabilities`/`hooks` or read the design studio for choice values. |
| `custom_css`: no `@import`, no external `url()` (http/https/`//`), no `<`, ≤ 64 KiB (warning at 16 KiB). | The site runs CSP `style-src 'self'` and promises **zero third-party fetches**; the stylesheet is inlined into every page load on low-resource hosts. |
| `meta.name` must equal `tokens.Name`; `meta.category` must be one of `Flagship, Dark, Light, Minimal, Reading, Editorial, Newsletter, Business, Community, Creative, Vibrant, Mono`. | Deploying a store entry applies the preset by that exact name; the store filters by the fixed category set. |
| The name must not collide with a built-in preset. | The catalogue is keyed by name. |

Every built-in preset passes these rules — enforced by test
(`TestBuiltinPresetsSatisfyThemeContract`), so the published contract and the
shipped themes can never disagree.

### Motion & elevation tokens (build on the shared system)

Every compiled theme (`/theme.css`) exposes a shared, sovereign motion and
elevation vocabulary (ADR-0136) that your `custom_css` can use directly — the
same premium primitives the VayuOS admin uses, with **zero build step and no
dependency**. Prefer these over hand-typed durations and shadows so your theme
feels consistent with the platform and honours reduced-motion automatically.

| Token (bare alias) | What it is | Example |
|--------------------|------------|---------|
| `--sh-sm` / `--sh` / `--sh-lg` | Scheme-adaptive elevation (subtler on dark, more present on light) | `box-shadow: var(--sh)` |
| `--ease` / `--ease-out` / `--ease-spring` | Easing curves | `transition-timing-function: var(--ease)` |
| `--dur-fast` / `--dur` / `--dur-slow` | Durations (90 / 160 / 280 ms) | `transition-duration: var(--dur)` |
| `--t` / `--t-fast` / `--t-slow` / `--t-spring` | Composed `duration + easing` (drop into a `transition`) | `transition: box-shadow var(--t)` |
| `--vp-sp-1 … --vp-sp-12` | Spacing scale (rem) | `padding: var(--vp-sp-4)` |
| `--vp-z-base … --vp-z-toast` | z-index scale | `z-index: var(--vp-z-modal)` |

(The `--vp-*`-prefixed canonical names are also available, e.g. `--vp-sh`,
`--vp-ease-spring`.) Example:

```css
/* in a theme's custom_css */
.post-card { transition: transform var(--t), box-shadow var(--t); }
.post-card:hover { transform: translateY(-2px); box-shadow: var(--sh); }
```

Motion is enhancement, never requirement: the public stylesheet already carries
a global `prefers-reduced-motion` guard, and native page **View Transitions**
(`@view-transition { navigation: auto }`) are enabled site-wide — same-origin
navigation crossfades with zero JavaScript and degrades cleanly where
unsupported. You do not need to add either; just build on the tokens.

**The validator catches token typos.** These `--vp-*` names are the sovereign
vocabulary the compiler owns — a theme never defines them. If your `custom_css`
references a `--vp-*` token the compiler does not emit **and gives no fallback**
(e.g. `var(--vp-shdw-lg)`), it would silently resolve to nothing; `vayu-compat`
flags it as a warning so you catch the misspelling before shipping. A reference
with a fallback (`var(--vp-x, 0)`) is always accepted. The flagship themes
themselves are built on this vocabulary, as reference examples.

---

## API permissions and key provisioning

Extensions that call the VayuPress HTTP API declare the capabilities they need
as `section:action` tokens (full vocabulary and semantics:
[/docs/compatibility/vayuapi](/docs/compatibility/vayuapi)).

The provisioning flow:

1. The manifest declares, e.g. `"api_permissions": ["posts:read", "posts:write"]`.
2. `vayu-compat` (and the host) refuse unknown tokens and **any wildcard** —
   an extension may never ask for `*:*`.
3. The operator mints a key in **VayuOS → API Keys**, ticking exactly the
   declared boxes (the 12 × 6 permission grid). The key can do nothing else.
4. At runtime, every call is checked against the route's required capability,
   metered against the key's per-minute budget, and appended to the
   tamper-evident audit log.

---

## Security rules (non-negotiable)

- **No external fetches, ever.** CSP is `style-src 'self'`; themes cannot
  `@import` or reference third-party hosts; plugin network access must be
  declared and is surfaced to the operator.
- **Integrity is verified, not trusted.** Plugin binaries are SHA-256-checked
  before every launch; distribution artifacts are hash-verified on install
  over HTTPS only.
- **Least privilege.** Deny-by-default sandbox (no FS, no network, own PID/IPC
  namespaces, no inherited environment); deny-by-default API keys; wildcards
  refused in manifests.
- **Zero telemetry.** An extension must not phone home. Analytics stay on the
  operator's box.

## Performance rules

- Themes: CustomCSS ≤ 64 KiB hard, ≤ 16 KiB advised — it ships inline on every
  page load.
- Plugins: declare `memory_max_bytes` (warning if you don't); default hook
  timeout is 2 s; the IPC message cap defaults to 1 MiB. VayuPress targets
  low-resource VPSes — design for them.
- API calls: default budget 600 requests/minute per key (per-key override at
  mint time). Handle `429` + `Retry-After`.

---

## Validating a package

```bash
cd tools/vayu-compat && go build -o vayu-compat ./cmd/vayu-compat
./vayu-compat check --manifest ./my-plugin/plugin.json --host 3.13.41 --files
./vayu-compat check --manifest ./my-theme/theme.json
```

Exit code 0 = compatible; 1 = at least one ERROR finding. Every finding
carries a stable machine code (`theme.color.invalid`,
`plugin.hook.unknown`, …) suitable for CI annotations. The same checks run in
this repository's CI gate (`scripts/preflight-ci.sh`, section 11), so the
contract cannot rot.

---

## Versioning and stability

- `"vcb": 1` is the manifest schema version. The host refuses higher versions
  outright (fail-closed); additive fields within a version keep old manifests
  valid.
- `min_host`/`max_host` express host compatibility; versions compare
  numerically part by part (`3.13.41`).
- The plugin IPC wire shape, the permissions JSON envelope, and the signed-
  article schema are **Stable** contracts under
  [/docs/compatibility/stability-matrix](/docs/compatibility/stability-matrix):
  changing one requires an RFC + a deliberate golden update.

## Known limitations (deliberately deferred)

Documented so nobody mistakes silence for a promise — see ADR-0135 for the
reasoning:

1. **The event namespaces are not unified.** Plugin hooks (`article.create`),
   webhook events (`article.created.v1`), outbox envelope types, SSE broadcast
   names, and the typed in-process buses are separate vocabularies describing
   overlapping facts. VCB catalogues each honestly rather than pretending one
   name space exists.
2. **Event payload schema validation is shallow.** The JSON-schema registry
   checks strings/integers/booleans and required-field presence only; arrays,
   objects and `date-time` formats are declared but not enforced (and it is
   not wired into the emit paths).
3. **In-process hooks have no capability model.** Only subprocess plugins
   carry sandbox capabilities; Go code registered in-process runs with full
   process privileges. Third-party code must ship as a sandboxed subprocess —
   the in-process registry is for first-party wiring.

---

## Development process

- Follow the [governance constitution](/docs/CI-GOVERNANCE): zero telemetry,
  minimal dependencies, sovereignty by default.
- Validate with `vayu-compat` locally and in your CI before publishing.
- Changing a **Stable** interface needs an RFC (see the stability matrix);
  adding a hook, option, or capability is additive and needs a catalogue entry
  plus documentation.
