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
  - **A hotfix is not "a plan", and several of them in a row are not a violation.**
    The rule counts *feature* releases inside one plan. Four hotfixes in an
    afternoon, each for something broken in the field, is the exception working —
    not the rule being ignored four times. Do not batch a live breakage to keep a
    release count down, and do not apologise for the count afterwards; the thing
    worth examining in that situation is why the breakage shipped, which is a
    testing question, not a release-cadence one.
  - When in doubt, do not bump. Ask.

### The worked example, because the abstract rule was not enough

VayuVeil (ADR-0150) was **one** plan: "make a proper plan for this ADR, build
step by step, do the hacker audit, then release." It shipped as **three**
releases in one afternoon — P0, then the enforcement and capture suite, then the
second control and the boot log. That is the failure mode this section names, at
exactly the count it names.

The rationalisation was that each follow-up instruction ("do it", "I need
perfect VayuVeil") started a fresh plan. It did not. They were continuations of
the same request, and the paragraph above already says so: *a plan means the
whole thing the user asked for — every phase, every follow-up item, every task
on the list — not the increment that happens to be finished.*

The tell is simple and worth checking before every bump: **if the operator would
describe all of it as one piece of work, it is one release.** "Build VayuVeil"
is one piece of work whether it takes one turn or six. Cutting a version because
an increment feels finished, or because a tag makes the progress visible, is the
same mistake wearing a reason.

What should have happened: every commit lands on `main`, the notes accumulate
under `## [Unreleased]`, and ONE version is cut when the operator's request is
actually satisfied.

### Before every release cut: a hacker audit, then improve

**No version is bumped until an adversarial pass has run over everything going
into it. The audit gates the release; it does not trail it.** Not optional, not
"if there is time", and not a code review under a different name.

The distinction that makes it worth doing: a review asks whether the code does
what it says. An audit asks what you would do to it if you wanted it to fail.
Those find different things. A feature review of the multi-node work passed
cleanly; attacking the same code found an unmetered compute sink on the lane
reserved for the admin plane, a control that silently did not exist for IPv6, and
observe-only mode enforcing on other people's machines.

How to run it:

- **Attack everything accumulated under `## [Unreleased]`**, plus anything it
  touched. Start from "what would I do to this", never from the feature list.
- **Write the finding as a failing test first**, in the attacker's voice, with
  the consequence spelled out. A finding with no test is an opinion.
- **Mutation-test every fix.** Re-break the code and confirm the test fails. A
  test that passes against the broken version proves nothing, and this has
  happened: an assertion on the first response passed while the gate was
  refusing people, because the deny path is generous to a first request. The two
  only parted company under sustained load.
- **Check the claims, not just the code.** A panel row, a posture verdict or a
  copy line that overstates what is enforcing is a defect of the same kind — the
  report that told an operator their readers were broken, on the strength of the
  operator's own request, was found this way.

Findings are fixed and land under `## [Unreleased]` **before** the version bump,
so they ship in the release they were found in rather than a version later. That
is the whole reason the audit runs first: a release that is known to contain a
hole, shipped anyway because the fix "can go in the next one", is a decision
nobody would defend out loud.

Two consequences worth stating, because both have been got wrong:

- **A clean audit is a real result, not a skipped step.** Record that it ran and
  what was attacked. "Nothing found" written after genuinely trying is different
  from silence, and only one of them is evidence.
- **Fixing an audit finding does not restart the cycle.** Re-attack what the fix
  touched — mutation-testing it covers this — but a fix landing under
  `[Unreleased]` does not oblige a fresh full pass, or no release ever ships.

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
python3 scripts/spdx-headers.py --check  # every .go file carries Apache-2.0
```

Go 1.25+ is required (govulncheck / x/tools need it).

The SPDX check is in this list because it was missing from it: a new test file
was pushed with every other gate green and CI failed on the one line nobody had
run locally. `python3 scripts/spdx-headers.py` (no `--check`) writes the header
into any file that lacks it.

**These gates are necessary and not sufficient.** They prove the code compiles,
passes its own tests and carries no known vulnerable dependency. They cannot tell
you whether a control is reachable, whether a claim on the panel is true, or what
a determined person would do to the surface you just added. Before a **release**
specifically, the adversarial pass in §1 runs as well — green gates have never
been the bar for cutting a version.

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

## 11. VayuOS page design — the house style (standing rule)

**Every new VayuOS page follows the Monetization page's design sense unless the
user asks for something else.** This is the default, not a suggestion — do not
invent a new layout per page, and do not ask which style to use.

The reference is `cmd/vayupress/admin_os_monetization.go`. The shape:

1. `page-header` — `<h1>`, plus a `page-actions` div holding any docs link and a
   `role="status" aria-live="polite"` span for inline feedback.
2. `page-sub` (or a `text-sm muted` lede) — one sentence on what the page is for.
3. `stat-grid` of `stat-card` tiles — the four numbers that answer "what is the
   state of this?" at a glance. `stat-card--warn` tones a tile that wants
   attention.
4. `section-head` (`section-head__title` + `section-head__hint`) to open each
   band of content.
5. `mon-stack` of `monAcc(icon, title, subtitle, chip, open, body)` accordions.
   `monAcc` is pure CSS `<details>` — no JS, CSP-safe, keyboard-accessible. Give
   the first/most important one `open: true`; give every summary a `mon-chip`
   (`mon-chip--on` / `mon-chip--off`) so state reads while collapsed.
6. Card bodies are `<div class="card">` with `settings-block-title`,
   `text-sm muted` prose, `field` / `field-label` / `field-hint` inputs, and
   `btn btn--primary btn--sm` actions.
7. One inline `<script nonce="…">`, or a page-script const passed to
   `adminOSShellFoot`. Never an inline `style="…"` attribute — `assertCSPSafe`
   fails on it, along with the literals `cdn`, `googleapis`, `unpkg`, `jsdelivr`.

Render via `adminOSShellHead(nonce, title, navKey, cfg)` + body +
`adminOSShellFoot(nonce, pageScript, pageUsesAlpine(body))`, then `writeOSHTML`.

Worked examples to copy from: `/os/vayukeep`, `/os/buzz`, `/os/claudecode`.

Also, for any page that mints API keys: add its nav key to `adminAreas` in
`osPathMinLevel` (`handlers_auth.go`) or it silently inherits the permissive
author default, and pin that with a test.

## 12. Writing posts on the blog (standing rule)

Posts are published to the live site through the MCP server. Follow this
without being asked; it is not a per-post negotiation.

### Style

- **Length: always OVER 2000 words** unless the user says otherwise.
- **Unique content** — no duplicated blocks across posts; repetition is bad SEO.
- **SEO- and GEO-optimised by default**, every post.
- Tone: **human-written, professional, beautiful, informative** — never robotic
  or templated.
- Technical, in-depth feel: summary block, **real code blocks**, comparison
  tables, deep sections, **FAQ**, **conclusion**.
- Dark-native design: **SVG hero with no text badge**, **label-free summary
  box** (no "Executive summary" heading), feature cards, cross-links.
- **No competitor product names.** Say "a managed relational database", not the
  vendor; "an independent synthetic monitor", not the brand.
- **Ground every detail in the source** — ADRs, handlers, real pragmas. Never
  invent a number or a mechanism.
- Backlink to related posts **and** to the product site and GitHub repo.

### Posting

- **Publish directly.** No draft step, no asking permission, no pre-publish
  audit. Edit directly afterwards if something needs changing.
- Still confirm genuinely destructive or site-wide actions.

### Tooling — the two mistakes that have actually happened

- **`content` takes RAW HTML.** Passing entities (`&lt;div&gt;`) stores them as
  literal text and the post renders as visible source code. This shipped once.
- **Always re-fetch with `get_post` after writing** to confirm it stored as
  markup. The `queued` response tells you nothing about correctness.

### Numbers in a post

- **Read them from an API or tool, never off a screenshot.** A dashboard header
  read "1,609,007 views" next to a 90-day card reading "173,340"; the API
  confirmed 173,342 for the period. Quoting the header would have overstated it
  ninefold.
- **Publish ranges, not headline numbers**, when a metric varies between runs
  (e.g. mobile Performance 99–100). A reader who tests and sees something lower
  discounts the whole page.
- **Watch for metrics that are artifacts of your own activity.** The write-queue
  p99 sits at 0 on a read-only blog and only rises while something is
  publishing — including the agent writing the post. Publishing that as a
  steady-state figure would be wrong.
- State the limits of any benchmark explicitly. A result that only flatters
  itself gets discounted; naming what it does *not* prove is what makes the rest
  credible.

## 13. Fixes must be reachable from VayuOS — standing rule

**Never hand the operator a terminal command as the solution.** The whole
premise of VayuOS is that an install is operated from the panel; an answer that
starts with `ssh` or `sudo` is a product failure being narrated rather than
fixed.

This was said after several replies in a row ended in a shell command, and it is
correct. The rule:

- The fix goes in the **binary** wherever it possibly can, because the binary is
  what the in-app updater delivers. A root-side shell fix reaches only operators
  who re-run an installer over SSH — which is exactly the thing being ruled out.
  The API_KEY provisioning failure is the worked example: the shell fix was real
  and reached nobody, and moving it into `config.LoadLocalCLI()` made the same
  repair arrive through **Update & Backup**.
- Where a step genuinely needs root, the panel **requests** it (the
  provision.request flag → root-side watcher) and **reports what happened**.
  It never instructs.
- Where something truly cannot be done from the panel — installing a systemd
  unit on a first deploy — the panel shows the exact command, copyable, with the
  reason. That is the ceiling, and it is stated on the page rather than in a
  chat reply.
- Diagnostics belong on the page too. "Run `nginx -t` and paste it to me" is the
  same failure in diagnostic clothing: the console should already be showing it.

## 14. How this repo is worked — the operator's standing instructions

Everything below was said out loud during a working session and then repeated,
which is the signal that it belongs here rather than in a chat log. None of it is
a per-task negotiation.

### The terminal is an instrument, never the answer

§13 rules out a shell command **as the solution**. It does not rule out the shell
as a way of *looking*. The operator has said plainly: hand them a command, they
will run it and paste the result back. That is the fastest way to inspect a live
box from here, and it is welcome.

The line between the two, because they get confused:

- **Diagnosis** — "run this, paste the output" — fine, and often the only way to
  see the machine. Ask for exactly what is needed and say what it will show.
- **The fix** — lands in the binary, or the panel requests it, or (the absolute
  ceiling) the page shows a copyable command with its reason. Never a chat
  instruction.

A reply that ends in `sudo …` and stops has not fixed anything. It has described
a repair the operator now performs by hand, which is precisely what the in-app
updater exists to remove. §13's "the console should already be showing it" is
about the *product's* diagnostics — it does not forbid asking for a command
while building.

### Verify against the live install; never infer its state

The install at **johal.in** is reachable through its MCP connector and is the
source of truth for anything about that install. Read the site config, the
manifest, the posture — do not reason about what the state "must" be.

This is not a stylistic preference. An investigation into why a published clone
did not match its source ran for hours on a theory about styling, while the panel
had been reading `4 file(s), deployed 06:06` the entire time: every upload had
been refused for an unrelated reason, and one `get_site` call would have said so
on the first attempt. When a tool can answer the question, call the tool.

### English, professional register

Replies to the operator are in English, in the register of a colleague reporting
on work.

### What an update notice may contain

The in-app update notification says **what the next version changes**. It carries
no infrastructure detail — no real IP addresses, no host paths, no internal
endpoints. A release notice should teach a reader what is in the release and
nothing whatsoever about the machine serving it.

### An audit gates every release, without being asked

The pre-release adversarial pass in §1 is not prompted per release. Do not ask
whether to run it, do not report a version as ready before it has run, and record
that it ran even when it finds nothing.

## 15. Settled product decisions — do not re-open

- **Publishing an uploaded site is admin-only.** Customers do not upload their
  own bundles; the operator publishes on their behalf. This was asked and
  answered — no self-service upload surface is wanted, so none should be built or
  proposed.
- **`vayupress.com` stays on GitHub Pages for the moment.** DNS therefore answers
  the ACME challenge elsewhere and this install cannot issue a certificate for
  that host. The standing `certificate: failed` line for it is expected, not a
  fault to chase. The operator repoints it once the hosting side is proven; until
  then the work is functionality.

## 16. Habits this repo has already paid for

Each of these was learned by getting it wrong here, and each cost real time.

- **Read the state before theorising about it.** The panel, the manifest and the
  connector all answer faster than a hypothesis does.
- **An assertion that cannot say WHICH element it matched is not an assertion.**
  Searching a whole page for a class passes on any page that uses it elsewhere;
  counting a substring double-counts when a modifier shares its prefix; an
  extractor keyed on an opening prefix will happily return the inner element.
  Extract the one element, then assert on it.
- **A build failure is not a killed mutation, and a no-op edit is not a
  mutation.** Both were scored as kills and both had to be re-run. A mutation
  must compile and must change behaviour, or it has proved nothing.
- **A gate can match itself.** A check whose own source contains the pattern it
  forbids reports a violation in the sentence describing the violation. Split the
  literal (`scrip[t]`) so the rule cannot fire on its own text.
- **One structural edit per build.** Converting two sections of markup in a
  single pass left an unclosed literal and had to be reverted whole. Half a
  conversion is worse than none.
- **Do not assert on a handler's source text.** A test that reads source for a
  phrase fails an honest refactor and passes a regression that deletes the line
  from the output and leaves it in a comment. Render the page and read that.
- **Check the exit code you think you are checking.** A command chained into
  `tail` reports `tail`'s status; a commit was made on a red suite that way.
- **Labels are HTML-escaped on the way out.** Asserting on `A & B` finds nothing
  when the page renders `A &amp; B`.
- **Release assets are listed alphabetically, not meaningfully.** Choosing "the
  first asset" shipped an archive as though it were the binary and took a live
  install to a 502. Match by name, then verify the bytes are an executable image.
- **One plan is one release, and "it feels finished" is not the test.** VayuVeil
  went out as three versions in an afternoon because each increment looked
  complete on its own. The rule was written down, in detail, and was still broken
  three times in a row — so the check is now mechanical: before bumping, ask
  whether the operator would call the whole thing one piece of work. If yes, it
  waits.
- **A small change is not a reason to run a subset of the gates.** A hotfix
  touched `CHANGELOG.md`, so build + gofmt + SPDX + the one changed package felt
  like enough. The attribution gate lives in `cmd/vayupress` and was never run;
  it catches the exact leak that shipped, naming the file and line. §4 is the
  list for every push, and a release commit is the last place to shorten it.
- **One rejected file rejects the whole upload.** A bundle containing a single
  disallowed extension was refused in full, silently, repeatedly. Skip known
  junk, report what was skipped, and make a refusal impossible to miss.

## 17. Code quality — the standing bar

Said by the operator and therefore not a per-task negotiation: **write only the
code that is needed, and make it enterprise-grade.** Less code, higher quality.
No filler, no scaffolding "for later", no helper that exists because it felt
tidy. Every line shipped is a line someone maintains and an attacker can reach.

This is a bar, not a leash. It does not mean being timid or doing less than the
task requires — if the right answer is a substantial change, make it. What is
ruled out is *volume without purpose*.

### Debugging must not break what already works

The first duty of any fix is to leave every working path working. Before a
change lands, know what legitimate use it could take away, and say so.

- **The narrowest rule that closes the hole wins.** The author-ownership guard
  refuses another domain's post and another author's post, and deliberately
  still allows unattributed primary-site content — because imported archives
  carry no `author_id`, and the "obvious" stricter rule would have made an
  author's own back catalogue uneditable.
- **Delete a fix that costs someone their access.** `VerifyApprovedDevice` had
  a loop cap written, reviewed and working, to bound an Argon2id amplifier. It
  was removed before commit: the credential query has no `ORDER BY`, so the cap
  would have checked the OLDEST devices and refused a person holding one
  enrolled later — a lockout on the one path that exists to undo a lockout. The
  bound went where it costs nobody anything (a per-address budget and the
  process-wide ceiling). Writing the code was not the mistake; keeping it would
  have been.
- **A control that rations real people is its own outage.** The webmail lockout
  was namespaced and the magic-link per-source budget loosened because behind a
  CDN every reader shares one address. Ask who else is on the other side of the
  check.

### Verify under the real conditions, not convenient ones

A check that does not match reality proves nothing about reality, in **either**
direction. Both have already cost a red CI here:

- **Stricter than the product** — a test harness refused an empty address while
  the shipped predicate fails open on it, so a missing control was hidden and
  the mutation survived.
- **More permissive than the runner** — the suite was run as root, where a test
  could bind port 110; CI is unprivileged and could not. The test passed locally
  for a reason unrelated to the code.

### What "enterprise-grade" means concretely here

- **Reuse the mechanism that already exists.** The From-header binding shares
  `headerFromAddress` with the inbound path rather than parsing again, and
  `WithSenderCheck` wires one predicate to both fields — so the envelope and the
  header cannot drift apart. A second copy of a rule is a future divergence.
- **Fix at the root, not at the call site.** POP3's missing test port was fixed
  in the shared config, because the next POP3 test would have hit it too.
- **A comment earns its place by saying WHY**, especially why an obvious
  alternative was rejected. Restating what the next line does is noise.
- **No dead code, no speculative interfaces, no unused exports.** The deadcode
  gate is in §4 for a reason.
