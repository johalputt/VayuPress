# Admin UI — the VayuOS console

The operator console lives at **`/os`**. It is server-rendered Go HTML with
HTMX fragment swaps and a small set of same-origin scripts; there is no client
framework and no build step.

The earlier consoles are gone. `/admin`, `/admin/v2[/…]` and `/admin/v3[/…]`
answer with a permanent redirect to their `/os` equivalent
(`cmd/vayupress/admin_legacy.go`). The JSON API (`/api/v1/*`) and the operator
sub-pages such as `/admin/modes` and `/admin/faults` have their own lifecycle and
are not redirected.

---

## Design constraints

Every console page is served under the strict policy built by
`render.BuildCSP` (`internal/render/csp.go`):

```text
default-src 'self'; font-src 'self'; style-src 'self'; style-src-attr 'unsafe-inline';
script-src 'self' 'nonce-<nonce>' <theme-script hash>; img-src 'self' data: https:;
connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'
```

What that means in practice:

- **No CDN, no `eval`, no `new Function`.** HTMX 2 is vendored as
  `static/js/htmx.min.js`; everything else is first-party.
- **Every inline `<script>` carries the per-request nonce** (`render.CSPNonce`).
  Event handlers are attached from script, never with `onclick=`/`onerror=`
  attributes.
- **Stylesheets are `'self'` only.** Inline `style="…"` attributes are allowed
  (`style-src-attr`); `<style>` elements are not.
- **Fonts are self-hosted** woff2 under `/static/fonts/`: Space Grotesk
  (headings), Inter (body), JetBrains Mono (code).

A page that breaks one of these does not degrade gracefully — the browser
blocks it. Test in a real browser against a built binary, not only with Go
tests.

---

## Assets

Console assets are compiled into the binary. At boot they are written to
`STATIC_DIR`, and `serveAdminOSAsset` serves the disk copy, falling back to the
embedded one when the directory is missing or read-only. URLs carry
`?v=<assetVer>` so a release never serves a stale script.

| File | Role |
|------|------|
| `static/css/admin-os.css` | The console stylesheet: tokens, shell, components |
| `static/js/admin-os.js` | Shell bootstrap: theme, toasts, command palette, list keyboard layer, shared helpers |
| `static/js/admin-os-<app>.js` | One script per app (mail, talk, members, editor, website, …), loaded only by that app's page |
| `static/js/htmx.min.js` | Vendored HTMX |

`admin-os.js` is one closure of init blocks. Anything that uses its helpers
(`$`, `$$`, `on`) must sit inside it; the list keyboard layer once sat after the
closing line and threw on every keypress. `tests/e2e/console.spec.js` runs the
shell in Chromium and fails on any page error.

---

## Shared client helpers

Use these rather than writing another copy:

| Helper | Where | Use |
|--------|-------|-----|
| `vpToast(msg, kind)` | `admin-os.js` | Non-blocking feedback. Kinds: `ok`, `error`, `warn`, `info` (`success`/`danger`/`warning` are mapped) |
| `vpConfirm(opts, onYes)` | inline shell script, `admin_os_ui.go` | The console's confirmation modal |
| `vpPrompt(opts, onDone)` | inline shell script, `admin_os_ui.go` | The console's single-field input modal |
| `vpPost(url, body, onok, onerr)` | `admin-os.js` | CSRF-protected JSON POST that reports every failure |
| `vpCsrf()` | `admin-os.js` | The double-submit token for hand-rolled requests (`X-CSRF-Token`) |
| `vpActions` | `admin-os.js` | Registry of command-palette actions |

Native `alert`/`confirm`/`prompt` are forbidden in console scripts;
`TestConsoleScriptsUseTheConsolesOwnDialogs` scans every file. HTMX's own
`hx-confirm` is routed through `vpConfirm` by the shell.

---

## Design tokens

Tokens are CSS custom properties on `.vp-os`, redefined for the light theme and
for `prefers-color-scheme` when the theme is `auto`. The authoritative values
are in `static/css/admin-os.css`; the ones most code needs:

| Token | Role |
|-------|------|
| `--bg-base`, `--bg-surface`, `--bg-surface-2`, `--bg-surface-3` | Page and layered surfaces |
| `--text`, `--text-2`, `--text-3` | Primary, secondary and helper copy |
| `--brand`, `--on-brand` | Primary actions and the text on them |
| `--accent` | Secondary emphasis |
| `--ok`, `--warn`, `--danger`, `--info` (each with `-dim`) | Status colours and their tinted backgrounds |
| `--border`, `--border-strong` | Hairlines |

Contrast is enforced, not hoped for: `admin_os_contrast_gate_test.go` fails when
a reading pairing drops below its WCAG bar in either theme. The console also
carries its own `prefers-contrast` and `forced-colors` rules.

**Theme.** `light` / `dark` / `auto`, toggled from the top bar and saved as the
`admin.theme` setting. The console mirrors the choice into
`localStorage['vp-os-theme']`, which the sign-in pages read (`os-theme.js`)
because nobody is signed in there to read the setting.

---

## Media storage

Uploads are written to `MEDIA_DIR` (default `/var/lib/vayupress/media`) and
served same-origin from `/media/{file}`.
