# CLAUDE.md — standing instructions for every session in this repo

This file is auto-loaded at the start of every Claude Code session in this
repository. Follow it without being asked. It complements `AGENTS.md` (read
that too — it holds the release-versioning rule).

## 1. Releases

- **Micro by default; roll over at 99.** See `AGENTS.md`. Normally bump the third
  (micro) segment: `v3.13.96 → v3.13.97`. Each segment counts **0–99** and never
  exceeds 99: when micro is at **99**, the next release bumps the **minor** and
  resets micro to 0 (`v3.13.99 → v3.14.0`); when minor is at **99** and micro rolls
  over, the next release bumps the **major** and resets minor+micro to 0
  (`v3.99.99 → v4.0.0`). Never bump minor/major early — only the 99 rollover does.
- A release bumps **all three** in one commit, then push to `main`:
  1. `.release-version` — e.g. `v3.13.95` (**no trailing newline**; match the
     existing byte format). Changing this file triggers `tag-release.yml`
     (build → cosign → publish the signed `vayupress` binary + release).
  2. `cmd/vayupress/main.go` → `var Version = "3.13.95"` (no `v` prefix).
  3. A matching `## [3.13.95] — <date>` section in `CHANGELOG.md`.
- Ship as two commits when it reads cleaner (fix commit + release commit), but
  keep the three version files consistent within the release commit.
- **Release only after the WHOLE plan is complete — never per step.** When
  working through a multi-step plan (e.g. a security-audit remediation track),
  do NOT bump the release after each individual fix. Keep every change
  accumulating under the `## [Unreleased]` heading in `CHANGELOG.md`, and leave
  `.release-version` / `main.go` `Version` at the last released value. Only when
  the entire plan is done do you rename `[Unreleased]` → `[X.Y.Z]`, bump all
  three version files in one release commit, and push. (Each individual fix still
  lands on `main` as its own commit — only the *version bump / tag* waits for the
  whole plan.)
- **This is the rule most often broken, so it is spelled out.** A plan means the
  whole thing the user asked for — every phase, every follow-up item, every task
  on the list — not the increment that happens to be finished. While a plan is in
  flight: commit and push each piece to `main`, accumulate the notes under
  `## [Unreleased]`, and cut **exactly one** release at the end.
  - Multiple releases inside one plan is the failure mode. Three micro releases
    in an afternoon means the rule was ignored three times.
  - The only exception is a **fix to something already released and broken for
    users right now** — that ships immediately on its own, because leaving a live
    install broken to respect a batching rule is the wrong trade. Say so
    explicitly in the release notes when this exception is used.
  - When in doubt, do not bump. Ask.

## 2. Branch & push model

- **Push directly to `origin/main`.** The whole pipeline is release-on-`main`
  (tag-release for the binary, `deploy-site` for the website). Do not open PRs
  or use feature branches unless the user explicitly asks.
- `git push` uses `-u origin HEAD:main`. On network failure, retry up to 4×
  with exponential backoff (2s, 4s, 8s, 16s). Never force-push `main`.

## 3. Commit / PR attribution — hard rule

- Commit author is **`johalputt <ankushchoudharyjohal@gmail.com>`**.
- **Never** put "Claude", the model name, or any model identifier in commit
  messages, PR titles/bodies, code comments, or any other artifact pushed to a
  repo. Keep AI attribution out of the git history entirely. (Chat replies are
  fine; pushed artifacts are not.)

## 4. Gates to run before every push

Run these locally and make them pass before pushing (they mirror CI in
`.github/workflows/ci.yml`):

```
gofmt -l <changed .go files>          # must be empty
go build ./...
go vet ./...
go test ./...                          # at least the packages you touched
staticcheck ./...                      # honnef.co/go/tools/cmd/staticcheck@latest
golangci-lint run ./...                # v2: github.com/golangci/golangci-lint/v2
gosec -severity high -confidence high ./...
govulncheck ./...
bash scripts/deadcode-gate.sh          # no NEW unreachable code
```

Go 1.25+ is required (govulncheck / x/tools need it).

## 5. Marketing website (`docs/site/`)

- Source: `docs/site/index.html` + `docs/site/assets/app.js` (Alpine.js data
  root `app()`), served at **vayupress.com** via GitHub Pages.
- Pushing anything under `docs/**` (or `static/css/docs.css`) to `main`
  triggers `deploy-site.yml`, which publishes to vayupress.com. Custom domain
  is carried via `docs/site/CNAME`.
- The page pulls **Tailwind, Alpine and Google Fonts from CDNs** at runtime, so
  it cannot be faithfully screenshotted in a sandbox without network — verify
  edits structurally instead (tag balance, `node -e "new Function(fs.read…)"`
  to parse `app.js`, no dead `x-`/data references).
- **CSP-safe prose:** the `TestConnectorCardsCSPSafe` gate (and `assertCSPSafe`)
  forbid the literal substrings `cdn`, `googleapis`, `unpkg`, `jsdelivr` in
  connector-related admin copy. Say "proxy"/"origin", never "CDN", in that copy.
- After a site push, remind the user to hard-refresh (Cmd/Ctrl-Shift-R); GitHub
  Pages + cache take a few minutes to propagate.

## 6. Product facts to keep accurate everywhere (site, docs, copy)

- **Ten products, one binary:** VayuOS, VayuMail, VayuTalk, VayuShield,
  VayuAnalytics, Website & Blog, VayuPGP, VayuAPI + VCB, **VayuMCP**,
  VayuTor. Headline: "One binary. Ten products."
- **The VayuMail mobile app onboards by direct-connect** — the user enters
  their domain and signs in once; the app provisions a per-device app password
  and configures IMAP/SMTP automatically. **QR scanning was removed** — do NOT
  mention QR codes / "scan to connect" anywhere in the app, site, or docs.
- **VayuMCP** (ADR-0139): a built-in MCP server (`/mcp`) + OAuth 2.1
  authorization server gives one-click Connect from claude.ai for **Claude and
  Claude Code** — and any MCP client (claude.ai is just one). The user-facing
  name is **VayuMCP** everywhere (VayuOS page, site, docs); do not call it the
  "Claude Connector". Optional dedicated `mcp.<domain>` host
  with its own auto TLS cert (`scripts/setup-mcp-subdomain.sh`); needed when a
  CDN in front of the apex can't skip its bot challenge per-path. The `form-action`
  CSP on the consent page must include the client's validated redirect origin,
  or the post-approval redirect is blocked.
- **VayuTor** (ADR-0138): one-click Tor v3 onion for every hosted domain,
  clearnet + onion together, count-only stats.

## 7. Security & operational constraints

- **Never commit secrets** (API keys, tokens, passwords) to the repo or to any
  pushed artifact — not even in examples.
- If a live API key is used during a task, tell the user to **rotate it**
  afterward; do not persist it anywhere.
- **Do not disable TLS verification** or bypass the agent proxy. On TLS/proxy
  errors, consult `/root/.ccr/README.md` rather than working around it.
- Be frugal with GitHub comments — only reply when genuinely necessary.

## 8. Current initiative — VayuOS Spaces (Clearnet / Tor), ADR-0141

- **Goal:** two separate, independently switchable worlds. Whole-install switch
  `VAYUOS_MODE=clearnet|tor` (default clearnet; `tor`/`onion`/`anonymous` enable
  Tor mode via `config.Cfg.OnionMode`). Run **two installs** (own DBs) for both.
- **Tor world is web-only** (Tor Browser): VayuMail·Tor is webmail only and
  VayuTalk·Tor is a web client only — no mobile-over-Tor.
- **Anti-leak in Tor mode:** no clearnet callbacks. The central kill-switch is
  `safefetch.SetBlockClearnetEgress(OnionMode)` — it closes EVERY `safefetch`
  call site (IndexNow, webhooks, remote images, embed unfurl, WKD). Paths that
  bypass safefetch are individually guarded too: AI-generate routes through
  `safeOutboundTransport()`, the external SMTP relay is refused via
  `safefetch.ClearnetBlocked()`, plugin-registry downloads and the Stripe/PayPal
  nil-client fallback use the guarded transport. Webmention is inbound-only and
  gravatar is serve-only (no outbound), so neither leaks. Also: block external
  hotlinked images, keep `img-src 'self' data:` (never widen to `http:`), serve
  http onion (no CA-TLS, no HSTS; auth/CSRF cookies ARE `Secure` — Tor Browser
  treats a v3 `.onion` as a potentially-trustworthy origin, so it stores/sends
  them over the http onion; `auth.CSRFCookieSecure` is now unconditionally true),
  bind loopback only
  (`onionSafeBindAddr`). `seo.Origin(host)` is the scheme source of truth —
  `.onion` gets `http://`, clearnet stays `https://`.
- **Verifiable posture:** `internal/anonaudit` computes an honest anonymity
  report shown on `/os/spaces` and logged at boot — NEVER claims "100%
  anonymous". The one-click in-app Tor Space (the child supervisor, ADR-0141)
  spawns with `VAYUOS_MODE=tor`, so it inherits every anti-leak guard; its child
  env whitelists only `PATH` (no parent secret/DOMAIN bleed) and each keystore
  DEK falls back to its own host-bound keyfile (no `VAYU_SECRET` needed).
- **Sync/migrate is content-only:** `vayupress migrate export|import --file
  x.vaybundle` (checksummed, offline-movable; `--mode=merge|add-only`). Accounts,
  mailboxes, PGP keys and Talk IDs never cross (no `author_id`). A Live Mirror
  agent is opt-in with a correlation warning.
- **Phases:** P1 whole-install mode + onion-primary + stop onion→clearnet Host
  rewrite + request-aware cookies + anti-leak + deploy branch + top-bar mode
  indicator; P2 VayuMail·Tor; P3 rotatable VayuTalk. Reuse the VayuTor engine
  (ADR-0138). Onion-only reverses ADR-0138's Tor-only refusal (guard-railed).

## 9. Recently shipped (v3.14.x) — don't re-derive

- v3.14.0 AI-generate hardening (per-user rate limit + concurrency cap, generic
  provider errors, per-credential custom-gateway routing).
- v3.14.1 editor AI **model picker** (live `/models` + curated fallback), **safe
  SVG** in Diagram/HTML blocks (sanitised inline, CSP backstop), external
  Pixabay/Unsplash images render (`referrerpolicy=no-referrer` on hero/cards/
  trending) + page→`og:image` resolve at save + auto-hero from first body image.
- v3.14.2 `seo.Origin` scheme backbone. v3.14.3 Content Bundle export/import.
- **api.<domain>** hardened REST host exists (`scripts/setup-api-subdomain.sh`):
  CDN-proxy-off, exposes only `/api` + `/health`.

## 10. CI gotchas (mirror these locally before pushing)

- **markdownlint** lints every `**/*.md`. MD004: a wrapped line starting with
  `+`/`*` is read as a list bullet — reword (use `plus`). Run
  `markdownlint-cli2 <file>` on any changed `.md` before pushing.
- **gosec** CI **excludes** the taint series `G702,G703,G704,G709,G710`
  (see `ci.yml`); local `gosec` over-flags operator CLI file paths — run with the
  same `-exclude=…` to match. Operator CLI paths use a `//nosec G703` rationale.
- **golangci-lint v2** must be 0 issues (it's on PATH in this env). `rowserrcheck`
  wants `rows.Err()` checked — use `_ = rows.Err()` for best-effort loaders.
- The **P16 go-native** job runs golangci-lint first (fail-fast), then gosec /
  race build+test / govulncheck / deadcode. `govulncheck` can't fetch its DB in
  this sandbox (proxy) but passes on GitHub — don't block on the local failure.
