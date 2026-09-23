# UX audit & upgrade plan — VayuMail and VayuTalk

**Date:** 2026-08-26 · **Scope:** every user-facing surface of VayuMail (`cmd/vayupress/vayuos_mail*.go`,
`vayuos.go` inbox/reader/search, accounts/contacts/DNS/devices/recovery handlers,
`static/js/admin-os-mail*.js`) and VayuTalk (`vayuos_talk.go`, `handlers_vayutalk.go`,
`tor_talk_*.go`, `internal/vayuos/vayutalk/`, `static/js/admin-os-talk.js`), plus the shared
Admin-OS shell they live in (`admin_os_ui.go`, `static/js/admin-os.js`, `static/css/admin-os.css`).
**Method:** adversarial source review walked as user journeys, same discipline as
`SECURITY-AUDIT-2026-07` but optimising for *"super cool and easy to use"* instead of attacker outcomes.

Security posture is **not** re-audited here: `docs/SECURITY-AUDIT-2026-07-VAYUMAIL-VAYUTALK.md`
records all prior findings as fixed with regression tests. This document is the usability twin:
what it costs a human to get something done, where the product silently lies or goes quiet, and
where the day-to-day surfaces fall short of the operations surfaces' own standard.

Finding IDs: **T-n** = VayuTalk, **M-n** = VayuMail, **S-n** = shared shell/platform.
Severity: **P0** breaks the product promise · **P1** major friction or trust damage ·
**P2** recurring annoyance · **P3** polish. Every finding cites file:line evidence.

---

## Verdict

The bones are better than the feel. Both apps run on a genuinely strong platform — token-based
design system with dark/light/Tor palettes, HTMX fragment backbone, strict CSP done right, a real
component library (toasts, modals, skeletons, empty states), and exemplary XSS hygiene
(textContent-only DOM building). The *operations* surfaces — DNS setup with live verification,
recovery console, device approval, TLS diagnostics — are best-in-class for a self-hosted panel:
plain language, honest failure copy, enumeration-safe flows.

The pain is concentrated and diagnosable. Five root causes produce nearly every finding below:

1. **Failure paths are unloved.** The happy path is polished (optimistic bubbles, undo-send);
   every error/reconnect/destructive path either goes silent, lies ("Copied" without copying),
   destroys user work (failed sends, draft race), or resurrects the dead (the P0).
2. **There is no shared interaction foundation.** Every app re-rolls `cookie()/csrf()/toast`;
   Members-era surfaces still use `window.confirm/prompt/alert` + full reloads while newer ones
   use modals + toasts — inside the *same product*.
3. **The render model fights the data model.** Full-folder filesystem scans on every poll/action
   make the mailbox get slower precisely as it gets more useful. Perf here *is* UX.
4. **Trust signals whisper.** For an E2E-encrypted product, a peer key change is silent, a
   failed decryption is indistinguishable from a friend going quiet, and Sent/Delivered/Read is
   never explained.
5. **Admin-console patterns where app patterns belong.** Nine flat tabs, tables everywhere, no
   pagination, no presence, no shortcuts worth the name — feature-rich, but it reads like a panel,
   not a mail/chat app.

The plan (Part 4) attacks these in order: correctness first, one feedback system second, then the
mail-app feel, then the delight bets.

---

## Part 1 — What is already excellent (do not regress)

Any upgrade must preserve these; several were explicit design wins found during the audit:

- **XSS/CSP posture**: all dynamic UI built via `createElement/textContent`; no innerHTML with
  untrusted data; nonce'd scripts; DOMPurify-guarded single sink (`admin-os-editor.js:1714`);
  OnionMode strips external origins at one chokepoint (`internal/render/csp.go:31-48`).
- **Honest, human copy on the hard paths**: onion-delivery failures name each reason
  (`onion_transport.go:286-324`); TLS mismatch predicts the exact Gmail-app symptom
  (`vayuos_mail.go:1370-1407`); recovery flows are enumeration-proof with constant responses
  (`handlers_mail_recovery.go:41-66`).
- **Recovery console**: codes shown once with Copy-all / Download / print, link consumed only on
  POST, assisted queue asks the operator to verify identity channel out loud
  (`handlers_mail_recovery_pages.go:96-179`, `admin-os-mail-recovery.js:92-251,350-355`).
- **DNS/domain setup**: 7 copy-paste values with per-value Copy, live verification for every
  domain, grey-cloud warning, deliverability self-check (`vayuos_mail_dns.go:63-172`).
- **Composer depth**: chips with inline validation, scoped private contact autocomplete,
  plain-text formatting toolbar with honest framing, preview, dropzone, per-identity signatures,
  PGP toggle, optional HTML alt part, word count, 8s undo-send with keepalive flush
  (`vayuos_mail.go:45-257`, `admin-os-mail.js:102-573`).
- **Quota truth**: warn/full bar plus hard over-quota send block with plain words
  (`vayuos.go:2024-2043`, `vayuos_mail.go:526-529`).
- **VayuTalk privacy model**: messages tab-memory-only; only contact names/keep-flags and
  fingerprints persist; notifications carry sender name never text
  (`admin-os-talk.js:14-21,109-140`).
- **Bounded relay state, ownership-checked acks, SSRF-guarded onion dialer**
  (`internal/vayuos/vayutalk/store.go:19-34`, `handlers_vayutalk.go:246-279`,
  `tor_talk_identity.go:68-76`).
- **Platform guards that keep quality high**: build-breaking CSS contract tests
  (`css_contract_test.go:21-98`), `assetVer()` content-hash cache busting, role-gated nav.

---

## Part 2 — Findings

### 2.1 VayuTalk (`/os/talk`)

| ID | Sev | Area | Finding | Evidence | Fix |
|----|-----|------|---------|----------|-----|
| T-01 | **P0** | Ephemeral trust | **Burned messages resurrect on reconnect.** Console never acks; the server keeps envelopes up to 24h; client deletes expired ids from its dedupe map, so the next SSE reconnect re-flushes "destroyed" messages with restarted countdowns | `admin-os-talk.js:485-494,577-586`; `vayuos_talk.go:401-416,449-451`; `store.go:42,179-189` | Session-scoped tombstones now (keep `{expired:true}` in `byId`); root fix = per-reader cursors (see Phase 3) |
| T-02 | P1 | Feedback honesty | Copy buttons show "Copied" even when `navigator.clipboard` is absent (http onions) or rejected — the primary "share my code" action can silently do nothing | `vayuos_talk.go:341` | Feature-check + `.catch` → "Copy failed"; selectable `<code>` fallback |
| T-03 | P1 | Error recovery | Failed sends destroy the typed text; only a tiny status label remains, no retry affordance | `admin-os-talk.js:646-668` | Don't clear input until 2xx, or attach ↻ Retry to the failed bubble |
| T-04 | P1 | Destructive action | Identity **Rotate** is irreversible, unconfirmed, and `location.reload()`s away all in-flight chats; federation toggle also reloads | `vayuos_talk.go:337-340`; `tor_talk_identity.go:135-146` | Confirm dialog stating consequences; patch DOM in place |
| T-05 | P1 | Accessibility | Conversation rows are `<li>` + mouse-click only — keyboard/screen-reader users cannot switch conversations at all | `admin-os-talk.js:197-217,228-230` | Rows as buttons/tabindex+Enter; `aria-current` |
| T-06 | P1 | Trust | **No key-change alert**: a previously verified fingerprint silently un-verifies with zero UI signal | `admin-os-talk.js:396-399,413-422` | Loud persistent banner + row badge on fingerprint mismatch |
| T-07 | P2 | States | Starting a chat with yourself or blank input is a silent no-op | `admin-os-talk.js:680-681` | Inline helper text |
| T-08 | P2 | Errors | Web send collapses every relay error to "Could not send message" while the app API distinguishes 413 too-large / 429 queue-full | `vayuos_talk.go:544-548` vs `handlers_vayutalk.go:229-236` | Mirror the `errors.Is` mapping |
| T-09 | P2 | States | Stream-cap (429) is a silent dead end; pill loops "Reconnecting…" forever | `vayuos_talk.go:381-384`; JS:600 | Count consecutive failures → explanatory terminal state |
| T-10 | P2 | Compose | Tor world still shows placeholder/type `name@domain` though recipients are 70-char anonymous codes; no paste aid | `vayuos_talk.go:311` | Per-world placeholder; wide input/paste button |
| T-11 | P2 | Friction | No recipient picker — users must know exact addresses; server already knows local mailboxes | `vayuos_talk.go:108-115,311` | `<datalist>` of local mailboxes + recent peers |
| T-12 | P2 | Consistency | Rename uses native `window.prompt` | `admin-os-talk.js:292-301` | Inline editable field / house modal |
| T-13 | P2 | Expectations | No presence or typing signals; Sent vs Delivered never explained; hub knows who's online and doesn't say | JS:667; `hub.go:183-187` | Online dot + delivery-state mini-legend |
| T-14 | P2 | Privacy quirk | Draft survives identity switch — stale text can be sent from another identity | JS:709 vs 234 | Clear/stash draft in `switchIdentity` |
| T-15 | P2 | Mobile | Phone layout is squeezed desktop: rail capped at 40vh above the thread; composer below fold | `admin-os.css:5648-5653,5368-5375` | List ⇄ thread dual-view <600px; sticky composer |
| T-16 | P2 | Accessibility | Nothing announced: no aria-live for status/new messages; verify toggle lacks `aria-expanded`; badges visual-only | JS:626-630,337-343,500-505 | Live region + aria-expanded |
| T-17 | P2 | Silent loss | Undecryptable envelopes vanish without trace — indistinguishable from silence | `vayuos_talk.go:439-441,644-653` | Anonymised "1 message couldn't be decrypted" chip |
| T-18 | P3 | Feedback | Preflight failures swallowed; verify panel shows "—" with no "couldn't check" state | JS:407-408 | Transient reachability-unknown state |
| T-19 | P3 | Polish | HH:MM-only timestamps; no day separators near the 24h horizon | JS:74-79 | Date when ≠ today |
| T-20 | P3 | Polish | Conversation search has no "no results" feedback | `vayuos_talk.go:345-347` | Empty-result line |
| T-21 | P3 | Trust | "⚠ unverified" badge never clears retroactively when the sender's key arrives moments later | JS:469-473; GO:659-664 | `peerkey` stream event → re-render badges |
| T-22 | P3 | Noise | Notification permission requested on bare page load AND every Start click | JS:683-684,733-734 | Gesture-only + enable-chip |
| T-23 | P3 | Design language | Emoji icons (📌 ✎ 🛡 🔥 ⚠ 🧅) mixed with crisp inline SVGs | JS:202,256,264,333,470 | Standardise on SVG set |
| T-24 | P3 | i18n | All strings hardcoded English across JS + Go templates | e.g. GO:226-249 | String tables when i18n lands |
| T-25 | P3 | Robustness | Live-mode envelope can be dropped on full subscriber buffer after "delivered" semantics | `hub.go:21-24,153-161` | Document; ideally per-subscriber acceptance |
| T-26 | P3 | Doc drift | ADR-0131 amendment says web drops the Live toggle (it ships); main body describes superseded ack behaviour | ADR-0131:151-154,203-205 | Update ADR |
| T-27 | P3 | Layout | Thread height hardcodes `calc(100vh - 13rem)`; chrome changes cause double scrollbars | `admin-os.css:5368-5375` | Flex-fill within layout container |

### 2.2 VayuMail

| ID | Sev | Area | Finding | Evidence | Fix |
|----|-----|------|---------|----------|-----|
| M-01 | **P1** | Perf/inbox | Folder view renders **every** message: `ListFolder` reads + MIME-parses each file for headers alone; re-run on every 90s poll, row action, folder switch; no pagination/virtualization/cap | `internal/vayuos/mail/folders.go:85-125`; `vayuos.go:2013,2048` | Cap newest-N + "Load older"; cache header summaries keyed by filename+mtime (structural: header index, Phase 3) |
| M-02 | **P1** | Perf/accounts | Accounts list sums `MailboxUsage` per mailbox per render (walks all folders × new/cur), and the whole list re-renders on every inline action | `vayuos_mail_accounts.go:52,413`; `maildir.go:139-159` | TTL-cached stats computed in one pass; lazy refresh |
| M-03 | **P1** | Data loss/privacy | Panel account-delete uses store-level `Delete` (DB rows only) so a reissued address can **inherit the previous holder's mail**, while its own confirm claims permanent removal; JSON endpoint deliberately uses `DeleteMailbox` — two divergent delete paths | `vayuos_mail_accounts.go:635-638` vs `vayuos_mail.go:1616-1635`; `accounts.go:458-476` | Route op=delete through `DeleteMailbox`; align copy |
| M-04 | **P1** | Compose/drafts | Drafts silently drop **CC, BCC and attachments**; reopening restores none | `vayuos_mail.go:585-594` vs `admin-os-mail.js:431-453`; reopen `vayuos_mail.go:271-286` | Extend draft schema (headers + staged attachments); until then, hint "attachments not saved in drafts" |
| M-05 | P2 | Contacts | Add/delete errors discarded (`_ =`); invalid address re-renders panel unchanged, typed input lost | `vayuos_mail_contacts.go:114-118,129` | Reuse the existing alert-banner pattern; preserve input |
| M-06 | P2 | Security UX | "Set password" via `window.prompt` — cleartext on screen, no masking/confirm/generator; beside a polished TOTP modal in the same file | `admin-os-mail.js:691-700` (modal scaffold 632-679) | Proper masked modal |
| M-07 | P2 | Bulk actions | Inbox bulk/row endpoint ignores every per-message error while the sibling endpoint reports partial failures | `vayuos.go:2281-2286` vs `vayuos_mail.go:706-756` | Toast "N done, M failed" |
| M-08 | P2 | Search | Filters apply **after** the 200-result cap (refining hides matches with no notice); body fallback scans raw bytes incl. base64 attachments | `folders.go:153-156,170-174`; `vayuos.go:2369-2392` | Push filters into scan loop; "showing first N" notice |
| M-09 | P2 | Loading | No indicator on inbox/search/accounts HTMX swaps — a heavy scan looks frozen; only DNS tab wires `htmx-indicator` | `vayuos.go:2322` vs `vayuos_mail_dns.go:161-162` | Skeleton/spinner pattern on all swap targets |
| M-10 | P2 | Reader | Rich HTML mail effectively never displayed: any text/plain alternative wins (nearly all real mail); sanitiser strips styles/images entirely | `vayuos.go:2809-2823`; policy `vayuos_mail.go:38-41` | Opt-in "Render HTML view": same sanitiser, remote images blocked-by-default with click-to-load |
| M-11 | P2 | Compose | "Save as draft" navigates to Drafts on a fixed 700ms timer regardless of save outcome — slow/failed save strands the user | `admin-os-mail.js:468-476` | Navigate in success callback |
| M-12 | P2 | Reply | Quote line has **no date** ("On a previous message, X wrote:") | `vayuos_mail.go:328-337` | Thread original Date through `quoteBody` |
| M-13 | P2 | Search speed | Every keystroke-pause triggers bounded linear scan over up to 5,000 messages incl. raw reads (ADR-0083 trade-off) | `vayuos.go:2322`; `folders.go:148-184` | Short term: min-2-chars + longer debounce; structural: header index |
| M-14 | P2 | Errors | Send/draft failures surface raw engine strings (SMTP transcripts, DNS jargon) | `vayuos_mail.go:572,620`; `vayuos.go:2050` | Map common failures to plain language |
| M-15 | P3 | Unread model | Badges count only Inbox+Junk — unread Archive mail never surfaces | `vayuos.go:1753-1774` | Include Archive + snooze-wake counts |
| M-16 | P3 | Threading | Conversation grouping is normalized-subject-only; unrelated same-subject mail merges; ignores Message-ID/References | `vayuos.go:2141-2163,2171-2186` | References/In-Reply-To primary key, subject fallback |
| M-17 | P3 | Consistency | Premium Mail-IDs console does full-page reloads + inline styles — off-pattern for mail | `admin_os_mailids.go:64-66,84-101,143` | Fragment swaps; classes |
| M-18 | P3 | Shortcuts | `c` (compose) drops current `?user=` context — admin reading a mailbox composes from postmaster fallback | `admin-os-mail.js:952` | Build URL from active toolbar mailbox |
| M-19 | P3 | Feature parity | Snooze UI exposes Tomorrow/Next week only although engine supports "later today" (+4h) | `vayuos.go:2726-2729,2908-2925` | Add the button |
| M-20 | P3 | App passwords | One-time secret reveal is bare `<pre>` — no Copy/Download (recovery codes have both) | `vayuos_mail.go:2085-2088` | Reuse those affordances |
| M-21 | P3 | Accessibility | TOTP modal focuses input but no Escape-to-close, no focus restore | `admin-os-mail.js:632-679` | Keydown-Escape + return focus |
| M-22 | P3 | Empty states | Empty folder is one muted sentence; no welcome/CTA on first login | `vayuos.go:2089-2091` | Rich empty state with Compose CTA; first-run orientation |
| M-23 | P3 | Retention UI | Five fixed presets though setter accepts arbitrary days; no display of when deletion strikes | `vayuos_mail_accounts.go:443-457`; setter `vayuos_mail.go:1704-1711` | Custom number input + "deleted nightly" helper text |

### 2.3 Shared shell / platform

| ID | Sev | Area | Finding | Evidence | Fix |
|----|-----|------|---------|----------|-----|
| S-01 | P1 | Foundation | No shared client module: `cookie()/csrf()/api()/errText()` re-implemented per file with divergent behaviour; no shared confirm-modal/dropdown controller; Members/intel still use `alert/confirm/prompt` + reloads | `admin-os-members.js:11-32,99-100,128,151-156,251-257`; `admin-os-mail.js:73-95`; foot glue `admin_os_ui.go:1488` | New `admin-os-common.js`: csrf fetch wrapper, `vpConfirm()` modal, toast helpers; migrate call sites incrementally |
| S-02 | P2 | Feedback bug | `vpToast('…','success')` passed but CSS defines only ok/error/info/warn → that toast renders unstyled | `admin-os.js:806` vs `admin-os.css:1811-1814` | Fix kind (and/or alias success→ok defensively) |
| S-03 | P2 | Loading | Skeletons exist but used in exactly two places; htmx swaps otherwise show nothing | `admin-os.css:1572-1586`; usage `admin_os_media.go:250-253` | Standard swap-placeholder convention |
| S-04 | P2 | Scale | No list virtualization/windowing anywhere; whole-list innerHTML swaps per action | `vayuos.go:2062-2079` | Covered by M-01 cap + pagination first |
| S-05 | P3 | Palette | ⌘K index cached under a sessionStorage key changing every ~10s (defeats caching); quick-actions dispatch via `window[fn]` globals | `admin-os.js:280,286,349` | Stable key + registered-action registry |
| S-06 | P3 | i18n | Zero console i18n hooks (`internal/i18n` serves public tier only) | `handlers_tier4.go:176-193` | Extract strings when i18n lands |
| S-07 | P3 | A11y dialogs | Modals have no focus trap/rest; native `<dialog>` unused repo-wide | `admin-os-members.js:35-73` | Focus management in shared modal (S-01) |
| S-08 | P3 | A11y theme | `high-contrast.css` targets neither `.vp-os` scope nor its token names — dead for `/os` | `high-contrast.css:1-2` | Retarget to live tokens |
| S-09 | P2 | Mobile nav | Bottom-nav Inbox item points at `/os/messages`, which isn't in sidebar hrefs, so the role filter hides it — mobile operators lose the slot | `admin_os_ui.go:1511`; `admin-os.js:171-178` | Repoint to `/os/vayumail/inbox` |
| S-10 | P3 | Theme | Theme persistence split-brain: fire-and-forget POST on console vs localStorage switcher on auth pages | `admin-os.js:32-39` | Single model |
| S-11 | P3 | Docs | `docs/ADMIN-UI.md` documents the retired `/admin/v2` palette/assets, not today's shell | `ADMIN-UI.md:39-50,119-126` | Rewrite against live tokens |

---

## Part 3 — Upgrade plan

Four phases, each shippable independently, ordered by trust-per-effort. Sizes: **S** ≤ half day,
**M** 1–3 days, **L** > 3 days. Every item lists its finding IDs; nothing here requires new
dependencies, CSP changes, or design-language forks (guardrails in Part 5).

### Phase 0 — Correctness & trust (ship first)

*Goal: the product stops lying and stop losing user work. These are small diffs with outsized credibility impact.*

| # | Item | IDs | Size |
|---|------|-----|------|
| 0.1 | Talk: session-scoped expired-id tombstones (never delete ids from the dedupe map); add regression test simulating reconnect-after-expiry | T-01 | S |
| 0.2 | Talk: honest clipboard (feature-check + catch → "Copy failed") with selectable-code fallback | T-02 | S |
| 0.3 | Talk: retry on failed sends (retry button on error bubble; don't strand text) | T-03 | S |
| 0.4 | Talk: confirm-before-Rotate with consequence sentence; patch rotate/federation in place, no `location.reload()` | T-04 | S |
| 0.5 | Talk: conversation rows keyboard-accessible (`tabindex`, Enter/Space, `aria-current`) | T-05 | S |
| 0.6 | Talk: loud key-change warning banner + row badge on stored-fingerprint mismatch | T-06 | S |
| 0.7 | Mail: route panel account-delete through `DeleteMailbox`; align confirm copy with actual outcome | M-03 | S |
| 0.8 | Mail: persist CC/BCC in drafts (+ visible hint that attachments aren't drafted, until 3.3 lands) | M-04 | S |
| 0.9 | Mail: feedback on contacts add/delete (reuse alert-banner pattern; preserve failed input) | M-05 | S |
| 0.10 | Mail: partial-failure toast on bulk inbox actions (port logic from message-action endpoint) | M-07 | S |
| 0.11 | Mail: fix Save-as-draft navigation race (navigate on success only) | M-11 | S |
| 0.12 | Mail: replace password `window.prompt` with the masked modal (clone TOTP scaffolding) | M-06 | S |

**Done when:** a reconnect after burn no longer resurrects messages (test-pinned); deleting an
account can never leak old mail to a reissued address (test exists at store level — wire the panel
to it and pin at handler level); no destructive action proceeds unconfirmed; no failure path
silently discards user input.

### Phase 1 — One feedback system + consistency sweep

*Goal: every surface answers within a heartbeat and speaks in the same voice.*

| # | Item | IDs | Size |
|---|------|-----|------|
| 1.1 | Add `static/js/admin-os-common.js` (loaded beside purify): csrf-fetch wrapper, `vpToast` re-export, shared `vpConfirm()` modal with focus trap/restore, dropdown controller | S-01, S-07 | M |
| 1.2 | Fix `'success'` toast kind; defensive alias in vpToast | S-02 | S |
| 1.3 | Standard swap placeholders: skeleton rows/spinners on `#vm-inbox-list`, `#vm-search-results`, `#vm-accounts-list`, talk thread (reuse `.skeleton--*`) | S-03, M-09 | M |
| 1.4 | Plain-language send/draft error mapping (relay refused, temp-fail, quota already handled); port web-talk 413/429 specifics | M-14, T-08 | S |
| 1.5 | Talk stream resilience: consecutive-failure counter → terminal "too many sessions open" state; `aria-live` status + incoming announcements; `aria-expanded` on verify toggle | T-09, T-16 | S |
| 1.6 | Talk micro-friction pack: self-address feedback, Tor-world placeholder, inline rename editor, draft cleared on identity switch, preflight "reachability unknown", day-aware timestamps, search no-results line | T-07, T-10, T-12, T-14, T-18, T-19, T-20 | M |
| 1.7 | Mail polish pack: reply-quote dates, app-password Copy/Download, "Later today" snooze, `c` preserving `?user=`, retention custom-days + "deleted nightly" hint, Archive in unread badges | M-12, M-20, M-19, M-18, M-23, M-15 | M |
| 1.8 | Shell fixes: repoint dead `/os/messages` bottom-nav item; migrate premium Mail-IDs console to fragments/classes; retarget `high-contrast.css` to `.vp-os` tokens | S-09, M-17, S-08 | M |
| 1.9 | Begin migrating members/intel `alert/confirm/prompt` + reloads onto the shared modal/toast primitives (incremental, file by file) | S-01 | M |

**Done when:** every mutating action in Mail/Talk surfaces ends in a toast/fragment update (never
reload, never silence); a fresh install passes a keyboard-only walkthrough of inbox → read →
reply → compose → send.

### Phase 2 — Make it feel like a mail app

*Goal: speed and flow at realistic mailbox sizes; close the gap to the Gmail-class experience ADR-0083 set.*

| # | Item | IDs | Size |
|---|------|-----|------|
| 2.1 | Cap folder views at newest N (e.g. 200) + "Load older"/page control; interim header-summary cache keyed by filename+mtime to kill repeated MIME parsing; apply same treatment to accounts storage stats (TTL cache, one pass) | M-01, M-02, S-04 | M–L |
| 2.2 | Search ergonomics: filters evaluated inside the scan window, min-2-chars + 600ms debounce, "showing first N matches" notice | M-08, M-13 | S |
| 2.3 | Opt-in "Render HTML view" for received mail: existing bluemonday UGC sanitiser, remote images blocked-by-default with per-message click-to-load; text stays default | M-10 | M |
| 2.4 | Threading v2: References/In-Reply-To primary grouping, subject-normalisation fallback | M-16 | M |
| 2.5 | First-run & empty states: welcome card with "Send your first email" CTA on empty inbox; rich empty states with next-step links everywhere (pattern already exists) | M-22 | S |
| 2.6 | Presence-lite for Talk: expose hub's online-set via the peer endpoint; online dot in thread header + composer hint ("delivers instantly" vs "will queue"); Sent/Delivered/Read legend | T-13 | M |
| 2.7 | Recipient picker: `<datalist>` of local mailboxes + recently-contacted peers behind Start; per-world placeholder | T-11, T-10 | S |
| 2.8 | Talk mobile dual-view: list ⇄ thread with back button <600px, sticky composer, flex-fill layout replacing magic offsets | T-15, T-27 | M |
| 2.9 | Retroactive badge clearing via a `peerkey` stream event when a late sender-key arrives | T-21 | S |
| 2.10 | Decrypt-loss visibility: anonymised per-conversation "N messages couldn't be decrypted" chip | T-17 | S |

**Done when:** opening a 10k-message mailbox feels the same as a 50-message one (pinned by a
benchmark-style test on ListFolder path); HTML newsletters readable on demand without weakening
CSP; a phone user completes receive-reply-send without scrolling past a rail.

### Phase 3 — Structural bets (schedule deliberately, one ADR each)

*Goal: remove the causes Phases 0–2 worked around. Each needs a short ADR because it revisits a recorded trade-off.*

| # | Bet | Why | Size |
|---|-----|-----|------|
| 3.1 | **Message-header index** (SQLite: filename → from/to/subject/date/seen/folder, updated at delivery + flag flips). Listing O(page), instant search, free unread counts, true virtualization, cheap prev/next. Explicitly revisits ADR-0083's recorded revisit-condition | Root cause of M-01/02/08/13/15; the single biggest lever in this document | L |
| 3.2 | **Per-reader cursors for Talk**: record read-by-identity per envelope; `Queued()` filters what *this* identity has read; app ack remains the destroyer. Makes console a first-class reader without stealing the app's copy; reconnects idempotent by construction (root-fixes T-01 beyond tombstones; enables honest cross-device burn semantics) | Root cause of the P0 class | L |
| 3.3 | **Drafts as RFC-822 objects**: full headers + staged attachment MIME parts in Drafts, one upsert endpoint; eliminates client-side save/delete dance | Root-fixes M-04 properly | M |
| 3.4 | **Domain-setup wizard**: bind records + live verify + deliverability + TLS diagnostics into a stepped "Domain health" checklist with a persistent nav badge until green; keep current tab as reference view | Turns the best reference tab into a guided flow (journey b) | M |
| 3.5 | **Per-mailbox settings page** (routed, fragment-swapped): the seven-nested-`<details>` account card becomes a scalable sub-page with room for retention/quota explanations | Scaling + clarity for many-mailbox installs | M |
| 3.6 | **Share-links/QR for anonymous codes** (`…/talk?t=<code>` pre-filled Start screen) + Tor-world settings drawer for Rotate/Federation with confirmations | Kills the largest Tor-world friction cluster | M |
| 3.7 | **String extraction** for Mail/Talk UI copy (single English table now; feeds `internal/i18n` later); fix ADR-0131 drift while touching copy | Prepares i18n (T-24, T-26, S-06) | M |

---

## Part 4 — Guardrails (binding for any implementation)

These come from the platform audit and are enforced partly by tests, partly by CSP:

**Do**
- Model interactions as HTMX fragment endpoints (`hx-get/post → target innerHTML`), reuse the
  `vm-poller` + `HX-Trigger` fan-out patterns; CSRF comes free from shell glue
  (`admin_os_ui.go:1554-1603`).
- Style only via tokens and established prefixes (`vm-*`, `vtalk-*`, BEM-ish `--modifier`) in
  `admin-os.css` — the CSS contract tests fail the build otherwise (`css_contract_test.go:21-98`).
- Build DOM with `createElement/textContent`; route rendered-HTML needs (message previews!) through
  the DOMPurify sink (`renderSanitized`, `admin-os-editor.js:1714-1724`).
- Use `vpToast(msg,'ok'|'error'|'warn'|'info')` and fragment updates; never `location.reload()`.
- Reuse `.empty-state`, `.skeleton--*`, `.chip`, `.modal-panel`, `.dropdown-menu`, inline
  `currentColor` SVG icons verbatim.
- Ship assets through embed + `assetVer()`; register routes beside the others in
  `registerAdminOSUIRoutes`.

**Don't**
- No CDNs, external fonts/APIs, `unsafe-eval`, standard Alpine builds, or markup-expression
  compilers — under this CSP they are inert, not degraded (`csp.go:190-195`). Alpine stays
  islands-only (CSP build, registered before `alpine:init`).
- No inline `<style>`/`style=""` attributes; no invented classes without stylesheet rules.
- No `window.confirm/prompt/alert` in new or migrated surfaces.
- Nothing may break under OnionMode (all external origins stripped automatically).
- Never persist message content client-side; hidden ≠ unreachable — keep server-side gating
  authoritative (`gate()` + `osPathMinLevel`).

**Test hooks that must stay green (and grow):** `css_contract_test.go`,
`css_console_baseline_test.go`, `console_fonts_test.go`, `vayumail_ui_test.go`,
`client_surface_completeness_test.go`, plus new regression tests pinned by Phase 0
(reconnect-resurrection, delete-path inheritance) and Phase 2 (large-mailbox listing cost).

---

## Appendix — Implementation status (2026-09-13)

What has actually been built and verified locally against this document. Nothing is
committed; it all sits in the working tree.

**Verification method.** A baseline was captured on the current HEAD for the touched
packages (`cmd/vayupress`, `internal/vayuos/{mail,vayutalk,pgp}`, `internal/render`,
`internal/db`, `internal/settings`), then re-run after each phase and diffed by test
name. Final state: **64 failing tests before and after — zero regressions**. Those 64
are environment limits, not defects: systemd/nginx/firewall/cosign/bundle tests that
need a Linux host, and Maildir tests whose flag filenames contain `:` (illegal on
NTFS). Two loopback-listener tests (`TestSMTPReceiveDelivers`,
`TestIMAPStableUIDsAcrossReconnect`) time out intermittently under full-suite load on
this box and pass on re-run — both were verified individually before being dismissed.

**Tree caveat.** This work was built on a checkout where another session had
uncommitted, non-compiling "website v2" changes. Those were parked recoverably in
`git stash@{0}` (*parked by vayumail/vayutalk UX session*) with the diff and untracked
files also copied to `audit/parked-website-wip-2026-08-26/`.

### Phase 0 — Correctness & trust: 12/12 done

Every item implemented and pinned by `cmd/vayupress/vayuos_ux_regression_test.go`
(16 tests): the reconnect tombstone, honest clipboard reporting, retry-on-failure,
confirm-before-Rotate, keyboard-reachable conversation rows, the changed-safety-number
alarm, account deletion through `DeleteMailbox`, drafts that keep Cc/Bcc, visible
contact-save failures, bulk partial-failure reporting, the draft-navigation race, and
a masked password modal (focus restored on close).

### Phase 1 — One feedback system: 8/9 done

Done: toast kind normalisation (`success`/`danger`/`warning` were rendering unstyled),
`window.vpCsrf` exposed, `vpPost` hardened for non-JSON bodies and expired CSRF, swap
indicators on inbox/search/accounts, plain-language send errors, a bounded Talk
stream-retry that names the remedy, live-region announcements, `aria-expanded` on the
verify toggle, the Talk micro-friction pack, the Mail polish pack (dated quotes,
app-password copy/download, “Later today”, `c` keeping `?user=`, custom retention days,
Archive unread counts), the dead `/os/messages` bottom-nav link, console
high-contrast/forced-colors rules, and the premium Mail-IDs console (M-17) — which no
longer reloads the whole page after every approve/disapprove/add/remove, uses
`vpConfirm` instead of the native dialog, carries no inline styles, and serves its cards
as a fragment.

Partial: the shared-foundation item reused the existing `vpConfirm`/`vpPost`/`vpToast`
instead of adding `admin-os-common.js`, and ~23 native dialogs remain in the eight apps
outside this audit's scope (members, intel, editor, security, pages, newsletter, update,
storage). Mail and Talk are clean, and a test forbids `prompt`/`confirm` from returning
to either.

> Correction found while implementing: the console's empty-state variants are
> `.empty-icon`/`.empty-title`/`.empty-sub`, not the `-state-` names one fix note in
> this document used. The build-breaking CSS contract test caught it.

### Phase 2 — Mail-app feel: 10/10 done

- **2.1** A parsed-header cache in the Maildir (listing is stat-bound, validated by
  size+mtime, with a test proving a rewritten file is never served stale); the folder
  view windowed at 200 rows with “Load older” and the count of what is out of view, the
  window carried through polls and pane actions; a short TTL cache for *displayed*
  mailbox sizes while the quota gate measures fresh.
- **2.2** Search asks for two characters, debounces at 600 ms, and says when the scan
  hit its limit instead of reporting a floor as a total.
- **2.3** Opt-in HTML rendering with a plain-text default. Two sanitiser policies:
  the default strips image SOURCES (a privacy boundary — loading a tracker tells the
  sender the mailbox opened the message), the opt-in loads them. Verified by tests that
  assert scripts, handlers, iframes and every image URL are gone from the strict path.
- **2.4** Evidence-first threading: In-Reply-To/References decide, subject is the
  fallback, so a reply joins its parent even when the subject changed and unrelated mail
  sharing a subject is no longer merged. Eight tests, including a reference loop.
- **2.5** A real first-run empty inbox with Compose/Connect CTAs.
- **2.6–2.10** Presence-lite (one-sided by design: “online” only when a stream exists,
  never a claim about someone's phone), the Talk recipient picker, the phone dual-view
  (list ⇄ thread with a back control, dvh-sized thread, no more 40vh rail), a `peerkey`
  stream event that clears stale “unverified” badges, and the decrypt-loss chip.

### Phase 3 — Structural bets: 5/7 done

- **3.2 Per-reader cursors** — the root fix for the P0. `Envelope.WebRead` plus
  `QueuedExcludingWebRead`/`MarkWebRead` in the store, `SubscribeWeb` and the
  ownership-checked `MarkWebReadAs` on the engine, the console wired to both, and the
  client signalling reads in the clearnet world as well. Three engine tests pin that a
  reconnect stops re-flushing, the app's queued copy is untouched, and the app's ack
  remains the destroyer.
- **3.3 Drafts as real messages** — attachments are stored as MIME parts and the send
  path merges them back by draft id, so reopening a draft and pressing Send no longer
  ships a message without the files the composer was showing. Autosave deliberately
  skips while files are attached (it would re-upload them on every keystroke); the
  button still stores them and the composer says so. Four engine tests, including a
  hostile filename and a binary round trip.
- **3.4 Domain-health checklist** — a guided card above the DNS reference tables, built
  from the same one-per-render verdict set (so the two can never disagree), ending in a
  named next step, refreshed out-of-band with the Re-check button. Five tests.
- **3.6 Share links** for anonymous codes (minus QR): `/os/talk?t=<code>` pre-fills
  Start and the anon block offers “Copy share link”.
- **3.5 Per-mailbox settings page** — every per-mailbox setting (forwarding, vacation,
  aliases, recovery, handover, PGP, filters, picture picker) now lives on its own routed
  URL, `/os/vayumail/accounts/settings?user=…`. The accounts list keeps what an operator
  scans *across* mailboxes and links out, so the seven-nested-`<details>` card and its
  whole-list re-renders are gone. The section builders are shared, so the swap target is
  adapted in one documented place and the response surface is chosen from HTMX's
  `HX-Target`; tests assert the settings page contains no list-surface target and that a
  list-surface control still receives the list, so neither surface can silently swap the
  other. This retired `TestAccountCardHasEverySetting` by design and replaced it with
  `TestEveryPerMailboxSettingLivesOnItsOwnPage`, which asserts nothing became unreachable.

**Also fixed along the way (not in the original plan):** the composer's link inserter
was the last `window.prompt` in the Mail/Talk surfaces — it is now a console modal, and
a test forbids `window.prompt`/`window.confirm` from returning to either file
(`window.alert` is allowed exactly once, as the documented toast fallback).

### Still open

- **3.1 SQLite message-header index.** Deliberately deferred, and this is a considered
  call rather than a shortage of time: the header cache shipped in 2.1 already removed
  the per-message read cost, so the remaining work is directory stat + sort. An index
  adds a second source of truth that must be invalidated on delivery, move, flag flip,
  delete, IMAP mutation and the retention sweep — and a missed invalidation shows the
  *wrong mail*, which is far worse than a slow list. It needs its own ADR and its own
  test strategy before it is worth having.
- **3.5 Per-mailbox settings page.** Done — see above. `TestAccountCardHasEverySetting`
  was retired deliberately: it pinned the nested-in-one-card design, and the replacement
  test asserts the stronger property (nothing became unreachable).
- **3.7 String extraction / i18n prep.** Not started. A partial extraction (a table used
  by only the strings this pass touched) would add indirection without delivering a
  translatable product, so it is better done as its own piece of work with the public
  tier's `internal/i18n` as the target.
- **Phase 1 leftovers:** the shared client foundation was reused (existing
  `vpConfirm`/`vpPost`/`vpToast`) rather than centralised, and ~23 native dialogs remain
  in the eight apps outside this audit's scope (members, intel, editor, security, pages,
  newsletter, update, storage). Mail and Talk are clean.
- **3.4 nav badge** and **QR codes** for share links were not built: a badge needs a
  cached DNS verdict in settings, or every console page render would trigger DNS lookups.

### Corrections to this document found while implementing

1. The console's empty-state variants are `.empty-icon`/`.empty-title`/`.empty-sub`, not
   the `-state-` names one fix note used — the build-breaking CSS contract test caught it.
2. `high-contrast.css` could not have reached `/os` even after a retarget: it is a
   public-tier asset, and it is a minified Go const behind a codegen test. The console
   now carries its own `prefers-contrast`/`forced-colors` rules in `admin-os.css`.
3. The “Copied” bug was wider than one call site: `'danger'` and `'warning'` were also
   being passed to a toast that only styles `ok`/`error`/`info`/`warn`. Fixed at the
   source, so no call site can lose its colour.

### Totals

**56 new test functions** across `cmd/vayupress` and `internal/vayuos/{mail,vayutalk}`,
plus one extended engine test. Final gate: `go build ./...` and `go vet` clean; the
touched-package suite reports **63–64 failures, matching the pre-work baseline exactly —
zero regressions** (the count moves by one only because two loopback-listener tests are
flaky on this box).


