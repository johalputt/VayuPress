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
