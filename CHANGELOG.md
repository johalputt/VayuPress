# Changelog

All notable changes to VayuPress are documented here.

Format: [Added / Changed / Deprecated / Fixed / Security / Upgrade Notes / Ethical Updates]

---

## [3.15.30] — 2026-07-25

### Security
- **No address becomes a member without being verified.** Requesting a
  passwordless sign-in link used to create the member row immediately, before the
  email had even been handed to the mail queue. Anyone could therefore mint a
  "member" out of any string that parsed as an address — a typo, a throwaway
  domain, or an address whose delivery failed outright — and the account stayed
  behind forever, counted in the member total and announced as *"Member joined"* in
  the activity feed, for somebody who never received, let alone opened, anything.

  Membership now begins at the one moment control of an address is actually
  demonstrated: when the emailed token comes back. Requesting a link writes only
  the token, which carries the address on its own and expires in 30 minutes, so an
  address that never confirms leaves nothing durable behind. Two supporting
  properties:

  - The rule is **structural, not a convention**. `verified_at` is stamped by the
    single INSERT rather than by each caller, and all three surviving creation
    paths — magic link consumed, VayuMail mailbox credential authenticated,
    payment completed — are already proof. There is no exported way to create an
    unverified member, so this cannot regress by someone adding a new caller.
  - The endpoint stays **non-enumerable**. It behaves identically for a known and
    an unknown address, so it still cannot be used to ask who is a member — which
    was the reason it was written to respond generically in the first place.

  The first-time welcome email moved with it: it is sent on the first *confirmed*
  join instead of on every link request, so a greeting no longer goes out to an
  address that never joined.

### Added
- **Unconfirmed addresses are visible and removable in the console.** Rows created
  by the old behaviour are flagged `unconfirmed` on the Members list with a Remove
  action, and a summary card offers to clear them all at once. The card only exists
  while there is something to clear. Removal deletes the member record, its
  sessions and any pending sign-in link — so a link issued before the cleanup
  cannot recreate the row afterwards — and the store **refuses to delete a
  verified member**, so the cleanup path cannot take a real account with it. If the
  person turns out to be real, opening a link still enrols them normally, and a
  legacy row is confirmed in place rather than left flagged.

### Fixed
- **Stylesheet changes were invisible for up to a year.** Everything under
  `/static/css` is served `public, immutable, max-age=31536000`, which is only safe
  if the URL changes whenever the bytes do — an immutable response is never
  revalidated. Several stylesheets were linked **without a version**, most
  consequentially `portal.css`: the member portal panel was restyled across two
  releases and could not be seen, because browsers and the edge kept serving the
  copy they had cached first. Every stylesheet link now carries a content hash.

  The member portal needed one extra step, since its stylesheet is injected by
  script rather than linked from page HTML: the version travels through the widget,
  and the widget's own version is computed over the finished body. A restyle
  therefore moves the script URL too, so a client holding a cached script still
  learns about the new stylesheet instead of quietly asking for the old one. The
  script is served with an ETag so a stale copy revalidates cheaply rather than
  being refetched in full.

  A test now sweeps every Go source that emits HTML for an unversioned
  `/static/css` link, so a stylesheet cannot be shipped behind a year-long cache
  entry again.

### Upgrade Notes
- Migration `078-member-verified` adds `members.verified_at` and backfills it for
  members whose control of their address is already evidenced — they have signed in
  at least once (which requires a consumed link or a verified mailbox credential),
  or they hold a paid tier. Anything left unset was created by the old pre-send
  path and is surfaced in the console for review; nothing is deleted
  automatically.
- Because stylesheet URLs now carry content hashes, the first page load after
  upgrading fetches them once more and is then cached as before. If a panel still
  looks unchanged, a hard refresh (Cmd/Ctrl-Shift-R) clears the previously pinned
  immutable entry.

---

## [3.15.29] — 2026-07-25

### Added
- **Membership &amp; billing on the member account page — including the next payment
  date.** The subscription record already carried the period end, trial end,
  cancel-at-period-end flag and start date, and **none of it was ever shown**: a
  paying member could see *what* plan they held but nothing about when they would
  next be charged, whether a trial was about to convert, or whether a cancellation
  had actually taken. The card now shows status, price and cadence, **next payment
  date**, trial end, member-since, and recent membership activity.

  The wording follows the member's real situation, because getting this wrong
  misinforms people about their own money:
  - after cancelling, the period end is when **access** stops, so it reads
    "Access until" with "No further payments will be taken" — calling that date
    "next payment" would say they are about to be charged again;
  - a **trial** shows its end date *and* states plainly that it converts
    automatically;
  - a **complimentary** plan says "Renews" and notes that no payment is taken;
  - dates render in your **site timezone**, so a renewal reads as the day the
    member's own calendar shows;
  - a free member sees no card — there is nothing to bill.

### Changed
- **The member panel is properly compact, and premium from materials rather than
  size.** Narrowed to 19.5rem with tighter padding and a smaller type scale
  throughout (32px avatar, smaller controls, less air between rows). Because
  compactness alone can read as cramped, the finish now comes from a hairline
  accent edge along the leading border, a subtly lit surface, a plan chip that
  reads as status, and a separator above the account actions so a dense layout
  still has visible structure. Sign out sits last and quiet.

### Note
- **The VayuMail address for paying members already works end to end** — an
  entitled member sees a claim card (pick an address, set a mailbox password) and a
  free member sees it presented as a Premium benefit. Entitlement is **per tier**:
  enable *mail* and set a quota on the paid tier in the console's member tier
  editor, and the claim card appears for those members.

## [3.15.28] — 2026-07-25

### Fixed
- **The plans page now knows you are signed in.** It never resolved the current
  member, so every call to action was hard-coded to `/signup` — a signed-in member
  was offered "Get started", "Become a member" and "Already a member? Sign in",
  which both told them the page did not know them and hid the one action they came
  for. Each card now speaks to their real position:
  - the plan they already hold is **stated** ("Your current plan", marked
    "You're here") rather than sold;
  - a signed-in member on a lower tier gets **"Upgrade to &lt;tier&gt;"**, routed to
    checkout when payments are enabled;
  - a paying member looking at the free tier is pointed at their **account**,
    because downgrading is a billing decision, not a checkout;
  - an anonymous visitor keeps exactly the previous flow;
  - the heading and footer follow suit — "Your membership" and a link back to the
    account, instead of an invitation to sign in.

  Because the page is now personalised it declares `Vary: Cookie`, and the
  signed-in variant is `private, no-store` so no shared cache can serve one
  member's plan page to somebody else. The anonymous variant — the one search
  engines index and most visitors receive — stays publicly cacheable, so speed and
  SEO are unaffected.

### Changed
- **Member surfaces are compact and calm.** The portal panel was sized for
  editorial reading; it is now a narrower column with tighter padding, a smaller
  type scale, a 38px avatar in place of 52px and less air between rows — it is a
  utility surface for "who am I, what is my plan, sign out". The account and plans
  pages come down from 2rem display headings and tall cards to a working scale,
  with one clearly primary action per card, and colour now marks state (the plan
  you are on) rather than shouting.
- **Motion dialled back to what communicates state.** The button sheen sweep is
  gone, hover lift and scale are much gentler, shadows are softer, the close button
  fades rather than spins, and the panel's reveal stagger is short enough to be
  felt rather than watched. Hover still enlarges — just quietly. Everything stands
  down under reduced-motion, and touch targets stay at 44px and up.

## [3.15.27] — 2026-07-25

### Added
- **Premium motion for the Vayu flagship theme and the member portal — at no cost
  to speed or SEO.** Both changes are **CSS only**: no markup moves, so headings,
  structured data, canonical tags and the geo/analytics hooks are byte-identical and
  nothing here can shift an SEO or geo result either way. No new requests and no web
  fonts, so LCP is unchanged. Every effect animates only transform / opacity /
  shadow / background-position — properties the compositor handles without a layout
  pass — and all of it stands down under `prefers-reduced-motion`.
  - **Member panel &amp; login buttons** (shared by every theme): buttons now
    **enlarge on hover** — lift plus scale, an accent-tinted shadow and a sheen that
    sweeps across once — with a press state and a real focus ring. The launch button
    rises in and gains a breathing accent ring. The panel slides with a touch of
    scale over a blurred backdrop and reveals its rows in a short stagger. On phones
    it is now a **bottom sheet** — full width, rounded top, grab handle, slide-up,
    and 46px minimum tap targets. Input focus rings follow the active theme's accent
    instead of a hard-coded indigo.
  - **Vayu theme**: a slow aurora glow behind the hero, a drifting gradient on the
    hero heading, an accent edge that draws itself down each card on hover, a firmer
    glow-lift, a staggered rise for the first screen of cards, and hover lift on the
    stats, nav sign-in and pagination.

### Performance
- **Feed cards skip rendering work while offscreen** (`content-visibility` paired
  with an intrinsic size so the scrollbar stays stable) — a real Core Web Vitals
  win rather than a cosmetic one.
- **The nav underline no longer animates `width`**, which forced a layout pass on
  every frame; it is now a composited `scaleX` on a full-width bar, visually
  identical.

### Fixed
- **A gradient heading can no longer render invisible.** Gradient text needs the
  glyph fill to be transparent; a bare `color: transparent` is honoured by every
  engine, so anywhere `background-clip: text` is unsupported the most important
  text on the page would simply vanish. The hero heading now uses
  `-webkit-text-fill-color`, matching the other gradient headings — an engine that
  cannot clip a background to text ignores it too and keeps the solid colour.

## [3.15.26] — 2026-07-25

### Fixed
- **"I'm logged in, but after closing the window the site shows the sign-in page —
  and deleting `/login` from the address bar gets me straight in."** The sign-in
  page already redirects an authenticated visitor, and that redirect works — which
  is precisely why editing the URL succeeded. What served the form was a **cache in
  front of the origin**. The page carried `no-store`, but a shared cache needs more
  than that to be reliably forbidden from storing a response and handing it to
  another visitor: it needs `private`, and it needs to know the response depends on
  the cookie. Auth pages now send `private` + `Vary: Cookie` + a CDN-specific
  `no-store` alongside the existing directives, so no edge can answer for the origin
  and show a stale form to somebody holding a live session.
- **Signing in now replaces your session instead of stacking on it.** Opening a
  sign-in link for one account in a browser already signed in as another left *two*
  live sessions, so which account you were acting as depended on resolution order
  rather than on what you just clicked. (The link itself was never the problem — it
  is single-use and atomically consumed.) Both member sign-in paths — magic link and
  mailbox portal — now destroy the incoming session server-side before issuing the
  new one. This also closes session fixation: a session created before
  authentication can no longer survive it.
- **Logging out no longer signs you back in as the previous account.** A signed-in
  operator is deliberately accepted as a member-equivalent on the public site, so
  clearing only the member cookie left that identity standing and you were shown as
  signed in again immediately. Member logout now also ends the console session and
  clears its cookie, mirroring what the console logout already did for the
  membership session.

  **Note the deliberate tradeoff:** an operator who signs out from the public site
  now also leaves the console and signs in again at `/os/login`. That is the safe
  direction — leaving a browser authenticated after an explicit logout is the wrong
  failure mode, especially on a shared machine.

## [3.15.25] — 2026-07-25

### Fixed
- **A learned signature can no longer convict a real browser** — the actual root
  cause of the "Access denied" 403 shown to Chrome and Brave visitors. The
  diagnostic added in 3.15.24 named it exactly: *score 0.85 · BadBot · learned
  bad-bot signature (confidence 0.19) · identified*. Confidence 0.19 is far below
  the 0.8 threshold, so the row could only have short-circuited scoring because it
  was marked operator-verified. It got there honestly — confirmed once in the review
  queue (which sets 0.99), then decayed 0.2 per solved challenge by the
  false-positive guard, 0.99 → 0.79 → 0.59 → 0.39 → 0.19, as real people proved they
  were human four times over. The database had recorded four times that it was
  wrong, and kept blocking anyway: operator-verified overrode confidence entirely,
  and the bad-bot branch then manufactured a 0.85 score from a 0.19 belief.

  The learned short-circuit is now gated by three independent guards, any one of
  which stops this case:
  1. **A bad-bot verdict is never taken for a mainstream-browser fingerprint.**
     That fingerprint is shared by everyone on the same browser build, so no stored
     row — auto-learned, imported, or confirmed — may turn "this is Chrome" into a
     hard block.
  2. **A row with recorded false positives is disputed** and no longer convicts on
     its own.
  3. **A bad-bot verdict needs confidence still above a floor**, so a one-time
     Confirm can no longer outrank the evidence accumulated since.

  Good-bot, AI-agent and human verdicts are untouched (they only widen access), and
  a confident, undisputed, non-browser signature still convicts — so the adaptive
  database keeps its teeth against real scrapers.

### Note
- **Network hardening was not implicated.** The "Access denied" page exists only in
  the application's own block path: Tier 2 drops packets in the kernel (which
  surfaces as a connection timeout, not a styled page) and Tier 3 shaping would
  return the reverse proxy's own error. Tiers 2 and 3 remain safe to leave on.

## [3.15.24] — 2026-07-25

### Added
- **Theme Studio redesigned to the Monetization-console language.** All thirteen
  control groups now carry an icon, a title, a one-line subtitle and a status chip
  in the same summary layout as the Monetization accordions, and section dividers
  group the panel into **Start here · Colour · Layout &amp; type · Fine detail ·
  Advanced** — so thirteen sections read as five decisions. The single-open
  accordion behaviour and the sticky jump bar are unchanged.
- **Readability readout (WCAG contrast).** Contrast is the one property you cannot
  eyeball — it depends on relative luminance, not on how bold a colour feels. The
  Brand colours group now measures each accent against the background it is
  actually read on and reports the grade (AAA / AA / AA-large-only / Fails) with
  the measured ratio. The collapsed chip grades the **weakest** pairing, so it
  cannot reassure you when your accent is unreadable on one of the two
  backgrounds. It reports — it never refuses a colour.
- **Preview colour-scheme toggle.** The palette is keyed off the preview page's
  theme attribute, so the light-mode colours you were editing could not be seen
  without changing your OS setting. Dark / Light buttons in the preview toolbar now
  reload the preview in the chosen scheme.

### Fixed
- **A VayuShield block now explains itself.** The Visitor &amp; crawler check
  reported only a bare status, so a red row said "403" and left the cause ambiguous
  between a compiled-in signature, a learned signature and plain heuristic scoring.
  Each failing probe now shows the score, the classification, the bot name, the top
  reason the scorer recorded, and whether the verdict was treated as *identified*.
- **Recovery now reaches operator-verified signatures.** The amnesty deliberately
  preserves signatures someone confirmed, assuming a human vetted them — but
  confirming a review-queue candidate during a false-positive run marks a **real
  browser** as an identified bad actor, and identified verdicts get no leniency, so
  the amnesty could not free those visitors. A new **Forget all learned
  signatures** control clears every learned row, including verified ones.
  Compiled-in known-bad signatures are untouched, so real bots stay recognised.

## [3.15.23] — 2026-07-25

### Fixed
- **"Access denied" for real Chrome and Brave visitors — the signature learner was
  poisoning itself.** When VayuShield blocks a request it records that client's
  fingerprint as an auto-learned bad bot (confidence 0.7), and every repeat adds
  +0.10. Past 0.8 the scorer stops scoring and treats the record as an *identified*
  bot, which lands straight in the block band. So an earlier run of false positives
  taught the database that real browser fingerprints were bots; each refusal raised
  the confidence further, and because the verdict then read as "identified" the
  navigation carve-out deliberately refused to soften it. A fingerprint is shared by
  everyone on the same browser build, so this hard-blocked whole populations of real
  readers — including the operator's own browser — with no way to prove otherwise.
  - A **mainstream browser fingerprint is never auto-learned as a bad bot**. Real
    bad actors wearing a browser User-Agent are still handled by scoring, the
    challenge ladder and the reputation engine.
  - Verdicts now distinguish an **operator-verified or compiled-in** signature from
    an **auto-learned guess**, and only the former forfeits the benefit of the
    doubt. An auto-learned guess now yields the solvable challenge on a page load
    instead of a dead 403; an identified bot is still blocked.
  - **Release all sentences now** also forgets auto-learned signatures (keeping
    operator-verified ones). Clearing jails without clearing what was learned from
    them left the operator still blocked, because the learner cannot unlearn on its
    own — every refusal looks like confirmation.
- **Visitor &amp; crawler check no longer raises a false indexing alarm.** Crawler
  identity is confirmed by published IP range and reverse DNS, which a probe from
  your own server cannot imitate — so a synthetic Googlebot is *correctly* treated
  as an impostor. Those rows now read "Not testable here" and explain why, instead
  of reporting your indexing as broken.

### Performance
- **VayuOS console responsiveness.** Four avoidable costs on admin render paths:
  the settings accessor copied all ~85 entries to read a single value (it is called
  several times per page from ~60 sites); the shield self-test drove eight requests
  through the full middleware on *every* render of the Bot Shield page (now memoised
  for 60s and refreshed on save); the timezone picker re-parsed ~50 zoneinfo entries
  per render (now memoised per zone per day); and the dashboard and Media header
  stat-ed and sorted every media file just to count them.

## [3.15.22] — 2026-07-25

### Added
- **Site display timezone — dates and times finally match your clock.** VayuPress
  stores every timestamp in UTC (unambiguous, survives a server move, never shifts
  under daylight saving) but it also *displayed* UTC, so an operator on IST read
  every admin timestamp 5½ hours behind their own clock, and a post published after
  05:30 local time showed readers the **previous day's date**. Set your zone in
  **VayuOS → Settings → General → Date &amp; time** (an IANA picker showing each
  zone's live UTC offset) and it applies everywhere immediately, with no restart:
  - public post dates, the machine `<time datetime>` and the OG / schema.org
    timestamps all render in your zone, so the visible date, the machine date and
    your clock finally agree (RFC3339 keeps its offset, so machine timestamps stay
    unambiguous for search engines);
  - every admin timestamp renders in your zone and is labelled with the **real**
    zone abbreviation instead of a hard-coded "UTC".

  Stored data is untouched — changing the setting only changes how times read — and
  leaving it unset keeps the previous all-UTC behaviour. Export filenames, RFC3339
  API/bundle fields and the analytics window bounds deliberately stay UTC, because
  those are machine values and the analytics rows are keyed by UTC date.

### Fixed
- **Trending &amp; pinned posts now show on every post, every time.** The widget
  hides itself when it has nothing to show, but *every* response carried a
  five-minute public cache header — so a reader whose fetch happened to land during
  a cold start, or whose cheap warm-up response came back empty, cached that empty
  answer and kept getting the hidden widget from their own browser cache for the
  next five minutes, across every post they opened. That is the "sometimes shows,
  sometimes not". An empty or degraded response is now sent `no-store`, so the very
  next page load re-asks and gets real data. Two supporting fixes: the warm-up
  payload is built on a detached context (a reader navigating away mid-fetch used to
  cancel its queries and produce the empty payload), and a computation that returns
  nothing is no longer memoised — a transient query failure could previously pin an
  empty list for up to an hour.

## [3.15.21] — 2026-07-25

### Fixed
- **VayuShield no longer hurdles, stalls or jails real readers.** Three
  compounding false-positive paths meant a genuine visitor could be met with a
  5-second stall and then the auto-retrying "just a moment" page for hours, with
  no operator switch that stopped it.
  - **The reputation jail ignored every operator toggle.** The blocklist gate
    checks the auto-block switch; the self-learning reputation jail did not — so an
    operator who turned OFF rate limiting, load shedding, auto-block, adaptive mode
    and Sovereign Surge still had sentences served to their readers, and no switch
    left to reach for. It is now gated on auto-block like the blocklist. The engine
    still *observes* while enforcement is off, so re-enabling is instant.
  - **A score-only tarpit or block could hit ordinary page loads.** Both are
    reached from the same "score above the block threshold" condition, so a single
    mis-scored browser lost 5 seconds per page *and* took reputation damage — two
    or three page loads then collapsed its standing past the jail floor and the
    escalating sentence answered every later request. A top-level navigation from a
    merely-unproven client is now handed the ordinary solvable challenge instead:
    cleared in well under a second, with no reputation damage, so the spiral cannot
    start. Clients identified by signature and all non-navigation traffic (asset
    and API hammering) keep the full tarpit/block treatment.
  - **A mis-jailed visitor waited out the redeem budget on a dead page.** Solving
    the challenge both issues the clearance cookie and pardons the sentence, so a
    browser navigation is now always offered it while there is no active flood —
    out on the first page load. Non-browser traffic still gets the cheap rejection,
    and the carve-out collapses under attack.

  Crawler handling is untouched: the SEO fast path still runs before every gate.

### Added
- **Visitor & crawler check — live proof on the Bot Shield page.** The SEO canary
  already drove synthetic crawlers through the live middleware at boot, but only
  wrote the result to the log. It now also probes four ordinary first-time readers
  (Chrome, Safari/iPhone, Firefox, Chromium) as real top-level navigations, and the
  whole report is surfaced as a panel: a per-probe served / challenged / blocked
  verdict measured against **your current settings**, with a Refresh button. The
  card auto-expands and names the problem when something is wrong rather than
  showing green — so "is the shield hurting my readers or my indexing?" is answered
  with evidence. Probes run in-process and are never counted as real traffic.
- **Release all sentences now** — an operator amnesty control on the Bot Shield
  page. Sentences self-expire but escalate to hours, so after fixing the cause of a
  false-positive run this frees your readers at once instead of waiting the
  punishment out.

### Changed
- **Website page redesigned to the Monetization-console style.** A stat-grid header
  (what the root serves · active design · designs available · custom build) plus
  animated accordion cards grouped under Hosting, Design &amp; content and Custom
  build, replacing the flat stack of cards.

## [3.15.20] — 2026-07-24

### Changed
- **Posts manager redesigned into premium per-post cards.** The wide,
  button-heavy table is replaced with a Monetization-style stack of animated
  collapsible cards — one per post. Each card shows a bulk-select checkbox, the
  title, its slug/updated line and a live status pill; tapping a card reveals
  ONLY that post's actions (Edit, View, Pin, Publish/Unpublish, Ping IndexNow,
  Delete) rather than showing every button on every row at once. The select
  checkbox sits outside the disclosure, so ticking it for bulk selection never
  opens the card. Pin, status and IndexNow keep their in-place HTMX updates, and
  bulk select-all moves to a small header above the list.

## [3.15.19] — 2026-07-24

### Changed
- **Premium Monetization-console polish across every workspace page.** The whole
  VayuOS workspace now shares one design language: each page carries a styled
  subtitle under its title — Posts, Pages, Comments, Media, Members, Newsletter,
  Messages, Security, Tools, Storage, Advertising, Governance, Monitoring, Website
  and My profile — matching Monetization, SEO, Analytics and Bot Shield. The Posts
  page also gains a Total / Published / Drafts stat-grid header. A new page-subtitle
  style in the admin theme also upgrades the pages that already used the class
  before it was styled. Dashboard and the consolidated hubs already carried the
  premium section-head + stat-grid layout and are unchanged.

## [3.15.18] — 2026-07-24

### Changed
- **VayuAnalytics redesigned to the Monetization-console style.** The
  privacy-first analytics page drops the flat tab strip for animated accordion
  cards grouped under section dividers — Traffic (traffic-over-time chart, live
  visitors), Content &amp; audience (top pages &amp; referrers, devices/browsers/OS,
  geography), Acquisition &amp; actions (campaigns, custom events, goals, journey)
  and Export. The headline KPIs (unique visitors, visits, pageviews, bounce rate)
  and the engagement strip stay pinned at the top. The live-visitor poller, goals
  controls and period selector keep their existing hooks, so behaviour is
  unchanged — only the layout is new.
- **Theme Studio &amp; Theme Store restyled to match.** Theme Studio gains a page
  subtitle and its sidebar control groups now carry the same open-state shadow and
  animated reveal as the Monetization accordions. Theme Store gains a subtitle and
  a stat-grid header (themes, categories, active theme, external-assets = 0).

## [3.15.17] — 2026-07-24

### Fixed
- **IndexNow now works (was HTTP 422) — and is fully automatic.** The key
  verification file was served under `/.well-known/<key>.txt`, but IndexNow only
  lets a key authorise URLs at or below its own directory — so a `/.well-known/`
  key could not vouch for root-level post URLs, and every submission was rejected
  with "HTTP 422 · URLs don't belong to the host." The key is now served at the
  site **root** (`https://<domain>/<key>.txt`) and submitted with the root
  `keyLocation`, so it covers the whole site. **One-click setup:** the SEO page's
  **Connect & verify IndexNow** button now auto-generates and stores the key,
  hosts the verification file and verifies with IndexNow — no manual key handling.

### Changed
- **SEO page redesigned to the Monetization-console style.** A subtitle,
  `section-head` dividers (Indexing · Site health) and animated accordion cards
  with status pills — matching the Bot Shield console — replace the flat card
  stack. The crawl-activity panel is expanded by default so crawl proof is
  front-and-centre.

### Added
- **Search engine & AI crawl-activity panel on the SEO page.** VayuShield now
  tallies, server-side, how many page requests it has served to each verified
  crawler (Googlebot, Bingbot, GPTBot, ClaudeBot, PerplexityBot, …) and shows it
  on VayuOS → SEO, split into search engines vs AI systems. Because it counts on
  the server (crawlers never run the JS analytics beacon), it reflects real crawl
  traffic — live proof the shield is serving crawlers, not blocking indexing.

### Performance
- **Theme switcher inlined — one fewer render-blocking request on every page.**
  The light/dark theme script must run before paint (to avoid a flash), so it
  could not be deferred; it was loaded as an external render-blocking file. It is
  now inlined in the page head (no network round-trip), allowed under the strict
  `script-src 'self' 'nonce'` CSP via the script's constant SHA-256 hash — which
  stays valid across disk-cached pages where a per-request nonce cannot. Improves
  mobile First/Largest Contentful Paint.

## [3.15.16] — 2026-07-24

### Fixed
- **VayuShield no longer throttles your whole audience behind Cloudflare/CDN
  (critical).** When a site is proxied through Cloudflare, every request reaches
  the origin from one of Cloudflare's edge IPs, so VayuShield saw the entire
  audience as a handful of IPs — that pool instantly blew past the per-IP rate
  limit, auto-block jailed the Cloudflare IP, and **every real visitor got the
  "Just a moment" throttle page in a loop.** A new **"Behind Cloudflare / a CDN"**
  toggle (VayuOS → Bot Shield → Availability & anti-DDoS, or `TRUST_CLOUDFLARE=1`)
  makes the origin read each real visitor's IP from `CF-Connecting-IP` /
  `True-Client-IP`, so rate-limiting, reputation and lockout apply per visitor.
  Only genuine Cloudflare edge ranges are trusted, so the header cannot be
  spoofed from a direct connection. Applies live, no restart.

### Security
- **Auth/session and CSRF cookies now always carry `Secure` (resolves the CodeQL
  "Secure attribute is not set to true" findings).** The earlier OnionMode
  exception was based on a wrong premise: clearnet is HTTPS, and a Tor Space is
  served only to Tor Browser, which treats a v3 `.onion` as a
  potentially-trustworthy origin and therefore stores/sends `Secure` cookies over
  the http onion. `auth.CSRFCookieSecure` is now unconditionally true, so there
  is no transport on which a real client drops the cookie and no runtime
  exception for a static analyzer to flag.

## [3.15.15] — 2026-07-24

### Changed
- **The Bot Shield & Analytics console now matches the Monetization page — clean,
  animated accordion cards.** The shield admin page was reskinned to the same
  design language as the Monetization console: a subtitle, `section-head`
  dividers (Protection · Defense & intelligence · Analytics), and animated
  disclosure cards with emoji icons and status pills (the shield on/off pill, a
  live badge on network hardening). Every live HTMX behaviour is unchanged — the
  status hero and Aegis strip still auto-poll, the settings form still saves in
  place, and each section body still refreshes on its own trigger. Pure
  markup/CSS reuse of the existing admin stylesheet (no new CSS, CSP-safe).

## [3.15.14] — 2026-07-24

This release is the VayuShield indexing + performance hardening track (phases
1–5): the WAF no longer de-indexes a site, verifies crawlers by identity instead
of a spoofable User-Agent, is faster on the hot path, is safe in a Tor Space, and
now ships gently enabled by default.

### Added
- **Tor Space never locks visitors out, and a boot-time SEO canary (VayuShield
  hardening, phase 5/5).** A Tor Space serves a plain-http `.onion` where the
  browser leaves `window.crypto.subtle` undefined, so the proof-of-work challenge
  solver cannot run — any challenge there would lock **every** human out. The
  shield now never issues a browser challenge (and never engages Sovereign Surge)
  in `OnionMode`; it fails open to content while the non-crypto gates (blocklist,
  reputation jail, rate limit, load-shed) still enforce. Separately, a startup
  **SEO canary** drives synthetic Googlebot / Bingbot / GPTBot / PageSpeed
  requests through the live shield and logs a loud error if any is met with a
  challenge/403 instead of content — so a de-indexing regression is caught at
  deploy, not weeks later through lost rankings.
- **Verified-bot engine — crawlers are authenticated by identity, not a
  spoofable User-Agent (VayuShield hardening, phase 2/5).** New
  `internal/vayushield/verifiedbot` confirms a request really is the search
  engine / AI system it claims before granting the gate-0 SEO fast path, the way
  Cloudflare's "verified bots" work — self-hosted in the binary. Two tiers, never
  UA-alone: (A) the client IP is matched against the vendor's **published
  IP-range JSON** (Google, Bing, DuckDuckGo, Apple, OpenAI, Anthropic,
  Perplexity), loaded into an in-memory longest-prefix set, refreshed daily and
  cached to disk so it works immediately after a restart and offline; (B)
  **forward-confirmed reverse DNS** (reverse-lookup → vendor PTR suffix →
  forward-resolve back to the same IP) for vendors with no feed (Yandex, Baidu,
  Amazonbot, Petal, Seznam) and as a backstop, run on a bounded background worker
  pool so it never blocks a request. A 256-shard verdict cache keeps the hot path
  to one lock-free map read. The registry is deliberately broad — every major
  search engine and AI system, plus link-preview and uptime/PageSpeed bots — so
  no legitimate crawler a site depends on is ever challenged. Verification
  degrades **SEO-safe**: a crawler we cannot confirm (feed not yet loaded, or a
  UA-only vendor) is still allowed, because a fetch failure on our side must never
  de-index a real crawler.

### Changed
- **VayuShield now defaults ON (gentle).** After the hardening in this release —
  crawlers are never de-indexed, verified by IP not UA, hot-path optimised,
  Tor-safe — the shield ships enabled by default: bot **classification** is active
  so real browsers pass silently and verified search/AI crawlers are fast-pathed,
  while the aggressive resilience gates (tarpit, rate-limit, load-shed,
  auto-block, under-attack, surge) stay **off** until you opt in. The panel toggle
  is authoritative; `VAYUSHIELD=off` force-disables for a quick rollback.
- **Broader, current crawler recognition + the "go dark" switch no longer breaks
  the operator's own tooling (phase 4/5).** The classifier's static bot database
  now recognises the current fetcher variants and more of the market: Google
  (`GoogleOther`, `AdsBot-Google`, `APIs-Google`, `FeedFetcher-Google`,
  `Google-Read-Aloud`), Bing (`msnbot`), Baidu, Petal, Seznam, Naver, Sogou,
  Qwant, DuckAssistBot, more Yandex variants, link-preview bots (Discord,
  Telegram, WhatsApp, Pinterest, Reddit) and AI systems (`OAI-SearchBot`,
  `Claude-SearchBot`, `claude-code`, `Meta-ExternalFetcher`, Cohere, You.com,
  Mistral, Diffbot). **R8:** the "Search engine & AI crawler access → go dark"
  switch now exempts operator tooling — PageSpeed/Lighthouse, GTmetrix,
  UptimeRobot, Pingdom, StatusCake, Site24x7 are never `403`'d, so going dark
  hides the site from indexers without breaking the operator's own performance
  scoring or uptime dashboards.

### Performance
- **VayuShield hot-path hardening — lower TTFB under load (phase 3/5).** Three
  changes remove per-request cost so a busy blog stays butter-smooth:
  - The L2 fair-shed pre-filter computed a fresh `"g/1.2.3.0/24"` string and
    re-hashed it on **every** non-verified request just to key the subnet sketch.
    It now hashes the network group directly from the address bytes
    (`subnetHash`) — **zero allocation** on the hot path (guarded by an
    `AllocsPerRun` test).
  - The TLS-fingerprint capture store used a single global mutex, serialising
    exactly the traffic a flood produces; it is now sharded into 64
    independently-locked stripes (matching the reputation/signature caches).
  - Auto Sovereign Surge is now debounced with a hysteresis band
    (`surgeHysteresis`): it engages only after the L0 lane stays ≥90% full for a
    short dwell and relaxes below 75%, so a brief legitimate spike (a post going
    viral) never trips a site-wide browser check for real readers. A sustained
    flood still engages it, and recognised crawlers bypass surge entirely (phase
    1). The 5-second tarpit remains off by default.

### Security
- **A spoofed crawler UA no longer earns a free pass (VayuShield).** A request
  whose User-Agent claims a crawler from an IP that is **not** the vendor's real
  network (checked against the published ranges + reverse DNS above) is flagged a
  spoof suspect: it is denied the gate-0 fast path AND stripped of the classifier's
  good-bot allow, so it falls to behavioural scoring and is challenged like any
  other unproven client instead of being trusted on its UA string alone. A
  genuine crawler is verified upstream and never affected. In a Tor Space the feed
  fetches are refused (no clearnet egress), so the engine degrades to
  UA-recognition without leaking a DNS/HTTP call.
- **Session/auth cookies now enforce `HttpOnly` at the chokepoint (CodeQL
  hardening).** `auth.WriteSecureCookie` forces `HttpOnly=true` so a session or
  auth cookie routed through it can never accidentally be script-readable, even
  if a caller forgets. The one cookie that MUST be readable — the double-submit
  `vp_csrf` token, whose whole purpose is to be echoed back in the
  `X-CSRF-Token` header — moved to a dedicated `auth.WriteReadableCookie`, so the
  readable exception is explicit and auditable in one place. (The remaining
  CodeQL "Secure not set to true" note is a required Tor-mode design exception: a
  `Secure` cookie is dropped by the browser over the plain-http `.onion`, so it
  is intentionally request/host-aware via `CSRFCookieSecure`, ADR-0141.)

### Fixed
- **VayuShield no longer de-indexes the site: recognised search/AI crawlers take
  an SEO fast path before every availability gate (VayuShield hardening, phase
  1/5).** The shield only decided "this is Googlebot, allow it" at classification
  (gate 5), but every earlier gate — blocklist/reputation jail, load-shed,
  fair-shed, the per-IP rate limit (which a fast crawler on a concentrated IP
  range trips → 429 + auto-jail), and **Sovereign Surge** (auto-engaged with zero
  config at ≥90% L0 saturation, serving a JavaScript proof-of-work 503 that a
  non-JS crawler can never solve) — ran first and keyed only on `!verified`.
  A crawler carries no signed session, so it was treated as an anonymous
  stranger and met with a 429/503 or the "verify your browser" interstitial
  instead of the page; Google/Bing read a sustained non-200 (or a `noindex`
  challenge body) on a content URL as a crawl error and drop it. A new gate-0
  fast path serves a recognised crawler (Googlebot, Bingbot, Applebot,
  DuckDuckBot, GPTBot, ClaudeBot, PerplexityBot, PageSpeed/Lighthouse, uptime
  monitors, …) the real page before any of those gates and before surge. Phase 1
  recognises by User-Agent only and is used **exclusively to loosen** (never to
  block); phase 2 will confirm identity by published-IP-range + forward-confirmed
  reverse DNS so a spoofed crawler UA cannot ride the fast path.
- **`robots.txt` / `sitemap.xml` / feeds are never shed by the L0 sovereignty
  lane.** A 503 on `robots.txt` pauses all crawling and a 503 on `sitemap.xml`
  drops URLs; these machine endpoints now join the always-admit priority lane
  (matching the shield's own bypass) so a public-traffic spike can never return a
  non-200 for them.

## [3.15.13] — 2026-07-24

### Security
- **No DNS leak from the SSRF pre-flight in a Tor Space (ADR-0141).** The
  `safefetch` host guard resolved the request host (a DNS query) before the
  dial-time egress guard refused the connection — so in a Tor Space a clearnet
  fetch leaked the DNS query, and the intent to contact that host, to the system
  resolver even though the connection itself was blocked. The host guard now
  refuses a clearnet host BEFORE any lookup. (Audited the other resolvers too:
  the mail DNS/deliverability checks already redirect away in Tor mode, and
  outbound MX resolution never runs under the local-only deliverer.)

### Added
- **Opt-in: route Tor-Space egress THROUGH Tor instead of blocking it
  (ADR-0143).** By default a Tor Space refuses every clearnet connection. Setting
  `VAYUTOR_ROUTE_EGRESS=1` with `VAYUOS_TOR_SOCKS_ADDR` pointing at a Tor SOCKS
  proxy instead routes outbound clearnet through Tor — features (update checks,
  AI, etc.) keep working while the real IP stays hidden and hostnames resolve
  remotely inside Tor (no DNS leak). Fails safe: with no usable SOCKS proxy it
  stays in block mode and never leaks. The self-audit shows which mode is active.
  New `safefetch.SetTorEgressDialer()` / `TorEgressActive()`.
- **Process-wide egress tripwire in a Tor Space (ADR-0141).** As a
  belt-and-suspenders over the per-call-site guards, a Tor Space now installs a
  guarded `http.DefaultTransport`, so even a caller that bypasses `safefetch`
  (`http.DefaultClient`, a third-party library, or future code) cannot dial
  clearnet from the onion server's real IP. Every refused dial is counted; the
  count is surfaced in the anonymity self-audit (zero is expected — a rising
  count means a feature keeps trying to reach the internet and is being
  stopped). New `safefetch.BlockedClearnetCount()` / `GuardedDefaultTransport()`.
- **Anonymity self-audit on the Spaces page (ADR-0141).** A Tor Space now shows
  a live, honest report of its anonymity posture: which anti-leak controls are
  active (clearnet egress disabled, loopback-only bind, external images blocked),
  which residual risks remain (a configured external SMTP relay, a leftover
  clearnet domain), and — deliberately — the things software cannot do for you
  (use Tor Browser; your content can still identify you; no tool makes anyone
  "100% anonymous"). The same summary is logged at boot, and any control found
  "at risk" is logged as an error. New `internal/anonaudit` package (pure,
  tested); surfaced via `/os/spaces` and the boot log.

### Security
- **Tor Space anti-leak now covers the AI-generate and external-SMTP paths
  (ADR-0141).** The Tor-Space egress kill-switch already closed every
  `safefetch` call site, but author-triggered AI generation used a bare
  `http.Client` and the external SMTP relay dialed directly — either would reach
  a third party (AI provider, SMTP relay) from the onion server's real IP in a
  Tor Space. AI generation now routes through the SSRF-hardened, egress-guarded
  transport, and the external SMTP relay is refused (before any dial) whenever
  the guard is engaged and the relay host is not loopback. Adds
  `safefetch.ClearnetBlocked()` / `IsLoopbackHost()` for non-HTTP callers.
- **Plugin-registry downloads and payment-gateway fallback clients now route
  through the guarded transport too (ADR-0141).** The plugin registry fetched a
  registry-supplied `download_url` with a bare `http.Get` — an SSRF vector and a
  Tor-Space clearnet leak; it now uses the SSRF-hardened, egress-guarded
  transport (the SHA-256 integrity check is unchanged). The Stripe/PayPal client
  constructors' nil-client fallback no longer defaults to `http.DefaultClient`
  (which bypassed the guard) but to the same guarded client.

## [3.15.12] — 2026-07-24

### Security
- **Managed-Tor expert bundle is integrity-checked before it is executed (audit
  L3).** VayuTor downloaded the Tor expert-bundle tarball and ran the `tor`
  binary (and any bundled `.so`) with no checksum or signature verification —
  fine against an ordinary network attacker (the download hosts are fixed HTTPS
  constants) but not against a TLS compromise. The tarball is now buffered and
  SHA-256-hashed BEFORE anything is written or made executable: set
  `VAYUTOR_BUNDLE_SHA256` (the digest from the Tor Project's signed `sha256sums`,
  obtained over a trusted channel) to pin it and a mismatch aborts; unpinned, the
  observed digest is logged so it can be pinned, and `VAYUTOR_REQUIRE_VERIFIED_BUNDLE=1`
  refuses any unpinned bundle. A bundle containing no `tor` binary (lone `.so`
  files) is now rejected without extracting anything.
- **PGP private keys are no longer encrypted at rest with a key derived from the
  wire-exposed API key (audit M8).** The AES-256-GCM key sealing every user's PGP
  private key was `sha256("…" || API_KEY)` — coupling the confidentiality of the
  most sensitive material in the system to an authentication credential that
  travels on every admin/REST call, and freezing it so rotating the API key would
  brick every stored key. The keystore now uses envelope encryption: a random,
  persistent Data Encryption Key seals the private keys, and that DEK is wrapped
  by a dedicated Key Encryption Key from `VAYU_SECRET` (else a host-bound
  `.vayupgp-kek` file). On first boot an existing store migrates transparently —
  the DEK is set equal to the old API-key-derived key, so every stored key still
  decrypts with zero re-encryption — after which the API key can rotate freely and
  the KEK can be rotated in place. With neither `VAYU_SECRET` nor a writable KEK
  file, the DEK is stored unwrapped and the server warns loudly.
- **Service-credential encryption key moved out of the database by default
  (audit L6).** With no `VAYU_SECRET` configured, the Data Encryption Key that
  seals every stored service credential (Stripe/PayPal/BTCPay keys, webhook
  signing secrets) was written as plaintext hex in the same SQLite DB as the
  ciphertext — so a single DB read recovered every secret. The DEK is now wrapped
  by a host-bound key file kept beside the database (0600, `VAYU_SECRET_KEK_FILE`
  to override), mirroring the CSRF secret, so a database-only read no longer
  yields the key. A legacy plaintext DEK is transparently migrated into the key
  file on next boot; if no key file can be created the store keeps working but
  warns loudly that the at-rest key lives in the DB. `VAYU_SECRET` remains the
  strongest option and is unchanged.
- **OAuth/MCP access tokens now expire and refresh tokens rotate with reuse
  detection (audit L9).** Minted access tokens carried no expiry and the token
  response omitted `expires_in`, so a leaked connector token was a permanent
  full-admin credential and clients never rotated. Access tokens are now minted
  with a bounded 1-hour lifetime and the response advertises `expires_in`; each
  refresh rotates the underlying key AND resets its short expiry. Refresh tokens
  gain a finite lifetime (30 days) and OAuth 2.1 reuse detection: a rotated-out
  refresh token that is replayed is treated as a breach and revokes the entire
  grant (all refresh tokens for the key plus the access key itself). Legacy
  tokens issued before this change keep working until their next refresh.
- **OAuth authorization-server metadata no longer trusts an arbitrary request
  Host (audit I1).** The advertised `issuer`/endpoints derived their host from
  the raw request `Host` header. The host is now trusted only when it resolves to
  a domain this install actually serves (keeping per-domain issuers correct on a
  multi-domain install); otherwise it falls back to the configured canonical
  domain, so an injected `Host`/`X-Forwarded-Host` cannot steer the reflected
  metadata. (The scheme was already fixed via `seo.Origin`.)

## [3.15.11] — 2026-07-24

### Changed
- **Every same-policy cookie now flows through one `WriteSecureCookie` chokepoint.**
  Extending v3.15.10, the member-session, operator-session, and CSRF cookies across
  the app and the auth package now set their `Secure` flag from a single
  `auth.WriteSecureCookie` helper instead of each handler repeating the
  determination. Net effect: the static-analysis "Cookie 'Secure' attribute is not
  set to true" surface drops from ~18 duplicated sites to a single correctly
  request/host-aware site (plus the Tor-world proxy's two cookies, which are
  intentionally per-request `Secure: r.TLS != nil` because that proxy serves both
  the clearnet-HTTPS and http-`.onion` faces). `Secure` remains conditional by
  necessity — a hard-coded `true` would be dropped by the browser over the
  plain-http `.onion` and break Tor mode (ADR-0141).

## [3.15.10] — 2026-07-24

### Security
- **Member, portal, and logout cookies are now Tor-Space aware.** Four session
  cookies set their `Secure` flag from a raw `Domain != "localhost"` check that
  did not consult OnionMode, so on a plain-http `.onion` (Tor Space) they forced
  `Secure` on and the browser silently dropped them — breaking member sign-in and
  logout in the Tor world. They now use the same request/host-aware source as the
  CSRF cookie (`auth.CSRFCookieSecure`: off on the http `.onion` and localhost, on
  for clearnet HTTPS).

### Changed
- **The double-submit CSRF cookie is now written from a single helper.** The
  identical `vp_csrf` cookie-set block was duplicated across ~15 handlers; it is
  now one `setCSRFCookie` helper, so the cookie attributes (including the
  Secure-flag policy) are defined in exactly one place. This also collapses the
  duplicated code that a static analyzer flagged ~14 times as "Cookie 'Secure'
  attribute is not set to true" down to a single, correctly request-aware site
  (Secure cannot be a hard-coded `true` — an http `.onion` and localhost require
  it off).

## [3.15.9] — 2026-07-24

### Security
- **Operator custom theme CSS can no longer reach an external origin.** The
  clearnet CSP keeps `img-src 'self' data: https:` so authors can hotlink content
  images, which meant a `background:url(https://…/beacon)` (or an `@import`) in
  operator-supplied custom CSS would load from an external origin — a
  tracking/deanonymisation beacon on public signup/member/legal pages, contrary to
  the "served same-origin, self-contained" promise. `@import` and external/
  protocol-relative `url()` are now stripped from custom CSS at render time
  (`data:`, relative and same-origin `url()`s are preserved), and the operator UI
  copy is corrected to describe this.

## [3.15.8] — 2026-07-24

### Security
- **The operator password-change form is now CSRF-protected.** `/os/change-password`
  relied on `SameSite=Strict` alone, so a foothold on a same-registrable-site
  subdomain could auto-submit a silent password reset. The form now carries a
  double-submit CSRF token (host-only cookie a subdomain cannot read) validated by
  `CSRFTokenMiddleware`.
- **Two more session-authenticated `/os` mutations now require a CSRF token.**
  `/os/api/search/reindex` and `/os/api/feed/regenerate` were registered without
  CSRF middleware, relying on `SameSite=Strict` only; both are now wrapped
  (API-key callers are exempt as before, and neither has a browser-form caller —
  the panel's regenerate button targets `/os/api/seo/regenerate`).

## [3.15.7] — 2026-07-24

### Security
- **The public GraphQL endpoint now enforces a query-cost limit.** `/api/v1/graphql`
  is unauthenticated and some resolvers load up to 1000 rows each, so a single
  64 KiB document that aliased an expensive root field hundreds of times could fan
  out into ~10⁶ database resolver calls and saturate the connection pool. Queries
  are now parsed and rejected before execution when their total field-selection
  count (200) or nesting depth (12) exceeds a bound — a real query uses a handful
  of fields at shallow depth.
- **Member mailbox claiming is now atomic (no entitlement bypass).** The
  "one included mailbox per member" rule was a read-then-act check, so a member on
  a single-mailbox tier could fire N parallel claims with different localparts and
  each would provision a real, outbound-capable mailbox. The slot is now reserved
  with a single conditional `UPDATE … WHERE mail_address IS NULL/''` before any
  mailbox is created, so exactly one claim wins; a provisioning failure releases
  the reservation for a clean retry.

## [3.15.6] — 2026-07-24

### Security
- **Image uploads are guarded against decompression bombs.** `imageproc.Optimize`
  decoded PNG/JPEG uploads directly and only measured dimensions *after* the full
  decode, so a tiny file declaring e.g. 25000×25000 allocated a multi-GB bitmap
  and OOM-killed the single-process binary (an author-reachable, unauthenticated-
  via-remote-import DoS). It now checks the declared dimensions via
  `image.DecodeConfig` first and refuses anything beyond a 40-megapixel budget
  (int64 math, no overflow) before decoding.
- **VayuMail/member login no longer leaks account existence via timing.**
  `VerifySecretArgon2id` returned in microseconds for an empty stored hash (a
  lookup for a non-existent/inactive account) while a real account ran the full
  ~tens-of-ms Argon2id — a timing oracle for enumerating valid mailboxes. It now
  verifies an empty hash against a fixed decoy so both paths spend equal time and
  always fail, closing the oracle for every caller (portal login and scoped
  credential verification alike).

## [3.15.5] — 2026-07-24

### Fixed
- **CI: removed a newly-unreachable function** (`safefetch.ClearnetEgressBlocked`)
  that failed the deadcode gate in the go-native job. The egress kill-switch keeps
  its setter (`SetBlockClearnetEgress`); the unused accessor is gone.

### Security
- **Payment fulfilment is now atomic (no double-provisioning).** `MarkPaid` read
  the order status and then issued an unconditional `UPDATE`, so the browser-return
  and a gateway webhook (or a gateway retry) could both observe "pending" and both
  fulfil — duplicate receipts, duplicate `payment.completed` events, and a
  subscription started twice. The transition is now a conditional
  `UPDATE … WHERE status<>'paid'` that acts only when exactly one row flips;
  otherwise it returns `ErrAlreadyPaid`.
- **Magic-link login is now atomic single-use.** `ConsumeLoginToken` deleted the
  token unconditionally and returned the email regardless of whether that call
  removed the row, so two concurrent verifies of a captured link both minted a
  session. It now requires the `DELETE` to have removed exactly one row, so a
  captured link can never be replayed into a second session.

## [3.15.4] — 2026-07-24

### Security
- **WKD external-key lookup is now SSRF-hardened.** The Web Key Directory
  discovery fetch used a default HTTP client that would follow redirects and
  reach any address a crafted recipient domain resolved to (loopback, cloud
  metadata). It now uses the same resolve-and-pin `safefetch` transport as every
  other outbound fetch (refuses private/reserved IPs, no proxy) and refuses
  redirects.
- **Outbound webhooks can no longer reach internal services.** Webhook delivery
  rode the shared outbound client, whose loopback allowlist (for local
  Meilisearch/Ollama) let a webhook URL target `127.0.0.1`/private ranges. The
  webhook store now uses its own strict SSRF transport with no allowlist, screens
  private/reserved IP-literal targets at registration, and offers an explicit
  `AllowHosts` opt-in for an operator who deliberately points a hook at a local
  receiver.
- **OAuth/MCP consent now shows where the authorization code will be sent.** The
  consent screen displayed only the client's self-declared name (dynamic
  registration is unauthenticated, so a client can call itself "Claude"); it now
  prominently shows the exact **redirect destination host** and warns that the
  app name is unverified — so an operator can't be phished into delivering a code
  to an attacker's host.
- **OAuth metadata issuer no longer trusts a client header.** The
  discovery/issuer origin is derived from the host via `seo.Origin` (https for a
  clearnet domain, http only for a `.onion`) instead of a spoofable
  `X-Forwarded-Proto`.

## [3.15.3] — 2026-07-24

### Security
- **Inbound SMTP no longer auto-creates a mailbox for unknown recipients.** Port
  25 accepted mail for any local-part on a served domain and delivery created a
  Maildir per address, so a single unauthenticated connection could spray
  hundreds of thousands of directories/inodes (a whole-install disk-exhaustion
  DoS and a silent mail sink). RCPT now requires the address to map to a real
  provisioned mailbox or alias (`550 5.1.1` otherwise), and envelope recipients
  are capped per transaction.
- **The IMAP FETCH MIME parser is now depth- and size-bounded.** A crafted
  message with deeply chained multipart containers made the FETCH parser recurse
  without limit, so one stored ≤25 MiB message could OOM / stack-overflow the
  whole single-binary process on the victim's next `FETCH` — a persistent,
  unauthenticated remote DoS. The parser now stops at `maxMIMEDepth`, caps the
  total part count, and bounds each part's retained bytes.
- **Authenticated submission now binds the sender to the login.** On 587, an
  authenticated mailbox user could set `MAIL FROM`/`From:` to another local
  mailbox (e.g. an intern sending as the CEO) — convincing intra-server
  spearphishing. Submission now refuses an envelope sender that is a *different*
  local mailbox the authenticated account does not own; own addresses, owned
  aliases, and external identities are unaffected (the guard fails open on any
  ambiguity so legitimate delivery is never broken).
- **Client-submitted (587) mail is now DKIM-signed.** Previously only webmail
  Compose and the autoresponder signed; 587 (mobile app / Thunderbird) traffic
  was relayed unsigned, leaving DMARC alignment on SPF alone. Submission now
  DKIM-signs with the sender-domain key (best-effort; a signing failure relays
  unsigned rather than bouncing).

## [3.15.2] — 2026-07-24

### Security
- **Central egress kill-switch for a Tor Space (ADR-0141 anti-leak).** A Tor
  Space must make no clearnet callback, yet several outbound paths were not
  OnionMode-gated — most importantly outbound **webhooks**, which fired on
  publish/payment events and opened a direct clearnet connection from the onion
  server's real IP, letting the receiver (or a passive observer) correlate the
  anonymous onion's timeline with that IP. Egress is now blocked in two places:
  `dispatchWebhook` short-circuits in OnionMode, and — the real chokepoint — the
  shared `safefetch` dialer refuses **every** non-allowlisted (public) host when
  OnionMode is armed at boot. That single guard fails closed for webhooks, remote
  image import, embed unfurl, and any future `safefetch` caller (loopback
  services like Meilisearch/Ollama still work).
- **Onion CSP now strips every external origin, not just images.** `applyOnionCSP`
  is the single CSP chokepoint: in a Tor Space it removes every `https://…`/
  `http://…` source and the bare `https:` scheme from **every** directive, and
  `BuildAdCSP` now routes through it — so a video-embed `frame-src` or a Google
  AdSense origin can no longer appear in a Tor Space's policy and leak a reader's
  IP. Google AdSense injection is also disabled outright in OnionMode (no loader,
  no unit); self-hosted/house ads are unaffected.
- **Auth/CSRF cookie `Secure` flag is OnionMode-aware.** On the plain-http
  `.onion` a Secure cookie is silently dropped by the browser (breaking sign-in
  and CSRF); OnionMode now forces `Secure` off for the auth/CSRF cookies, instead
  of relying on a global override that also weakened clearnet.

## [3.15.1] — 2026-07-24

### Security
- **VayuOS `/os/api/*` authorization is now fail-closed.** The console session
  guard (`osPathMinLevel`) previously granted *author* level to any API area not
  explicitly listed as admin, so several sensitive `/os/api/*` endpoints
  inherited low-privilege access. Any un-enumerated `/os/api/*` path now requires
  **admin** by default; only an explicit allowlist of author/editor-safe areas
  (content authoring, self-service 2FA, chat, dashboard) opens lower. This closes
  a privilege-escalation class in which a low-privilege author session could
  reach the payment-gateway connect, order-ledger, premium mail-ID, domain, and
  credential-reveal endpoints. Belt-and-suspenders `admin` guards were also added
  directly to the credential save/reveal/delete handlers. A regression test now
  fails the build if any future `/os/api/*` endpoint is added without a level.
- **The internal/system API key can no longer be rotated through the API
  surface.** `Store.Rotate` now refuses the reserved internal key with
  `ErrInternalProtected` — matching `Revoke`/`Delete`/`SetActive` — so a rotate
  (which returns the fresh secret) can no longer be used to mint an unconditional
  superuser token. It is refreshed only by `EnsureInternal` at boot.
- **API-key lifecycle mutations require an administrator or a superuser key.**
  Rotate, revoke, delete, and activate/deactivate now reject a scoped
  (non-superuser) key caller, so a routine `settings:write` integration key can
  no longer revoke the operator's superuser keys or rotate keys it does not own.

## [3.15.0] — 2026-07-21

### Fixed
- **Clearnet↔Tor world switch (and logout/login) now work in the installed app.**
  The PWA service worker intercepted page navigations with a plain `fetch(req)`,
  which follows a server redirect and returns a `redirected` response — and the
  browser refuses to let a service worker back a navigation with one, so the
  navigation silently failed. Because entering the Tor world ends with a redirect
  (`/os/world` → `/os`), the toggle appeared dead on the installed PWA (it started
  the world but never entered it); logout and login redirects were hit the same
  way. The worker now fetches navigations with `redirect: 'manual'`, so a server
  redirect comes back as an opaque redirect the browser follows itself. Still
  zero-cache.

## [3.14.99] — 2026-07-21

### Added
- **New mail now shows in the topbar notification bell.** The bell only surfaced
  mail-device approvals; it now includes a **"New mail — N unread …"** row (an
  admin sees the total across every mailbox on the primary and each mail-enabled
  secondary domain; a staff member sees only their own), and its badge counts the
  unread mail. Clicking the row opens the mailbox. The poller also **live-bumps
  the bell badge** the moment new mail arrives, so the count rises without a
  reload — the same signal that raises the desktop notification. This makes new
  mail visible even where OS notifications are blocked or unavailable (denied
  permission, or the http `.onion`).

### Changed
- **Unread counts are now readdir-only.** The bell and the new-mail poll no longer
  read every message file to count unread — new `Maildir.Counts` /
  `Engine.Summaries` / `Engine.SummariesForDomain` tally `new/`+`cur/` by
  directory listing alone, so surfacing unread mail on every page render and on a
  short poll stays cheap even for large mailboxes.

## [3.14.98] — 2026-07-21

### Changed
- **New-mail notifications now fire through the service worker** — so they work
  in the installed VayuOS app (PWA) and on Android/mobile browsers, where the
  page `new Notification()` constructor is unsupported. The worker's new
  `notificationclick` handler focuses an existing console window (or opens a new
  one) and steers it to the mailbox, so a single tap lands on the right mailbox
  even when no tab was open. Desktop browsers without an active worker still use
  the page Notification API; the http `.onion` still falls back to a clickable
  toast. The worker stays zero-cache.

## [3.14.97] — 2026-07-21

### Added
- **New-mail desktop notifications in the console.** VayuOS now watches every
  mailbox you can access and raises a desktop notification the moment new mail
  arrives in ANY of them — even while you're on another console page — and
  **clicking the notification opens that mailbox directly**. A new read-only
  endpoint `GET /os/vayumail/unseen` returns per-mailbox unseen counts (an admin
  sees every mailbox across the primary and any mail-enabled secondary domain; a
  staff member sees only their own assigned mailbox — the same scoping as the
  mailbox pages), and `admin-os.js` polls it, diffing counts to fire the alert.
  The first poll only records a baseline, so already-unread mail is never
  announced on load. Desktop notifications require a secure context (HTTPS); on
  the http `.onion` (Tor) the browser API is unavailable, so it degrades to a
  clickable in-app toast plus an unread badge in the tab title — both of which
  also open the mailbox in one click.

## [3.14.96] — 2026-07-21

### Changed
- **VayuOS is never cached — always live, on every device.** The console's
  service worker is now a zero-cache worker: it never stores a single console
  response and, on upgrade, purges every cache a previous worker version may have
  created, then takes control of open pages immediately. A stale install self-
  heals, and a themed change or an update is visible the instant the server serves
  it. The registration also reloads the page once when a new worker takes control.
  (Console HTML was already `no-store`; static assets stay content-hash-versioned.)
- **Higher text contrast across the whole console.** The two muted text tiers of
  the dark theme (secondary and helper/placeholder copy) were lifted to meet AA
  against every dark surface, so body text, hints, placeholders and editor helper
  panels are easier to read app-wide — the sidebar and headings are unchanged.

### Added
- **PWA install is showcased on the site and README.** The marketing site gains a
  one-click "Install VayuOS" section (with a device mock) and a nav entry, and the
  README documents installing the console as an app — so operators can put VayuOS
  on a home screen or desktop in one click.

## [3.14.95] — 2026-07-21

### Added
- **Install VayuOS as an app (PWA).** The console now ships a web-app manifest
  (start_url /os, standalone), a privacy-first service worker scoped to /os/
  (pages always come from the network — never a cached, per-operator dashboard —
  and only the versioned static shell is cached, so the installed app opens
  instantly), and app icons built from the official VayuPress mark. A one-tap
  **Install** button appears in the topbar when the browser reports the app is
  installable (Chrome/Edge/Android); on iOS it shows the "Add to Home Screen"
  hint. Works on desktop and mobile.

### Fixed
- **Update "checksum error" is now diagnosable.** When a proxy/CDN hiccup between
  the server and GitHub returns an HTML/JSON error page (or an empty body) instead
  of the release binary, the updater reported a cryptic "checksum mismatch". It now
  detects that and says it is a transport problem (with the hosts to allow), and a
  genuine mismatch explains it likely means a corrupted/intercepted download.
  Checksum parsing also now tolerates a combined SHA256SUMS listing, not only a
  per-binary `.sha256`.
- **Clearer Clearnet⟷Tor switch feedback.** The world switch now surfaces the
  server's actual reason on failure (e.g. settings unavailable, HTTP status)
  instead of always blaming "administrator only" — so a failure is diagnosable on
  mobile where there is no console to inspect.

### Changed
- **Update & Backup page aligned to the Monetization style.** The page now leads
  with a `page-header` + one-line `page-sub`, then an **"Install an update"**
  section header over the one-click install card (its Installed → Latest → Status
  tiles read as the overview), and a **"Backup & history"** section header over
  the collapsible Backup / Update-history accordions — the same layout language as
  the Monetization and VayuMail pages.

## [3.14.93] — 2026-07-21

### Changed
- **VayuMail redesigned to the premium Monetization style — every page.** The
  whole VayuMail console now speaks the same design language as the Monetization
  page: a clean `page-header` + one-line `page-sub`, a premium **segmented pill
  tab bar** for the sub-nav, **lifting stat cards** for the metric strips, and
  **section headers** with hints grouping each page's content.
  - **Overview** is rebuilt as a flagship: stat grid (mailboxes · domains ·
    queued · failed · delivered) → "Your mailbox" workspace cards (Inbox,
    Compose, Sent, Connect) → "Infrastructure" → "Health & status".
  - **DNS, Security, PGP Keys, Connect** group their records/checks/keys under
    section headers, with the intro moved to a `page-sub`.
  - **Accounts, Mailbox, Outbox, Compose** adopt the same header shell; Accounts
    gains "Add a mailbox" / "Mailboxes" section headers.
  One shared restyle of the tab bar and stat strip lifts all nine pages at once.

### Added
- **Premium cartoon avatars.** The prebuilt avatar generator (members and
  mailboxes) was redesigned from flat dot-eye faces to polished portraits:
  gradient backgrounds with a soft sheen, real eyes with catch-lights, shaded
  skin/hair, and distinct styles (glasses, beard, beanie, bun, afro, long/wavy
  hair). Still deterministic per seed and fully self-contained SVG (internal
  gradient paint only — no script, no external fetch), so it stays CSP-safe as
  an `<img>` and works in the Tor world too.
- **VayuTalk shows real avatars.** The conversation list and thread header now
  show a contact's actual mailbox picture when one exists (your own identity and
  any local mailbox), falling back to coloured initials otherwise. Same-origin
  only — no external callback — so it is safe in the Tor world.
- **VayuTalk new-message notifications (web).** A peer's message you are not
  looking at flashes the tab title and, when the tab is backgrounded and
  permission was granted, raises a desktop notification (sender name only, never
  the message text). Over an http onion, notifications degrade gracefully to the
  title badge.

### Changed
- **VayuTor page redesigned** to the premium Monetization style: a stat-grid
  overview (status · Tor visits · onion count · network), a clean one-click
  control card, and the advanced controls (bridges, custom address, hardening,
  health, page stats, privacy) grouped into collapsible accordions with live
  status chips.

## [3.14.91] — 2026-07-21

### Added
- **VayuTalk: keep a chat so its id survives a reload.** Conversations are
  ephemeral by default (a reload wipes them — no message ever touches disk), but
  the thread header now has a **Keep** pin: pin a contact and its row is restored
  the next time you open VayuTalk. Only the address you chose to keep is
  remembered — never any message. Kept rows show a small 📌 in the list.
- **VayuTalk: show a name, not the raw mail id.** Once a chat is added, the list
  and thread header show a friendly name derived from the address (`john.doe@…`
  → “John Doe”) instead of the full address, and a **Rename** button lets you set
  any name you like. The full address is only ever revealed inside the collapsible
  verify panel, where it is needed to compare safety numbers.
- **VayuMail: pick a prebuilt avatar for a mailbox.** The Accounts avatar section
  now offers eight one-click cartoon avatars (seeded per address, like the member
  picker) so a mailbox can have a picture without uploading a file. Rendered from
  a same-origin preview endpoint and stored as a self-contained SVG.

## [3.14.90] — 2026-07-21

### Added
- **VayuTalk: verify your contacts (safety numbers).** The chat rail now has a
  collapsible **"Your safety number"** panel — the same value the mobile app
  shows. Read it to a contact (or compare in person); if you both see the same
  number your chat is end-to-end encrypted with no one in the middle. One-click
  copy included.
- **VayuTalk: search your conversations.** A search box filters the conversation
  list as you type (client-side, instant).

### Changed
- **VayuTalk got a clean design pass** in the calm VayuOS style: a proper
  identity card, a labelled Conversations section, a tidy empty state ("No
  conversations yet — start one above"), and refined spacing throughout the
  left rail. No behavioural changes to messaging, timers or Live mode.

## [3.14.89] — 2026-07-21

### Changed
- **VayuOS calmed down — the busy visuals are gone.** Reverted the recent
  "cosmic" styling that read as noise: no more animated background aurora,
  coordinate grid, corner blooms or vignette (the canvas is a clean, flat dark
  navy again); headings are solid, high-contrast text instead of a gradient;
  the accent is back to a single calm **sky-blue** (Tor world stays purple);
  cards are plain panels (no glass glow, no cursor-following halo); primary
  buttons are solid, not gradient. The genuinely useful pieces stay: the
  Clearnet⇄Tor sliding switch with its world icons, the uniform top-bar icons,
  the redesigned Update & Backup page, and the update-ready bell notice.

## [3.14.88] — 2026-07-21

### Added
- **New releases now surface in the notification bell.** A lightweight background
  watcher checks for a newer signed release (every 6 h, clearnet-only — it never
  runs in a Tor Space, and only records a history row when the answer changes).
  When one is available, the topbar bell shows a pulsing **"VayuOS update ready —
  Install vX.Y.Z in one click"** item linking straight to Update & Backup, so you
  no longer have to open that page to know an update landed.

### Changed
- **Update & Backup is a premium, collapsible console.** The software-update
  panel is now a glassy **hero** with a drifting accent aura, an **Installed →
  Latest** version flow with an arrow, and a live status chip (grey → teal
  "Update available" → green "Up to date"). Backup & restore and the long update
  history collapse into **animated accordions** (smooth fade/slide reveal,
  rotating chevron), so the page opens focused on the one action that matters.
  All existing controls and behaviour are unchanged — only the presentation.

## [3.14.87] — 2026-07-21

### Changed
- **Gradient primary buttons.** Primary actions now use a teal→teal-dark sweep
  (violet in the Tor world) with a soft accent shadow and a brighten-on-hover
  lift — the site's CTA feel. White label text stays on the deeper teal stops so
  it remains readable (this also retired a stray sky-blue hover colour).
- **Cursor-follow glow on panels.** Cards, stat tiles and workspace tiles now
  carry a soft accent halo that tracks the pointer as it moves across them — the
  site's hero-card effect. It is pure CSS driven by two custom properties that
  `admin-os.js` updates through the CSSOM (strict-CSP-safe — not an inline style
  attribute), throttled with `requestAnimationFrame`, and skipped entirely on
  touch devices so phones never pay for it.

## [3.14.86] — 2026-07-21

### Changed
- **VayuOS now fully wears the vayupress.com identity — teal → saffron.** The
  accent shifted from sky-blue to the site's **teal**, with **saffron** as its
  gradient partner. Big titles (page + section headers) render as a **teal→saffron
  gradient** (guarded by `@supports`, so unsupported browsers keep solid text);
  primary actions, links, focus rings and card hovers follow the teal accent. In
  the Anonymous Tor world the whole scheme — accent, gradient, blooms — turns
  **violet**, so the two worlds stay unmistakable.
- **A real cosmic background, not a flat fill.** The canvas now carries a teal top
  glow, **saffron and violet corner blooms**, a masked **coordinate grid**, an
  edge vignette, and a slow-drifting **aurora** — the site's futuristic depth,
  pure CSS and CSP-safe (motion is disabled under reduced-motion; light mode gets
  a soft tint instead of a glow).
- **Premium card glass.** Cards gain a hairline top highlight and a soft
  teal-tinted lift/glow on hover, so panels read as glass rather than boxes.

## [3.14.85] — 2026-07-21

### Changed
- **Cosmic canvas + higher-contrast text across VayuOS.** The console background
  now matches vayupress.com more fully: a deep-ink canvas with a top glow, two
  corner accent blooms, an edge vignette, and a slow-drifting **aurora** layer
  behind the content (pure CSS, disabled under reduced-motion). Headings pick up
  the site's tighter tracking.

### Fixed
- **Muted text is now readable in both light and dark themes.** Secondary and
  tertiary text tokens (labels, descriptions, hints, timestamps) were too faint
  against the new darker canvas (dark) and too light on white (light). They are
  retuned to meet AA contrast in both modes, and card borders are a touch
  stronger so panels separate cleanly.

## [3.14.84] — 2026-07-21

### Changed
- **VayuOS wears the vayupress.com skin.** The console now sits on the same deep
  "ink" cosmos as the marketing site — a near-black canvas with two faint accent
  blooms — and every card is a glassy translucent panel (theme-aware: light mode
  keeps solid white cards so nothing washes out).
- **Premium side panel.** Each navigation item carries its own light accent
  colour: on hover the icon lights up in that colour with a soft, icon-shaped
  glow and gently enlarges, and the active item shows a glowing accent rail.
- **Sidebar Clearnet ⟷ Tor switch, redesigned like the site's world toggle.** It
  is now a sliding-thumb pill with the two worlds' real marks — a **globe** for
  Clearnet and the **Tor onion** for Tor. The thumb rides sky-blue under Clearnet
  and springs across to violet when you hover Tor, previewing the switch before
  you commit.

### Fixed
- **Top-bar icons are now uniform and glow in their own colours.** The
  notification bell was rendering larger than its neighbours (its SVG had no
  fixed size) — every top-bar glyph is now locked to 18×18. On hover each icon
  lights up with a soft glow that follows the **glyph's shape** (not a
  rectangular box) in its own premium colour: hamburger sky, feedback amber,
  bell rose, theme violet — and lifts slightly. The same icon-shaped, per-item
  glow is applied throughout the side panel.

## [3.14.83] — 2026-07-21

### Fixed
- **The VayuOS admin JavaScript is now cache-busted — so fixes actually reach
  your browser after an upgrade.** The main `admin-os.js` (and several sibling
  admin scripts) were served with a one-hour `max-age` but **no `?v=` version
  query**, while the stylesheet already had one. That meant an upgraded server
  kept handing the browser the *old* JavaScript: you would see restyled buttons
  (fresh CSS) but the *old behaviour* (stale JS). This is why the previous
  top-bar ☰ fix appeared to do nothing on phones — mobile browsers have no
  easy "hard refresh", so they stayed pinned to the cached script. Every admin
  script now carries a content-hash `?v=` query (matching the CSS), so a new
  release changes the URL and the browser fetches the new code immediately.
  Knock-on effect: the ☰ sidebar collapse **and** the sidebar's Clearnet⇄Tor
  world switch now behave as shipped once you upgrade.

## [3.14.82] — 2026-07-21

### Fixed
- **The VayuOS top-bar ☰ button now collapses the sidebar (desktop) instead of
  freezing the page.** A regression in v3.14.80 (giving `.btn--icon` a flex
  display) unhid the hamburger on desktop, where clicking it ran the *mobile
  drawer* logic and locked page scroll. It is now a proper **premium sidebar
  collapse**: on desktop it smoothly slides the sidebar off-screen and lets the
  content reclaim the full width (state **persisted** across pages, no scroll
  lock); on mobile it opens/closes the slide-in drawer as before. The button
  gets the same brand-glow-on-hover as the other top-bar icons and tints while
  collapsed so its state is legible.

### Changed
- **Monetization is now a clean, premium, collapsible console.** The long
  wall of always-open cards is gone: every payment method and product is a
  **premium accordion** — a summary row (icon · title · one-line description ·
  live **status chip**) that expands, with a smooth fade/slide animation and a
  rotating chevron, to reveal its settings. At a glance you can see which
  gateways are **Connected** vs **Not set up**, how many premium addresses have
  **sold**, how many posts are **priced**, and how many orders are **pending** —
  without scrolling through every form. Grouped under **Payment methods**,
  **Products &amp; pricing** and **Orders** (the ledger stays open by default).
  Fully responsive — the layout reflows and chips tuck away on narrow screens —
  and pure-CSS (native `<details>`, no scripts), so it stays CSP-safe and
  keyboard-accessible.

### Changed
- **Top-bar icons are now uniform and premium.** The three top-bar controls
  (💡 feedback · 🔔 notifications · 🌙 theme) are the same 36×36 size and
  perfectly centred — the feedback control is a link, so it previously rendered
  slightly off — and all three now share a **soft brand glow** (a crisp ring plus
  a coloured bloom) on hover/focus.
- **Crypto setup is no longer a mystery.** The BTCPay card now leads with a
  collapsible **“How to turn this on (self-hosted) — 5 steps”** guide that walks
  through, in order, running BTCPay, copying the Store ID, generating the API key
  with the exact permissions, registering the webhook (with the precise URL and
  event), and testing the connection. So a self-installer can see exactly where
  every value comes from.
- **Premium Monetization payment cards.** Stripe, PayPal and BTCPay now share a
  cleaner framed card with a thin accent bar; the crypto card wears a coin-
  spectrum accent (BTC → XMR → ETH → USDT). Clearer section framing throughout.

### Security
- **Documented the WKD SHA-1 usage** (`internal/vayuos/pgp/wkd.go`): SHA-1 there
  is mandated by the Web Key Directory spec to form the *public* directory key
  from an already-public address — it is not applied to any secret — so a
  static-analysis “weak hash on sensitive data” alert on that line is a known
  false positive. Substituting a stronger hash would break interoperability with
  every other WKD client (GnuPG, Thunderbird, …).

### Added
- **Crypto payments — accept BTC, Monero, Ethereum and stablecoins.** A new
  self-hosted **BTCPay Server** gateway sits alongside Stripe and PayPal in
  VayuOS → Monetization. VayuPress creates an invoice via BTCPay's Greenfield API
  and sends the buyer to BTCPay's own hosted checkout (coin choice, QR and
  on-chain confirmation all happen there); a **HMAC-verified settlement webhook**
  then marks the order paid and provisions the entitlement. Funds settle straight
  into the operator's own BTCPay wallet — **no processor, no custody, no KYC** —
  so **anonymous / Tor buyers can pay without an account**, which the identity-
  and clearnet-bound card gateways can't offer. A **Pay with crypto · BTC · XMR ·
  ETH · USDT** button appears at checkout whenever BTCPay is connected, and the
  post-payment page reflects the on-chain "confirming" state honestly.
  - Config lives in Monetization: server URL + store id (settings) and the
    Greenfield API key + webhook secret (encrypted at rest, AES-256-GCM), with a
    one-click **Test connection** and the exact webhook URL to register in BTCPay.
  - Settlement is verified authoritatively: the signed webhook triggers a
    server-to-server invoice re-fetch, and fulfilment is idempotent (a duplicate
    webhook or a page refresh never provisions twice).

### Security
- New `VayuPGP`-independent payment primitives: a minimal BTCPay Greenfield
  client (`payments.BTCPayClient`) and a constant-time webhook signature verifier
  (`payments.VerifyBTCPaySig`, HMAC-SHA256 over the raw body). The invoice id is
  format-validated before it is ever placed in an API path.

### Changed
- **Feedback now reaches the VayuPress team by default.** The VayuOS feedback
  button (💡) defaults to `feedback@vayupress.com` out of the box, so bug
  reports, improvement ideas and feature requests come straight to the project.
  Operators can still point it at their own inbox from Power & Maintenance →
  Feedback inbox (setting `vayupress.feedback_email`).

### Added
- **Encrypted mail can now carry attachments — and encrypt to multiple
  recipients.** VayuMail's composer previously used *inline PGP*, which encrypts
  only the plain-text body, so it disabled encryption the moment you added an
  attachment, a Cc or a Bcc. The composer now emits **PGP/MIME (RFC 3156,
  `multipart/encrypted`)**, which encrypts the **whole message — body and every
  attachment — to all recipients at once** (To, Cc and Bcc), plus the sender so
  the Sent copy stays readable. This is the same standard Thunderbird, Proton
  Mail and the VayuMail mobile app use, and VayuMail's transparent-decryption
  hook already understood it, so encrypted mail round-trips cleanly in webmail,
  over IMAP and to third-party clients. The bug-report/feedback flow is now
  genuinely PGP-encrypted with screenshots attached.

### Changed
- **Encryption fails safe.** VayuMail encrypts only when **every** recipient has
  a resolvable PGP key; if any recipient lacks one it sends honest readable text
  rather than ciphertext they could never open (previously encrypting to a
  keyless address produced an unreadable blob). The composer's PGP hints were
  updated to reflect that encryption now covers attachments and multiple
  recipients.

### Security
- New multi-recipient PGP primitive (`VayuPGP.EncryptToRecipients`) encrypts one
  armored message to every recipient's key in a single pass, reporting any
  addresses whose keys were not found so the caller can decide to fall back to
  plaintext.

### Added
- **One-tap feedback in VayuOS — report a bug, request an improvement or
  suggest a feature.** A new 💡 button in the VayuOS top bar (present on every
  page, in **both** the clearnet and the Tor console) reveals a small premium
  popover on hover explaining what it's for; clicking it opens the VayuMail
  composer in **feedback mode**: the recipient is pre-filled to your feedback
  inbox, a structured template (type · what happened · steps · expected · actual,
  stamped with the install's version and world) is dropped into the body, and
  **PGP encryption is pre-enabled** for text-only reports. Screenshots and files
  can be attached like any message (attachments send DKIM-signed over TLS rather
  than PGP-encrypted, which the composer already indicates live).
  - The feedback inbox defaults to `feedback@<your-domain>`, so it works the
    moment that mailbox exists. You can point it at any address from **Operations
    → Power & Maintenance → Feedback & bug reports** (setting
    `vayupress.feedback_email`).

### Added
- **Search engine & AI crawler block (Operations → Power & Maintenance).** A
  single on/off power switch that takes the public site dark to automated
  indexers — classic search engines and AI crawlers alike — with three
  reinforcing layers so it holds even against crawlers that ignore robots.txt.
  First, `robots.txt` disallows everything and names the major AI bots
  explicitly (GPTBot, ClaudeBot, PerplexityBot, Google-Extended, CCBot,
  Bytespider, Amazonbot, meta-externalagent, …), served instantly with no
  artefact regeneration. Second, a hard **403** for any request whose
  User-Agent matches a known search-engine or AI crawler, reusing VayuShield's
  compiled bot database. Third, `X-Robots-Tag: noindex, nofollow` on every
  remaining public response, so an unknown crawler with a generic User-Agent
  still won't index a page. Humans, the VayuOS console, health probes and
  **VayuMCP** (first-party AI connect, not crawling) are never affected. While
  the block is on, IndexNow is automatically paused — the site won't invite the
  crawlers it's shutting out.
- **Live IndexNow self-test (SEO page).** A **Test IndexNow now** button submits
  your homepage through the exact on-publish path and reports the precise
  outcome — key configured?, domain public?, key format valid?, endpoint
  accepted (HTTP 200/202) or rejected (with a plain-English reason for 400 / 403
  / 422 / 429) — so "is instant indexing working?" is answerable in one click.

### Fixed
- **IndexNow ping now works when the key was pasted with stray whitespace.** A
  trailing newline or space on the submission key silently broke IndexNow on
  **both** sides at once: the `keyLocation` URL became malformed and the
  verification-file handler's exact match failed, so every submission was
  rejected. The key is now trimmed at its single source (`indexNowKey`), fixing
  the ping and the `/.well-known/<key>.txt` verification file together, and the
  key-file handler tolerates a trailing-whitespace request path too.
- **IndexNow failures are now legible.** Rejections record and surface the HTTP
  status *with* an explanation (e.g. "HTTP 403 — key not found or not matching
  the key file at keyLocation"), and the submitter sends an explicit
  `Content-Type: application/json; charset=utf-8` and `User-Agent`.

### Changed
- **Maintenance mode: the "VayuOS is never locked out" guarantee is now covered
  by a test.** The path-exemption used while the public site is in maintenance is
  extracted into `maintenancePathExempt` and pinned by a regression test asserting
  the whole admin console — the **login page** (`/os/login`), its assets
  (`/os/static/…`) and the Power page (`/os/power`) — plus the health probes and
  operational surfaces stay reachable, while public pages get the maintenance
  page (segment-aware, so e.g. `/osborne` is NOT treated as `/os`). So an operator
  can always sign in and turn maintenance back off from the web, and a future
  refactor can't silently break that.

## [3.14.73] — 2026-07-21

### Added
- **Power & Maintenance controls in VayuOS** (Operations → Power & Maintenance).
  A single, safe control room for taking the site offline or restarting the app:
  - **Maintenance mode (on/off)** — the reversible power switch. While on, the
    public site serves a clean, premium, self-contained **"We’ll be right back"**
    maintenance page (503 + Retry-After, auto-refreshing), with an optional custom
    message. The VayuOS console (`/os`), the health probes and the signed-in
    operator’s own browsing always stay live, so the site can never lock you out —
    flip it back off from the same page. A **Preview** link shows exactly what
    visitors see without taking anything down.
  - **Restart** — gracefully restarts the app (drains in-flight requests via the
    existing SIGTERM path; the auto-restart service brings it back live in
    seconds) for a quick refresh or after an update.
  - **Shut down** — turns maintenance on and restarts, so the app comes back with
    the public site off and stays that way until you lift maintenance — a
    "stay offline" that never hard-kills the process or blocks admin access.
  - New setting keys `site.maintenance` / `site.maintenance_message`, a public
    `maintenanceMiddleware`, and CSRF-protected admin endpoints
    `POST /os/api/power/{maintenance,restart,shutdown}`.

## [3.14.72] — 2026-07-21

### Fixed
- **VayuShield: a jailed browser now clears itself again with no interaction.**
  v3.14.71 made the "verify you are human" challenge wait for a checkbox click,
  which meant a false-positive jail (e.g. the operator's own IP) no longer
  self-healed — a visitor could get stuck bouncing on the auto-retry page. The
  proof-of-work now **auto-runs on load** (the checkbox ticks itself to show it is
  working, and remains clickable to retry if the auto-run fails), so solving —
  and the jail pardon it triggers — happens automatically in one page load. The
  jail's Retry-After was also shortened (10s → 5s) so the calm auto-retry page
  bounces a visitor back into the solvable challenge sooner.

### Security
- **CodeQL request-forgery (Critical) on the onion-to-onion delivery sinks —
  resolved.** The strict v3-onion validation added in v3.14.70 was routed through
  a helper, so the analyzer couldn't connect the guard to the host used in the
  URL. The anchored regexp (`^[a-z2-7]{56}\.onion$`) is now matched **directly**
  on the host at both `onion_transport.go` sinks (delivery + read receipt),
  matching the barrier that already cleared the WKD sink.
- **CodeQL reflected-XSS (High) on the checkout instructions page — resolved.**
  The offline-payment reference page is now rendered with `html/template`
  (context-aware auto-escaping) instead of manual string building, so every
  dynamic field (order reference, the payer's own email, operator instructions)
  is escaped for its exact HTML context by construction.

## [3.14.71] — 2026-07-21

### Changed
- **VayuShield: a jailed visitor now gets a solvable "Verify you are human"
  challenge instead of a dead end.** A suspected/jailed IP was already offered a
  self-hosted proof-of-work interstitial that pardons the jail on success — but it
  was silent, and once the per-IP redeem budget (~1 challenge / 30s) was spent the
  request fell through to a flat "Request rejected by VayuShield (blocked)" page
  with no way for a real person to prove themselves. Now:
  - The interstitial is a clean, branded **"Verify you are human"** card with a
    visible checkbox the visitor ticks; ticking it runs the proof-of-work
    (Web Crypto SHA-256) and, on success, clears the jail (the existing
    `RewardProof` pardon lifts the blocklist, the reputation sentence and any
    kernel ban) and continues. No third-party CAPTCHA, no external calls — fully
    self-hosted, in keeping with the zero-telemetry posture.
  - When the redeem budget is momentarily spent, a browser navigation now gets a
    calm **"Just a moment…"** page that **auto-retries itself** (meta-refresh on
    the Retry-After) and bounces the visitor straight back into the solvable
    challenge — instead of the flat dead-end text. Non-navigational requests
    (API/asset/bot traffic that doesn't ask for HTML) still get the near-free flat
    rejection, so the shield's cheap-rejection economics and anti-amplification
    guards are unchanged (status codes and headers are identical either way).
  - The signed-challenge → `POST /__vayushield/pow` → clearance-cookie backend,
    the redeem budget, and the under-attack stand-down are all untouched.

## [3.14.70] — 2026-07-21

### Security
- **Hardened the onion-to-onion delivery network sinks against request forgery
  (CodeQL, Critical).** The outbound Tor lanes (VayuTalk envelope delivery, read
  receipts, and WKD-over-Tor key fetch) previously gated the target host with a
  loose `".onion"` suffix check, which would still accept a host carrying a port,
  path, userinfo (`evil@…`) or an extra label. They now require an **exact bare
  v3 onion** (`^[a-z2-7]{56}\.onion$`) via a new `isDeliverableOnionHost` gate on
  the precise value used to build each request URL, so an attacker-influenced
  recipient/sender address can never steer a request at another host. The host is
  case/space-normalised first. (These lanes are already `.onion`-only and off by
  default; this closes the theoretical smuggling gap.)
- **Reflected-XSS hardening on the checkout pages (CodeQL, High).** The shared
  checkout page shell now escapes the document `<title>` at the boundary itself
  (`checkoutShell`), so no caller can place unescaped, reflected text (an order
  reference, a tier name) into the document head. Page bodies were already
  escaped field-by-field; this makes the shell safe by construction.

## [3.14.69] — 2026-07-21

### Added
- **Premium member dashboard + self-serve 2FA with a scannable QR.** The member
  account page (`/members/account`) is redesigned into a clean, guided dashboard:
  a plan card with your current benefits, a Free-vs-Premium comparison for free
  members, a VayuMail-ID card, and a security card.
  - **Two-factor authentication members can turn on themselves.** A paid member
    who holds a VayuMail address can enable TOTP 2FA from the dashboard by
    **scanning a QR code** (the `otpauth://` label pre-fills the account name in
    the authenticator app) — no more typing a secret by hand. Disabling requires
    the current code. New member-scoped endpoints: `POST /api/v1/members/totp/
    begin|verify|disable` (begin returns `{secret, uri, qr}`). The second factor
    is the same one already enforced at VayuMail-credential sign-in.
  - **Included-mailbox claim** inline on the dashboard: pick your address +
    password and your PGP-encrypted `@domain` mailbox is provisioned.
- **Sign in with your VayuMail address** is now surfaced on the `/members`
  sign-in page (email + password + 2FA code when enabled), alongside the
  passwordless magic link.
- **Free-vs-Premium benefits** are shown on the public signup page so visitors
  see exactly what a free account gives and what upgrading unlocks, with a link
  to pricing.

### Changed
- **Operator mailbox 2FA now shows a QR modal** instead of a plain text prompt.
  Enabling two-factor for a mailbox from VayuOS presents a scannable code (plus
  the manual key as a fallback) and a confirmation field in a clean dialog. The
  `begin` action now also returns a `qr` data image.

## [3.14.68] — 2026-07-20

### Fixed
- **Clearnet VayuTalk between the app and the web console appeared to stop
  delivering after entering the Tor world — root cause found and fixed.** The
  "Enter Tor world" view cookie (`vp_world=tor`) was **persistent for 30 days**,
  so a single exploratory click silently kept the operator's whole `/os` console
  — including VayuTalk — proxied into the **separate Tor instance** (its own
  isolated relay + anonymous identity) for a month, while the mobile app stayed
  on the clearnet relay (`/api/v1/talk`, never proxied). Two different relays →
  the app and website could not message each other, with nothing obvious saying
  you were still viewing Tor.
  - The view cookie is now a **session cookie** (no 30-day persistence): every new
    browser session starts on Clearnet; entering Tor is a deliberate per-session
    act.
  - The Tor-world VayuTalk page now shows a clear callout that its chat is a
    separate relay from your Clearnet mailbox chat and the mobile app, with a
    one-click **switch to Clearnet**.
- Audit confirmed there is **no crypto/relay regression**: the clearnet relay,
  the app-facing `/api/v1/talk` handlers, and the signature-verification/read-
  receipt changes are all behavior-preserving on clearnet (a failed signature
  only shows a badge, never drops a message). The mobile app is unchanged and
  always uses the clearnet relay.

## [3.14.67] — 2026-07-20

### Added
- **Onion-to-onion VayuTalk — cross-onion read receipts.** When the recipient
  reads a message that was delivered to another `.onion`, a **"read" receipt is
  forwarded back to the sender's onion over Tor**, so the sender sees the message
  marked read and it burns on both sides — the ephemeral timer that already works
  same-instance now works across onions.
  - In the Tor world the web console is the sole reader (no phone app), so it now
    read-destroys on reveal via a new `POST /os/talk/read`. In the clearnet world
    this is a deliberate no-op — the phone app remains the authoritative reader
    and the console never steals its queued copy.
  - A new inbound `POST /api/v1/talk/onion/receipt` accepts the receipt (same
    closed-by-default gating as delivery; it acts only on a receipt for a message
    *this* code sent) and surfaces it on the sender's live stream.

### Notes
- Receipts are best-effort over Tor; a lost one only means the sender doesn't see
  a read mark. Cross-onion behaviour still needs validation on two real `.onion`
  installs — it cannot be exercised in CI.

## [3.14.66] — 2026-07-20

### Added
- **Onion-to-onion VayuTalk — managed-tor SOCKS auto-open (Phase 6).** The
  outbound Tor lane no longer needs a separate tor: while federation is on in the
  Tor world, VayuPress's managed tor **opens a loopback-only SOCKS port
  automatically** (`127.0.0.1:9250`) and closes it again when federation is turned
  off. Toggling federation nudges the Tor engine so the port opens/closes promptly
  instead of at the next reconcile tick. `VAYUOS_TOR_SOCKS_ADDR`, when set, still
  overrides and points the lane at a tor you run yourself.

### Security
- The managed SOCKS port is **bound to `127.0.0.1` only** (never a public
  interface) and is absent (`SocksPort 0`) unless federation is explicitly on —
  proven by a torrc unit test. This narrowly relaxes the previous
  onion-services-only posture, on an opt-in basis, purely for the guarded
  outbound onion lane.

### Notes
- With this, onion-to-onion VayuTalk is feature-complete (opt-in): enable it in
  the console and message codes on other `.onion` sites. Live delivery must still
  be validated on two real `.onion` installs — it cannot be exercised in CI.

## [3.14.65] — 2026-07-20

### Added
- **Onion-to-onion VayuTalk — inbound sender-signature verification (Phase 5).**
  Incoming messages are now checked for a valid signature from the code they
  claim to be from. A new `DecryptAndVerifyForEmail` decrypts and reports whether
  the message is signed by the sender's key; the web VayuTalk stream carries a
  `verified` flag, and an unverifiable incoming message shows a **⚠ unverified**
  badge (verified ones stay unbadged). Plaintext is always returned —
  confidentiality never depends on verification.
- To make replies and repeat senders verify, when an incoming message from
  another `.onion` can't be verified the server best-effort fetches that sender's
  key over Tor (deduped, gated on federation + `VAYUOS_TOR_SOCKS_ADDR`, driven
  only by the operator's own authenticated stream) so later messages verify.

### Notes
- Sender-key lookup for verification is local/onion-only — no clearnet WKD is
  triggered in the Tor world. Live behaviour still requires validation on two real
  `.onion` installs.

## [3.14.64] — 2026-07-20

### Added
- **Onion-to-onion VayuTalk — operator controls + guide (Phase 4).** The Tor-world
  VayuTalk page now has an **Enable / Disable onion-to-onion** button, so the
  operator can flip `talk.onion_federation` from the console (CSRF-checked,
  admin-only, Tor-world-only) and see its live state and the reminder to set
  `VAYUOS_TOR_SOCKS_ADDR`. New operator guide,
  `docs/VAYUTALK-ONION-FEDERATION.md`, documents how to enable it, the security
  model, and the experimental limits.

### Notes
- Inbound sender-signature verification and a managed-tor SOCKS convenience remain
  planned follow-ups; live two-onion delivery must be validated on real
  deployments.

## [3.14.63] — 2026-07-20

### Added
- **Onion-to-onion VayuTalk — outbound delivery (Phases 2b + 3).** The sending
  half: when you message a code on a different `.onion` and federation is on, the
  server fetches the recipient's key from their `.onion` over Tor, encrypts+signs
  locally, and hands the ciphertext to their delivery endpoint over Tor — so it
  reaches their live stream.
  - **Guard-railed Tor lane.** A dedicated HTTP client routes only through a Tor
    SOCKS proxy and **refuses to dial any host that is not a `.onion`** — the
    check runs in the dialer, before any connection, and never resolves DNS
    locally or follows redirects. This deanonymization-critical guard is covered
    by direct unit tests (clearnet hosts, bare IPs, and `*.onion.evil.com`
    look-alikes are all refused).
  - **Tor-routed key fetch.** A new `LookupOnionKey` fetches a peer's key from
    their onion's WKD over the guarded client (`http://` only; onion domains
    only) — separate from the clearnet `LookupExternalKey`, which still refuses
    to run in the Tor world.
  - **Honest failures.** Every branch has a distinct reason: federation off, no
    Tor SOCKS configured, key not reachable, or peer refused.

### Configuration
- The lane is **inert by default**. Beyond switching on `talk.onion_federation`,
  the operator points `VAYUOS_TOR_SOCKS_ADDR` at a Tor SOCKS proxy; with it
  unset, no client is built and nothing is ever dialled.

### Notes
- Live delivery between two `.onion` installs must be validated on real
  deployments — it cannot be exercised in CI. Inbound signature verification, an
  admin toggle, and the managed-tor SOCKS convenience land in the final phase.

## [3.14.62] — 2026-07-20

### Added
- **Onion-to-onion VayuTalk — Phase 2a: inbound delivery endpoint.** A new
  `POST /api/v1/talk/onion/deliver` accepts a ciphertext envelope from a remote
  `.onion` and drops it into the local relay so the recipient's live stream
  receives it. Security posture, all enforced and unit-tested:
  - **Closed by default** — returns 404 unless the install is in the Tor world
    *and* the operator has switched on `talk.onion_federation`, so it is not even
    discoverable otherwise.
  - **Not an open relay** — accepts an envelope only when it is addressed to the
    install's own current anonymous code; a foreign recipient is refused (403).
  - **Bounded** — a valid onion sender code is required (clearnet senders
    refused), the payload rides the existing 64 KiB ciphertext cap and the
    per-recipient/global queue caps, and the route carries the public-discovery
    rate limit. The payload is opaque ciphertext and is never decrypted here.

### Notes
- This is the receiving half. The outbound Tor lane (sending to another onion) and
  the send-path wiring land next; cross-onion delivery is not yet end-to-end.

## [3.14.61] — 2026-07-20

### Added
- **ADR-0142 + groundwork for onion-to-onion VayuTalk delivery.** Design record
  and Phase 1 of letting two people on **different** `.onion` installs exchange
  VayuTalk messages over Tor. This release lays the foundation:
  - A new opt-in setting, `talk.onion_federation` (**default off**), gates the
    experimental cross-onion path. While off — the default — behaviour is
    unchanged.
  - The Tor-world Talk page now shows whether onion-to-onion delivery is on or
    off, so it is clear when cross-onion messages will be attempted.
  - Sending to a code on a **different** `.onion` now returns a precise,
    actionable reason (federation off → "turn it on"; on → "rolling out") instead
    of a generic key error, via a new send-path classifier.

### Notes
- The outbound Tor lane, remote key fetch, and inbound delivery endpoint land in
  the following phases; cross-onion delivery is **not active yet**. Two people on
  the same install / clearnet VayuTalk are unaffected. End-to-end delivery can
  only be validated on real two-onion deployments, not in CI.

## [3.14.60] — 2026-07-20

### Fixed
- **Tor VayuTalk: rotated code no longer piles up in the PGP manager.** In the
  Tor world your chat identity is an anonymous, rotatable code (`anon…@onion`),
  and using or rotating it mints a throwaway keypair. Those keys were being
  listed under **VayuMail → PGP Keys** as if they were mailboxes, so every
  rotation left another entry behind. The key manager now hides anonymous
  VayuTalk chat handles (matched by their exact minted shape) — only real
  mailbox keys are shown.
- **Tor VayuTalk: sending to an unreachable code now fails honestly instead of
  silently pretending to send.** The recipient-key resolver used to *fabricate*
  a local keypair for any unknown address, so a message to a code on another
  `.onion` was encrypted to a made-up key, queued locally, and reported as
  sent — while the real person received nothing. In the Tor world the resolver
  no longer invents keys; the composer now shows a clear "can't reach this code
  over Tor" message. (Clearnet mint-on-demand for a local mailbox that predates
  auto-keygen is unchanged.)

### Known limitation
- Two people on **different** `.onion` installs still cannot exchange VayuTalk
  messages: the relay is in-process and there is no onion-to-onion delivery yet.
  This release makes that state visible (honest error) rather than silent;
  cross-onion delivery is tracked as follow-up work.

## [3.14.59] — 2026-07-20

### Added
- **Members can advertise on your site — self-serve, moderated, monetized.** A
  new **Advertise here** panel in every member's account lets any signed-in
  member (paid or free) submit an image ad, choose its placement (header, above
  or below the post, sidebar, footer), and pay a flat fee. Payment reuses the
  same one-time substrate as premium mail-IDs and paid posts — Stripe one-time
  when connected, else the sovereign direct/offline method. Only image creatives
  are accepted from members; member-authored HTML never reaches the page.
- **Operator moderation queue.** Each paid submission lands in **Advertising →
  Member ad submissions** as `pending_review`; nothing renders publicly until
  you **Approve** it (or **Reject** to decline). The queue shows a live
  awaiting-review count.
- **Member ad price is operator-controlled** from the Advertising console —
  set the flat per-ad fee in whole currency units; it drives every member
  checkout.

### Changed
- Member-ad orders flow through the shared payments ledger and appear in the
  Monetization **Orders** audit trail alongside subscriptions, premium mail-IDs
  and paid-post unlocks, so every paid section stays auditable from one place.

## [3.14.58] — 2026-07-20

### Changed
- **Monetization is now the single control centre; Growth is clean.** Every
  revenue control — the KPI hero (revenue, paid members, pending orders, premium
  addresses sold), the premium mail-ID marketplace overview, the order ledger and
  all product pricing — now lives inside **Monetization** (`/os/monetization`),
  organised into clear premium sections: **Payments** (Stripe / PayPal / direct /
  webhook) · **Products & pricing** (premium mail-IDs, paid posts) · **Orders**.
  The **Growth** hub is stripped back to a clean launcher (Audience + a
  Monetization card), so nothing is duplicated and the money surface is one calm,
  elegant place. The order ledger now labels each order by product (Membership ·
  tier, Premium mail-ID, Paid post) instead of a raw slug.

## [3.14.57] — 2026-07-20

### Added
- **Paid posts — one-time per-post unlock (Phase 6).** A signed-in member can buy
  permanent access to a single gated post without a subscription: the paywall
  shows a **Buy this post — $X** button that opens a one-time Stripe Checkout
  (instant) or the sovereign direct/offline gateway. Purchase settles through the
  shared fulfilment path (a pending `article_purchases` row, migration 074, flips
  to paid), and the post then unlocks for that member. The operator sets a post's
  access level + price from a new **Paid posts** control on the Monetization
  console (and the audit ledger labels these orders "Paid post"). Reuses the exact
  one-time-payment substrate built for premium mail-IDs.

## [3.14.56] — 2026-07-20

### Added
- **Premium Mail-ID management console** (`/os/monetization/mailids`, linked from
  Growth). The operator can now **see every premium (vanity) address sale**,
  **approve** a pending/offline order (the buyer can then activate it), and
  **disapprove (revoke)** any grant — plus manage the **premium-name list**: add
  or remove exact localparts that are held back from the free member claim and
  sold at the premium price. Backed by new members-store methods
  (`AddPremiumLocalpart`/`RemovePremiumLocalpart`/`ListPremiumLocalparts`,
  `ApprovePremiumGrant`/`RevokePremiumGrant`) and migration 075.
- The free-claim guard, the live availability check and the purchase check now
  treat an operator-added premium name as premium (held back / sellable), not just
  the built-in ultra-short + curated handles.

## [3.14.55] — 2026-07-20

### Added
- **Growth: orders audit ledger.** The Growth control panel now includes the
  single audit trail for every paid section — a newest-first table of all orders
  (reference, product, buyer, amount, gateway, status, date), with each row
  labelled by product (Membership · tier, Premium mail-ID, and paid content as it
  lands) so the operator can audit and confirm every payment from one place.

## [3.14.54] — 2026-07-20

### Added
- **Growth command centre.** The Growth hub (`/os/growth`) is now the operator's
  full monetization control panel: headline KPI cards (revenue collected, paid
  members, pending orders, premium addresses sold), a **premium mail-ID
  marketplace control unit** (per-status counts, the live price, and a table of
  every vanity address a member has bought — buyer, status, date), and the
  Audience + Monetization section cards that drill into each detail page. Built on
  the existing admin card/stat/table system, elegant and CSP-safe.

## [3.14.53] — 2026-07-20

### Added
- **One-click premium mail-ID purchase (Stripe one-time).** A paid member can now
  buy a premium (vanity) VayuMail address end-to-end: the portal's address search
  shows a **Buy** button on a premium name, which opens a one-time Stripe Checkout
  (card, instant) — or the sovereign direct/offline gateway when Stripe isn't
  connected. Payment is decoupled from provisioning via a **grant ledger**
  (`premium_mailid_grants`, migration 073): buying opens a `pending` grant; the
  shared payment-fulfilment path flips it to `paid` on confirmation (Stripe
  success/webhook or operator "Mark paid"); the member then **activates** it by
  setting a password, which provisions the real PGP mailbox (with WKD + a recorded
  terms acceptance) exactly like the included-mailbox claim. Purchased-but-unactivated
  addresses surface in the portal for activation.
- **Stripe one-time payments.** The dependency-free Stripe client gained a
  `mode=payment` path (non-recurring price, PaymentIntent-tagged metadata) beside
  the existing subscription mode, powering one-off purchases without creating a
  subscription. The subscription checkout is byte-for-byte unchanged.

### Notes
- Premium purchases route through Stripe one-time when connected, else the
  direct/offline gateway; the reader is never left at a dead end. A premium address
  is a real additional mailbox — a member can hold their included generic address
  and purchased vanity addresses side by side.

## [3.14.52] — 2026-07-20

### Added
- **Monetization console: VayuMail address marketplace card.** The operator can
  now set the premium (vanity) address price and edit the mailbox terms directly
  in `/os/monetization`, instead of only via the settings API. The card explains
  that premium names are held back from the free claim to be sold, and that every
  member's terms acceptance is recorded as proof of agreement. Saves through the
  existing `/os/api/settings` endpoint (CSRF-protected).

## [3.14.51] — 2026-07-20

### Added
- **Mailbox terms & acceptance gate (operator protection).** Before a VayuMail
  address is provisioned to a member, they must accept the operator's mail-ID
  terms (an editable acceptable-use agreement, `monetization.mailid_terms`, with
  a sensible default). Each acceptance is recorded — the address taken, a SHA-256
  of the exact terms text agreed to, and the timestamp (new `mailid_agreements`
  table, migration 072) — so the operator holds durable proof of agreement and is
  protected from misuse of the address. The claim form shows the terms with a
  required "I agree" checkbox; clearing the terms text relaxes the gate.
- **Configurable premium mail-ID price.** The operator sets what a premium
  (vanity) address costs (`monetization.premium_mailid_price_cents`, default
  $5.00 in the checkout currency). The member portal's live availability check
  now shows the price on a premium name ("✦ vip@domain is a premium address —
  $5.00"), so premium addresses read as priced, sellable inventory.

## [3.14.50] — 2026-07-20

### Added
- **Premium (vanity) mail-ID classifier — Phase 3 groundwork.** Member mailbox
  addresses now fall into three classes: **reserved** (role/infrastructure names,
  never claimable — unchanged), **premium** (ultra-short handles plus a curated
  set of high-demand words like `vip`, `ceo`, `shop`, `inbox`), and **generic**
  (everything else). A paying member's free tier claim provisions a *generic*
  address only; premium names are held back as the operator's sellable vanity
  inventory. The live availability check in the member portal now marks a premium
  name distinctly ("✦ premium address — not included on the free claim") instead
  of a plain "unavailable", so it reads as sellable rather than broken.

### Changed
- The member mailbox self-claim (`/api/v1/members/mailbox/claim` and its
  `…/available` check) refuses premium localparts on the free path, closing the
  gap where a paid member could self-claim a high-value vanity address (e.g. a
  two-letter or `ceo@` handle) for free. Operator-created mailboxes are
  unaffected — the guard applies only to the member self-service claim.

## [3.14.49] — 2026-07-20

### Added
- **Homepage ad slots.** The activation-gated advertising surface now reaches the
  highest-traffic page: with the Ads module on, the **Header** slot renders at the
  top of the homepage feed and the **Footer** slot closes the content column
  (above the trending strip and site footer), using the same slots already
  managed under Monetization. Self-hosted image/HTML creatives cache with the page
  as before; a page that renders a Google AdSense unit is served no-store with the
  widened ad CSP applied per request and is not disk-cached — matching the
  per-article ad path exactly. The above/below-post placements stay
  article-specific, and the sidebar placement remains reserved until a themed
  aside column exists.

## [3.14.48] — 2026-07-20

### Added
- **VayuTalk shown as an included paid benefit.** A paid member's mailbox panel
  and claim screen now state that **end-to-end encrypted VayuTalk chat is
  included**. This surfaces what the architecture already delivers: claiming the
  tier's mailbox (v3.14.46) gives the member the mail identity VayuTalk is keyed
  to, so signing into the VayuMail mobile app with that address unlocks VayuTalk
  for them with no separate setup. (Web VayuTalk stays operator-only by design;
  the member VayuTalk client is the mobile app.)

## [3.14.47] — 2026-07-20

### Added
- **"Your mailbox" in the member portal — claim your address in a couple of
  clicks.** A paid member on a mail-enabled tier now sees a **📬 Your mailbox**
  panel in the account overlay: they pick a name, get **live availability**
  feedback (reserved and taken addresses are rejected as they type), set a
  password, and hit **Claim my mailbox** — provisioning a real VayuMail account
  with PGP + WKD and their tier's storage quota (the Phase 2b backend). Once
  claimed, the panel shows the address, quota and a link to open VayuMail, plus a
  reminder to sign in on the mobile app. CSP-safe (same-origin portal script, no
  new inline handlers).

## [3.14.46] — 2026-07-20

### Added
- **Paid members can claim the mailbox their tier includes — reserved names
  protected.** New member-authed endpoints provision a real VayuMail mailbox for a
  paid member on a mail-enabled tier: `GET /api/v1/members/mailbox` (entitlement +
  status), `GET /api/v1/members/mailbox/available?localpart=` (live availability),
  and `POST /api/v1/members/mailbox/claim`. The member picks a **generic** address
  and a password; VayuPress creates the account, its Maildir, an auto **PGP keypair
  (+ WKD)**, and applies the **tier's storage quota** — reusing the same proven
  provisioning path as the operator console (now factored into one helper).
  Migration 071 links the claimed address to the member so each gets exactly one.

### Security
- Added `mail.IsReservedLocalpart` and `mail.ValidLocalpart`: the member claim path
  refuses **admin/role-critical localparts** (postmaster, abuse, admin, security,
  billing, RFC 2142 role names, infrastructure names…), malformed localparts, and
  already-taken addresses before anything is provisioned. Members can only ever
  claim a generic ID; the operator keeps the reserved names as inventory.

## [3.14.45] — 2026-07-20

### Added
- **Membership tiers can now bundle a VayuMail mailbox.** The tier editor in the
  Members console gains two settings — *Include a VayuMail mailbox* and *Mailbox
  size (MB)* (e.g. `1024` = 1 GB, `2048` = 2 GB, `0` = unlimited) — persisted on
  the tier (migration 070). An operator can now offer plans like "$1 → 1 GB
  mailbox, $2 → 2 GB mailbox". This is the entitlement/configuration layer; the
  member-facing claim and provisioning (a unique address with PGP + WKD, quota
  enforced, plus reserved-name protection so members only claim generic IDs)
  builds on it next.

## [3.14.44] — 2026-07-20

### Added
- **One-click PayPal — auto-renewing subscriptions alongside Stripe.** A *PayPal*
  panel in the Monetization console: enter your PayPal REST client id and secret
  (stored **encrypted at rest**, AES-256-GCM), tick **sandbox** for testing, and
  press **Test connection** for instant verification. When both gateways are
  connected the checkout page shows **Pay by card** and **Pay with PayPal**.
  VayuPress creates the PayPal catalog product and billing plan for each
  tier/price automatically — cached in a new `paypal_plans` table (migration 069),
  so a price change transparently makes a new plan and stale prices can never be
  charged — then sends the reader to a **PayPal-hosted approval page**: no PayPal
  SDK, no browser JS, the strict CSP is untouched (ADR-0090). On return,
  `/checkout/paypal/return` **confirms the subscription against PayPal
  server-side** (the browser is never trusted) before upgrading the member and
  emailing a receipt — idempotent. Funds settle into the operator's own PayPal
  account (direct credentials — no middleman).

## [3.14.43] — 2026-07-20

### Added
- **One-click Stripe checkout — take card payments straight into your own
  account.** A new *Card payments · Stripe* panel in the Monetization console
  (`/os/monetization`): paste your Stripe secret key once (stored **encrypted at
  rest**, AES-256-GCM) and press **Test connection** for instant live
  verification. Readers then subscribe through a **Stripe-hosted checkout** —
  VayuPress never embeds Stripe.js or any SDK, so the reader is simply redirected
  out and back (a top-level navigation) and the **strict CSP is untouched**
  (ADR-0090). The flow reuses the sovereign order ledger: `/checkout` opens an
  order plus a Stripe session tagged with its reference; on return,
  `/checkout/success` **confirms the payment against Stripe server-side** (the
  browser is never trusted to assert payment) before upgrading the member and
  emailing a receipt — idempotent, and mirrored by the webhook. It works with
  just the secret key; add the optional webhook signing secret to also sync
  cancellations and renewals. Payments settle into the operator's own Stripe
  account (direct keys — no middleman, no platform fee).

### Changed
- The Stripe webhook now reads its signing secret from the operator-editable
  encrypted store (falling back to the `STRIPE_WEBHOOK_SECRET` env var) and, when
  an event carries a VayuPress order reference, fulfils that exact order rather
  than only matching on email — so a connected subscription upgrades the right
  member every time.

## [3.14.42] — 2026-07-20

### Added
- **Edit & delete your own comment; operators can delete any comment.** A
  signed-in commenter now sees inline **Edit** and **Delete** controls on their
  own comments — Edit swaps the body for a textarea and saves in place; Delete
  removes it (and, for a top-level comment, its replies, so none are left
  orphaned). An operator/staff user additionally gets **Delete** on every comment
  to remove conflicting or abusive content. New same-origin endpoints (`PATCH`
  and `DELETE` on `/api/v1/articles/{slug}/comments/{id}`) authorise each action
  by session — the author for edits, the author or an operator for deletes —
  never by a client-sent flag, so no one can touch another person's words.

### Fixed
- **Comment country flag now renders on every platform (including Windows).** The
  widget drew the flag from Unicode regional-indicator characters, which Windows
  has no glyph for and shows as two letters ("IN") rather than a flag. It now
  uses the real, self-hosted SVG flag already bundled for VayuAnalytics
  (`/os/static/flags/<cc>.svg`, flag-icons, MIT), served same-origin so the
  strict `img-src 'self'` CSP still covers it on clearnet and Tor alike, with a
  neutral globe fallback when a country has no flag.

## [3.14.41] — 2026-07-20

### Fixed
- **Comment cards now actually render on the public site (avatar sized, card
  drawn).** The premium comment styles had been added to the editor-preview
  stylesheet, not the public `article.css` bundle — so on a live article the
  avatar showed at full size and no card was drawn. The card, 44px circular
  avatar (photos `object-fit: cover`), reply thread rail and compose styling now
  live in the public stylesheet, scoped under `#vayu-comments` so they reliably
  win, and use the site theme's own tokens (light + dark).

### Added
- **Member avatars — real photo, prebuilt cartoons, or a gender-aware auto
  avatar (foundation).** New `members` columns (gender optional, avatar choice +
  photo blob) and endpoints: a public `/api/v1/members/avatar/{id}` that serves a
  member's uploaded photo, a chosen cartoon, or a deterministic self-contained SVG
  cartoon face (CSP-safe — no external assets), plus member-authed upload
  (**≤100 KB**, PNG/JPEG/WebP/GIF) and choose/gender endpoints. Comments now
  resolve the author's real photo (CMS user) or their member avatar, so every
  commenter gets a proper picture. The in-portal picker UI (photo upload, cartoon
  grid, gender) lands next.

## [3.14.40] — 2026-07-20

### Fixed
- **Comment date/time now shows correctly in the reader's own timezone.** The
  `created_at` column is a SQLite `DATETIME`; a single fixed parse layout rejected
  the value the driver returned and silently produced the zero time (rendered as
  "1 Jan 1"). A tolerant `parseDBTime` now accepts every shape SQLite/database-sql
  hands back (treating a naive timestamp as UTC), so the widget renders the exact
  local date + time.
- **"Your activity" now lists a signed-in owner's own comments.** The portal's
  Activity tab resolved only reader-membership sessions, so an operator's comments
  (stored under their console email) never appeared ("you haven't commented yet").
  It now resolves the unified principal, matching who is allowed to comment.

### Changed
- **Real avatars + a location cue on every comment.** A comment now shows the
  author's **real profile photo** when they are a CMS user/owner (resolved by
  email, with a graceful initials fallback if the image fails); readers with no
  account get a deterministic gradient initials chip. Each comment also carries a
  **country flag** when geo was captured, or a neutral **🌐 globe** otherwise, so
  it is never blank. **Replies now merge into their parent** with an accent thread
  rail and a tinted card, reading as one conversation.

## [3.14.39] — 2026-07-20

### Security
- **Public comments no longer expose the commenter's email.** The comment list
  and submit endpoints returned the raw stored record, which included each
  commenter's **email** (and finer region/city). Responses now go through a
  `publicComment` projection that carries only the author, body, coarse **country**
  and timestamp — email, region and city never leave the server.

### Changed
- **Premium, theme-merged comments with country flag + exact time.** Each posted
  comment is now a rounded card (gradient avatar, hover lift) that shows the
  commenter's **country flag** (from the coarse, GDPR-safe country code, rendered
  as a flag emoji — no external asset), the **exact local date + time** in a
  semantic `<time>` element (with the relative age as a tooltip), and the body.
  The compose box, reply threads, "posted" states and empty state are all styled
  from the active theme's own tokens (accent / ink / surface), so the whole
  comment system adapts to light, dark and every preset — and honours
  `prefers-reduced-motion` and small screens.

## [3.14.38] — 2026-07-20

### Changed
- **Premium VayuOS chrome — search, command palette & theme toggle.** Pure-CSS
  polish (CSP-safe; no new runtime): the **topbar search pill expands on
  hover/focus** with a brand ring and an icon nudge (the flex spacer absorbs the
  growth, so the right-side controls stay put). The **⌘K command palette** gets a
  deeper backdrop blur + vignette, a sweeping brand hairline, a gradient panel, a
  focus-glow search row, rounded result rows that slide + gain an accent bar and
  icon on hover/active, an inline **↵** hint on the active row, and premium key
  chips. The **light/dark/auto toggle** is now a proper animated switch — sun,
  moon and auto glyphs that rotate + crossfade to the active theme. All of it
  honours `prefers-reduced-motion` and is responsive down to mobile.

## [3.14.37] — 2026-07-20

### Changed
- **Operator/staff comments post instantly (auto-approved).** A signed-in
  operator already holds moderation power over every comment, so their own
  comments now **skip the moderation queue** — the server approves them on submit
  and the widget **shows them live** (reloads the thread, reports *"Posted ✓"*)
  instead of *"awaiting moderation."* A reader member's comment still enters the
  queue exactly as before. When an operator's live comment is a **reply**, the
  parent author's *"you got a reply"* notification still fires (it would otherwise
  be skipped by bypassing the console moderation path).

## [3.14.36] — 2026-07-20

### Fixed
- **Signed-in owner/staff can now comment (unified member auth).** The public
  comment widget asks `/api/v1/members/me` who is signed in — which recognises a
  **VayuOS operator/staff console session** and renders the *"Commenting as …"*
  form — but the comment **POST** only accepted a reader-membership session, so
  the same signed-in owner was bounced with *"Please sign in as a member to
  comment."* A new **`resolveCommenter`** resolves one unified principal — a
  reader member (magic-link / VayuMail portal) **or** a console operator — so
  every member-gated surface agrees with what the UI shows. A signed-in person is
  now **authorised according to the power they hold**: operators/staff can
  comment, and the **paywall no longer gates an owner out of their own members-
  or paid-only articles**. Anonymous visitors are still refused.

## [3.14.35] — 2026-07-20

### Changed
- **Topbar notification centre replaces the New Post button.** The admin topbar
  now carries a **notification bell** with a live count badge and an **expandable
  panel** that lists every actionable signal in the system — **new messages,
  comments to moderate, mail devices awaiting approval, domains waiting to sync**
  — each a **direct link to the page that clears it**. Every item is gated to the
  viewer's access level, so it never points at a page they cannot open. Present on
  every admin page and in **both worlds** (the Tor console reads its own DB). The
  toggle is wired CSP-safely in `admin-os.js`; New Post now lives on the dashboard
  workspace and the command palette.
- **Slimmer, calmer dashboard.** Removed the **Publishing trend** chart, the
  **Storage** card and the **Recent articles** table; the dashboard now focuses on
  the **Workspace** tiles, **System health**, and a full-width **Recent activity**
  feed. The **Anonymous Tor `.onion` never appears on the clearnet dashboard**
  anymore — it is shown only inside the Tor console itself (ADR-0141
  anti-correlation); entering the Tor world stays on the sidebar world switch.

## [3.14.34] — 2026-07-20

### Changed
- **Premium, animated Domains registry + per-site manager.** The Domains page is
  now a responsive grid of premium cards with a staggered entrance (replacing the
  Stage-1 table): each hostname shows its identity, live post/member/mailbox
  counts, sync/TLS/status pills and lifecycle actions, and stays a clean
  add / list / remove surface. Every secondary site gains its **own manager**
  (`/os/domains/{id}`), reached from its card's **Manage** button and from a new
  **"Your websites"** row in the **Optimize** hub (one card per site) — so you can
  edit a site's **branding** (name, tagline, description, accent colours, browser
  theme-colour), assign posts to it, jump into Theme Studio / Website / Analytics
  for that site, and run its lifecycle (sync / enable / remove) all in one place.
  Both worlds get the redesign (shared handlers); the Tor console keeps its
  one-click **Add Tor site** flow. Per-site branding is prefilled into
  html-escaped `value` attributes and the manager script reads the domain id from
  a hidden data node, so no brand value can break out of the markup (CWE-116).

## [3.14.33] — 2026-07-20

### Changed
- **Flat, label-less sidebar; System surfaces folded into the Optimize hub.** The
  everyday config surfaces — **Tools & Plugins, Domains, Settings, VayuAPI**
  (renamed from *API Keys*) and **VayuMCP** — now live in the **Optimize** hub
  (new *Configuration & integrations* row), and the **PRODUCTS** and **SYSTEM**
  section headings are gone. The clearnet sidebar is now a clean flat list:
  **Dashboard · Growth · Optimize · Operations**, plus pinned **VayuMail ·
  VayuTalk · VayuTor · Update & Backup**. Hub cards stay role-gated, so editors
  never see the admin-only ones.
- **Tor console gets the same hub treatment (design parity, ADR-0141).** The Tor
  world's **System** group (Storage & System, Settings, My Profile) is consolidated
  into a single pinned **System** hub tab, matching the clearnet card-hub pattern
  so both worlds share the identical minimal design.

### Build
- Merged the pending CI dependency bumps (`setup-go`, `upload-artifact`,
  `fetch-metadata`, `deploy-pages`, `action-gh-release`).

### Changed
- **Monitoring, Governance, Storage & System and Security moved into the
  Operations hub.** The Operations tab now has two card rows — *Controls &
  diagnostics* (System Modes, Policy Inspector, Topology, Replay Explorer, Fault
  Engine, ADR Registry) and *Health & governance* (Monitoring, Governance, Storage
  & System, Security). The Storage & System card shows a usage badge when disk is
  running high (≥ 75%), alongside the existing non-normal-mode badge on System
  Modes. The System sidebar row now keeps only the everyday config surfaces (Tools
  & Plugins, Update & Backup, Domains, Settings, API Keys, VayuMCP). Every card
  links to its existing page — only the navigation moved.

### Changed
- **New "Optimize" hub consolidates the Optimize sidebar group.** SEO, Analytics,
  Bot Shield, Theme Studio and Theme Store are merged into a single pinned
  **Optimize** tab (like Dashboard / Growth / Operations) that opens a premium card
  grid. Each card is gated to the viewer's role exactly like the sidebar it
  replaced, so an editor sees SEO/Analytics/Theme but never the admin-only Bot
  Shield card. The **products (VayuMail, VayuTalk, VayuTor)** are intentionally
  kept pinned in the sidebar under their own row, since they are opened often. The
  clearnet sidebar is now Dashboard · Growth · Optimize · Products · System ·
  Operations. Every card links to its existing page — only the navigation moved.

### Changed
- **New "Operations" hub consolidates the Operations sidebar group.** System
  Modes, Policy Inspector, Topology, Replay Explorer, Fault Engine and ADR
  Registry no longer each take a sidebar row — they're merged into a single pinned
  **Operations** tab (like Dashboard / Growth) that opens a premium card grid. The
  System Modes card shows a status badge whenever the install is not in normal mode
  (read-only / degraded / quarantined), so a non-normal state stands out. Every
  card links to its existing page — only the navigation moved. These are
  clearnet-only infrastructure controls, so there is no Tor-world counterpart; the
  design upgrades that DO apply to both worlds (dashboard command-center, cards,
  world card, Tor analytics, minimal sidebar) already run in the Tor console via
  the same binary.

### Changed
- **New "Growth" hub consolidates the Audience & Monetization sidebar groups.**
  Members, Newsletter, Monetization, Advertising (and My Profile) no longer each
  take a sidebar row — they're merged into a single pinned **Growth** tab (like
  Dashboard) that opens a premium card grid, with live counts (members, newsletter
  subscribers) on the cards. This continues the dashboard-hub pattern and keeps the
  VayuOS sidebar minimal and clean. Every card links to its existing page — nothing
  about those pages changed, only the navigation — and My Profile also stays
  reachable from the sidebar footer avatar.

## [3.14.28] — 2026-07-19

### Fixed
- **Logo upload accepts real logo images now (PNG, JPEG, WebP, GIF, ICO), up to
  1 MB.** The upload previously accepted only PNG/ICO at ≤256 KB and the file
  picker itself filtered to those two types — so a JPEG/WebP/GIF logo (what most
  people actually have) could not even be selected, and a larger image was
  rejected. This was the most common reason "the logo won't change": the upload
  was silently refused. The asset doubles as the nav-bar logo, so the size cap is
  now logo-friendly. SVG stays refused on purpose (an SVG served same-origin can
  carry active content). Every format is served correctly because browsers honour
  the Content-Type, not the URL's `.png`/`.ico` extension.

### Fixed
- **Tor-world logo now actually changes.** The default (built-in) favicon/logo was
  served with a year-long `immutable` cache, so a browser that had cached it never
  re-requested the asset — a custom logo uploaded later silently "did nothing".
  This bit the Tor world hardest (a fresh Tor instance starts on the shared
  default). The default logo now serves with an ETag + short revalidation, so a
  custom logo appears within a minute. Uploaded logos were already content-hashed
  and unaffected. (Both worlds benefit; each keeps its own logo in its own DB.)
- **Tor-world social/OG image is emitted over the correct scheme.** `og:image`,
  `og:url`, `canonical`, `prev`/`next` and the JSON-LD publisher URL were built as
  hard-coded `https://<host>` even for a `.onion` — an unreachable URL on a v3
  onion (which is http-only). They now route through `seo.Origin`, so a Tor site
  emits `http://<onion>/…` and its share image resolves. Clearnet output is
  byte-identical (still `https://`).

### Added
- **Tor analytics in the Tor console.** The Anonymous Tor world's sidebar now has
  an **Insights → Analytics** entry showing the visit count and which pages were
  visited — privacy-preserving (aggregate counts only, no per-visitor data),
  served entirely from the Tor world's own database. The homepage `/` is now also
  recorded server-side, so Tor Browser visitors (JS commonly disabled, so the
  client beacon never fires) are still counted.

## [3.14.26] — 2026-07-19

### Security
- **World-switch cookie deletion now carries the same attributes it was set with**
  (CodeQL #70/#71). Clearing the `vp_world` view cookie (switching back to
  Clearnet) previously emitted a bare `Set-Cookie` with no `HttpOnly`, `SameSite`
  or `Secure`. The deletion now mirrors the set path — `HttpOnly`, `SameSite=Lax`
  and request-aware `Secure` (https clearnet gets `Secure`; the http `.onion` world
  deliberately does not, or the delete would be dropped over http and the operator
  could never switch back). No behavioural change; closes the code-scanning alerts.

## [3.14.25] — 2026-07-19

### Changed
- **Dashboard is now the command center (both Clearnet and Tor).** The content
  areas — Posts, Pages, Comments, Messages, Media, New Post and (on clearnet)
  Website — have moved OUT of the sidebar and INTO the dashboard as a premium
  **Workspace** grid: large, clickable tiles that each show their live count and,
  where it matters, a notification badge (pending comments, unread messages). The
  sidebar now keeps only the Dashboard hub and the broader system areas, so it is
  shorter and calmer. The dashboard also gained a **System health** row (queue
  jobs) alongside the existing publishing trend, storage, activity feed and recent
  articles. The Tor world console gets the same premium dashboard.
- **The anonymous-world status moved off the sidebar toggle onto the dashboard.**
  The old "Anonymous world live · copy .onion" line under the Clearnet⟷Tor switch
  is gone; the switch is now a clean two-segment control. The world's live status
  and its `.onion` (with a copy button and an Enter/Back link) are shown as a
  dedicated **world card** at the top of the dashboard, where there is room to
  present it properly.

### Added
- **One-click "Add Tor site" in the Tor world (ADR-0141).** The Domains section of
  the Anonymous Tor world now has an **Add a Tor site** card: pick what it serves
  (blog, business or mail-only) and, optionally, turn on anonymous mail — no
  hostname to type. VayuPress mints a fresh dedicated `.onion` for the new site
  automatically and it appears in the table within about a minute (the row shows
  **Minting .onion…** and the page auto-refreshes until the address lands). Each
  Tor site is a fully separate onion fronting the same isolated Tor-world instance,
  routed by its own address — so you can run several independent anonymous sites
  (each with its own blog and VayuMail·Tor) from one switch.

### Changed
- The clearnet "Add a domain" host form is hidden while you are viewing the Tor
  world; there you only ever add auto-onioned Tor sites, keeping the two worlds'
  domain surfaces distinct.

### Security
- The parent↔child control channel that mints and assigns each Tor site's onion is
  authenticated by the Tor world's own distinct API key (bearer) over loopback,
  exactly like the enter-the-Tor-world proxy — no clearnet session or key crosses
  over. A transient child hiccup never tears a published `.onion` down: the engine
  only reconciles site onions on a definitive answer from the child.

## [3.14.23] — 2026-07-19

### Added
- **VayuTalk·Tor uses an anonymous, rotatable code (ADR-0141).** In the Tor world
  your chat identity is no longer your mailbox address — it is a random handle
  (`<random>@<onion>`) with no mailbox behind it, so chat is not linked to a mail
  account. The VayuTalk page shows the code with **Copy** and **Rotate**; Rotate
  mints a brand-new handle and keypair on demand, so you can hand out a throwaway
  code and drop it whenever you like. The relay is identity-agnostic, so this is a
  pure identity change — messages stay end-to-end encrypted and ephemeral.

## [3.14.22] — 2026-07-19

### Fixed
- **Clearnet keeps its own colour (ADR-0141).** The Tor (purple) palette now paints
  the console ONLY when you are viewing the Tor world — not merely because the Tor
  world is enabled. Previously the clearnet console turned purple as soon as the Tor
  world was switched on, so the two worlds looked the same. Now clearnet stays its
  normal colour and only the Tor world is purple.

## [3.14.21] — 2026-07-19

### Fixed
- **The world switch works again (ADR-0141).** On the clearnet console the switch
  now always shows **Clearnet** as the active segment and **Tor** as the
  click-to-enter one — previously, once the Tor world had been enabled, the switch
  drew Tor as "active" even while you were viewing clearnet, so clicking it did
  nothing and you were stuck on clearnet. The active segment now reflects which
  world you are VIEWING, not whether the Tor world happens to be running.

### Added
- **VayuMail·Tor — encrypted mail inside the Tor world (ADR-0141).** VayuMail now
  runs in the Tor world as an onion-only, fully-encrypted service: create mailboxes
  and app passwords, compose, read and search, all on the Tor world's own database.
  Mailbox-to-mailbox mail on the same `.onion` is delivered locally and encrypted at
  rest with VayuPGP (transparently decrypted on read). It makes **zero clearnet
  callbacks**: outbound to any external address is bounced (never dialed), the
  privileged SMTP/IMAP/POP3 listeners never bind, ACME is off, WKD external key
  discovery is disabled, and the DNS/deliverability page is turned off in Tor mode.
- **VayuTalk·Tor — anonymous encrypted chat inside the Tor world (ADR-0141).**
  VayuTalk now runs automatically in the Tor world. Its relay is entirely in-memory
  and in-process (ephemeral, burn-after-read), messages are end-to-end encrypted
  with VayuPGP, and it makes no external/clearnet calls — so it is safe over the
  onion out of the box.

## [3.14.20] — 2026-07-19

### Fixed
- **No more "stuck in Tor" (ADR-0141).** The Clearnet switch-back inside the Tor
  world console is now ALWAYS a working link, regardless of how the Tor instance
  was launched — previously it could render as a dead label, leaving the operator
  unable to return to the clearnet console. The world-view cookie is also cleared
  robustly (both current and any legacy path) so flipping back to Clearnet always
  takes effect.

## [3.14.19] — 2026-07-19

### Added
- **Flip to Tor now ENTERS the Tor world (ADR-0141).** Switching the sidebar to Tor
  no longer just recolours the clearnet console — it steps you INTO the separate
  Tor-world instance in the same browser. While the Tor view is active, the admin
  console (`/os/*`) is proxied into that isolated instance, so you manage ITS data —
  its Tor domains, Tor blog, Tor mail, Tor chat, from its own database — never
  clearnet's. Flip back to Clearnet (a switch inside the Tor console, or the
  clearnet segment) to return. The bridge authenticates to the Tor world with that
  instance's OWN API key (checked before any session), so no clearnet login or
  account crosses over; the two databases stay separate. If the Tor world is still
  booting, a clear "starting…" page with a Back-to-Clearnet link is shown instead
  of an error.

## [3.14.18] — 2026-07-19

### Changed
- **Tor world is now its own Tor-only console (ADR-0141).** When a VayuOS install
  runs in Tor mode (the anonymous `.onion` world, served in Tor Browser), its
  sidebar shows ONLY what belongs there — the blog (Dashboard, Posts, Pages, Media,
  Comments, Theme), the anonymous services (VayuMail, VayuTalk) and its onion
  Domains — with every clearnet-only section hidden (monetization, ads, newsletter,
  members, SEO/IndexNow, VayuMCP, analytics, VayuTor and the operations panels). The
  Tor world has its own separate database, so it reads as a clean, standalone
  anonymous system. The clearnet console is unchanged.

## [3.14.17] — 2026-07-19

### Fixed
- **The Anonymous Tor Space toggle now actually persists (ADR-0141).** The new
  `tor.space_enabled` / `tor.space_api_key` settings keys were defined but never
  registered in the settings allowlist, so `SetMany` silently dropped the write and
  returned success — the one-click world switch appeared to work (the page
  reloaded) but the value never saved, so the reloaded console always read "off" and
  nothing changed. Registered both keys (with defaults) and added a settings
  regression test that fails if a Tor-Space key is missing from the allowlist. This
  was the real cause of "clicking Tor only refreshes, nothing changes".

## [3.14.16] — 2026-07-19

### Changed
- **Self-contained world switch — no separate page (ADR-0141).** The top-of-sidebar
  Clearnet ⟷ Tor switch now shows the anonymous world's live status and its
  copyable `.onion` address inline, directly beneath the switch, so there is no
  "Spaces" page to open and nothing to activate twice. Flipping to Tor gives instant
  on-screen feedback ("Starting your anonymous world…"), then shows "Anonymous world
  live" with a one-click **copy .onion** once it publishes. A dedicated
  whole-install Tor world shows its address the same way.

### Notes
- The functional in-app toggle first shipped in v3.14.14. On a server still running
  an earlier binary the toggle button is present but does nothing — update the
  install (VayuOS → Update & Backup) to v3.14.16 so the switch is wired.

## [3.14.15] — 2026-07-19

### Changed
- **One-click world switch, pinned to the top of the sidebar (ADR-0141).** The
  Clearnet ⟷ Tor switch now lives at the very top of the VayuOS sidebar on every
  admin page: one click flips the whole install between the public Clearnet world
  and the Anonymous Tor Space (and the chrome recolours to match). It works from
  anywhere — the handler primes the CSRF cookie, then posts the toggle — and is
  administrator-only. On a dedicated whole-install Tor world it shows as a static
  indicator (that world is switched by running a separate install, not from here).
- **Fewer sidebar rows.** The standalone "Spaces" navigation row is removed; the
  top switch is its control and its address/detail page is reached from the small
  link beneath the switch — a cleaner, more beginner-friendly sidebar.

## [3.14.14] — 2026-07-19

### Added
- **One-click Anonymous Tor Space — go-live (ADR-0141).** A clearnet install can now
  turn on a second, fully isolated VayuPress as an anonymous `.onion` world from the
  Spaces page with a single toggle — no terminal, no server-side setup. The parent
  supervises the isolated child (its own database, accounts and identity), the
  VayuTor engine mints the child's dedicated onion, and the whole VayuOS chrome
  shifts to the Tor (purple) palette while it is on so the active world is always
  obvious. The card states plainly that both worlds share the same server, so this
  compartmentalises identity and content — it is not machine-level anonymity.

### Fixed
- **Tor Space onion is re-published after any control reconnect.** The dedicated
  Space onion's live-host record is now cleared wherever the tor control connection
  is dropped/replaced (reconnect and teardown), so it is re-issued from its
  persistent key and the SAME address comes back — instead of the admin card naming
  a dead address after a transient control error, a managed-tor restart, or an
  off→on toggle.
- **A failing per-domain onion no longer blocks the Space onion.** The per-domain
  ADD_ONION loop is resilient: one host's bad identity is recorded and skipped
  instead of aborting the whole reconcile, so enabling the anonymous Space never
  depends on the health of unrelated clearnet-domain onions.
- **Tor Space child cannot be double-spawned.** The supervisor serialises its whole
  start/stop transition, so the reconcile ticker and the toggle POST can never both
  spawn a child (and the orphan-reaper can never fire against a child a concurrent
  start is launching).
- **Tor Space child is drained deterministically at parent shutdown.** The detached
  child is now signalled from the shutdown sequence (SIGTERM → WAL checkpoint) and
  the supervisor is latched off so a reconcile tick racing shutdown cannot respawn
  it — instead of the child being left running after the parent exits.
- **Spaces toggle reliability + diagnostics.** The Spaces page GET re-issues the
  `vp_csrf` cookie (so the toggle no longer 403s on a fresh session or after a
  restart), the toggle button re-enables itself if the request fails, and the card
  surfaces the VayuTor engine's actionable reason (e.g. no tor control port) when
  the onion is not yet live instead of showing "Publishing…" forever.

### Security
- **Anonymous Tor Space + VayuTor are admin-only.** `/os/spaces`, `/os/spaces/toggle`,
  `/os/tor` and `/os/tor/toggle` now require administrator access (route guard plus
  an explicit in-handler check) and their sidebar entries are hidden from
  author/editor sessions — closing a gap where a non-admin console user could start
  or stop the anonymous world (and the VayuTor onions).

## [3.14.13] — 2026-07-19

### Added
- **Anonymous Tor Space — dedicated onion (ADR-0141).** The VayuTor engine can now
  mint ONE dedicated onion for the Tor-Space child, separate from the per-domain
  onions: it targets the child's loopback port, carries its OWN persistent key
  under a reserved store host (so the address is stable across restarts), and is
  kept out of the per-domain map so the reconcile reap loop can never tear it down.
  Driven by two new engine config hooks (`SpaceOnionTarget`, `SpaceOnionReady`);
  the decision core (`spaceOnionPlan`: mint / restore / teardown / noop) is a pure,
  unit-tested function. Inert until the toggle wires the target — a `nil` hook is a
  no-op on every reconcile, so clearnet behaviour is unchanged.

## [3.14.12] — 2026-07-19

### Added
- **Anonymous Tor Space — supervisor (ADR-0141).** The lifecycle manager for the
  isolated Tor-world child (`internal/vayuos/torspace/supervisor.go`), ported from
  the managed-Tor supervision pattern with the anonymity review's must-fixes baked
  in: a curated child environment (never the parent's), **crash-loop backoff** so a
  child that fails to boot can't hot-spin a second VayuPress, a **graceful**
  `SIGTERM → drain → SIGKILL` stop (the child owns a live SQLite DB), and reaping a
  prior-run orphan by an **environ role marker** (`VAYUOS_SPACE_CHILD=1` + its own
  DB path) — never by binary name, which the parent shares. Health is a loopback
  `/health` poll; the child binds a fresh loopback port. Fully unit-tested via
  injectable spawn/health seams (lifecycle, backoff, spawn-error) and race-clean.
  Still inert — no process is spawned until a later increment wires the toggle and
  verifies the child's graceful-drain path; the supervisor API is baselined in the
  deadcode allow-list until then.

## [3.14.11] — 2026-07-19

### Added
- **Anonymous Tor Space — isolation foundation (ADR-0141).** Groundwork for a
  one-click, no-terminal Anonymous Tor Space: the running clearnet process will
  supervise a second, isolated copy of itself as the Tor world (reusing the
  self-exec + managed-child patterns the binary already uses unprivileged). This
  release ships the security-critical isolation contract (`internal/vayuos/
  torspace`): the child gets a **curated environment** — never the parent's, so
  the API key and other secrets can't leak or correlate the two worlds — a
  **distinct API key**, and every writable path (`DB`, cache, media, static,
  logs, tmp) pinned under an isolated `DATA_DIR/tor-space` root (closing the
  content/media-bleed the sibling script had). A `VAYUOS_SPACE_CHILD` sentinel is
  the recursion guard (a child never supervises a grandchild or runs an onion
  engine). No process is spawned yet — this locks the contract first, with tests.
  Documented honestly in ADR-0141: this is content/identity *compartmentalisation*
  (separate DB, media, accounts, `.onion`), **not** anonymity against a
  server-access or network-level adversary — that needs a physically separate
  machine.

## [3.14.10] — 2026-07-19

### Added
- **VayuOS "Spaces" admin page (`/os/spaces`, ADR-0141).** A new sidebar page
  puts the Clearnet and Tor worlds side by side in the admin: it shows which
  world *this* install is (a Clearnet or Tor Space badge, from `VAYUOS_MODE`),
  explains the two-world "no mesh" model, and — on a clearnet install — surfaces
  the one command that stands up a separate Tor Space alongside it
  (`sudo bash scripts/setup-tor-space.sh`, in a copy box) with best-effort live
  status (provisioned? its `.onion`?). On a Tor install it shows that Space's own
  onion address. Read-only: the privileged provisioning is still run by the
  operator on the server, never by the web tier. `setup-tor-space.sh` now records
  the onion where the page can read it.

## [3.14.9] — 2026-07-19

### Added
- **One-command Tor Space provisioner — run both worlds at once (ADR-0141,
  Phase 1).** New `scripts/setup-tor-space.sh` stands up a second, fully separate
  VayuPress instance in anonymous mode *alongside* an existing Clearnet install:
  it reuses the already-installed binary and Tor daemon, provisions a persistent
  Tor v3 `.onion`, writes an isolated `vayupress-tor` systemd service with its own
  database, cache and logs (`VAYUOS_MODE=tor`, no clearnet callbacks, no CA-TLS,
  a tighter sandbox with no privileged-port capability), starts it, and prints the
  onion address. The two worlds share **nothing** (separate databases), so they
  can never be correlated — "no mesh" by construction. Idempotent; re-running
  keeps the same `.onion`. Remove just the service with `--remove` (identity and
  data are preserved). Documented in `docs/INSTALLATION.md` and `.env.example`
  (new `VAYUOS_MODE` guidance).

### Security
- **A Tor Space binds loopback only (ADR-0141).** In `VAYUOS_MODE=tor` the HTTP
  server now binds `127.0.0.1:<port>` instead of every interface — the onion is
  reached solely through Tor's HiddenServicePort, so a port scan of the host can
  no longer serve the "anonymous" site in cleartext on its public clearnet IP (a
  deanonymisation vector). Clearnet installs bind all interfaces exactly as before.
- **The mail engine is disabled in a Tor Space.** The Tor world is web-only, so
  VayuMail no longer starts under `VAYUOS_MODE=tor` — closing an outbound
  direct-to-MX clearnet leak and stopping the futile privileged-port binds the
  Tor sandbox cannot grant.

### Changed
- **`setup-tor-space.sh` hardening (adversarial review).** Applies torrc changes
  with a SIGHUP `reload` instead of a full tor `restart`, so standing up the Tor
  Space never drops the clearnet install's live onions; converges the service on
  re-run; waits for `/health` before declaring success (surfaces a crash-loop
  instead of a false "started"); and prints the auto-generated first-run admin
  credentials so the operator can sign in.

## [3.14.8] — 2026-07-19

### Added
- **Space-mode indicator in the VayuOS topbar (ADR-0141, Phase 1).** Every admin
  page now carries an unmistakable badge next to the page title showing which
  world this whole install controls — a green **Clearnet** Space (public HTTPS
  domain) or a purple **Tor** Space (anonymous `.onion`, clearnet callbacks off).
  The mode is fixed per install by `VAYUOS_MODE`, so the badge is a read-only
  status label, not a toggle — the two worlds are never confused when operating
  either. Rendered with a same-origin CSS class (no inline style), so the strict
  admin CSP (`style-src 'self'`) is untouched.

## [3.14.7] — 2026-07-19

### Changed
- **Onion-primary routing in a Tor Space (ADR-0141, Phase 1).** With
  `VAYUOS_MODE=tor`, an incoming `.onion` request keeps its own Host all the way
  to the domain resolver — the onion is treated as the site's first-class domain
  rather than being rewritten to a clearnet host. This lets a site run purely as
  its `.onion` (no clearnet twin) with correct canonical URLs and cookies. The
  count-only visit tally is preserved. Clearnet Spaces are byte-identical: the
  onion→clearnet overlay rewrite still applies exactly as before whenever
  `VAYUOS_MODE` is unset or `clearnet`.

## [3.14.6] — 2026-07-19

### Security
- **Tor-mode CSP hardening (ADR-0141, Phase 1).** With `VAYUOS_MODE=tor`, the
  reader Content-Security-Policy now drops external `https:` from `img-src`
  (`img-src 'self' data:` only), so a reader's Tor Browser can never fetch an
  off-onion image and leak its IP — the browser-enforced version of the anti-leak
  rule (it covers even already-stored content, not just newly-saved posts).
  Clearnet Spaces are byte-identical (they keep `img-src 'self' data: https:`,
  confirmed by the compatibility golden tests).

### Added
- **Tor-mode anti-leak sweep (ADR-0141, Phase 1).** With `VAYUOS_MODE=tor`, the
  install now also suppresses the two remaining publish-time clearnet callbacks:
  **social auto-post** (Mastodon) and the **Cloudflare cache purge** — both would
  otherwise phone home to a clearnet endpoint (and social would publish the onion
  URL), reducing anonymity. Together with the IndexNow gate (v3.14.4), a Tor
  Space makes no publish-time clearnet callbacks. The active mode is logged at
  startup. Clearnet installs are unaffected (every guard is off by default).

### Added
- **VayuOS Spaces — whole-install mode switch (ADR-0141, Phase 1 start).** A new
  `VAYUOS_MODE` env selects the install's world: `clearnet` (default — unchanged)
  or `tor`/`onion`/`anonymous` (`config.Cfg.OnionMode`). Only an explicit
  tor/onion/anonymous value enables Tor mode, so a typo can never silently drop
  clearnet features. First anti-leak guardrail wired: in Tor mode the IndexNow
  clearnet callback is skipped (it would otherwise submit the onion URL to a
  public search endpoint and phone home). Clearnet installs are byte-for-byte
  unaffected. More anti-leak sites (webmention, WKD, onion-primary routing,
  request-aware cookies) follow in later increments.

### Notes
- Refreshed the repo's `CLAUDE.md` project memory with the VayuOS Spaces plan,
  the v3.14.x shipped features, and CI gotchas, so future sessions start warm.

### Added
- **Content Bundle export/import (VayuOS Spaces sync/migrate — ADR-0141).** A new
  CLI to move content between two separate VayuPress installs (e.g. a Clearnet
  install and an anonymous Tor install):
  - `vayupress migrate export --file site.vaybundle [--include-pages=false]` —
    writes a single, **checksummed** JSON bundle of posts + pages (title, slug,
    content, tags, excerpt, feature image, flags, dates).
  - `vayupress migrate import --file site.vaybundle [--mode=merge|add-only]
    [--dry-run]` — imports by slug; `merge` upserts, `add-only` skips existing.
    Content is re-sanitised on the way in and the bundle checksum is verified
    before anything is written (a tampered/truncated bundle is refused).
  - **Content only, by design:** accounts, mailboxes, PGP keys, sessions and Talk
    identities never cross — importing identity would let the two installs be
    correlated and de-anonymise the Tor one. The bundle moves fully offline (e.g.
    by USB), the safe path for an air-gapped anonymous install.

### Added
- **ADR-0141 — VayuOS Spaces (Clearnet 🌐 / Tor 🧅).** Design record for splitting
  VayuOS into two isolated, independently switchable worlds — a public Clearnet
  Space and an anonymous Tor Space (onion-only sites with no clearnet domain,
  Tor-native VayuMail, and a standalone rotatable VayuTalk identity), with
  enforced anti-leak guardrails. This reverses ADR-0138's Tor-only refusal, with
  lock-out guardrails. Phased delivery starts here.
- **Scheme-aware public origin (Phase 1 backbone).** A single source of truth,
  `seo.Origin(host)`, now decides the absolute-URL scheme: a `.onion` host is
  emitted as `http://<onion>` (v3 onions are self-authenticating — no CA TLS),
  every clearnet host stays `https://`. Article canonical URLs and social/OG
  image URLs route through it. **No change for clearnet sites** (byte-identical,
  guarded by the compatibility golden tests); it lets a future onion-only site
  emit correct links instead of dead `https://` ones.

### Added
- **Pick your AI model from a dropdown.** In the editor's ✨ AI panel, choosing a
  provider now loads that provider's **model list** (fetched live — OpenRouter /
  OpenAI `/models`, Ollama `/api/tags` — with a curated fallback when the live
  catalogue can't be reached). Pick from the dropdown or still type any custom
  model name. New endpoint `GET /os/api/editor/ai-models`.
- **SVG diagrams render in posts.** A raw SVG pasted into a **Diagram** block or an
  **HTML** card now displays as the diagram instead of being flattened to
  run-together text. The SVG is sanitised (script/style/foreignObject, inline
  event handlers, `javascript:` and non-local `href`s are stripped) and emitted
  inline with full geometry preserved; the strict CSP (`script-src 'self'
  'nonce-…'`, no `unsafe-inline`) is a hard backstop against any active content.
  The live editor preview handles pasted SVG too.
- **Automatic hero image.** When a post has no feature image set, it now adopts the
  first image in the body as its hero automatically.

### Fixed
- **Third-party images (Pixabay/Unsplash) render reliably.** The hero, post cards
  (home + tag pages), and the trending widget now emit `referrerpolicy="no-referrer"`
  so hotlinked external images load past referrer-based hotlink protection (and
  the reader's page URL isn't leaked). Additionally, a pasted image **page** link
  (e.g. a Pixabay/Unsplash photo *page* rather than the direct image) is resolved
  at save time to the direct image it advertises via `og:image` — for both body
  images and the feature image. Nothing is downloaded or re-hosted: only the URL
  is stored (storage-neutral), bounded, and fetched through the SSRF-safe client.

## [3.14.0] — 2026-07-18

### Security
- **Bounded, abuse-resistant AI generation.** The editor's AI-draft endpoint is
  reachable by any author-role account and spends the operator's stored (often
  paid) provider key on every call. It now enforces a **per-user rate limit**
  (10 drafts/minute) and a **process-wide concurrency cap** (4 at once), so a
  compromised or careless account can't run up unbounded third-party inference
  cost or pin many outbound connections. Over the limit returns 429/“busy”.
- **AI provider errors no longer leak the endpoint.** A failed generation used
  to reflect the raw upstream/transport error to the caller, which could expose
  the operator's configured provider URL (including internal hosts) to a
  lower-privileged author. The caller now gets a generic message; the detail is
  logged server-side for operators.

### Fixed
- **Custom AI gateway picker is now accurate.** The editor only offers a “Custom
  (OpenAI-compatible)” backend once it actually has **both a key and a base
  URL** (custom has no built-in default), and offers **one entry per custom
  credential** — selected by credential id — so with several custom credentials
  a draft always routes to the exact gateway you picked, never the
  most-recently-saved one (which could be an unrelated, non-LLM secret).
- **Toolbar 🖼 Image insert position.** The toolbar image upload inserted one
  block too low in some browsers; it now inserts directly at the caret,
  matching paste/drop.
- **AI draft is a single undo step.** Inserting an N-block AI draft rebuilt the
  canvas N times and needed N presses to undo; it now inserts in one operation —
  one canvas rebuild, one “undo”.

## [3.13.99] — 2026-07-18

### Added
- **Write a post draft with AI, from any provider.** A new **✨ AI** button in the
  editor opens a panel: type a prompt, pick a provider, and a full draft is
  inserted as editable blocks (the first heading fills an empty title). Providers:
  a local **Ollama**, **OpenAI**, **OpenRouter**, or any **OpenAI-compatible**
  endpoint — using the API keys you store (encrypted at rest) in **VayuOS → API
  Keys** (added an OpenAI provider card; OpenRouter/Ollama already existed). The
  model only ever produces a suggestion you insert — never auto-saved or
  published. Generated Markdown is turned into real blocks through the same
  sanitized import path as everything else. New endpoints:
  `POST /os/api/editor/generate` and `GET /os/api/editor/ai-providers`, with the
  backend resolved from the stored provider credentials (or the env Ollama).
- **Direct image upload from the toolbar.** A new **🖼 Image** button uploads a
  file to your media store and inserts it at the caret in one click — no trip to
  the media library to copy a URL (drag-drop and paste already worked).

### Fixed
- **External images (Pixabay, Unsplash, anywhere) now render by direct link — no
  re-hosting.** Images referenced by an external `https://` URL are shown as-is
  (nothing is downloaded into your storage), and each `<img>` is emitted with
  `referrerpolicy="no-referrer"`, which loads past simple referrer-based hotlink
  protection *and* stops leaking your reader's page URL to the third-party host.
  A render-time scheme allowlist (`safeImageURL`) means only site-relative or
  `http(s)` image URLs are emitted, so a dangerous scheme never produces even a
  `src`-stripped `<img>`. (Requires a direct **https** image URL — an `http://`
  URL is blocked as mixed content by the browser, and a page URL is not an image.)

## [3.13.98] — 2026-07-18

### Added
- **Direct API host: `api.<domain>` (hardened, proxy-off) + it's surfaced in the
  admin.** Scripts, CI jobs and AI agents call `/api/v1/…` machine-to-machine and —
  like VayuMCP and VayuTalk — cannot solve a proxy JavaScript challenge (e.g.
  Cloudflare Bot Fight Mode / managed challenge), which a free-plan WAF rule can't
  scope per-path. `scripts/setup-api-subdomain.sh` (run automatically by deploy +
  every update, idempotent + non-fatal) issues a dedicated Let's Encrypt cert for
  `api.<domain>` and writes a **hardened** nginx vhost that proxies **only** `/api`
  and `/health` — everything else, including the `/os` admin console, login and the
  OAuth/MCP endpoints, returns **404** on the direct host. The machine surface is
  minimal and key-authenticated (per-key rate-limited, WORM-audited, and still
  guarded by VayuShield, which already bypasses JS challenges on `/api`), while the
  apex keeps full proxy protection for browser traffic.
- New **`VAYUOS_API_HOST`** setting: when set (e.g. `api.<domain>`), **VayuOS → API
  Keys** now shows a **"Your API base URL"** card with the recommended, challenge-free
  base URL to copy; when unset it shows the apex URL plus guidance to provision the
  dedicated host. Docs: `docs/INSTALLATION.md` DNS table + a new "Direct API host"
  section, and a "Base URL" section in `docs/compatibility/vayuapi.md`.

### Added
- **Per-post IndexNow status + manual re-ping in the Posts manager.** Every
  published post is already announced to IndexNow automatically on publish/update;
  now that outcome is **persisted per post** (migration 067, `indexnow_submissions`)
  and **shown on each row** in VayuOS → Posts as a small badge: **✓ IndexNow**
  (submitted, with the time on hover), **⚠ IndexNow failed** (with the reason), or
  **IndexNow: not sent** (never submitted). Drafts show a neutral **—** since they
  have no public URL. When a post was not submitted — no key at publish time, a
  transient failure, or a proxy challenge — a **Ping IndexNow** button submits it
  on the spot (and reads **Re-ping** once done); the row's badge updates in place
  over HTMX with no reload. The manual endpoint (`POST
  /os/api/posts/{slug}/indexnow-fragment`) is admin-only and CSRF-protected, and
  reuses the same `pingIndexNow` path, so a manual ping is announced exactly like an
  automatic one and its result is recorded the same way.

### Changed
- **The connector is now branded "VayuMCP" everywhere, not "Claude Connector".**
  claude.ai is just one client — VayuMCP is a standard MCP + OAuth 2.1 endpoint, so
  **any MCP client** (Claude, Claude Code, Claude Desktop, Cursor, or anything that
  speaks MCP over HTTP) connects the same way. Renamed the VayuOS page title, `<h1>`,
  and sidebar nav to **VayuMCP**; reframed the page copy to say "connect a client"
  and lead with "any MCP client" while keeping the concrete Claude how-tos; and
  relabelled the one-click grants to `VayuMCP (full control / author / read-only)`.
  Updated the marketing site (products tab renamed **VayuMCP**, hero terminal line,
  JSON-LD) and `docs/compatibility/mcp.md` (retitled, added a "claude.ai is just one
  client" note and a plain-language "what you can ask a connected client to do"
  summary). No behaviour or endpoint change — `/mcp` and the OAuth flow are unchanged.

### Fixed
- **OAuth consent "Approve" now actually redirects back to the client.** The whole
  flow completed server-side — consent APPROVED, a 303 with the authorization code
  to the client's redirect URI — but the browser silently dropped that redirect and
  the operator just saw the page "refresh" and nothing happen. Cause: the consent
  page carries the strict baseline CSP `form-action 'self'`, and browsers enforce
  `form-action` across the **entire** form navigation, including the redirect target.
  So the POST to `/oauth/authorize/consent` was allowed, but its 303 to
  `https://claude.ai/…` was blocked (server log: `CSP violation:
  directive=form-action blocked=…/oauth/authorize/consent`). `/oauth/authorize` now
  extends `form-action` for just that one page to also allow the client's
  already-validated redirect origin (`form-action 'self' https://claude.ai` for a
  web callback, or the native custom scheme for a desktop client), so the
  authorization code can be delivered back and the connection completes. Verified the
  emitted header now includes the client origin.

## [3.13.94] — 2026-07-18

### Changed
- **Full OAuth-flow logging** (`component: "oauth"`) so the entire connect can be
  followed from the server log: the consent decision (APPROVED/DENIED + grant), the
  303 redirect back to the client (scheme+host+params only — never the code/token),
  and the token exchange result (OK, or the exact failure reason). This makes the
  post-approval hops (redirect → `/token` → first `/mcp` call) diagnosable without
  guessing.

## [3.13.93] — 2026-07-18

### Fixed
- **OAuth consent "Approve" now works even when the client blocks third-party
  cookies.** 3.13.91 made the consent CSRF + session cookies `SameSite=None; Secure`,
  but Claude's OAuth window blocks third-party cookies outright, so *no* cookie
  survived the cross-site approval POST and it still failed with `csrf_invalid`. The
  consent step is now **completely cookie-free**: `/oauth/authorize` mints a
  stateless, HMAC-signed token that binds the approval to the authenticated admin
  and a short expiry, and carries it in the form. `/oauth/authorize/consent` verifies
  that token (no CSRF cookie, no session cookie needed) — form fields always survive
  a POST. It is unforgeable without the server secret and is only ever handed to an
  authenticated admin, so a token the server accepts proves the POST came from the
  real consent page, and it names the approver for ownership + audit. Verified
  end-to-end: the consent POST succeeds with **zero cookies** and a tampered token is
  rejected (400). Removed the now-unneeded `SameSite=None` cookie handling and the
  `ReissueSessionCookieCrossSite` helper.

### Changed
- **Installation guide documents the Claude/MCP connector.** `docs/INSTALLATION.md`
  now lists the optional `mcp.<domain>` DNS record (proxy **off**) in the records
  table, tells operators to point every subdomain they'll use *before* running the
  installer, and adds a *"Connect Claude to your site"* section covering the
  proxy-off subdomain, `setup-mcp-subdomain.sh`, and the `curl /health` check.

## [3.13.92] — 2026-07-18

### Changed
- **`setup-mcp-subdomain.sh` now issues a DEDICATED certificate for `mcp.<domain>`**
  (`certbot --cert-name mcp.<domain> -d mcp.<domain>`) instead of expanding the apex
  certificate. Expanding the shared cert re-validates every existing SAN, including
  the CDN-proxied apex — whose HTTP-01 challenge can be intercepted by the CDN and
  fail the whole reissue. Because `mcp.<domain>` is grey-clouded (proxy OFF), its
  own challenge always reaches the origin directly, so a dedicated cert provisions
  reliably and never risks the apex/mail certs. Verified end-to-end on a live host:
  cert issued, vhost written, `https://mcp.<domain>/health` serves JSON. This is the
  method to use when a CDN's bot-challenge can't be scoped per path (e.g. Cloudflare
  free Bot Fight Mode).

## [3.13.91] — 2026-07-18

### Fixed
- **OAuth consent "Approve" failed with `csrf_invalid` in Claude's connect flow.**
  The consent form is reached from an external OAuth client (claude.ai / Claude
  Desktop) and the approval POST is made in a cross-site context, where the browser
  drops the `SameSite=Strict` `vp_csrf` (and session) cookie — so the double-submit
  check saw no cookie and rejected the approval. `/oauth/authorize` now issues both
  the CSRF cookie and a re-issued admin session cookie with **`SameSite=None; Secure`**
  so they are delivered on the cross-site consent POST. CSRF protection is unchanged
  in strength — it rests on the HMAC-signed, unforgeable double-submit token (plus
  the re-validated admin session), not on `SameSite`. Falls back to the hardened
  default on a non-TLS dev instance. Verified end-to-end locally: the consent POST
  now returns the authorization code. (The `/os` console keeps its Strict cookies.)

### Added
- **`scripts/setup-mcp-subdomain.sh`** — auto-provisions a `mcp.<domain>` connector
  subdomain (TLS SAN via Let's Encrypt + nginx vhost that proxies the full app) for
  operators who front their site with a CDN/WAF whose bot-challenge can't be scoped
  per-path (e.g. Cloudflare free Bot Fight Mode). Point `mcp.<domain>` straight at
  the origin with the CDN proxy **OFF**, and Claude reaches `/mcp` + `/oauth`
  unchallenged while the main domain keeps full CDN protection. Wired into
  `deploy-vayupress.sh` and `update-vayupress.sh` (idempotent, non-fatal, skips
  cleanly until the DNS record exists). The connector page documents the option.

## [3.13.90] — 2026-07-18

### Fixed
- **CI (race/test job) went red on 3.13.89.** The new "behind a proxy/WAF"
  troubleshooting copy on the connector page used the word "CDN", which the
  `TestConnectorCardsCSPSafe` guard flags as a literal substring (it forbids
  `cdn`/`googleapis`/`unpkg`/`jsdelivr` to catch any external asset host slipping
  into an admin fragment). Reworded the copy to "proxy / WAF" — same guidance, no
  forbidden token — so the guard stays strict and the build is green.

## [3.13.89] — 2026-07-18

### Fixed
- **A native OAuth client using a custom URI scheme could register but then fail at
  the final redirect.** `isValidRedirectURL` (the redirect-sink guard in
  `oauthRedirectWith`) still accepted only https + http-loopback, while registration
  (`validRedirectURI`) was broadened in 3.13.88 to accept private-use / custom URI
  schemes. The two disagreed, so a client that registered `myapp://cb` reached the
  consent screen, approved, and then hit *"Invalid redirect URL"* when the code was
  delivered. The sink guard now mirrors the registration rule exactly (https any
  host, http loopback, or a non-dangerous custom scheme), while remaining the
  name-matched barrier the code-scanning open-redirect query recognises.

### Changed
- **The Claude Connector page now documents the CDN/WAF challenge gotcha.** When a
  site is behind Cloudflare (or any WAF) with Bot Fight Mode / a managed challenge /
  Under-Attack mode, Claude's *machine-to-machine* requests to `/oauth/*`, `/mcp`
  and `/.well-known/*` are blocked by a JavaScript challenge before they reach
  VayuPress — the connect fails with "couldn't register" and nothing appears in the
  server log. The page now explains this and how to fix it (Skip rule for those
  paths; on the free plan, extend an existing Skip rule rather than adding a new
  one), with a `curl /health` litmus test.

## [3.13.88] — 2026-07-18

### Fixed
- **Claude's Connect could fail at dynamic client registration for native MCP
  clients.** The OAuth server only accepted `https` and `http`-loopback redirect
  URIs, so a native client that registers a **private-use / custom URI scheme**
  (RFC 8252 §7.1, e.g. `com.anthropic.claude:/…`) was turned away at
  `POST /oauth/register` — surfacing in Claude as *"Couldn't register with <site>'s
  sign-in service."* `validRedirectURI` now also accepts custom URI schemes
  (rejecting only the script/data/file schemes: `javascript`, `data`, `vbscript`,
  `file`, `blob`, `about`), which is safe because PKCE S256 is mandatory. Cleartext
  `http` to a non-loopback host, fragments, and userinfo are still rejected.
- **Loopback redirect URIs are now matched port-agnostically (RFC 8252 §7.3).** A
  native client (e.g. Claude Code) that registers `http://127.0.0.1:<port>/callback`
  binds a fresh ephemeral port per session; the authorize/token step now matches on
  host + path + query and ignores the port for loopback hosts. All other redirect
  URIs (https, custom schemes) are still exact-matched.

### Changed
- **The OAuth endpoints now log the flow** (`component: "oauth"`): the exact
  `client_name` and `redirect_uris` received at `/oauth/register` (and the rejection
  reason if any), the `client_id`/`redirect_uri` at `/oauth/authorize`, and the
  `grant_type` at `/oauth/token`. A failed connect is now diagnosable straight from
  the server log instead of guessing.

## [3.13.87] — 2026-07-18

### Fixed
- **Claude's one-click Connect could not register (VayuShield was blocking it).**
  When bot protection is enabled, VayuShield bypassed `/.well-known` — so OAuth
  *discovery* succeeded — but not `/oauth` or `/mcp`. Anthropic's backend then hit
  `POST /oauth/register` (dynamic client registration) as a non-browser client with
  no session, was classified as a bot, and got a proof-of-work/JS challenge it can
  never solve — surfacing in Claude Desktop as *"Couldn't register with <site>'s
  sign-in service."* `/mcp` and `/oauth` are now on VayuShield's bypass list (and
  the sovereignty lane's priority list), alongside `/api` and `/.well-known`. This
  is safe: `/mcp` still requires an API key and enforces the per-key rate budget;
  `/oauth/register` and `/oauth/token` sit behind the discovery rate limit; and
  `/oauth/authorize` still requires a signed-in admin. A regression test asserts
  both prefixes stay bypassed.

## [3.13.86] — 2026-07-18

### Changed
- **Claude Connector page now documents the shipped one-click Connect.** The page
  previously said a one-click OAuth Connect button was "on the roadmap"; the OAuth
  2.1 server shipped in 3.13.84, so the copy now explains the real flow: the Connect
  button lives on **Claude's side** (claude.ai → Settings → Connectors → Add custom
  connector → paste your `/mcp` endpoint → **Connect** → approve the access level on
  this site's consent screen) — no key to copy. The Desktop/CLI pasted-key snippets
  remain for clients that use a static key.

### Security
- **Reworked the two open-redirect guards into the boolean form CodeQL recognises.**
  The 3.13.85 sanitizers were runtime-correct but expressed as a string-returning
  helper (`safeLocalNext`) and an inline `Hostname()` check on a different variable
  than the one redirected — neither of which the code-scanning open-redirect query
  models as a barrier. Both are now name-matched boolean guards (`isLocalURL`,
  `isValidRedirectURL`) applied directly to the value passed to `http.Redirect`, so
  the analyser recognises the neutralisation. Behaviour is unchanged: the post-login
  `next` bounce still accepts only site-rooted relative paths, and OAuth client
  redirects still allow only https (or http to a loopback host), never userinfo.

## [3.13.85] — 2026-07-18

### Security
- **Hardened the static-analysis barriers on three redirect/output paths** flagged
  by CodeQL code scanning. All three were defence-in-depth (the runtime guards were
  already in place); the fix makes the sanitizer barrier one the analyser can see,
  and tightens each guard while doing so.
  - **OAuth consent page (reflected-XSS finding).** Every dynamic value is now passed
    through `html.EscapeString` **inline at the point of interpolation** rather than
    via a local alias/closure, which is what lets the reflected-XSS query recognise
    the escape as a barrier. Output is byte-for-byte identical; only the escaping call
    site changed.
  - **Post-login `next` bounce (open-redirect finding).** `safeLocalNext` now parses
    the candidate with `url.Parse` and accepts it only as a **purely relative
    reference** — no scheme, no host/authority, no userinfo, no opaque part — in
    addition to the existing backslash/control-character/`://` rejections, so a
    post-login redirect can never leave the site.
  - **OAuth client redirect (open-redirect finding).** `oauthRedirectWith` re-asserts
    the scheme/host invariant **inline** before `http.Redirect` (https, or http to a
    loopback host, and no userinfo), on top of the caller's exact-match validation.
- No behavioural change for any legitimate flow; the OAuth authorize bounce and the
  client redirect round-trip exactly as before.

## [3.13.84] — 2026-07-18

### Added
- **VayuMCP Stage 4 — one-click Connect on claude.ai (OAuth 2.1).** VayuPress now
  runs a built-in **OAuth 2.1 authorization server** (ADR-0140), so claude.ai and
  any OAuth MCP client can connect with a real *sign in → approve* button — no key
  to copy and paste. It advertises itself via a `401 WWW-Authenticate` challenge on
  `/mcp` plus RFC 8414 / RFC 9728 discovery metadata, supports RFC 7591 dynamic
  client registration, and runs a PKCE (S256) authorization-code flow with refresh
  tokens. The consent screen offers the same **Full control / Author / Read-only**
  access levels as the connector page.
  - **The access token is a scoped VayuPress API key.** On the token exchange the
    server mints an `apikeys` key with the approved grant and returns it as the
    `access_token`, so the capability table, per-key rate budget, audit log, and
    revocation all apply unchanged — and **no bearer token is ever stored at rest**
    (the authorization code carries only the grant; the key is minted at exchange
    time). Revoking the key in VayuOS → API Keys disconnects the connector and drops
    its refresh token.
- The VayuOS login page gained a strictly same-origin `next` return path so the
  consent flow can bounce through sign-in without ever redirecting off-site.

### Security
- PKCE **S256 is mandatory** (no `plain`); authorization codes and refresh tokens
  are single-use, hashed at rest, and codes expire in 5 minutes.
- Redirect URIs are **exact-match** and validated **before** any redirect, so the
  authorize endpoint can never be used as an open redirect; only a signed-in
  **administrator** can approve, re-checked on the consent POST (CSRF-guarded).
- Redirect-URI validation parses the URL and matches the real **host** against a
  loopback allowlist (never a string prefix), so `http://localhost.evil.com`,
  `http://127.0.0.1@evil.com` (userinfo trick), and similar cannot register a
  cleartext off-site callback — closing an open-redirect / code-exfiltration path
  an adversarial review of this release found. A wrong `client_id` on a refresh
  exchange is now a harmless rejection that never consumes the token, and the
  OAuth-minted key + audit trail are attributed to the approving administrator.

## [3.13.83] — 2026-07-18

### Added
- **VayuMCP Stage 3c — Claude can restyle the site and see the media library.**
  Three new tools: `list_media` (browse the media library — names, `/media/<name>`
  URLs, sizes — so Claude can embed existing assets; needs `media:read`),
  `list_themes` (the built-in theme presets; `themes:read`), and `apply_theme`
  (apply a preset to the whole site in one step, live; `themes:apply`).
  `apply_theme` reuses the exact Theme Studio path — compile-validate → persist →
  activate CSS → purge cache — via a shared `saveAndActivateTheme` helper, so a
  connector restyle behaves identically to an operator clicking a preset. Each tool
  is capability-gated and `apply_theme` is audit-logged.

## [3.13.82] — 2026-07-18

### Added
- **VayuMCP Stage 3b — Claude can build pages.** New `create_page` tool creates a
  standalone page (About, Contact, a landing page — the building blocks of a
  "custom website") that renders at `/<slug>` and is excluded from the blog feed;
  `list_pages` enumerates them. Editing, reading, and removing a page reuse the
  existing slug-based `update_post` / `get_post` / `delete_post` tools. Both new
  tools are capability-gated (`posts:write` / `posts:read`) and audit-logged.

### Fixed
- **Page creation is now race-free.** The `is_page` flag flows through the async
  write pipeline (a new persisted `Article.IsPage` field written by the queue
  worker's insert) instead of a follow-up `UPDATE` that could momentarily publish
  a page as a blog post before the flag was set. The VayuOS "Add a page"
  quick-create was migrated to this path, removing the previous
  create-then-`setArticleIsPage` race entirely.
- **A new page is no longer briefly served with blog-post chrome.** The write
  worker pre-renders a published article into the disk cache; it rendered with the
  post layout (published date, tags, related, comments, author box) regardless of
  kind, so a freshly created page could be served as a post until the cache was
  purged. The worker now skips the pre-seed for pages (`is_page=1`) — they render
  correctly on demand via the live article handler. (Surfaced by the review of the
  race-free `is_page` change above.)

## [3.13.81] — 2026-07-18

### Added
- **VayuMCP Stage 3a — `update_site_settings` (Claude can now edit the site's
  look).** The write counterpart to `site_settings`: a full-control (or
  `settings:write`) connector can rebrand and restyle the site — name, tagline,
  description, theme colours, navigation, footer, custom CSS, and SEO meta — and
  the change applies live. It reuses the same `SetMany` write path and render
  refresh the VayuOS settings screen uses (now a single shared helper), and is
  bounded to the **same presentational allowlist** as the read tool: operational
  configuration (private Tor bridge lines, VayuShield thresholds) is never
  writable through it, even if requested. Every change is written to the audit log.

### Changed
- The settings→render mapping is now a single `reloadRenderSettings` helper shared
  by the VayuOS settings API and the new tool (was duplicated inline), so a live
  settings change behaves identically however it is made.

## [3.13.80] — 2026-07-18

### Added
- **VayuMCP Stage 2 — one-click Claude connector page + a broader toolset.** A new
  **VayuOS → Claude Connector** page (`/os/connector`) makes connecting Claude a
  one-click affair: **Grant full control** mints a superuser key (Claude can then
  run the whole site), while **Author** and **Read-only** presets mint safely
  scoped keys — and a custom grant is one link away on the API Keys page. The page
  shows your live connector endpoint and fills your freshly-minted key straight
  into ready-to-paste **Claude Desktop** and **Claude Code** configurations, and
  lists every active connector with an instant **Revoke**. The page is admin-only
  and adds **no new write surface** — it mints and revokes through the existing
  CSRF-protected API-key endpoints.
- **New MCP tools:** `site_settings` (read the site's non-secret configuration —
  name, tagline, theme, navigation, SEO meta; needs `settings:read`) and
  `analytics_summary` (privacy-first traffic overview + top pages for the last N
  days; needs `analytics:read`). A full-control key gains both automatically; a
  scoped key sees a tool only if its key grants that section — the same
  enforcement as every other tool.
- **VayuDomains: bulk "Sync all pending" approval.** The manual sync gate now has
  a batch action so a set of freshly-registered secondary domains can be approved
  for provisioning together instead of one row at a time. VayuOS → Domains shows a
  **Sync all pending (N)** button whenever any domain is on manual hold, and the
  CLI gains `vayupress domains sync --all` / `hold --all`. Like the per-domain
  action, approving only adds the domains to the out-of-process helper's work
  list — nothing is provisioned by the unprivileged binary itself. Idempotent:
  re-running reports how many rows actually changed (0 when nothing was pending).

### Security
- The connector page requires **admin** console level (it can mint a full-control
  key), enforced by the same per-path level guard as every other admin area.
- `site_settings` returns only an **allowlist** of presentational keys (site
  identity, theme, navigation, SEO meta). Operational configuration — the
  operator's private Tor bridge lines and the VayuShield thresholds — is never
  projected into the tool's output, and third-party credentials remain in the
  separate encrypted store, unreachable through it.
- **Key minting is now subset-bounded.** A key-authenticated caller of the
  key-create endpoint can no longer mint a key more powerful than itself (a scoped
  key requesting `*:*`, or any capability it does not hold, is refused). The
  interactive admin UI (session-authenticated), including the connector's one-click
  "Grant full control", is unaffected — this closes a defense-in-depth gap in the
  "a key is never more powerful than its grant" invariant.

## [3.13.79] — 2026-07-18

### Added
- **VayuMCP — a built-in Claude / MCP connector (Stage 1).** VayuPress now serves
  its own **Model Context Protocol** server from the same binary at `POST /mcp`,
  so an AI assistant (Claude Desktop, Claude Code, or any MCP client) can drive
  the site natively — the way Claude connects to GitHub. Stage 1 ships tools for
  posts (`create_post`, `update_post`, `delete_post`, `list_posts`, `get_post`),
  `search_content`, and `site_info`. Auth reuses the scoped-key model
  (ADR-0134): each tool declares the `section:action` it needs and is checked
  with `KeyInfo.Can()` before it is listed *or* called, so a connector can do
  **exactly** what its key grants — a superuser key gives Claude **full control**;
  a scoped key exposes only its tools, and ungranted tools are invisible and
  refused ("unknown"). The endpoint is authenticated, rate-limited, and every
  call is WORM-audited with the calling key's label (never the secret). Toggle
  with `VAYUOS_MCP=off`. Protocol layer is a self-contained, dependency-free
  `internal/mcp` package. See [ADR-0139](docs/adr/ADR-0139-vayu-mcp-connector.md)
  and [docs/compatibility/mcp.md](docs/compatibility/mcp.md). Stage 2 adds a
  one-click VayuOS connector page + more tools; Stage 3 adds OAuth for a
  claude.ai one-click Connect button.

### Security / hardening
- Adversarial review of the connector (no auth/capability-bypass found) surfaced
  three fixes now applied: **article updates now enforce the same field limits as
  create** (`ValidateArticleInput` in `ArticleService.Update`) — closing an
  oversized-row / limit-bypass hole that also affected the REST `PUT`; MCP
  error responses now always include the JSON-RPC `id` (Null on parse error) per
  spec; and MCP audit rows are attributed to the key's **stable unique id**, not
  its mutable/non-unique label, so a destructive tool call is always traceable to
  exactly one key.

## [3.13.78] — 2026-07-18

### Security
- **Dependency refresh — all direct modules current.** Updated Go modules to
  their latest compatible releases, notably `golang.org/x/crypto` v0.53.0 →
  v0.54.0 and `golang.org/x/net` v0.56.0 → v0.57.0 (which carry upstream security
  fixes), plus `golang.org/x/sys`, `golang.org/x/text`, `github.com/phuslu/iploc`,
  and `github.com/dlclark/regexp2/v2`. Every direct dependency is now up to date
  (green on the new Dependency Freshness check); build, vet, tests, staticcheck,
  golangci-lint, and deadcode all clean. The Tor pluggable-transport pins
  (lyrebird/goptlib) were intentionally left unchanged.

### CI / infrastructure
- **Dependency-freshness system.** Added `.github/dependabot.yml` (weekly grouped
  auto-update PRs for Go modules and GitHub Actions, each gated by full CI) plus a
  scheduled **Dependency Freshness** workflow that goes **red** when a *direct*
  third-party dependency is behind its latest release, with a job-summary table of
  exactly what needs updating. Transitive/indirect modules are reported as
  collapsed info so the check stays actionable rather than perpetually red. A
  status badge is on the README; logic lives in `scripts/dep-freshness.sh`.
- **Dependabot auto-merge for safe bumps.** `dependabot-automerge.yml` enables
  GitHub auto-merge on Dependabot PRs that are **minor or patch** updates (major
  bumps stay manual). Auto-merge still waits for all required CI checks, so
  nothing broken lands — zero-touch for routine updates, deliberate review for
  breaking ones. Requires "Allow auto-merge" enabled in repo settings.

## [3.13.77] — 2026-07-17

### Fixed
- **CI lint gate:** switched the vanity onion code from `golang.org/x/crypto/sha3`
  to the standard library `crypto/sha3` (Go 1.24+), which the current `govet`
  inline analyzer requires — restores a green build.

### Security
- **CodeQL "Zip Slip" (High):** the managed-Tor expert-bundle extraction already
  neutralised traversal via `filepath.Base`, but it now also verifies each
  destination stays within the target directory before writing — an explicit,
  analyzer-recognised guard against archive path traversal.

## [3.13.76] — 2026-07-17

### Added
- **VayuTor vanity (custom-prefix) onion addresses.** Give a domain a
  recognisable `.onion` that starts with letters you choose instead of a random
  one. From the VayuTor page, pick a domain and a short prefix (`a–z`, `2–7`) and
  VayuTor brute-forces a matching v3 ed25519 key **in the background on the
  server** — no key ever leaves the box — using all cores but one. Progress
  (attempts, elapsed) updates live; the search survives navigating away and is
  cancellable. When it lands, the new identity is persisted and the domain's
  onion is republished under the vanity address (replacing the old random one).
  Difficulty scales ~32× per character: 1–4 chars is seconds, 5 a few minutes,
  6–7 can take hours (capped at 7, with a warning past 5). The v3 address
  derivation is verified in tests to round-trip its checksum exactly as a Tor
  client does, and the stored `ED25519-V3` key is proven to re-derive the same
  address.

### Added
- **VayuTor onion hardening.** A new "Hardening" card plus always-on protections
  for onion visitors. Responses served over a `.onion` now **omit HSTS** (it's
  meaningless over a plain-HTTP, self-authenticating address) and send
  **`Referrer-Policy: no-referrer`** so the onion URL never leaks as a Referer to
  any off-onion navigation or subresource — while clearnet responses keep HSTS
  and the standard policy. The strict CSP is unchanged and already onion-safe (no
  `upgrade-insecure-requests`), and VayuTor still opens no inbound ports.
- **Onion-Location advertising toggle** (`tor.onion_location`, default on): turn
  off to keep onions live without announcing them to Tor Browser on clearnet
  responses. Read live by the middleware — no restart.

### Notes
- A "disable clearnet / Tor-only" switch is intentionally **not** offered: it
  would make the site unreachable to every non-Tor visitor, and that policy
  belongs at the web-server/DNS layer, not a per-site toggle that could lock the
  operator out. Onion abuse continues to be handled by VayuShield.

## [3.13.74] — 2026-07-17

### Added
- **VayuTor onion health & alerts.** VayuTor now tracks a coarse onion-service
  health state — `healthy` / `starting` / `degraded` (some domains unpublished) /
  `down` (control port unreachable) / `off` — with a short transition history,
  all shown on a new "Health & alerts" card. When onions go **down** or
  **recover**, it POSTs a signed alert to any webhook subscribed to
  `tor.onion_down` / `tor.onion_recovered` (reusing the existing signed-webhook
  dispatcher). Alerts are debounced (a bad state must persist ~90s), so a brief
  blip or the normal startup climb never fires a false alarm; a latch guarantees
  exactly one "recovered" even across a `down → starting → healthy` path.
  Deactivation never alerts. The alert payload is operational only (state,
  reason, onion/domain counts, bootstrap %) — no visitor data. Health is
  re-judged at every reconcile, including early error exits, so an outage is
  noticed even when reconcile bails early.

### Added
- **VayuTor "Popular pages" — opt-in, privacy-safe onion analytics.** A new card
  on the VayuTor page can show which pages are most visited over Tor, as an
  **aggregate cumulative count per page** (host + path → total views). It is
  privacy-preserving by construction: no IP (an onion service never sees one), no
  time, no session, no user agent, no ordering — so individual visits can't be
  correlated and a visitor can't be identified. Off by default (preserving the
  stricter "not even the path" posture); the operator enables it with one click,
  and can reset the counts. Counts are bounded (top pages, capped cardinality)
  and persisted alongside the visit tally. Setting: `tor.page_stats`.
- The privacy-posture text on the page now reflects the actual state — it only
  claims "no path" while per-page counts are off, and states the aggregate-only
  guarantees when they're on.

### Not implemented (by design)
- **Visitor country / geolocation for onion visits is impossible and omitted.**
  Over a Tor onion service the visitor's IP never reaches the server (the
  connection arrives from a Tor rendezvous point), so there is nothing to
  geolocate — any "country" would be fabricated and would betray VayuTor's
  privacy guarantee. The page says so explicitly rather than offering it.

## [3.13.72] — 2026-07-17

### Added
- **VayuTor runs its own current Tor when the system's is too old — nothing to do
  on the server.** On a host whose system `tor` is missing or too old to validate
  today's consensus (e.g. an EOL distro shipping 0.4.2.x), the managed tor now
  downloads the Tor Project's official static "expert bundle" into VayuPress's own
  writable state dir (`<tor-dir>/dist/`) and runs *that* binary — as the
  unprivileged service user, no root, no apt, nothing edited on the server. The
  bundled build is pinned to its own libraries via `LD_LIBRARY_PATH`, verified to
  run and be ≥ 0.4.7 before use, and cached across restarts. The download is
  discovered from the Tor distribution index (newest stable first, primary host +
  archive mirror, current and legacy filename schemes), fetched over CA-validated
  HTTPS. Controlled by `VAYUOS_TOR_DOWNLOAD` (default on).
- **Graceful, no-regression fallback.** If the download can't be reached, or the
  modern build won't start on an ancient host libc, VayuTor falls back to the
  system tor exactly as before (a failed attempt is cooled down for 15 min;
  toggling VayuTor off→on forces an immediate retry). The admin page then shows
  the precise reason and a one-time, run-once-as-root `apt` command to install a
  current, self-updating Tor from Tor Project's official repository. When the
  managed build *is* in use, the publishing card shows "running VayuPress-managed
  Tor <version>".

## [3.13.71] — 2026-07-17

### Security
- Bumped `filippo.io/edwards25519` v1.1.0 → v1.2.0 (patches a low-severity
  `MultiScalarMult` advisory), pulled in transitively by the embedded obfs4
  transport. Not reachable from VayuPress code paths; upgraded for hygiene.

### Added
- **VayuTor reports the Tor daemon version and flags an ancient Tor.** When Tor
  can't validate the network consensus ("not signed by sufficient authorities"),
  the page now shows the detected version and, if it predates ~0.4.7 (e.g. an EOL
  distro's 0.4.2.x), says plainly that Tor itself is too old for today's network
  — which bridges cannot substitute for — and points to current Tor. Read via
  the control port (`GETINFO version`).

## [3.13.70] — 2026-07-17

### Fixed
- Release-hygiene build: the green-CI cut of 3.13.69's embedded obfs4 transport
  (the standalone-staticcheck `net.Error.Temporary` deprecation is folded in). No
  behaviour change from 3.13.69.

## [3.13.69] — 2026-07-17

### Added
- **obfs4 is built into VayuPress — obfs4 bridges now need zero server install.**
  VayuPress runs the obfs4 pluggable transport itself (the lyrebird obfs4 client,
  exposed as an internal `__run-obfs4` subcommand tor execs); the managed tor is
  pointed at `ClientTransportPlugin obfs4 exec <vayupress> __run-obfs4`. So an
  operator on a Tor-blocking VPS can paste obfs4 bridges in the VayuOS **Bridges**
  panel and it just works — **no `obfs4proxy` package, nothing done on the
  server**. A system `obfs4proxy`/`lyrebird` is still used when present. Client
  transport only — no new inbound surface; state lives under the writable tor dir.

### Fixed
- **VayuTor: clearer bridge guidance.** The Bridges panel now warns when the
  pasted bridges are **IPv6-only** (they need server IPv6, which most VPS lack —
  the usual cause of "connections died in state connect()ing"), and steers to
  IPv4 obfs4 bridges. The page also reports when the obfs4 transport is somehow
  unavailable.

## [3.13.68] — 2026-07-17

### Added
- **Configure Tor bridges entirely from VayuOS — no server access.** The VayuTor
  page now has a **Bridges** panel: paste obfs4 bridge lines (from
  bridges.torproject.org) and save. They're stored in the site database
  (`tor.bridges`) and applied **live** — the managed Tor restarts through the
  bridges automatically, with no SSH, no env-file editing, and no restart of
  VayuPress. The settings value takes precedence over the `VAYUOS_TOR_BRIDGES`
  env var; clearing the box reverts to a direct connection. This makes the
  "network blocks Tor" fix fully self-service from the admin UI — which is
  exactly where an operator on a Tor-hostile VPS needs it.

## [3.13.67] — 2026-07-17

### Added
- **VayuTor works even when the network blocks Tor — Tor bridges + obfs4
  auto-escalation.** Some VPS/mail hosts null-route public Tor relay IPs, so
  bootstrap fails with "No route to host" and onions never publish. VayuTor now
  climbs a one-shot escalation ladder on its managed Tor — **direct → ports
  80/443 only → bridges** — driven by the existing stall detector (a slow but
  climbing bootstrap is never disturbed). When Tor's log proves an IP-level block
  (NOROUTE), or the operator has configured bridges, it **skips straight to
  bridges**, whose IPs aren't on any public-relay blocklist; obfs4 additionally
  disguises the traffic to defeat DPI.
  - Operator bridges via **`VAYUOS_TOR_BRIDGES`** (obfs4 lines from
    https://bridges.torproject.org, newline/`;`-separated). The deploy/update
    scripts now install **`obfs4proxy`**, which the managed Tor spawns as the
    same unprivileged user (no root, no new inbound surface).
  - The VayuTor page shows the current **transport** and, on failure, the exact
    remedy read from Tor's log (allow outbound / fix clock / install obfs4proxy /
    paste bridges).
  - Onion services stay E2E-encrypted and the onion↔domain mapping is already
    public, so routing the server's own circuits through bridges is anonymity-
    and confidentiality-neutral — the "count-only, nothing tracked" posture is
    unchanged. See ADR-0138.

## [3.13.66] — 2026-07-17

### Fixed
- **VayuTor: pinpoint and prevent the "consensus not signed by sufficient
  authorities" bootstrap failure.** When Tor reaches ~30% and then fails to
  validate the network consensus, it is almost always a **skewed server clock**
  (even a few minutes breaks consensus validation) or an outdated `tor` package.
  The VayuTor page now recognises this signature in Tor's log and shows the exact
  fix (`sudo timedatectl set-ntp true`, or update `tor`), rather than leaving the
  operator to decode a raw warning. The deploy and update scripts now **enable
  NTP time sync** when installing Tor, so a correct clock — required by Tor (and
  by TLS and DKIM) — is in place automatically.

## [3.13.65] — 2026-07-17

### Added
- **VayuTor self-recovers from a stalled Tor bootstrap, and shows the reason.**
  On locked-down hosts that only permit outbound 80/443, Tor can get stuck early
  in bootstrap. VayuTor now detects a genuinely **stalled** bootstrap (no forward
  progress for 150s — a merely-slow first bootstrap that keeps climbing is never
  disturbed) and automatically restarts its managed Tor in **firewall-friendly
  mode** (`ReachableAddresses *:80,*:443`), which gets through most restrictive
  egress firewalls. The VayuTor page also now shows the **last few lines of Tor's
  own log** while publishing, so the exact bootstrap phase or error is visible
  without shell access.

## [3.13.64] — 2026-07-17

### Fixed
- **VayuTor no longer breaks after an update ("connection reset by peer" on the
  control socket, onions disappear).** When VayuPress updates in place (the
  in-app one-click self-update re-execs without a full process restart), the
  previous managed `tor` child survived but the new process lost its handle to
  it. That orphan kept the data-directory lock and control socket, so the new
  VayuPress could not start its own `tor` and the control connection reset.
  VayuTor now **reaps an orphaned `tor`** before spawning — matched precisely by
  a pidfile and by scanning `/proc` for a `tor` launched against *our* torrc, and
  verified to actually be `tor` so PID reuse can never hit an unrelated process —
  then clears the stale lock/socket and starts cleanly. If the control connection
  still fails, the managed `tor` is torn down so the next reconcile respawns a
  fresh one instead of looping against a dead socket.

## [3.13.63] — 2026-07-17

### Added
- **VayuTor now shows real Tor bootstrap progress.** After activation the page
  reports "Connecting to Tor (NN%)" — read live from the tor daemon
  (`status/bootstrap-phase`) — and only flips to a green **Active** once tor has
  fully joined the network at 100%, which is when the `.onion` addresses actually
  become reachable. This explains the common "Onion site not found" seen for the
  first couple of minutes after activation (the descriptor can't publish until
  bootstrap completes) and tells the operator to allow **outbound** connections
  if it stalls. The page auto-refreshes to **Active** when 100% is reached.
- **Managed tor now writes a diagnostic log.** Its notice log is captured to
  `<state>/tor/tor.log` (and surfaced on the VayuTor page) so a stuck bootstrap
  or network-blocked server is inspectable instead of silent.

## [3.13.62] — 2026-07-17

### Fixed
- **VayuTor now activates the moment `tor` is installed — no VayuPress restart.**
  The managed tor's binary was resolved once at process start, so a server that
  installed `tor` *after* VayuPress was already running (or updated only the
  binary via the in-app one-click updater) never engaged managed mode and sat at
  the external "connection refused." The tor binary is now resolved **lazily at
  connect time**, so it is picked up on the next reconcile (≤60s) with no restart.
- **Actionable VayuTor status when Tor is missing.** The page now shows exactly
  what to do — `sudo apt-get install -y tor` (or re-run the updater) — instead of
  the confusing `dial tcp 127.0.0.1:9051: connect: connection refused`.
- Internal: wired the managed-tor error into the status snapshot (resolves the
  deadcode-gate CI failure on the 3.13.61 tag).

## [3.13.61] — 2026-07-17

### Added
- **VayuTor self-manages its own Tor daemon (zero-setup activation).** VayuTor no
  longer depends on a separately-configured system Tor service. When no external
  control port is reachable, VayuPress now runs its **own `tor` process** as the
  unprivileged service user — with a private data directory, a cookie-authenticated
  loopback control socket, and no SOCKS/relay/exit surface. This means VayuTor
  works with only the **`tor` binary present** — no root at runtime, no `systemd`
  unit, no `torrc` editing, no control-port or group setup — and it keeps working
  across the **in-app one-click self-update** (which only swaps the binary). An
  existing external control port (`VAYUOS_TOR_CONTROL_ADDR`) is still preferred
  when reachable; the managed instance is the automatic fallback. New env knobs:
  `VAYUOS_TOR_MANAGED` (default `on`), `VAYUOS_TOR_BINARY`, `VAYUOS_TOR_DIR`. The
  onion services remain bound to VayuPress's lifetime — deactivating VayuTor stops
  the managed `tor` entirely.

### Changed
- **Clearer VayuTor "bringing onions up" guidance.** The activation hint now
  reflects managed mode: VayuTor runs its own Tor and only needs the `tor` binary
  installed (the deploy/update script installs it in one step).

## [3.13.60] — 2026-07-17

### Fixed
- **VayuTor activation no longer fails with "CSRF token missing or invalid."**
  The one-click **Activate onion services** button on the VayuTor page posted a
  CSRF token that was baked into the page markup, but the admin layout rotates
  the double-submit `vp_csrf` cookie on every render — so by the time the form
  submitted, its token no longer matched the cookie and the request was rejected
  with `csrf_invalid`. The toggle now reads the *live* `vp_csrf` cookie at submit
  time (the same double-submit pattern every other VayuOS mutation already uses),
  so activating and deactivating VayuTor works reliably. The page also no longer
  issues its own competing CSRF cookie.

## [3.13.59] — 2026-07-17

### Added
- **VayuTor — one-click Tor onion services for every hosted domain (ADR-0138).**
  A new **VayuTor** page in VayuOS publishes each domain you host as its own Tor
  v3 `.onion` alongside its normal clearnet URL — both work at the same time,
  with no speed or quality trade-off. It is a heaven for privacy-focused
  visitors: no ISP, network provider, or on-path observer can see who reaches the
  site, and the server never learns a visitor's IP.
  - **One click.** Activation drives a local `tor` daemon's cookie-authenticated
    control port (`ADD_ONION`) — no torrc editing, no tor restart. Every hosted
    domain automatically gets a stable `.onion` (its key is pinned in your own
    DB, so a restore brings the same address back).
  - **Both URLs, one site.** An incoming `<onion>.onion` request is mapped to the
    right domain and served the exact same content as clearnet; clearnet
    responses advertise the onion via the `Onion-Location` header so Tor Browser
    discovers it automatically. The clearnet path is byte-for-byte unchanged and
    costs nothing when VayuTor is off.
  - **Count-only analytics.** The VayuTor page shows a single number — total Tor
    visits. No identity, no time, no path, no user agent, no cookie: nothing
    per-visitor is ever recorded (Tor provides no IP to begin with).
  - **No new attack surface.** VayuTor opens **no** inbound port; onion traffic
    arrives through Tor's rendezvous circuits. Dormant by default (opt-in
    toggle), a non-critical subsystem — an onion outage never touches the
    clearnet site. The deploy/update script installs Tor and enables the loopback
    control port automatically. Strict CSP throughout (server-rendered page + a
    nonce'd same-origin island). Onion identities in migration 065.

## [3.13.58] — 2026-07-17

### Added
- **Theme Store: "Editor" — the world-newsroom flagship theme.** A
  broadsheet-grade newspaper theme for serious, multinational publications,
  built from the design language of the world's leading papers (hero-plus-grid
  front page, print rule hierarchy, rationed accent, serif display over sans
  chrome):
  - **Front page as a broadsheet.** A centred serif masthead over an uppercase
    section bar with double print rules, a hairline story lattice (FT/NYT-style
    column rules at any breakpoint with no extra markup), and a lead-story
    package — kicker-style meta, display headline, standfirst-sized excerpt and
    side art — from the first post automatically.
  - **Trending & pinned as a numbered "Most read" rail.** The platform's
    trending widget renders with oversized serif rank numerals, hairline rows
    and a pinned "package" treatment crowned by an accent rule.
  - **Newspaper reading experience.** Charter/Georgia book-serif body at a
    68ch measure, drop caps (double-pathed `initial-letter` + float fallback),
    centred hairline pull quotes, dateline-style meta, news-blue content links,
    balanced headlines (`text-wrap: balance/pretty`), and a signed author box
    ruled top and bottom.
  - **Premium motion with zero JavaScript.** A scroll-driven pure-CSS reading
    progress bar and soft cross-page view-transition fades — both strictly
    progressive (`@supports`-gated) and fully disabled under
    `prefers-reduced-motion`; hover language is underline-reveals and gentle
    art zooms, never card lifts.
  - **Newsroom component kit** (`.editor-*`): stripline/topbar with market
    up/down colours, breaking bar and WCAG-safe ticker, breaking/live kickers
    with a reduced-motion-safe pulse, opinion/compact/headline teaser
    densities, story packages with pinned treatment, section containers with
    slice layouts, key-points box, share rail, membership fade + prompt, and a
    fat sitemap footer.
  - **Theme Studio controls.** Five new Editor controls — paper tone
    (bright/ivory/FT-salmon), drop cap, story-grid rules, reading progress and
    page transitions — plus the shared density and heading-size extras. All
    realised through the standard options pipeline, previewable live, and
    picked up automatically by the VCB validator.
  - **Speed & sovereignty by construction.** Pure CSS, system font stacks only
    (ui-serif/Iowan Old Style/Palatino/Georgia display voice), no external
    requests, fixed image aspect ratios (zero CLS), compositor-only animation
    — the 100/100 PageSpeed posture of the platform is preserved untouched,
    light & dark.

## [3.13.57] — 2026-07-17

### Changed
- **Adding a secondary domain no longer provisions it automatically — sync is
  now a manual, per-domain decision (VayuDomains P5).** Previously, registering
  a domain in VayuOS → Domains was enough for the next deploy/update run to
  obtain its TLS certificate and publish its nginx vhost. Now a new domain is
  registered on **manual hold** and the provisioning helper skips it until you
  explicitly approve it: press **Sync now** on the domain's row (new *Sync*
  column shows `Synced` / `Manual hold`, with a matching **Pause sync** action),
  or run `vayupress domains sync <host>` on the server. Domains registered
  before this release were already provisioned, so they are backfilled as
  approved and keep being maintained across updates — nothing changes for them.
  - Migration 064 adds `domains.sync_state` (`approved` | `hold`).
  - `vayupress domains hosts` (the helper's work list) emits only sync-approved
    secondaries; new `--hold` and `--all` flags list what is parked / everything.
  - New CLI verbs: `vayupress domains sync <host>` and `vayupress domains hold
    <host>`; new CSRF-protected endpoint `POST /os/api/domains/{id}/sync`.
  - `setup-vayudomain.sh` treats explicit host arguments as operator intent
    (records the approval, provisions immediately) and logs which domains were
    skipped on manual hold, so deploy/update logs always explain themselves.

## [3.13.56] — 2026-07-17

### Added
- **VayuTalk self-destruct timers + Live mode (ADR-0131 amendment).** Messages
  now burn on a timer that starts **when they're read**, not when they're sent.
  - **Timer options:** 5 seconds, 1 minute, 5 minutes, 15 minutes, 30 minutes,
    1 hour — **default 5 minutes** (safe but usable). The web composer's
    "Disappears" selector carries all six; each message shows a live 🔥 countdown
    once it's read, and disappears on both sides on the same clock.
  - **🔥 Live mode:** a composer toggle for messages that are **never stored on
    the server** (delivered only while both of you are online) and vanish the
    moment they're read.
  - **Server:** the envelope now carries a `burn_seconds` timer (clamp widened to
    `[5s, 1h]`) separate from the unread holding window (`ExpiresAt`, default 24h,
    tunable via `VAYUOS_TALK_UNREAD_TTL_SECONDS`), so a short "5s after read"
    timer no longer evicts an unread message five seconds after it's sent.
    Everything stays in RAM — no new table, a restart still purges all. The web
    console remains a passive viewer that never read-destroys the app's copy.

## [3.13.55] — 2026-07-17

### Fixed
- **Traffic-chart hover tooltip was unreadable (dark text on the dark card).**
  The tooltip's date/value text referenced an undefined `--text-1` token, so in
  SVG it fell back to the initial fill (black) and blended into the dark tooltip
  background. Added the missing `--text-1` / `--surface-2` token aliases (so they
  follow light/dark automatically) and hardened the tooltip to an elevated
  surface (`--bg-surface-3`) with a strong border and bright `--text` labels —
  now clearly legible in both themes. This also fixes five other components that
  referenced the same undefined token.

### Added
- **Audience → Traffic channels.** A new card groups visits into **Direct**,
  **Organic search**, **Social** and **Referral** (with each channel's share),
  classified purely from the stored referrer host — cookieless, no-PII, offline.
- **Geography → Continents.** A world-region breakdown aggregated **offline**
  from the country codes VayuPress already resolves (no proxy headers needed), so
  the Geography tab always shows a real geographic distribution.

### Changed
- **Geography tab polished.** Long country/region/city lists now scroll inside
  their card (the page is no longer one endless column), and when region/city
  data is absent the tab shows a **precise, premium setup card** — the exact
  Cloudflare toggle (*Rules → Settings → Add visitor location headers*) plus the
  other recognised providers — instead of a wall of guidance text. Region and
  city still require a reverse-proxy location header by design (VayuPress never
  geolocates IPs itself); countries and continents work with no proxy.

## [3.13.54] — 2026-07-17

### Changed
- **Analytics Live page redesigned + GDPR-safe engagement depth (Overview).**
  The realtime "Live" tab is rebuilt from plain two-column tables into a premium
  surface, and the Overview gains four derived engagement metrics — all still
  cookieless, no-PII, aggregate-only (GDPR-safe by construction).
  - **Live hero:** a large active-visitor count with a "LIVE" badge, a pulsing
    status dot, and a concentric radar-ring motif. Below it, the three realtime
    panels (top countries, active pages, referrers) now render as **proportional
    coloured bars with each row's share of the total** — the same bar language
    used across the rest of Analytics — instead of bare number tables. Country
    rows keep their self-hosted flag image (neutral globe when unknown).
  - **Overview engagement strip:** Pages / visit, Views / visitor,
    Visits / visitor, and Engaged visits (inverse of bounce). These are pure
    ratios of existing aggregate counters — no new data is collected or stored.
  - **Speed & CSP:** no new scripts or libraries. The realtime poller stays a
    tiny vanilla `fetch` (already visibility-paused when the tab is hidden) and
    now builds the bars via DOM APIs only — no inline styles, no `innerHTML`, so
    it remains strictly CSP-safe (`style-src 'self'`). All motion (radar rings,
    row fade-in, live dot) is transform/opacity only and fully disabled under
    `prefers-reduced-motion`. Zero added page weight; the widths reuse the
    existing `w-*` utility classes.

## [3.13.53] — 2026-07-17

### Changed
- **Analytics traffic chart — interactive, premium, and instant.** The Overview
  chart is redesigned: a gradient-filled pageviews area, a dashed unique-visitors
  line, quartile value gridlines with axis labels, evenly-spaced date labels, and
  a "peak" badge. **Hovering any point now shows a styled tooltip with that day's
  date, pageviews and unique visitors** — with a guide line and highlighted dots,
  and the tooltip flips to stay on-screen near the edges. It is built entirely in
  pure SVG + CSS (no JavaScript, no charting library, no inline styles), so it is
  fully CSP-safe, adds zero page weight, and is buttery-smooth; a light one-shot
  draw-in animation respects `prefers-reduced-motion`. GDPR posture unchanged —
  aggregate, no-PII counts only.

### Changed
- **VayuShield detection is now self-maintaining in the background; the stale
  per-IP block list is gone (ADR-0137, stage 4).** The operator-reported problem
  — "the blocked-IP list is not updated" — is resolved by removing the list, not
  patching it: a scrolling table of hashed IPs was stale by design (the live jail
  is in memory, not that log) and was easily misread as the current block list.
  - The "Recent blocks" panel section is removed. Live enforcement state is the
    aggregate counters in the status hero (blocked / reputation-jailed / suspects
    / fair-shed / surge / cache-hit), which reflect the actual in-memory gates.
  - The existing background maintenance cycle now also **prunes efficiently**: new
    composite indexes (migration 063) make the prune and promotion passes
    index-driven instead of full-table scans, the deletes are **chunked** so each
    holds the single database writer only briefly (safe even with a huge swarm
    backlog), and the hashed block log is now **retention-bounded** so it can no
    longer grow without limit. No new goroutine — it extends the existing
    shutdown-aware reporter.

### Fixed
- **VayuShield learning system now actually learns and self-heals (ADR-0137,
  stage 3).** Three coupled fixes close the adaptive-detection loop the operator
  reported as "not working":
  - **Dead learning path revived.** The rule that was meant to record a
    suspicious-but-not-blocked fingerprint as a review candidate could never fire
    (its condition was self-contradictory), so the signature database only ever
    grew from hard blocks. It now correctly records a recurring *challenged*
    unknown client as an auto-learned candidate.
  - **Repeat offenders auto-block faster.** A recurring bot-like fingerprint now
    reaches the action threshold in a few sightings (steeper confidence curve),
    so the shield blocks it from the learned signature instead of re-analysing it
    every time.
  - **False-positive self-heal is now real.** Solving the browser check reports a
    false positive for that fingerprint, and any signature with a false positive
    is frozen — so a *coarse* fingerprint that many real users share (e.g. behind
    a reverse proxy that hides the fine-grained TLS signals) can **never**
    auto-escalate to a hard block. Previously this guard existed in the code but
    was never triggered, so it protected no one.

### Added
- **VayuShield Aegis — swarm-scale classification cache + zero-config surge on
  overwhelm (ADR-0137, stage 2).** Two changes that keep the site fast under a
  large scraper/crawler swarm:
  - **Signature-lookup cache (L6).** A fixed-memory, sharded, negative-caching
    layer in front of the adaptive bot-signature database, so classifying an
    unproven request no longer hits SQLite on every request. The common case — a
    fingerprint the database has never seen (real readers, flash crowds) — is
    served from memory; the cache is bounded (can't grow under a
    million-distinct-fingerprint flood), keyed only on the fingerprint hash (no
    IPs, GDPR-clean), and invalidated instantly when the operator verifies,
    dismisses, or flags a signature. A live "Cache hit (L6)" figure appears on
    the Bot Shield panel.
  - **Surge auto-engages when the site is genuinely overwhelmed.** Sovereign
    Surge now also switches on automatically when the admin-sovereignty lane is
    critically saturated — which catches a **low-and-slow, high-cardinality
    swarm** (a million distinct IPs each sending a little) that fills the server
    without ever spiking per-second request rate, a pattern the rate-based
    detector alone would miss. Mild load still uses the gentler fair-shed; only
    genuine overwhelm triggers the challenge.

## [3.13.49] — 2026-07-16

### Added
- **VayuShield Aegis — Sovereign Surge (Under-Attack Mode), ADR-0137.** A new
  in-binary defence tier that lets a single VPS absorb a **million-bot
  scraper/crawler swarm without a CDN and without slowing the site**. During a
  detected flood (or when the operator flips the switch), every unverified
  visitor is met with a cheap, stateless proof-of-work browser check **before**
  any bot classification, fingerprinting, template render or SQLite read. A real
  browser solves it once in a moment and then rides the priority lane untouched;
  a bot that will not run it never reaches the expensive path — it costs the
  server ~one hash. Sovereign and CSP-safe (the solver is a same-origin script,
  no CDN, no `unsafe-inline`/`eval`, no third party), and fail-open (a signer
  error serves the request rather than blocking it).
  - **Anti-replay clearance:** the clearance cookie is cryptographically bound to
    the solver's network, so a check solved once cannot be shared across a
    botnet (no new personal data stored).
  - **SEO-safe:** feeds, sitemaps and `robots.txt` are never challenged
    (matched by shape, so Hugo `/index.xml` and WordPress `/feed/` are covered),
    and the challenge response carries `Retry-After` so crawlers treat it as
    transient. A manually-forced surge auto-expires after 12h so a forgotten
    switch can never keep challenging visitors; automatic surge relaxes the
    instant the flood passes.
  - Live status + an "Surge checks (L3)" counter on the Bot Shield panel, plus a
    `Sovereign Surge (Under-Attack Mode)` toggle (also `VAYUSHIELD_SURGE`).

### Changed
- **Premium UX, phase 4 — flagship themes rebuilt on the sovereign token
  system (ADR-0136).** The flagship themes (Apex, Vayu) now consume the shared,
  scheme-adaptive elevation (`--sh-*`) and motion (`--t*`) tokens instead of
  hardcoding their own shadows and durations — so cards and surfaces gain depth
  that lightens on light schemes and deepens on dark automatically, with one
  cohesive motion feel. Pure CSS custom properties, literal fallbacks preserved
  for defensiveness; no build step, no dependency, no visual regression on
  unsupported engines.

### Fixed
- **Admin performance — Alpine loads only where an island exists (ADR-0136).**
  The eval-free Alpine runtime (and its document-wide `MutationObserver`) is now
  emitted **only** on admin pages that actually host an `x-data` island — today
  just the API Keys manager. Island-free pages (the overwhelming majority) ship
  no Alpine at all, eliminating an unnecessary ~61 KB parse and an always-on DOM
  observer on every other admin page. Detection is automatic from the rendered
  body, so any future island is covered without per-page bookkeeping.
- **Accessibility — API-keys live filter announces results (WCAG 4.1.3).** The
  client-side key filter now writes its match count / empty state into an
  `aria-live` status region, so screen-reader users hear how many keys matched
  instead of the table silently emptying.

### Added
- **VCB theme contract validates sovereign tokens (ADR-0136).** The Vayu
  Compatibility Bible validator now exports the sanctioned `--vp-*` token
  vocabulary (derived from the compiler, so it can never drift) and warns when a
  third-party theme's `custom_css` references a `--vp-*` token the compiler
  never emits with no fallback — a typo that would silently resolve to nothing —
  keeping the motion/elevation polish safe to scale across the ecosystem.

## [3.13.47] — 2026-07-16

### Added
- **Premium UX, phase 4 — theme-author documentation for the motion/token
  system (ADR-0136).** The Vayu Compatibility Bible
  (`/docs/compatibility/vcb`) now documents the shared motion & elevation token
  vocabulary every compiled theme exposes — the scheme-adaptive elevation
  scale (`--sh-*`), easing curves, durations, composed transition tokens, the
  spacing and z-index scales — with copy-paste examples, so third-party themes
  build on the same premium primitives the platform uses, with zero build step
  and no dependency. Note that native page View Transitions are enabled
  site-wide and reduced-motion is respected automatically, so theme authors get
  the polish for free.

## [3.13.46] — 2026-07-16

### Added
- **Premium UX, phase 3 — Alpine.js islands under strict CSP (ADR-0136).** The
  VayuOS admin can now use small, reactive client-side "islands" where they
  genuinely beat an HTMX round-trip — with **no `unsafe-eval` and no CDN**:
  - **Vendored the eval-free Alpine.js CSP build** (`@alpinejs/csp` 3.15.12,
    ~20 KB gzip) into `static/js/alpine-csp.min.js`, self-hosted and served
    same-origin like HTMX and DOMPurify. It interprets directives with a
    tokenizer + AST walker instead of `new Function`, so it runs under
    `script-src 'self' 'nonce'`. Integrity-verified and proven eval-free by a
    regression test; MIT license vendored alongside.
  - **`vayu-islands.js`** — a first-party `Alpine.data()` registry
    (`filterList`, `disclosure`, `copyable`), registered on `alpine:init` with
    components referenced by name only (CSP-build contract; never inline
    expressions). Loaded before the Alpine build, both deferred and nonce-
    threaded.
  - **First island**: the API Keys list gains an instant, client-side filter
    (type to narrow by label or key prefix). Purely additive — if Alpine is
    absent the input is inert and every row shows, and the create/rotate/revoke
    flows (vanilla JS) are untouched.
  - HTMX remains the backbone; Alpine is the exception, scoped to the admin and
    within the constitution's <50 KB-gzip admin-framework budget.

## [3.13.45] — 2026-07-16

### Added
- **Premium UX, phase 2 — native View Transitions (ADR-0136).** Same-origin
  navigation now crossfades instead of hard-cutting, with **zero JavaScript and
  zero new dependency** — a pure CSS at-rule (`@view-transition { navigation:
  auto }`) in the same-origin stylesheets, so `style-src 'self'` is untouched:
  - **Public site**: article→article and home→article navigations transition
    smoothly (a soft fade + 6px rise on the incoming page). Fully progressive —
    browsers without support just navigate normally.
  - **VayuOS admin**: full-page sidebar navigation transitions, and HTMX
    fragment swaps now ride the same View Transitions API
    (`globalViewTransitions` enabled in the htmx-config) for app-like partial
    updates.
  - Every transition runs on the compositor (GPU), off the main thread, and is
    fully neutralised under `prefers-reduced-motion`. No framework, no CDN, no
    `unsafe-eval`.

## [3.13.44] — 2026-07-16

### Added
- **Premium UX, phase 1 — a sovereign motion/design-token layer (ADR-0136).**
  The Go theme compiler now emits a shared design-primitive block into every
  compiled `/theme.css`, so **public themes inherit the same premium vocabulary
  the admin already uses** — with zero build step, zero dependency, and no CDN:
  - **Scheme-adaptive elevation** (`--vp-sh-sm/--vp-sh/--vp-sh-lg`, subtler on
    dark, more present on light) plus bare `--sh-*` aliases.
  - **Easing curves** (`--vp-ease`, `--vp-ease-out/in/spring`), **duration
    tokens** (`--vp-dur-fast/--vp-dur/--vp-dur-slow`), composed **transition
    tokens** (`--vp-t*`), a **spacing scale** (`--vp-sp-1…12`) and a **z-index
    scale** — all with bare aliases theme CustomCSS can use directly.
  - A visible first application: public post cards, embed cards, video facades,
    code blocks and buttons gain soft, token-driven elevation on hover. Purely
    additive and covered by the site's existing `prefers-reduced-motion` guard,
    so nothing animates for readers who ask it not to.
  - **ADR-0136** records the decision: adopt Tailwind's *aesthetic* as tokens
    (never the framework — CDN Tailwind breaks `style-src 'self'`, build-time
    Tailwind breaks "no build step"); keep HTMX as the backbone; and admit the
    eval-free Alpine.js **CSP build** (self-hosted, nonce'd) for later reactive
    islands — all within the strict CSP, zero-third-party, single-binary
    contract.

## [3.13.43] — 2026-07-16

### Security
- **VCB validator: confine the plugin executable file-check to the package
  directory (CodeQL #62/#63 — path expression from untrusted input).** The
  optional `--files` check resolved the manifest-supplied `executable` path
  with `filepath.Join` + `os.Stat`/`os.Open`, which CodeQL flagged as a path
  expression built from untrusted data. All filesystem access now goes through
  `os.OpenRoot(baseDir)` (Go 1.24+): the executable name is resolved *inside*
  the package root and can never reach a file outside it — not via an absolute
  path, a `../` sequence, or a symlink pointing out of the tree. A regression
  test proves an escaping symlink is refused rather than hashed. (The pre-existing
  `!IsAbs` / no-`..` guards already blocked the obvious cases; this removes the
  flagged sink entirely and hardens against symlink escape.)

### Added
- **One-click Compatibility (VCB) access from the API Keys console.** The
  VayuOS **API Keys** page (`/os/apikeys`) now has a **Compatibility (VCB)**
  button in its header and a dedicated card: open the Vayu Compatibility Bible,
  the API-keys & permissions reference, and ADR-0135 in one click, with the
  live contract endpoints (`GET /api/v1/vcb/contract`, `POST /api/v1/vcb/validate`)
  and the `vayu-compat` CLI documented inline. CSP-safe (same-origin links, no
  inline style).

## [3.13.42] — 2026-07-16

### Security
- **VCB theme validator: closed an external-`url()` bypass.** The
  `theme.css.external` check (which enforces the "zero third-party fetches"
  contract for a theme's CustomCSS) could be defeated two ways a browser still
  resolves to an external fetch: CSS backslash escapes
  (`url(\68ttps://evil.example/…)` → `https://…`) and CSS whitespace —
  including newlines — before the value. The check now CSS-unescapes and
  fully whitespace-normalises each `url()` body before the scheme test, so the
  decoded value it inspects is exactly what the browser would fetch. Same-origin
  and `data:` URIs remain allowed.

### Fixed
- **VCB theme validator: stop falsely rejecting per-theme options.** `density`
  and `headingscale` are applied by `CompileCSS` purely by option key,
  regardless of theme name, so any third-party theme may set them — but the
  validator built its option schema from the per-name narrowing, which omits
  those keys for any name that isn't a built-in preset (which a third-party
  theme can never be). It now validates against the full option vocabulary
  (`theme.AllOptionDefs`), matching what the compiler actually honors.
- **VCB plugin validator: `run_as` now matches the sandbox exactly.** The
  validator accepted values (`+`-signed, or above the uint32 ceiling like
  `4294967296`) that the sandbox's own `strconv.ParseUint(_,10,32)` rejects —
  silently leaving the subprocess running as the host user. `validRunAs` now
  mirrors the sandbox parse, so a manifest that validates drops privileges as
  declared.

All three were surfaced by the pre-release adversarial review and are the same
class of defect VCB exists to prevent: the validator disagreeing with the host
it certifies against. Each ships with a regression test.

## [3.13.41] — 2026-07-16

### Added
- **Vayu Compatibility Bible (VCB) — enforceable extension contracts
  (ADR-0135).** Any developer or AI agent can now build themes, plugins, and
  tools that work on the first try, validated by the exact code the host runs:
  - **`internal/vcb`** — the contract package: a versioned on-disk **plugin
    manifest** (`plugin.json`, strict parsing: unknown fields are errors, not
    silent no-ops) that converts 1:1 into the runtime sandbox manifest; a
    versioned **theme manifest** (`theme.json`) wrapping the real token
    struct; the **enumerated hook catalogue** (exactly what the host fires:
    `article.create`, `article.update`, `article.delete`, `cache.purge`); and
    the `section:action` capability vocabulary re-exported from VayuAPI.
  - **`internal/vcb/validate`** — static validation lifted from every real
    enforcement point: colours that would abort the theme compile, dimensions
    and font characters the compiler silently drops, option keys/values that
    are silent no-ops, CSP violations in custom CSS (`@import`, external
    `url()`), catalogue category and name-collision rules, sandbox capability
    sanity (absolute paths, numeric `uid:gid`, sane limits), executable
    SHA-256 verification, HTTPS-only distribution, host version ranges, and
    least-privilege API permissions (wildcards refused). Every built-in preset
    is tested against the published contract.
  - **`tools/vayu-compat`** — standalone CLI (`check`, `hooks`,
    `capabilities`): validates a `plugin.json`/`theme.json`, prints findings
    with stable machine codes, exits 1 on error. It imports the host's own
    contract packages, so it can never drift.
  - **Public docs** at `/docs/compatibility/vcb` (the Bible) and
    `/docs/compatibility/vayuapi` (keys, permissions, rate limits — the page
    the API's 403/429 error bodies have linked to since v3.13.38, previously a
    404, now real).
  - **CI gate** — preflight section 11 runs the vcb contract tests and
    builds/tests the vayu-compat module explicitly (nested modules are
    invisible to the root `./...`).

### Fixed
- **The plugin ecosystem docs finally match reality.** The plugin SPEC and all
  five example plugins documented hook names the host has never fired
  (`articles.write`, `article.created.v1`) — a plugin built from those docs
  would silently receive nothing. All corrected to the real catalogue.
- **The plugin IPC golden test now freezes the real wire contract.** It
  previously froze a hand-written field list containing a key that never
  existed on the wire (`hook_name`; the real JSON key has always been `hook`)
  and could not catch a genuine rename. The frozen list is now derived from
  the actual `sandbox.Request`/`Response` structs via `encoding/json`
  (deliberate golden update; the capabilities and response shapes are frozen
  too). Stale `hook_name` references in `api-contracts.md` and
  `sandbox-boundaries.md` corrected alongside.

## [3.13.40] — 2026-07-16

### Added
- **VayuAPI Stage 4 — the fine-grained key-manager UI (ADR-0134).** The VayuOS
  **API Keys** console now issues and governs scoped keys end to end, no SQL
  required:
  - **12 × 6 permission grid** — every section (posts, media, themes, design,
    plugins, domains, analytics, members, comments, settings, mail, api) × every
    action (read, write, delete, apply, install, manage) is a checkbox, with a
    per-row *all-actions* toggle and a grand **Full access** switch that dims the
    grid when a superuser key is intended. A key is granted **only** what is
    ticked — nothing it was not.
  - **Lifecycle in one place** — issued keys list their granted capabilities as
    compact chips, their expiry and last-used time, and a live status
    (**Active / Inactive / Expired / Revoked / System**). Actions are
    status-aware: rotate, **deactivate** (reversible disable) / **activate**,
    revoke (permanent), and delete.
  - **Optional hard expiry and a per-key rate budget** at creation. Expiry
    accepts the browser `datetime-local` value or full RFC3339, is normalised to
    UTC, and a past time is refused.
  - Keys are **owner-scoped** to the creating operator. The full key is shown
    exactly once, immediately after creation, in a copy-once banner.

### Changed
- **Per-call API audit is now written synchronously** (matching every other
  `AuditLog` call site) instead of on a detached goroutine. The record is
  guaranteed durable before the handler returns and can no longer be lost on
  shutdown; under SQLite's single writer connection the previous async write
  bought no throughput anyway. This also removes a data race the detached
  goroutine could trip against a database re-open under `-race`.

### Notes
- The console is fully **CSP-safe** — no inline styles or scripts; all
  presentation lives in `admin-os.css` under `.ak-*` classes, all behaviour in
  the nonce-gated page controller. A rendering test asserts the CSP contract and
  that every section × action checkbox, both select-all toggles, capability
  chips, and each lifecycle action are present.

## [3.13.39] — 2026-07-16

### Added
- **VayuAPI Stage 3 — per-key rate limiting, per-call audit, and expiry
  auto-cleanup (ADR-0134).** Every external API key now has its own request
  budget and usage trail:
  - **Per-key rate limit**, keyed by the key's id (not its IP), so a caller
    cannot escape the budget by rotating source addresses. Defaults to
    **600 requests/minute** (env-tunable via `VAYU_API_RATE_PER_MIN`, or a
    per-key `rate_per_min` override); over-budget calls get `429` +
    `Retry-After`. The internal system key is exempt. Enforced identically on
    both the `/api/v1` and `/os` surfaces.
  - **WORM audit** — every key-authenticated call appends
    `api.call | key:<id> | METHOD /path | <status>` to the append-only
    `audit_log`, giving a per-key usage record that never leaves the box.
  - **Expiry auto-cleanup** — a daily sweeper hard-deletes external keys whose
    expiry passed more than 30 days ago (the internal key is never touched).
    Expiry itself is still enforced the instant it passes; the sweeper only
    tidies long-dead rows.

### Notes
- Superuser keys (bootstrap, internal, pre-migration backfilled) are unaffected
  by permission checks but are still metered and audited (except the internal
  key, which is exempt). Tests cover the per-key budget + 429, the internal
  exemption, the env-tuned default, and the grace-window cleanup.

## [3.13.38] — 2026-07-16

### Added
- **VayuAPI Stage 2 — the enforcement layer (ADR-0134).** Scoped API keys are now
  actually enforced. A single route→capability table maps every protected route
  on **both** API surfaces — `/api/v1` (+ the legacy `/admin` API) and their
  `/os` twins — to a `section:action`, keyed by handler intent so a grant cannot
  be bypassed by hitting a different prefix. `RequireAPIKey` resolves the caller's
  key to its grant set and stamps it into the request context; the permission
  middleware then refuses any request whose key lacks the route's capability
  (HTTP 403 naming the missing `section:action`, e.g. `members:read`).
  - **Superuser keys pass everything** — the static bootstrap `API_KEY`, the
    internal system key, and every pre-migration key backfilled with a full grant
    in v3.13.37 — so all existing automation keeps working with no change.
  - **Scoped keys get exactly what they were granted**; a posts-only key can
    create articles but is denied members, theme-apply, backup export, etc.
  - **Fail-closed**: a scoped key hitting a route not in the table is refused;
    only a superuser key reaches unmapped routes. Session-authenticated operators
    are untouched — their access is still governed by session RBAC, never by key
    grants.

### Security
- The `/api` middleware and the `/os` session-or-key gate now share ONE
  resolution + permission path (`auth.ResolveValidAPIKey` → `keyMayCall`), so the
  two entry points can never disagree about which keys are valid or what a key
  may do — closing the prefix-swap escalation class by construction.

### Notes
- Verified end-to-end against a running server: a `posts:*` key gets 202/200 on
  posts routes and 403 on members/theme-apply/backup and any unmapped `/os` page,
  while the bootstrap key gets 200 everywhere. Tests pin the capability mapping
  for ~55 representative routes across all 12 sections, the fail-closed default,
  and the middleware behaviour; `-race`, deadcode, and preflight clean. Per-key
  rate limiting, per-call audit, and the scoped admin UI are the next stages.

## [3.13.37] — 2026-07-16

### Added
- **VayuAPI Stage 1 — the fine-grained API-key permission model (ADR-0134).**
  Every API key can now carry a precise grant set over **12 sections**
  (posts, themes, design, plugins, domains, analytics, media, comments, members,
  mail, settings, backup) × **6 actions** (read, write, delete, apply, install,
  export), written `section:action`, with `section:*` and the `*:*` superuser
  wildcards. Keys gain an **owner**, an optional **hard expiry**, a per-key
  **rate budget**, a usage counter, and a reversible **active/inactive** toggle
  (distinct from terminal revocation). Only SHA-256 hashes are stored, as before.
  - **Deny by default, fail closed**: an empty grant set permits nothing; a
    malformed or forward-version permission blob parses to deny-all — it can
    never widen access.
  - **Upgrades are safe**: migration 062 backfills every *pre-existing* key as
    superuser exactly once, so live automation keeps today's full access. The
    backfill can never touch an API-minted deny-all key (the store always writes
    the versioned envelope, which the backfill predicate cannot match).
  - **Hard expiry is exact**: the resolver re-checks `expires_at` on every cache
    hit, so an expired key is refused on the very next request — never lagged by
    the 30-second verification-cache TTL. (Caught by the pre-release adversarial
    security review; regression-tested.)
  - This stage is deliberately **inert at the auth layer** — nothing enforces
    grants yet, so behaviour is unchanged. The enforcement middleware, per-key
    rate limiting, per-call audit, and the scoped admin UI ship in the next
    stages on top of `Store.Resolve → KeyInfo.Can(section, action)`.

### Notes
- Adversarially reviewed pre-release by three independent lenses (security,
  correctness, migration-safety), each finding independently verified against the
  code; the single confirmed defect (expiry lag) is fixed and pinned by a
  regression test. ADR-0134 records the design; ADR INDEX also gains the missing
  0133 row.

## [3.13.36] — 2026-07-15

### Security
- **Fixed an open redirect in the federated avatar endpoint (CodeQL #61).** The
  `/avatar/<hash>` endpoint added in v3.13.35 honoured the Libravatar/Gravatar
  `?d=<url>` "fallback" parameter by 302-redirecting to it — but that URL is
  caller-supplied, so `…/avatar/<hash>?d=https://evil.example` turned the endpoint
  into an open redirect (a phishing primitive). The `?d=<url>` redirect is now
  removed entirely: a missing avatar always returns **404**, and the client uses
  its own local fallback (keyword defaults like `d=404`/`d=mp` already mean "no
  server image", which 404 conveys). Serving a real avatar is unchanged. A
  regression test asserts the endpoint never emits a `Location` header for any
  `?d=` value (absolute, protocol-relative, or path).

## [3.13.35] — 2026-07-15

### Added
- **Per-domain tabs on the mailbox directory.** When more than one mail domain is
  served, the mailbox landing page now shows a tab per domain; clicking a tab
  shows only that domain's mailboxes instead of stacking every domain down the
  page. Pure CSS (hidden radio + `:checked` sibling rules), so it needs no
  JavaScript and emits no inline styles (CSP-safe). A single-domain install is
  unchanged (one card, no tab chrome).
- **Federated (Libravatar/Gravatar-compatible) sender avatar.** A public
  `GET /avatar/<hash>` endpoint serves a mailbox's profile picture by the md5 or
  sha256 of its address, so a recipient's mail client or a service that supports
  avatar federation can display the sender's picture. It exposes only the image
  for an address that opted in by uploading one, honours the `?d=` default-URL
  fallback, 404s otherwise, and is rate-limited (same posture as WKD).

### Notes
- Real-world reach of the federated avatar depends on the recipient's client:
  clients/services that implement Libravatar federation (discovered via a
  `_avatars._tcp.<domain>` SRV record) will fetch it; clients that don't show
  sender avatars at all won't. It is standards-based best-effort, not a guarantee
  every recipient sees the picture. Tests cover the md5/sha256 hash derivation,
  lookup by either hash, no-match for a mailbox without a picture, and the
  public route's 404 / `?d=` redirect behaviour. Third in the mail-experience
  series (after profile pictures and the address book).

## [3.13.34] — 2026-07-15

### Added
- **Per-mailbox address book (Contacts) with one-click save and scoped compose
  autocomplete.** Every mailbox now has its own private contact list — one
  mailbox's contacts are never visible to another.
  - **Contacts panel** (👥 Contacts in the mailbox toolbar): an elegant HTMX
    panel to add, list, and remove saved contacts. Each row shows the contact's
    avatar/initials and links straight to a pre-addressed compose.
  - **One-click "＋ Save contact"** on any open message files the sender into the
    current mailbox's book (parsing "Name <addr>" into name + address) and swaps
    the button to "✓ Saved" with no reload.
  - **Compose autocomplete** now draws ONLY from the sending mailbox's saved
    contacts (the previous suggestion list was a shared, directory-wide source);
    typing a recipient suggests that mailbox's matching contacts via a native
    `<datalist>` — no JS, CSP-safe.
  - Isolation is enforced server-side: every contacts route resolves the owning
    mailbox with the same authorization the inbox uses, so a non-admin can only
    ever touch their own contacts. Saving is an upsert keyed on (owner, email),
    so re-saving a known address just refreshes its name.

### Notes
- New `vayumail_contacts(owner,email,name,created_at)` table (created idempotently
  alongside the other mail tables). Tests cover per-mailbox isolation, upsert +
  case-normalisation, self/invalid-input rejection, and scoped search/delete; the
  panel is verified CSP-safe (no inline styles). Second in the mail-experience
  series (after profile pictures).

## [3.13.33] — 2026-07-15

### Fixed
- **Uploaded mailbox profile pictures now show across the whole mail UI, not just
  the Accounts card.** The mailbox directory, the open-mailbox toolbar, message
  lists (inbox/sent/search), the message reader, and the VayuTalk identity all
  rendered a colored-initials chip and ignored the uploaded picture. They now show
  the photo for any local mailbox that has one, falling back to initials for
  mailboxes without a picture and for external senders (so there is never a broken
  image — the strict CSP forbids an inline `onerror` fallback). A short-TTL,
  upload-invalidated cache of "which mailboxes have a picture" keeps this to one
  lookup per render, not one per row.

### Notes
- First of a small series improving the mail experience (avatars, per-domain
  tabs, a per-mailbox address book). Tests cover the photo-vs-initials decision
  for a mailbox with a picture, a mailbox without one, and an external sender.

## [3.13.32] — 2026-07-15

### Added
- **The account panel + nav chip now show your profile photo, not just an
  initial.** The public account panel rendered a coloured circle with the first
  letter of your name; it never displayed an image. It now shows your profile
  picture when one is available. The membership snapshot resolves the avatar from
  the matching CMS user (the same public URL shown on your `/author` page), so
  the owner/staff who signed into the portal see their photo in the panel and the
  nav chip; members without a picture keep the initial. Works for same-origin and
  external image URLs (the public CSP already allows `img-src 'self' data:
  https:`).

### Notes
- No new storage or endpoint: the avatar is the existing CMS-user `AvatarURL`,
  surfaced read-only. Tests cover the panel's image-vs-initial rendering path,
  the member snapshot supplying the avatar when a matching CMS user has one, and
  omitting it otherwise.

## [3.13.31] — 2026-07-15

### Added
- **The public site now recognises a signed-in VayuOS operator.** Visiting your
  own public site (the homepage, an article) while signed in to the console used
  to still show **"Sign in / Sign up"**, because the public nav only knew about
  *reader/member* sessions — not your *operator* session. The membership snapshot
  (`GET /api/v1/members/me`, fetched same-origin so it already receives your
  `vp_session` cookie) now also resolves a console session: when an operator is
  signed in it returns `authenticated: true` with an operator chip. The nav then
  shows your name (and avatar, when set) linking straight to **/os (Dashboard)**,
  instead of the sign-in buttons — so you're recognised everywhere on your own
  site, VayuOS or public, exactly as expected. Readers' member sessions behave
  exactly as before; a member session still takes precedence when both exist.

### Notes
- Read-only and same-origin: the operator identity is resolved from the existing
  console session with no new cookie and no change to cookie posture
  (`HttpOnly`, `Secure`, `SameSite=Strict`). The public account chip links to the
  console; it never exposes operator capabilities to the page. Tests cover the
  operator snapshot shape (operator flag, console link, email fallback,
  avatar-only-when-set) and the end-to-end `/api/v1/members/me` recognition of a
  console session.

## [3.13.30] — 2026-07-15

### Fixed
- **The real cause of the "login page shows while signed in" bug: the PWA
  service worker was caching the admin console.** The offline service worker
  serves navigations *stale-while-revalidate* (`return cached || network`), and
  it only excluded the legacy `/admin` path — but the console lives at **`/os`**,
  which was never added to the exclusion. So the worker cached `/os/login` in
  Cache Storage and served the stale form on every visit, **bypassing the server
  entirely** — which is why it survived a hard refresh *and* a full browser
  restart (Cache Storage is persistent; HTTP-cache fixes and `no-store` headers
  can't touch it). Worse, the same path could serve one operator's cached
  dashboard to another on a shared device.
  - The service worker now routes the whole console — `/os`, every `/os/*`
    sub-path and asset, plus the legacy `/admin` — **straight to the network,
    never through the cache**, and this guard runs before the stale-while-
    revalidate branch.
  - `CACHE_NAME` is bumped to `vayupress-v2`, so on activation the worker's
    existing cleanup step **deletes the old `vayupress-v1` cache**, purging the
    poisoned `/os/login` entry. Because `/sw.js` is served `no-store`, browsers
    fetch the fixed worker on their next visit, install it (`skipWaiting`),
    activate it, and claim the page automatically — no manual cache-clearing
    needed.

### Notes
- Defense in depth now spans both layers: the HTTP response is `no-store`
  (v3.13.29) *and* the service worker never caches the console (this release).
  Public blog pages keep their offline stale-while-revalidate behaviour
  unchanged. Tests assert the console network-only guard, its ordering before the
  navigate branch, the `vayupress-v2` cache bump, and that `/sw.js` is served
  uncacheable so the worker self-updates.

## [3.13.29] — 2026-07-15

### Fixed
- **The `/os/login` redirect now actually fires — the login page is no longer
  cacheable.** v3.13.28 added the "already signed in → forward to `/os`" redirect,
  but the login page was written directly (bypassing `writeOSHTML`, where the
  dashboard gets its `no-store`) and carried **no `Cache-Control` header at all**.
  The browser was free to heuristically cache the rendered form and serve it on a
  later visit *without hitting the server*, so the session-aware redirect never
  ran — a signed-in operator kept seeing the login page while `/os` (always
  `no-store`) correctly showed the dashboard. That exact asymmetry was the bug.
  All four auth-page responses (login GET/POST, change-password GET/POST) now send
  `Cache-Control: no-store, no-cache, must-revalidate` + `Pragma: no-cache` +
  `Expires: 0`, so the redirect logic runs on every navigation. Verified
  end-to-end: a real session cookie against `GET /os/login` returns `303 → /os`.

### Notes
- Auth pages must never be cached anyway — they carry live session state, a
  prefilled email, and per-attempt error banners; a shared/heuristic cache
  serving a stale copy is both a correctness and a privacy issue. Tests assert
  the `no-store` header on the anonymous form render and on the authenticated
  redirect. Cookie posture is unchanged (`HttpOnly`, `Secure`, `SameSite=Strict`).

## [3.13.28] — 2026-07-15

### Fixed
- **Signed-in operators no longer see the login form at `/os/login`.** Opening
  the sign-in URL directly with a live session re-rendered the form (the
  dashboard only appeared once the path was cleared). `/os/login` now resolves
  the session server-side and, when it belongs to a real account (a CMS user or
  a VayuMail mailbox), forwards straight to `/os` with a 303 — the seamless
  posture whether the operator lands on `/os` or `/os/login`. A stale or unknown
  cookie still renders the form, so the guard trusts a *resolved* session, never
  the mere presence of a cookie.

### Added
- **"Remember me on this device" on the sign-in page.** Checked (the default,
  persistent posture) issues a cookie whose lifetime is the session TTL, so the
  sign-in survives browser restarts. **Left unchecked, the session cookie becomes
  a browser-session cookie** — no `Max-Age`/`Expires` — so the browser drops it
  the moment it closes and the operator is signed out on the next visit, the
  right posture for a shared or public machine. Applies to both CMS-account and
  VayuMail-account logins. The server-side session still expires on its own TTL;
  dropping the cookie is what logs the browser out.

### Notes
- Cookie hardening is unchanged in both modes — `HttpOnly`, `Secure` (when
  served over TLS), `SameSite=Strict`. Tests cover the persistent-vs-session
  cookie choice, the redirect-when-authenticated behaviour, and the render-form
  fallback for an anonymous visitor or a stale cookie.

## [3.13.27] — 2026-07-15

### Performance
- **The p95 finally reflects reader latency — VayuShield tarpit delays excluded.**
  The true cause of the stuck 8192 ms p95 was VayuShield's **tarpit**: it
  deliberately `sleep`s ~5 s on a confirmed-bad-bot request to waste its time
  (`serveTarpit`). That 5 s was being recorded as "request latency", so while
  bots kept probing the p95 was pinned at the shield's own delay — the shield
  *working* looked like the site being slow. HTTP latency now excludes every
  shield-defended request (tarpit, challenge, block, jail, load-/fair-shed,
  rate-limit) via a mutable per-request classification (`internal/reqclass`) the
  shield sets and the latency recorder honours. Real reader requests are unchanged.

### Fixed
- **`governance-breach` no longer floods from the admin UI's own inline styles.**
  The strict CSP is `style-src 'self'`, but the admin HTML carries ~150 inline
  `style="…"` attributes — each was blocked and reported as a CSP violation on
  every operator page view, exhausting the VIOLATION budget (and quietly degrading
  admin layout). The CSP now adds **`style-src-attr 'unsafe-inline'`**, which
  permits passive, developer-authored inline style *attributes* only. Stylesheets
  and `<style>` stay `'self'`, and the XSS-relevant `script-src` stays
  `'self' 'nonce-…'` with no `unsafe-inline` — the execution sandbox is unchanged.

### Notes
- Both are correctness fixes; request serving is unchanged. Tests cover the
  shield-mark flowing from an inner middleware to the outer latency recorder, and
  the CSP allowing inline style attributes while keeping script strict. The
  earlier fixes stand: `degradation-debt` stays healthy (bot-blocks isolated to
  `bot-attack-intensity`), and the Storage page footprint is served from cache.

## [3.13.26] — 2026-07-15

### Performance
- **Storage & System page no longer walks the render cache on the request path.**
  The page summed the on-disk footprint by `WalkDir`-ing the render cache, media
  and backup trees **synchronously per load** — on a multi-GB render cache that
  blocked for 4-8 s, and (as of v3.13.25's honest recent-window metric) those
  loads were the requests pinning the HTTP p95 at 8192 ms. Directory sizes are now
  computed by a **background refresher** (at boot, then every 10 min) and read
  from cache instantly; the DB size stays a live stat. A stale cache kicks a
  non-blocking refresh, so a delete still reflects promptly. This is the actual
  fix behind the p95 the v3.13.25 metric surfaced.

### Fixed
- **`degradation-debt` no longer exhausts just because the bot shield is
  working.** VayuShield hard-blocks charged a generic `severity.Warn`, which the
  strict general-purpose `degradation-debt` budget (limit 5) tracks alongside the
  tolerant, purpose-built `bot-attack-intensity` budget (limit 20) — so blocking a
  bot flood (hundreds of IPs) read as *system degradation* and marked the box
  "exhausted". Bot-blocks now charge **only** `bot-attack-intensity` (new
  `Ledger.RecordTo`), so `degradation-debt` reflects genuine degradation and the
  governance panel stops false-alarming under normal bot pressure.

### Notes
- Both are correctness/latency fixes with no behaviour change to serving. Tests
  cover the footprint cache (reads are served from cache, never a live walk) and
  the targeted budget charge (bot-blocks hit only the bot budget; genuine WARNs
  still charge degradation-debt).

## [3.13.25] — 2026-07-14

### Performance
- **Truthful HTTP p95 (recent window, streams excluded).** The Monitoring "HTTP
  p95" was an **all-time cumulative** histogram that also recorded the full
  lifetime of long-lived/streamed connections (a VayuTalk relay held open for
  minutes counted as one multi-minute "request"). Over long uptime and a bot
  burst, that tail could only climb — e.g. 32 ms at boot → 8192 ms — while real
  page render stayed ~64 ms. Latency is now measured over a **15-minute sliding
  window** and **excludes connections past the 30 s request timeout** (streams,
  not page loads), so the number reflects how fast the site is *right now* and a
  transient slow burst ages out instead of pinning it forever. The cumulative
  histogram is retained for Prometheus (monotonic counters); the tile is labelled
  "last 15 min".

### Fixed
- **Self-healing DB-backup disk hygiene.** The shell update path
  (`update-vayupress.sh`) drops a full `sqlite3 .backup` copy — `<db>.backup-<ts>.db`
  — next to the live database, and nothing pruned them, so they (and orphaned
  `.db-journal` sidecars) accumulated into multi-GB clutter shown on the Storage
  panel. The engine now sweeps them at boot and daily, keeping the newest
  `VAYU_DB_BACKUP_KEEP` snapshots (default 3) and removing orphaned journals — and
  never the live DB or its WAL/SHM/journal (those carry no `.backup-` infix). The
  update script prunes inline too.

### Added
- **`vayupress.com/docs` on the marketing site.** The project site is hosted on
  GitHub Pages (`docs/site/`), so the binary's `/docs` route (v3.13.24) did not
  make it appear there. A new cgo-free generator (`cmd/vayudocs`) renders the
  guides, security model, operations runbooks and every ADR to static HTML into
  the Pages build, so `vayupress.com/docs` now serves the same docs the running
  binary serves at `<site>/docs`. It mirrors the binary's DOM and shares
  `static/css/docs.css`, so both look identical; the Pages workflow
  (`deploy-site.yml`) builds it in seconds and redeploys on any `docs/**` change.
  A "Docs" link was added to the site nav.

### Notes
- The latency change is metrics-only — it changes what the dashboard *reports*,
  not how requests are served. If the recent-window p95 is still high after this,
  that is a genuine signal to investigate (pprof), not an accumulation artefact.
  Tests cover the windowed percentile (recency + aging-out) and the backup sweep
  (keep-newest-K, journal cleanup, live-DB safety).

## [3.13.24] — 2026-07-14

### Added
- **Public documentation site at `/docs`** (ADR-0133). Every guide, operations
  runbook, the security model and all 100+ Architecture Decision Records now ship
  as markdown **embedded in the binary** and are rendered at `<your-site>/docs` —
  a path, not a subdomain, hosted by your own install for public audit. Read-only,
  GET-only, rendered with the same sanitising pipeline as the VayuOS ADR registry
  (goldmark + bluemonday UGC), inside a self-contained, CSP-safe shell
  (`static/css/docs.css`; no inline styles/scripts). Routing is allowlist-based —
  a request can only resolve to a real embedded file, so no path escapes the tree.
  `/doc` redirects to `/docs`.

### Changed
- **Retired the `docs.vayupress.com` subdomain.** Every in-app reference (API error
  `docs` hints in `handlers_*`, `auth`, `queue`, `health`) is now the site-relative
  `/docs/…`, so hints resolve on whatever host serves the API (multi-domain
  included); documentation/config prose links point at `https://vayupress.com/docs/…`.
  Legacy anchor paths still used by error hints (`/docs/operations/…`,
  `/docs/api/…`, `/docs/governance/…`) alias to the guide that covers them, so no
  link dead-ends. The installer's systemd `Documentation=` now points at the docs
  site; `INSTALLATION.md` documents the built-in `/docs` (no DNS record to add).
- **Canonicalised `docs/EMAILS.md`** into the complete 24-address inventory
  (adds the ethics, admin/BDFL, architecture-lead and performance-lead contacts
  that were referenced elsewhere), with a note that the VayuMail-Mobile app defines
  no addresses of its own and shares this list.

### Notes
- Only markdown is embedded (~0.7 MB; the README screenshot/asset folders are
  excluded), so the hot request path is untouched and binary growth is bounded.
  ADRs are not re-embedded — the handler unions the existing `DocsADRFS`. Tests
  cover the index, guide/ADR rendering, the legacy-anchor alias, a real 404 and
  that path traversal cannot escape the embedded set. Single-domain installs are
  unaffected beyond gaining the new `/docs` route.

## [3.13.23] — 2026-07-13

### Security
- **goldmark v1.8.2 → v1.8.4.** With the pre-release filter in place (v3.13.22),
  the watcher now surfaces the real latest *stable* goldmark instead of the
  `v2.0.0-beta.5` pre-release; this bumps to it. Ships inside this signed release.
- **CodeQL CWE-116 (unsafe quoting), `admin_os_domains.go`.** The per-domain brand
  map was embedded in a `data-brands` attribute with a hand-built,
  quote-delimited string and `html.EscapeString`. Although escaped, CodeQL still
  flagged the manual quoting. It is now rendered through `html/template`, so the
  template engine's context-aware auto-escaper owns the attribute quoting — the
  recognised, defence-in-depth-correct way to embed a value in HTML. A new test
  proves a hostile brand value carrying `"><script>` cannot break out.
- **CodeQL CWE-770 (uncontrolled allocation size), `search_domain.go`.** The
  per-domain hit filter pre-sized its output slice from an expression involving
  the request-derived `limit`. It now sizes purely from `len(hits)` — the length
  of an already-materialised slice (itself capped at the 500-hit over-fetch bound)
  — keeping the allocation size off any request-tainted path. `limit` still bounds
  the loop, so results are unchanged.

### Notes
- Both CodeQL fixes are defence-in-depth: the previous code was already safe in
  practice (escaped output; a filter bounded by the batch), but the sanitisers
  weren't ones CodeQL's data-flow model recognises. The fixes remove the tainted
  value from the sink entirely, which is the durable fix. No behaviour change.

## [3.13.22] — 2026-07-13

### Security
- **go-sqlite3 v1.14.47 → v1.14.48.** Bumped the SQLite driver to the latest
  upstream patch. Because VayuPress is a single statically-linked binary, the
  patched library ships compiled into this signed release — installing v3.13.22
  via the one-click updater (VayuOS → Security → Update VayuPress) applies it;
  there is no server-side `go get`/rebuild step.

### Fixed
- **Security page no longer flags pre-release dependencies.** The upstream watcher
  compared only the numeric version and treated a beta major (e.g. goldmark
  `v2.0.0-beta.5`) as a newer stable release, showing a false "update available"
  and — worse — implying an operator should run beta software in production. The
  watcher now considers **stable release tags only** (any `-beta`/`-rc`/`-alpha`
  or `+build` tag is ignored), so goldmark correctly reads *up to date* on the
  latest stable `v1.8.x`.
- **Accurate upgrade guidance.** The "updates available" banner previously told
  operators to run `go get -u … && go build`, which contradicts the single-binary
  model explained lower on the same page. It now points at the built-in one-click
  self-updater (checksum + Ed25519 verified, atomic swap, auto-rollback), which is
  how dependency patches actually reach a server.

### Notes
- No schema, wire-format or behaviour change; the watcher stays disabled by
  default (privacy first). Tests cover stable-tag selection and that a beta tag is
  skipped in favour of the highest stable release.

## [3.13.21] — 2026-07-13

### Changed
- **Every per-mailbox control now lives inside its own account card.** Following
  forwarding and vacation (v3.13.20), a mailbox's **aliases** and **filter rules**
  have moved into that mailbox's card on VayuOS → Accounts too — so expanding an
  address shows its role, quota, retention, forwarding, vacation, aliases and
  filters together, and nothing else. The separate account-wide "Aliases" and
  "Filter rules" cards are gone. Each control saves over HTMX and refreshes the
  accounts list in place.
- **Aliases follow the mailbox's own domain.** An alias added from a card is now
  created on the target mailbox's domain, so a secondary-domain mailbox
  (VayuDomains) gets secondary-domain aliases (e.g. `sales@second.example`)
  instead of always the primary domain.

### Notes
- Admin-only UI reorganisation; the alias/forward/autoreply/filter endpoints and
  data model are unchanged. Single-domain installs see the same controls, just
  relocated into each card. Tests cover the consolidated card, the per-domain
  alias creation and the filter round-trip.

## [3.13.20] — 2026-07-13

### Changed
- **Per-mailbox forwarding & vacation now live inside each account's card.**
  On VayuOS → Accounts, expanding a mailbox now shows its own auto-forward
  address and vacation autoresponder alongside its role, quota and retention —
  every setting for one address in one place, instead of a shared "Aliases &
  forwarding" table and a separate "Vacation autoresponder" card that listed
  every mailbox. Both controls save over HTMX and refresh the accounts list in
  place. The Aliases card keeps its (address-scoped) alias table and now points
  to the account cards for forwarding.

### Notes
- Admin-only UI reorganisation; no data model, wire-format or behaviour change,
  and the forward/autoreply endpoints are unchanged. Single-domain installs are
  unaffected beyond the same in-card layout.

## [3.13.19] — 2026-07-13

### Added
- **Accounts grouped by domain.** VayuOS → Accounts now groups mailboxes under a
  header per mail domain (the primary first, then each secondary), so a
  multi-domain operator manages one domain's mailboxes at a time instead of one
  mixed list. A single-domain install renders the flat list unchanged.

### Notes
- Cosmetic/admin-only; no behaviour change. The next step folds each mailbox's
  forwarding, vacation, alias and filter controls into its own card.

## [3.13.18] — 2026-07-13

### Added
- **Per-mailbox profile pictures.** Each mail account can now have a profile
  picture, uploaded directly from inside its card on VayuOS → Accounts (≤500 KB;
  PNG, JPEG, GIF or WebP, validated by magic bytes — the client Content-Type is
  never trusted). The picture fills the account's avatar chip (falling back to
  initials) and can be removed. Stored in the account row and served from a
  same-origin, `nosniff` route; admin-only upload/remove.

### Notes
- Additive `avatar_blob` / `avatar_type` columns on `vayumail_accounts` (idempotent
  migration; empty for existing accounts, so no behaviour change). Tests cover the
  store round-trip and the magic-byte image sniffing (SVG/PDF/truncated rejected).

## [3.13.17] — 2026-07-13

### Added
- **Outbox auto-clear (retention) for delivered mail.** A new *Auto-clear
  delivered* control on the Outbox prunes DELIVERED delivery-queue rows older than
  the chosen window (Off / 7 / 30 / 90 days). Only the Outbox's delivery-status
  record is removed — the **Sent copy stays** in the mailbox's Sent folder
  (separate Maildir store). The choice is persisted and applied live; the sweep
  runs hourly. A `VAYUOS_MAIL_QUEUE_RETENTION_DAYS` env sets the default.
- **VayuMail Overview stat strip.** The Overview now shows live tiles for
  administrators — mailboxes, mail domains, and the outbound queue
  (queued / failed / delivered) — above the existing cards.

### Notes
- Off by default (nothing is auto-deleted until an operator picks a window), and
  the strip is administrator-only. Tests cover the delivered-only prune (recent,
  pending and failed rows are kept) and the runtime setter.

## [3.13.16] — 2026-07-13

### Changed
- **PGP encryption in the composer is now opt-in.** Previously a single-recipient
  message was silently encrypted whenever a recipient key was on file — so a plain
  "test" to a Gmail address arrived as an unreadable `-----BEGIN PGP MESSAGE-----`
  block (and scored as spam, since Gmail can't decrypt it). Compose now has an
  explicit **🔒 Encrypt with PGP** checkbox, off by default; a message is
  encrypted only when you tick it (and the recipient has a key, one recipient, no
  attachments). System/transactional mail was already never encrypted and is
  unchanged.

### Fixed
- **Full-view (⛶) reader no longer slides under the sidebar.** The enlarged
  message overlay now offsets by the sidebar width and sits above it (it spans
  full width only once the sidebar goes off-canvas on narrow screens).
- **Outbox fits the window — no horizontal scrolling.** The outbound table uses a
  fixed layout with sized columns; long To/Subject/error values truncate or wrap
  within their column (full value on hover) instead of forcing a left-right
  scroll.

## [3.13.15] — 2026-07-13

### Fixed
- **Sending to a `Name <email>` recipient no longer fails delivery.** Composing
  (or replying) to a display-name address such as `VayuPress Hello
  <hello@vayupress.com>` kept the angle bracket, so the recipient domain became
  `vayupress.com>` and outbound delivery failed with `dial tcp: lookup
  vayupress.com>: no such host`. The recipient list is now parsed with
  `net/mail` (both the send and the save-draft paths), extracting the bare
  address. As a bonus this also lets a reply to a **local secondary-domain
  mailbox** (e.g. `hello@vayupress.com`) deliver straight into its Maildir
  instead of being attempted over external MX.

## [3.13.14] — 2026-07-13

### Security
- **Fix CodeQL "potentially unsafe quoting" on the Domains page (CWE-116).** The
  per-domain brand map was interpolated raw into the inline `<script>`. Although
  `encoding/json` HTML-escapes `<`/`>`/`&` (so a `</script>` breakout was already
  impossible), the value now rides in an `html.EscapeString`-escaped data
  attribute and is `JSON.parse`d by the page script — a recognised-safe sink with
  no data flowing into script quoting.
- **Fix CodeQL "slice allocation with excessive size" in per-domain search
  (CWE-770).** `filterHitsByDomain` pre-sized its result slice from the
  request-derived `limit` (clamped in a different function, so the bound wasn't
  provable at the allocation). It now bounds the capacity by `len(hits)` — the
  true ceiling on the filtered output — clamped ≥0, which also avoids
  over-allocating for a large requested limit.

### Notes
- Both are hardening fixes with no behaviour change for a valid request. Tests
  pin the brand-map-via-attribute rendering.

## [3.13.13] — 2026-07-13

### Fixed
- **Secondary-domain mailboxes now open.** Clicking a `@secondary` mailbox (or a
  message/folder/search inside it) did nothing — the admin `?user=` sanitizer
  rejected the `@`, collapsing `hello@vayupress.com` to empty and re-rendering the
  list. A new mailbox-key sanitizer accepts a full `local@domain` address (still
  charset-locked and html-escaped, with malformed domains rejected), so every
  secondary mailbox, its messages, folders and search open correctly. The engine
  and Maildir already resolved the domain; only the admin param gate was blocking.

### Added
- **Mailbox: one card per domain.** The Mailbox directory now shows a separate
  card per mail domain (primary first, then each secondary) with a role badge and
  mailbox/unseen counts, instead of one mixed list.
- **Compose: sender identities grouped by domain.** The admin From selector groups
  addresses under a per-domain `<optgroup>` (primary first) so picking a `From` on
  a multi-domain install is clear; sending as a secondary address was already
  DKIM-signed per domain and filed in that domain's Sent folder.
- **Outbox: per-domain visibility.** The outbound queue gains a **From** column
  (with a domain badge) and a **per-domain filter** (HTMX chips) so an operator can
  see and narrow to a specific domain's outbound. The auto-refresh poll preserves
  the active filter.

### Notes
- No-op for single-domain installs: one mailbox card, a flat From list, and an
  Outbox with no filter chips and no From column — unchanged. All of this is
  administrator-only. Internally the primary-only mailbox-param sanitizer
  (`sanitizeMailLocalPart`) is superseded by `sanitizeMailUser`.

## [3.13.12] — 2026-07-13

### Added
- **VayuMail DNS tab — rebuilt for multi-domain.** The DNS tab is now a set of
  collapsible sections instead of flat tables. It adds the **Mail host &
  networking** section the old tab never showed — the `A`/`AAAA` records that make
  the mail host reachable, the reverse-DNS (PTR) alignment and the inbound ports —
  with a prominent warning that mail cannot be proxied, so those records must be
  published **DNS-only** (on Cloudflare, the grey cloud). Each record value gets a
  one-click **copy** button.
- **DNS verification for every mail domain.** A new *DNS verification — all
  domains* section checks the live MX/SPF/DKIM/DMARC of the **primary and every
  mail_enabled secondary** — not just the primary — and confirms each domain's MX
  actually routes to this install's mail host (a secondary still pointing
  elsewhere is flagged as misaligned rather than merely "present"). An **HTMX
  Re-check** control re-runs every lookup in place, no full-page reload.

### Fixed
- **Secondary-domain mailboxes now appear in the Mailbox list.** The Mailbox tab
  enumerated only the primary domain's Maildir, so `@secondary` accounts were
  invisible. It now lists every mail domain's accounts (each from its own
  domain-partitioned Maildir) with the full address, and opening one reads the
  correct mailbox.

### Notes
- No-op for single-domain installs: only the primary records and host section
  render, and the mailbox list is unchanged (byte-identical). The DNS tab is
  administrator-only, as before. Internally, `Engine.HealthForDomain` /
  `CheckHealthForDomain` and `Engine.MailboxesForDomain` supersede the
  primary-only `Health` / `CheckHealth`.

## [3.13.11] — 2026-07-13

### Added
- **VayuDomains — per-domain public branding.** Each secondary domain can now
  present as its own site. VayuOS → Domains gains a **Brand a domain** card where
  the operator sets a domain's site name, tagline, meta description, light/dark
  accent colours and browser theme-colour; every field is optional and a blank one
  inherits the primary site's value, so a domain can re-brand just its name and
  keep the rest of the design. The overrides are stored in the registry's
  `config_json` (no schema change) and overlaid onto the site settings when that
  domain's homepage, articles and `/theme.css` render — so one binary on one VPS
  serves many domains, each with its own identity.

### Notes
- **No-op for single-domain installs, byte-identical for the primary.** The brand
  overlay is reached only when a secondary domain is registered *and* it carries a
  brand; in every other case (single-domain install, the primary domain, or a
  secondary with no brand) the original renderers are called verbatim — same
  bytes, same ETag — so johal.in pays only one cached `HasSecondaries` bool on the
  hot path and nothing else. Accent/theme-colour inputs are hex-validated before
  they can reach `/theme.css` or `<meta theme-color>`, closing any CSS/attribute
  injection through the branding form. The primary domain's identity remains the
  global Website settings and is intentionally not editable here.

## [3.13.10] — 2026-07-13

### Added
- **VayuDomains (P4) — per-domain TLS + nginx automation.** A new privileged root
  helper, `scripts/setup-vayudomain.sh`, provisions each registered secondary
  domain's TLS certificate and nginx vhost out-of-process (the server runs
  unprivileged and cannot certbot/reload nginx itself). Each secondary gets its
  **own** certificate lineage (`certbot --cert-name <host>`, plus `www.` and, for a
  mail-enabled domain, `mail.<host>`), so the Let's Encrypt 100-SAN cap can never
  be hit. `deploy-vayupress.sh` and `update-vayupress.sh` invoke it (idempotent,
  non-fatal). A new `vayupress domains` CLI (`list` / `hosts [--mail]` / `set-tls`)
  is the read/notify surface the helper drives from — the binary records state, the
  helper acts on it.

### Notes
- No-op for single-domain installs (no secondary domains → the helper skips
  cleanly). This completes the VayuDomains roadmap through P4; the deferred
  sub-stages (member `UNIQUE(domain_id,email)` rebuild, context-free payment
  attribution, per-domain webmail branding) are recorded in **ADR-0132**.

## [3.13.9] — 2026-07-13

### Added
- **VayuDomains (Stage 4a) — per-domain member attribution.** A member who signs
  up now records which domain they joined on (additive `members.domain_id`,
  migration 061; default `''` = primary), and VayuOS → Domains shows a per-domain
  member count beside the article and mailbox counts. Because a member is keyed by
  the globally-unique email, login is unchanged — the domain is attribution +
  reporting, not a login filter.

### Notes
- **Byte-identical for single-domain installs.** Attribution is gated on a
  registered secondary domain, so a plain install writes `domain_id=''` and the
  member read/auth path is untouched (the member tests are unchanged). Relaxing the
  global `UNIQUE(email)` so one address can be a member of two domains — plus
  attributing the context-free Stripe fulfilment/webhook paths — is **Stage 4b**.
  See **ADR-0132**.

## [3.13.8] — 2026-07-13

### Added
- **VayuDomains (Stage 3d) — webmail access to secondary-domain mailboxes.** A team
  member can now be assigned a mailbox on a mail-enabled secondary domain (the
  assign form takes an optional domain, validated against the registry), and that
  user reads, searches and acts on their mail in the built-in webmail — fully
  isolated to their own domain's Maildir. The engine read API is now domain-aware
  (`mailboxKey` accepts a bare local part → primary, or a full address → that
  domain), and the webmail handlers resolve the mailbox key **server-side** from
  the signed-in identity, never from a client param.

### Notes
- **Byte-identical for single-domain installs.** The engine key collapses to the
  bare local part on the primary domain, so every existing webmail path is
  unchanged; the domain is only ever taken from server state (the user's stored
  address), carrying no XSS exposure (the `user` form param stays local-part-only).
  Per-domain webmail/console branding remains a Stage 3d follow-up; per-secondary
  TLS is **P4**. See **ADR-0132**.

## [3.13.7] — 2026-07-13

### Added
- **VayuDomains (Stage 3d) — per-domain DNS records panel.** VayuOS → mail now
  shows a *DNS records to publish* card for each mail-enabled secondary domain
  (`PlannedRecordsForDomain`): its MX points at this install's mail host, its
  SPF/DMARC are scoped to the secondary, and — because the DKIM key is shared — it
  publishes the same key value at its own `<selector>._domainkey` record. An
  operator can now stand up a secondary domain's mail end to end.

### Performance
- **`Registry.HasSecondaries` is now O(1).** The per-request VayuDomains gate — hit
  on every public blog page and VayuOS request — reads a boolean precomputed at
  cache refresh instead of scanning the domain map, so multi-domain support adds no
  measurable overhead to the blog or console. (Single-domain installs already took
  the zero-work path; this keeps it flat as domains are added.)

### Notes
- Byte-identical for single-domain installs: the DNS panel renders only when a
  mail-enabled secondary exists, and every per-domain path stays gated behind the
  cached `HasSecondaries` / primary-first `AcceptsMailDomain` checks. Per-domain
  webmail branding and full webmail access to secondary mailboxes remain **Stage
  3d** follow-ups; per-secondary TLS is **P4**. See **ADR-0132**.

## [3.13.6] — 2026-07-13

### Added
- **VayuDomains (Stage 3c) — per-domain outbound + autoconfig.** A mail-enabled
  secondary domain now sends **DKIM-aligned, deliverable mail**: each send signs
  with a signer whose `d=` tag is the sender's own domain (reusing the shared
  selector key), and the `Message-ID` + Sent-folder copy carry that domain too, so
  a secondary sender's outbound passes DKIM/DMARC alignment and its Sent copy stays
  in its isolated mailbox. Mail-autoconfig (`/.well-known/vayumail/autoconfig.json`
  and the Thunderbird XML) resolves the account domain from the request host, so a
  secondary domain's clients auto-configure with their own address.

### Notes
- **Byte-identical for single-domain installs.** The primary keeps the signer
  loaded at start and its historic Message-ID/Sent behaviour; per-domain signers
  are created only for a genuinely different sender domain. Because the DKIM key is
  shared, a secondary domain publishes the **same** `<selector>._domainkey` TXT as
  the primary (plus its own MX/SPF/DMARC). A per-domain DNS panel, webmail branding
  and full webmail access to secondary mailboxes are **Stage 3d**; per-secondary
  TLS certs remain **P4**. See **ADR-0132**.

## [3.13.5] — 2026-07-13

### Changed
- **VayuMail Accounts — enterprise HTMX redesign.** The Accounts admin section is
  rebuilt as a modern, seamless, HTMX-driven surface: an at-a-glance stats strip
  (mailboxes, active, 2FA, **devices pending**, storage), one **collapsible card
  per mailbox** with a native storage meter, and every inline action (enable/
  disable, role, quota, retention, delete) posting a fragment that swaps the list
  in place — the page never does a full reload. The create / set-password / 2FA
  flows refresh the same fragment via `htmx.ajax`, and their controls are bound by
  a single delegated listener so they survive every swap.

### Added
- **Live device approval — no page refresh.** The Devices card now self-refreshes:
  a newly-registered device surfaces as **pending approval** within seconds
  (a hidden HTMX poller on a new `GET /os/vayumail/devices/fragment`), with a
  prominent "N devices awaiting approval" banner and a live indicator — no more
  reloading the whole page to see a pending device. Approve/Block/Remove already
  swap the card in place.

### Notes
- CSP posture is unchanged: no inline styles (storage bars use the native
  `<meter>` element), the page's single `<script>` carries the nonce, htmx.min.js
  stays self-hosted, and the shared glue mirrors `vp_csrf` into `X-CSRF-Token` on
  every request. Admin-only enforcement on every account/device action is
  preserved.

## [3.13.4] — 2026-07-13

### Added
- **VayuDomains (Stage 3b) — per-domain receive + read mail isolation.** A
  mail-enabled secondary domain now receives external mail into its own isolated
  Maildir, and its users read only that mailbox over IMAP and POP3. The SMTP
  receive gate, the engine's local-recipient check and the bridge accept a
  secondary via a single registry-backed predicate; IMAP/POP3 sessions key every
  Maildir operation on the authenticated login's owning domain instead of the
  primary. The Accounts admin page grows a domain picker (shown only when a
  mail-enabled secondary exists) so an operator can provision a mailbox on a
  secondary domain, isolated end to end.

### Notes
- **Byte-identical for single-domain installs.** Every new mail path is gated on a
  registered mail-enabled secondary (the acceptance resolver is nil otherwise), and
  a bare or `local@primary` login always resolves to the primary Maildir — the
  mail-engine tests are unchanged and still pass. Webmail and CMS-user mailboxes
  remain primary-only, so there is no cross-domain leak; full webmail access to
  secondary mailboxes and per-domain outbound/branding (DKIM, autoconfig, DNS) ride
  with **Stage 3c**. See **ADR-0132**.

## [3.13.3] — 2026-07-12

### Added
- **VayuDomains (Stage 3a) — mail-domain foundation.** The VayuOS **Domains** page
  now shows, per domain, whether it carries mail and how many mailboxes it holds,
  so operators can see the mail rollout state at a glance. This is the first,
  deliberately conservative step of per-domain mail: the mailbox count is derived
  read-only from the account store (mailboxes are keyed by full address, so the
  host is derived, not stored).

### Notes
- **The mail delivery, authentication and Maildir-resolution paths are untouched**,
  so the primary domain's mail is byte-identical (proven by the unchanged
  mail-engine tests). Per-domain receive + read isolation is **Stage 3b**, and
  per-domain outbound + branded mail (DKIM, autoconfig, DNS, webmail theming) is
  **Stage 3c** — staged separately because the mail engine carries the product's
  isolation and no-plaintext guarantees. See **ADR-0132**.

## [3.13.2] — 2026-07-12

### Added
- **VayuDomains (Stage 2c) — per-domain tags, feeds, sitemap & search.** With a
  secondary domain registered, the remaining public discovery surfaces are now
  scoped to the active domain: the `/tags` index and each `/tags/<tag>` page
  count and list only that domain's published posts; `/sitemap.xml`, `/feed.xml`
  and `/robots.txt` are served from per-domain artefacts that carry only the
  domain's posts, tags and own host; and search — both the server endpoint and
  the client index that powers the instant-search modal — returns only the
  domain's own posts. This closes the Stage 2b gap where those surfaces were
  still global.

### Notes
- **Byte-identical for single-domain installs.** Every per-domain path activates
  only when a secondary domain exists; a plain install keeps its original global
  code path and artefacts with no added query or file. The shared VayuFind index
  is left untouched — search is scoped by over-fetch + ownership post-filter, not
  an engine change. See **ADR-0132**.

## [3.13.1] — 2026-07-12

### Added
- **VayuDomains (Stage 2b) — per-domain public homepage & article pages.** With a
  secondary domain registered, each domain's homepage feed now serves only its
  own posts (scoped query + a per-domain render cache), and an article is
  reachable only on its owning domain. Reassigning a post refreshes every
  domain's cache automatically.

### Notes
- **Byte-identical for single-domain installs.** The per-domain paths activate
  only when a secondary domain exists; a plain install takes its original route
  with no added work. Tag pages, feeds, sitemap and search remain global for now
  (next increment) — see **ADR-0132**.

## [3.13.0] — 2026-07-12

### Added
- **VayuDomains (Stage 2a) — per-domain content ownership.** A new additive
  `domain_id` column on articles (migration 060) lets each post be owned by a
  domain. The article repository and service gain domain-scoped reads
  (`ListScoped` / `GetScoped`, with an all-domains scope for the admin and JSON
  API), plus reassignment and per-domain counts. The **Domains** page in VayuOS
  now shows a post count per domain and lets you assign a post to a domain by
  its slug.

### Notes
- This stage is **byte-identical** for existing single-domain installs: every
  existing post defaults to the primary domain, and the public render path is
  intentionally unchanged. Serving per-domain content on the public site (which
  requires keying the homepage/article render cache by domain) is the next
  sub-stage — see **ADR-0132**. The global-unique `articles.slug` still means two
  domains cannot share a slug yet; relaxing that needs an articles-table rebuild
  and is deferred.

## [3.12.0] — 2026-07-12

### Added
- **VayuDomains (Stage 1) — one binary, one registry of every hostname it
  serves.** A new `domains` table (migration 059) and an in-process registry
  (`internal/domain`) become the authoritative source of truth for host
  resolution. A new **Domains** page under VayuOS (`/os/domains`) lists every
  registered hostname and lets an operator add, enable, disable or remove
  secondary domains and choose what each one serves (blog, business,
  business + `/blog`, static, or mail-only).
- **Host-resolution middleware** annotates every request with the registered
  domain that owns its `Host` header. It is a pure pass-through: an unknown or
  disabled host falls back to the primary domain, and no handler yet branches on
  the resolved domain — so the primary site is served **byte-identically**.
- **Primary-domain seed.** On startup the configured `DOMAIN` is idempotently
  registered as the single primary domain, with its certificate marked as
  managed outside the registry (the existing certbot cert). The primary domain
  is protected — it cannot be disabled, deleted, or edited from the page; its
  site type tracks the global Website (`site.mode`) setting.

### Notes
- This is Stage 1 (the foundation). Per-domain **content, mail and members**,
  and **automatic per-domain TLS/nginx provisioning**, are deferred to later
  stages and documented — with the hard constraints that shape them — in
  **ADR-0132**. Adding a domain here registers it; point its DNS at this server
  and provision a certificate before it serves traffic. The admin page states
  this plainly so nothing over-promises.

## [3.11.49] — 2026-07-12

### Changed
- **The VayuTalk subdomain now provisions itself on `update`, not just `deploy` —
  so no self-hoster has to run anything by hand.** The talk-subdomain setup moved
  into a single shared, idempotent, non-fatal script (`scripts/setup-talk-subdomain.sh`)
  that BOTH `deploy-vayupress.sh` and `update-vayupress.sh` call. Now the entire
  server side is automatic: point `talk.<domain>` DNS at the box (CDN proxy OFF),
  and the next VayuPress update (or deploy) adds the TLS SAN, writes the relay
  vhost, and advertises the host — the operator never touches nginx or certbot.
- **Certificate expansion now preserves the cert's existing names.** Instead of a
  fixed domain list, it re-issues the current certificate's SANs **plus** talk,
  and drops only names that no longer resolve in DNS. This fixes a real failure
  mode: a stale record (e.g. a `blog.<domain>` that was never created) no longer
  aborts the renewal, and live names like `mail.<domain>` are never dropped.

### Notes
- Existing servers get this the moment they update. The one manual step remains a
  single DNS record; everything downstream is hands-off.

## [3.11.48] — 2026-07-12

### Added
- **Automatic, CDN-proxy-off `talk.<domain>` subdomain for VayuTalk.** The mobile
  app's long-lived chat stream can't pass a CDN bot-challenge (it isn't a
  browser), so behind Cloudflare the app could send but never receive. VayuPress
  now automates a dedicated relay subdomain that bypasses the CDN while the main
  site keeps full protection — **the operator's only step is one DNS record**
  (`talk.<domain>` → server IP, proxy OFF). On the next deploy (every update runs
  the script) VayuPress:
  - **adds `talk.<domain>` to the Let's Encrypt certificate** (via `certbot
    --expand`, preserving the existing site/mail SANs, with graceful fallbacks so
    a missing record never breaks the main cert);
  - **writes a dedicated nginx vhost** exposing only the relay API and health,
    with SSE never buffered/gzipped; and
  - **sets `VAYUOS_TALK_HOST`**, so `/.well-known/vayumail/autoconfig.json`
    advertises the host and the app routes its chat stream there automatically.
  Until the DNS record exists the step is skipped and the app keeps using the main
  domain — fully backward compatible. Documented in `docs/TROUBLESHOOTING.md`
  ("Recommended: a dedicated, proxy-off talk.<domain> subdomain").

### Changed
- Autoconfig gains an optional `talk` field (the advertised relay host), omitted
  unless `VAYUOS_TALK_HOST` is set. The VayuMail app prefers it, but only after
  confirming it is within the mail domain and answering as a live relay, so a
  stale or foreign value can never redirect the connection.

## [3.11.47] — 2026-07-12

### Fixed
- **The web console no longer "eats" messages meant for your phone** — the real
  reason an app-to-app message would "only appear in the web." The VayuTalk relay
  keeps a single shared queue per recipient address, and a mailbox can be open in
  two places at once (the phone app *and* the web console, both as the same
  address). The console used to read-destroy (ack) every message the instant it
  rendered it, which deleted the queued copy before the recipient's **phone**
  could flush it on reconnect — so the message vanished from the queue and the
  app never received it. The web console is now a **passive viewer**: it displays
  messages and lets them expire by their TTL, while the app remains the
  authoritative reader that destroys a message when it is revealed. Because the
  console no longer acks, a stream reconnect re-flushes the queue, so the web
  client now dedupes by message id to avoid showing a message twice.

### Upgrade Notes
- This is a server-behaviour fix: **rebuild and restart VayuPress** for it to take
  effect. Confirm the running build afterwards with
  `curl -s https://YOUR-DOMAIN/health | grep -o '"version":"[^"]*"'` — it must
  report `3.11.47` (an older number means the old binary is still running, which
  by itself explains "the app can't receive").

## [3.11.46] — 2026-07-12

### Fixed
- **VayuPGP: one key per address, resolved the same way everywhere** — closes the
  root cause of "a message sent from the web never decrypts on the phone." Three
  linked keystore defects (found in an adversarial audit) are fixed:
  - **No duplicate keys.** Account creation now uses `EnsureKeypair`, not the
    unguarded `GenerateKeypair`, so a CMS user created for an address that already
    has a mailbox key reuses that key instead of minting a second one for the same
    email. Two keys per address is what let the web encrypt to one while a device
    held the other.
  - **Consistent resolution.** The email→key index now applies the same
    deterministic oldest-key-wins tiebreak at runtime (`save`) and at boot
    (`reindex`), so the web-encrypt path and a device's key-sync path can never
    resolve different keys for the same address.
  - **Revoked keys never win.** `reindex` no longer re-activates a revoked key on
    restart. Pinned by `TestKeystoreDeterministicEmailResolution` (now also
    asserting in-process resolution) and `TestKeystoreRevokedKeyNotActive`.

### Documentation
- **How to run VayuTalk behind a reverse proxy or CDN.** New
  `docs/TROUBLESHOOTING.md` → "VayuTalk Chat Issues" documents the two things
  that stop the chat stream from staying open: an nginx proxy that buffers/idle-
  times-out SSE (fixed with dedicated `location` blocks), and a CDN bot challenge
  (e.g. Cloudflare) that a browser can pass but the mobile app cannot — fixed by
  a WAF "Skip" rule for the `/api/v1/` and `/os/talk/` paths, so the rest of the
  site keeps full protection while only the VayuTalk stream is let through.

## [3.11.45] — 2026-07-12

### Fixed
- **VayuTalk streams no longer sit in a “Reconnecting…” loop** (the real cause of
  web↔app messages queuing instead of delivering live). Three fixes:
  - **nginx config**: the deploy script now gives the SSE endpoints
    (`/api/v1/talk/stream`, `/os/talk/stream`) dedicated `location` blocks with
    `proxy_buffering off`, `gzip off`, and a long `proxy_read_timeout` — the
    generic proxy blocks were buffering and 60-second-timing-out the long-lived
    streams, so a stream would connect, drain the queue once, then drop. **Re-run
    the deploy script (or apply the two blocks) and `nginx -s reload`** for this
    to take effect on a running server.
  - **Stream cap now evicts instead of rejects**: a reconnecting client used to
    pile up stale server-side streams and hit the per-user limit, spiralling into
    permanent rejections; a new stream now evicts that user's oldest so a
    reconnect always wins. Cap raised 3 → 5.
  - **Heartbeat** lowered 25s → 15s so an idle conversation's stream stays well
    under proxy idle timeouts. Pinned by `TestSubscribePerUserCapEvictsOldest`.

## [3.11.44] — 2026-07-12

### Fixed
- **VayuTalk: the app now actually receives messages (the “web→app never
  arrives” bug).** The app's SSE receive stream (`/api/v1/talk/stream`) was
  missing the `X-Accel-Buffering: no` header that the web stream already sets, so
  a reverse proxy in front of VayuPress (nginx buffers by default) held the
  stream and the app got nothing — no incoming messages, no read receipts — even
  though its POST /send worked, which is exactly the one-directional symptom
  seen. Both SSE streams now disable proxy buffering. Pinned by
  `TestTalkAPIStreamDisablesProxyBuffering`.

## [3.11.43] — 2026-07-12

### Changed
- **VayuTalk web: verify a contact once, not every reload.** A verification is
  now remembered (bound to the exact public fingerprint you confirmed), so a
  reload keeps the ✓ Verified badge instead of resetting it. If a peer's key ever
  changed, the mark clears and you're asked to re-verify — the correct security
  behaviour. Only the public fingerprint is stored locally; messages are still
  never persisted.

### Fixed
- **VayuPGP: the “active” key for an address can no longer flip between server
  restarts.** If two key files ever share an email, resolution is now
  deterministic (oldest key wins) instead of depending on directory order — so a
  message encrypted to, or a fingerprint verified against, that key never
  silently breaks after a reboot. This underpins stable, verify-once identities
  across the app and the web. Pinned by `TestKeystoreDeterministicEmailResolution`.

## [3.11.42] — 2026-07-12

### Security
- **VayuPGP keystore: stop naming key files after a bare hash of the mailbox
  address** (CodeQL: weak hashing of sensitive data). A key file used to be
  named `sha256(userID).key.json`, which let anyone able to read the key
  directory confirm whether a given address had a key by recomputing the hash.
  Files are now located through indexes rebuilt from file *contents* at startup,
  so a filename carries no recoverable link to its owner, and new files are named
  with a keyed HMAC (opaque without the master secret). Fully backward
  compatible: existing key and archive files load unchanged and are reused in
  place on the next write (no migration, no re-keying). The at-rest AES-256-GCM
  encryption of private keys is unchanged. Pinned by
  `TestKeystoreLegacyFilenameStillLoads` and `TestKeystoreFilenameNotBareHashOfUserID`.

## [3.11.41] — 2026-07-12

### Changed
- **VayuTalk web now always uses store-and-forward — the “Live only” toggle is
  gone.** A message is delivered in real time if the peer is connected and
  otherwise queued and delivered the moment they next connect, so nothing is
  ever dropped for being offline. Sent status reads **Delivered** (live) or
  **Sent** (queued), then **Read** when they open it. This removes the footgun
  where a “Live only” message to a momentarily-disconnected peer showed
  “not delivered.”

## [3.11.40] — 2026-07-12

### Added
- **VayuTalk “chat as” switcher.** An administrator — who owns every mailbox on
  the server — now gets a mailbox dropdown in the VayuTalk left rail and can chat
  as any of them; the recipient sees the selected address as the sender.
  Switching identity cleanly reconnects the stream, resets the conversation view,
  and refreshes your safety number. A normal mailbox holder still has exactly one
  identity (their own) and no switcher. The selection is authorized server-side
  (`talkSelf`): a non-admin can never send as another mailbox, and an admin is
  confined to mailboxes on their own server. Pinned by `TestTalkChatAsBoundary`
  and `TestTalkChatAsAdminSendsAsSelected`.

## [3.11.39] — 2026-07-12

### Added
- **VayuTalk key verification on the web.** Each conversation now has a
  **🛡 Verify** panel showing your safety number and the peer's, normalised
  identically to the app's Verify-contact screen so the two are directly
  comparable out-of-band. Mark a peer verified (per tab) and a shield appears on
  the conversation — the web counterpart to VayuMail Mobile's peer verification.
- **Recipient key preflight.** Opening a conversation checks the recipient's key
  (`GET /os/talk/peer`) and, if none exists yet, shows a clear banner explaining
  they must open VayuMail on this server once — instead of a message that
  silently fails to send.

### Fixed
- **Sending from the web now reports precisely what happened.** A missing
  recipient key returns a distinct, actionable “no VayuTalk key for <addr>”
  error (not a generic failure); a queued store-mode message reads “Queued —
  delivers when they connect” rather than looking like an error; a live message
  to an offline peer says so plainly. (The identity fix in 3.11.38 already
  resolved the underlying “couldn’t send at all” case for signed-in admins.)
  Covered by `TestTalkPeerEndpoint`, `TestTalkSendUnknownRecipient`,
  `TestFormatSafetyMatchesApp`.

## [3.11.38] — 2026-07-12

### Changed
- **VayuTalk is now its own system in the console**, not a tab inside VayuMail.
  It has a dedicated **VayuTalk** entry in the left sidebar and a clean
  `/os/talk` route — it reuses your mailbox identity but is a separate product
  surface, as intended.

### Fixed
- **VayuTalk no longer shows a false “No mailbox is assigned” dead-end** for a
  signed-in owner/admin. Chat identity is now resolved from the live session
  instead of a database re-read (which dropped the mailbox address a mailbox
  login carries, and which a pure CMS admin never has): it uses your bound
  mailbox, else your account email when it’s on the mail domain (“chat as who I
  signed in as”), else — for an admin — the first mailbox on the server or
  `postmaster@domain`. You can open VayuTalk and start chatting immediately with
  the identity you logged in as. (`TestTalkIdentityResolution`.)

## [3.11.37] — 2026-07-12

### Added
- **VayuTalk on the web.** The ephemeral, end-to-end-encrypted chat introduced
  in 3.11.36 now has a first-class client in the VayuOS console at
  **VayuMail → VayuTalk** (`/os/vayumail/talk`) — so web and app are one
  interconnected system over a single relay. A message sent from the browser
  reaches the mobile app and vice-versa; both decrypt, and both read-destroy.
  - The browser holds no private key. As with reading encrypted mail in webmail,
    the server does the PGP crypto on the signed-in mailbox's behalf: on send it
    signs+encrypts to the recipient (wire-identical to the app's ciphertext); on
    receive a server-side-decrypting **SSE** bridge pushes plaintext to the tab
    and immediately acks (read-destroys) the envelope. The relay, the network,
    and every intermediary still see only ciphertext; nothing is persisted or
    logged, and conversations live in tab memory only (a reload wipes them).
  - The client is deliberately tiny — vanilla JS over `EventSource` + `fetch`,
    no vendored crypto library — so the console stays fast and strict-CSP clean.
  - New engine primitive `VayuPGP.EncryptAndSignFromEmail` (sign as any local
    mailbox by address); bidirectional interop pinned by `TestTalkWebToApp` /
    `TestTalkAppToWeb`. See ADR-0131 (“Web client”).

## [3.11.36] — 2026-07-11

### Added
- **VayuTalk — ephemeral, end-to-end-encrypted messaging.** A sovereign
  identity now has a private, real-time conversation channel that reuses its
  mailbox address and PGP keypair — no new account, no phone number, no
  third-party messenger. A small REST + Server-Sent-Events API lives under
  `/api/v1/talk/*`: `connect` (mailbox credential → 12 h bearer token, same
  device-approval and anti-enumeration defences as VayuMail sign-in), `stream`
  (SSE push of envelopes, read/expiry receipts, and heartbeats), `send`
  (`live` = deliver only if online, or `store` = queue in memory until read or
  expiry), `ack` (read-destroy + read receipt to the sender), and `pubkey`
  (fetch a recipient's public key to encrypt to). Enabled whenever mail is
  enabled; switch off with `VAYUOS_TALK=off`. See
  [ADR-0131](docs/adr/ADR-0131-vayutalk-ephemeral-messaging.md).

### Security
- **VayuTalk stores no plaintext and nothing on disk.** The relay carries only
  opaque ciphertext plus minimal routing metadata in a bounded in-memory store;
  it never sees message plaintext, never writes to SQLite, and never logs
  message content. A process restart purges every envelope and token. Read
  messages are destroyed on ack and everything expires on a clamped TTL
  (60–3600 s). Every resource is capped (≤ 64 KiB per message, ≤ 50 queued per
  recipient, ≤ 5000 global, ≤ 3 streams per user, ≤ 500 global) so a hostile
  client cannot exhaust the host, and `connect` runs through the same
  brute-force throttle and mail-sync credential scope (device approval enforced)
  as the mail listeners. Threat model, the SSE-not-WebSockets rationale, and the
  forward-secrecy / double-ratchet-as-future-work discussion are in ADR-0131.

## [3.11.34] — 2026-07-11

### Added
- **VayuMail app privacy policy page** at `/vayumail/privacy` — a built-in,
  CMS-free legal page rendered with the site theme, for the Google Play
  listing of the VayuMail Android app (com.vayu.mail). It states plainly
  that the app collects no data, uses no trackers, and talks only to the
  user's own mail server.

## [3.11.33] — 2026-07-11

### Added
- **Webmail: print, full view, and a split view that gets out of the way.**
  The reading pane now stays collapsed until you open a message — the list
  gets the full width — and opens with a soft rise when you click a mail.
  A new ⛶ button expands the open message to a full-screen overlay (ESC or
  Close collapses it), and 🖨 prints exactly the open message — no list, no
  toolbars, no chrome. Message rows gained hover glide, an unread accent
  bar, and comfortable density; folder tabs behave like segmented pills.
- **Auto-delete read mail (retention).** Each mailbox can opt in to a
  retention window (Off/30/90/180/365 days, Accounts page): mail that has
  been READ for longer than the window is permanently deleted — no Trash
  detour, no recovery. Pinned messages and the Sent, Drafts, Archive and
  Snoozed folders are never touched, and unread mail always survives, so
  "save this" is one tap in any client. Read time is stamped on the
  message file at the moment it is first marked seen (webmail or IMAP)
  and survives every later flag change; sweeps run hourly and are
  audit-logged per mailbox. See
  [ADR-0130](docs/adr/ADR-0130-vayumail-retention-auto-delete.md).
- **VayuMail device approval — no mail sync without web approval.** A new
  device that signs into VayuMail now registers itself
  (`POST /api/v1/members/vayumail-device-register`) and starts **pending**: it
  cannot sync any mail — even with the correct mailbox password — until it is
  approved from the 2FA-protected VayuPress console. A new **Devices** card on
  the mail Accounts page lists every registered device (label, platform,
  status, registered, last used) with one-click Approve / Block / Remove and a
  per-mailbox "Require device approval" toggle; a companion
  `vayumail-device-status` endpoint lets the app poll its own approval state
  and start syncing the moment it flips. See
  [ADR-0129](docs/adr/ADR-0129-vayumail-device-approval.md).

### Security
- **A stolen mailbox password can no longer sync mail.** While a mailbox
  requires device approval (the default), the raw CMS/mailbox password is
  rejected on IMAP/POP3/SMTP-submission and on the private-key sync endpoint —
  only an **approved** device credential authenticates there; pending and
  blocked devices fail uniformly. The password keeps working for the member
  web endpoints (portal login, device registration), so a holder can always
  bootstrap a new device, and existing app passwords are grandfathered as
  approved so already-connected devices survive the upgrade. Every device
  action is audit-logged
  (`vayumail.device.register/.approve/.block/.remove/.require`), and both new
  endpoints reuse the shared brute-force throttle with byte-identical 401s
  (anti-enumeration).

## [3.11.32] — 2026-07-11

### Fixed
- **Webmail: opening a message works again — every message, every action.**
  The message-id sanitizer added in a security hardening pass rejected the
  `/` in real Maildir ids (`new/171…vm`, `cur/171…:2,S`), so every id
  arriving at the message reader, pin, mark-read, junk, move and delete
  handlers was blanked to "" and the reader answered "Message not
  available." On phones the reading pane hides while it shows only a
  placeholder, so tapping a mail looked like it did nothing at all.
  Reproduced end-to-end in a headless browser, then fixed: ids now allow
  exactly one `/` (the Maildir subdirectory), still reject `..`, absolute
  paths and multi-segment paths, and every allowed byte remains
  HTML-inert — the XSS barrier and the engine's own `filepath.Base` +
  `..` rejection stay intact.

## [3.11.31] — 2026-07-11

### Added
- **VayuMail-Mobile private-key sync for on-device decryption.** A new
  authenticated endpoint, `POST /api/v1/members/vayumail-privkey`, returns the
  caller's **own** mailbox PGP private key (armored) so the app can import it
  and decrypt received PGP mail on-device — WKD only serves public keys, which
  left the app able to encrypt and verify but never to read encrypted inbound
  mail. It authenticates with the same credential path as
  `vayumail-login` (mailbox password or a device app password), reuses the
  shared brute-force throttle, mints a keypair on demand for accounts that
  pre-date auto-keygen, and returns `Cache-Control: no-store`. Consistent with
  VayuPGP's existing server-side-decryptable model. See
  [ADR-0128](docs/adr/ADR-0128-vayumail-private-key-sync.md).

### Fixed
- **Webmail: tapping a message now reliably opens it in the reading pane.**
  On narrow screens the split-view pane renders above the message list, so
  after HTMX loaded the message the viewport could stay parked mid-list and
  the tap looked like it did nothing. The reader is now scrolled into view
  as soon as it loads in `#vm-readpane`.

### Security
- **Private-key endpoint is throttled, audited and non-enumerating.** Every
  failed attempt accrues a decaying per-mailbox delay; any failure returns a
  single generic `401` whose status and body are identical for an unknown
  mailbox and a wrong password, so the endpoint cannot be used to discover which
  addresses exist; every successful export writes a `vayumail.privkey.fetch`
  audit-log entry.

## [3.11.30] — 2026-07-10

### Fixed
- **CI markdown-lint gate unblocked.** A `main` commit that removed the badge
  note left two consecutive blank lines in `README.md`, so every branch merge
  failed MD012. Collapsed to a single blank line.
- **Broken VayuShield screenshot in the README showcase.** The Bot Shield &
  Analytics panel (`docs/screenshots/admin-os-shield.png`) was referenced but
  never present, showing a broken image. Added a faithful capture of the real
  console — the live status hero, the Aegis L0–L5 layer map, and the
  protection toggles — rendered from the shipping `admin-os.css`.

### Changed
- **README VayuShield section right-sized.** The five-bullet Aegis breakdown
  was longer than every other feature; condensed to one proportionate,
  enterprise-grade paragraph that still names all five layers and links to the
  architecture ADR.
- **Marketing site now shows the live release version** beside the live star
  count. Both use the same two-stage source: a same-origin JSON file baked
  fresh by the deploy workflow (instant, never rate-limited) plus a live
  in-browser refresh from the GitHub API. The deploy workflow now bakes
  `assets/version.json` from the latest release tag alongside `stars.json`, and
  the version chip links to that release.

## [3.11.29] — 2026-07-10

### Changed
- **New VayuPress logo mark, transparent, theme-aware.** The updated V-and-wing
  mark now leads the README and the marketing site with a genuinely
  transparent background — a white mark on dark surfaces, a black mark on light
  ones — so it sits cleanly on any backdrop. The README hero switches the mark
  by `prefers-color-scheme`, and "VayuPress" renders as real text beside it
  (correct capital V and P) rather than baked-in artwork. The site header and
  footer pick up the new transparent mark automatically, with the footer
  lock-up's intrinsic aspect ratio corrected so it no longer reserves a
  slightly wrong box. No unreferenced image files were added.

## [3.11.28] — 2026-07-10

### Changed
- **README overhaul — world-class showcase.** Added a dedicated **VayuShield
  "Aegis"** section (the built-in, self-learning bot shield & anti-DDoS that
  removes the need for Cloudflare — the L0 sovereignty lane, L2 fair-shed, L5
  reputation brain, L4 silent challenges, L1 kernel offload), a VayuShield
  showcase screenshot, expanded writer copy (typewriter, focus spotlight,
  paste-as-Markdown, live outline, footnotes, captions, duplicate, keyboard
  reordering) and VayuOS copy (14-day trend area chart, mobile card tables), a
  bot-&-DDoS row in the comparison table, VayuShield in the architecture
  diagram, and **live release + star badges** that update themselves from the
  repository. The Bot Shield panel (`/os/shield`) is now captured by the
  screenshot pipeline.
- **Marketing site (`vayupress.com`) — genuinely live GitHub-star count.**
  Replaced the `force-cache` fetch (which pinned a stale count) with a
  two-stage design: the deploy workflow bakes the current count into a
  same-origin `assets/stars.json` (with a **daily `schedule:` refresh** so it
  stays current with no code change), and the client reads that first — instant
  and rate-limit-proof — then revalidates against the live GitHub API with
  default caching. The site continues to deploy via GitHub Actions.

### Docs
- **ADR-0127** records the brand refresh (light/dark **VayuPress** wordmark
  lockups, capital V and P), the README showcase overhaul, and the live-stars
  site pipeline.

## [3.11.27] — 2026-07-10

### Changed
- **The dashboard's publishing trend is now a real area chart.** The flat
  sparkline grew into a proper 14-day chart: a soft gradient fill under a 2px
  line, recessive dashed gridlines with a max-value label, sparse date ticks
  (first · middle · last), the busiest day direct-labelled, and the newest day
  emphasised with a dot and its count. Hovering any day reveals its dot and an
  exact-value tooltip ("4 Jul · 2") — implemented as pure-CSS per-day hit
  columns, so the whole chart remains server-rendered SVG with **zero
  JavaScript and no inline styles** (strict-CSP safe), themed entirely through
  design tokens in both light and dark. Native `<title>` tooltips serve touch
  and assistive tech; the chart scales with the card at a fixed aspect so text
  never distorts. All-zero fortnights, single-day series and NaN-free geometry
  are covered by unit tests, and the render was visually QA'd in Chromium.
  The compact sparklines elsewhere (intel, newsletter cards) are unchanged.

## [3.11.26] — 2026-07-10

### Added
- **Live document outline in the post editor.** The sidebar now shows the
  post's heading structure as you write: every heading becomes a click-to-jump
  entry (indented by level), the section your caret is in stays highlighted,
  and the list updates instantly as headings are added, edited, reordered or
  removed. Jumping scrolls the heading to the centre of the view (instant when
  the reader prefers reduced motion) and places the caret in it. The outline is
  rebuilt from the block model behind a cheap string signature, so the
  per-keystroke cost when no heading changed is a single string compare — no
  DOM work — and it scrolls internally past ~14 entries so a long document
  never pushes the sidebar tools off-screen. Hidden entirely until the post
  has its first heading.

## [3.11.25] — 2026-07-10

### Added
- **Keyboard-accessible block reordering in the post editor.** The block drag
  handle is now fully operable without a mouse: focus it and press **Space**
  (or Enter) to lift the block, **↑ / ↓** to move it a step at a time, and
  **Space** or **Escape** to drop. Each step is narrated to screen readers via
  a polite live region ("Block lifted", "Moved to position 3 of 7"), the lifted
  block shows a clear highlighted state, and the handle has a visible focus
  ring. This closes the gap where reordering by handle was mouse-only — the
  existing drag-and-drop and ↑/↓ buttons are unchanged.

## [3.11.24] — 2026-07-10

### Added
- **App-password console for VayuMail Mobile (ADR-0126).** The Connect tab can
  mint device app passwords again — the creation path that disappeared with the
  setup-QR removal in v3.9.16. A new **App passwords** card creates a
  per-device credential for a mailbox (random 20-character secret, revealed
  once in copy-friendly `abcd-efgh-ijkl-mnop-qrst` blocks, stored only as an
  Argon2id hash) and lists/revokes existing ones — label and created date only,
  never the hash. Administrators manage any mailbox; a mailbox holder can
  self-serve their own. The secret signs in with or without the display dashes
  (the auth path normalises them). This also makes the opt-in
  `VAYUMAIL_2FA_ENFORCE` mode attainable again: it needs an app password to
  exist before it retires the mailbox password on IMAP/SMTP/POP3, and one can
  now be created.
- **Inline magnitude bars in the engagement analytics.** Each Views figure in
  Traffic sources, AI systems and Top pages now sits over a thin bar sized
  relative to the busiest row, so the tables read as a mini bar chart at a
  glance — which source or page dominates is obvious without comparing digits.
  CSP-safe (the bar width uses the pre-defined `w-N` utility classes, never an
  inline style) and it folds neatly into the stacked mobile card layout.
  Covered by unit tests for the width bucketing and cell markup.
- **Duplicate block in the post editor.** A new ⧉ control on every block (and
  ⌘/Ctrl+D while editing) clones the block — text, list items, checkboxes,
  image settings and all — directly below and moves the cursor into the copy.
  A natural companion to the existing move/insert/delete controls.

### Changed
- **Connect tab describes the real VayuMail Mobile flow.** The official-app
  card now walks the 30-second setup — install the app, enter your email + an
  app password (linked directly to the new form), and the app auto-discovers
  every server setting from `/.well-known/vayumail/autoconfig.json` and
  auto-syncs PGP keys via WKD. The Recommended-settings table now points at app
  passwords as the preferred device credential.

### Fixed
- **Numbered lists now export as numbered lists.** A list built in the editor
  (via the palette, the `1.` autoformat, or a paste) is stored as
  `{ type: list, style: ordered }`, but the Markdown serialiser's list case
  ignored `style` and always emitted `-` bullets — so a numbered list
  round-tripped out as a bullet list. The serialiser now honours the ordered
  style, and the Markdown loader was unified onto the same canonical shape, so
  there is exactly one representation of a numbered list across load, create
  and export.

## [3.11.23] — 2026-07-10

### Changed
- **VayuOS dashboard — a lighter, more three-dimensional stat grid.** The six
  headline cards gained a subtle top-light seam (a 1px gradient that catches the
  eye like a panel bezel), a whisper-soft vertical surface gradient, tabular
  figures so the numbers line up, and a gentle 2px lift + icon bloom on hover.
  All pure CSS via existing design tokens — no new assets, no extra requests,
  and every motion is disabled under `prefers-reduced-motion`. A solid-colour
  fallback sits behind the `color-mix` gradient for older engines.

### Fixed
- **Data tables now fold into phone-friendly cards even after an HTMX swap.**
  The responsive-table helper only ran once at page load, so any table
  delivered by HTMX later — the engagement analytics, the Bot Shield sections,
  the mailbox list — stayed a wide horizontal scroll on phones. It now also
  runs after every `htmx:afterSwap`, scoped to just the swapped subtree and
  idempotent, so HTMX-loaded tables get the same labelled-card layout as
  server-rendered ones. No effect on desktop.

## [3.11.22] — 2026-07-10

### Security
- **Defensive hardening: no DOM-derived string is ever interpolated into a
  query selector.** The VayuMail conversation thread-toggle previously built a
  `querySelectorAll('[data-vm-thread="…"]')` selector by concatenating a value
  read from a `data-*` attribute. Though that value is server-rendered, the
  toggle now iterates the candidate rows and compares the attribute value
  directly — so selector injection is structurally impossible and no similar
  DOM-text-in-selector pattern remains to be flagged. (The compose recipient
  chips were checked too: their field name is a hardcoded literal, never
  DOM-derived, so they were already safe.) This complements the v3.11.21 root
  fix of the reader-pane navigation.

## [3.11.21] — 2026-07-10

### Security
- **CodeQL #54 (DOM-text navigation) — fixed at the root by removing the DOM
  source.** Two earlier attempts (a regex guard, then URL reconstruction) both
  left the finding open because no in-function barrier satisfies
  `js/xss-through-dom` for a `location.assign` fed by `getAttribute`. The
  VayuMail reader-pane action buttons no longer read a full navigation URL from
  a `data-back`/`data-next` attribute. Instead the server emits only the raw
  next-message **id** (`data-next-id`), and the client **builds** the back/next
  targets from a literal path prefix plus `encodeURIComponent`-escaped
  `user`/`folder`/`id` components — so no DOM-derived text reaches `location`
  without passing through a recognised sanitiser. The resulting URLs are
  byte-identical to before (verified), and the now-unused `safeNav`/`localPath`
  helpers were removed.

## [3.11.20] — 2026-07-10

### Added
- **Post editor: real footnotes** (writing-experience audit). Markdown blocks
  now support proper academic-style footnotes — write `A claim.[^src]` inline
  and `[^src]: the supporting note.` anywhere in the block, and the renderer
  produces a numbered superscript reference linked to a footnotes section at the
  end, each note carrying a `↩` back-link to where it was cited. Enabled via
  goldmark's Footnote extension. The bluemonday sanitiser was taught to allow
  *only* the exact `fn:`/`fnref:` id anchors and same-document `#fn…` fragment
  hrefs the extension emits (matched by strict regexes), plus ARIA `doc-*`
  roles — so back-links resolve while no author-supplied `id` or off-site/scheme
  href can slip through. Covered by a render test (reference, note body, id
  anchors and back-link all survive sanitisation) and a narrowness test
  (arbitrary `id`s and non-footnote fragment hrefs are still stripped).

## [3.11.19] — 2026-07-10

### Security
- **CodeQL #55 — resource exhaustion (editor Markdown export).** Heading export
  built its `#` run with `new Array(level + 1).join('#')`. The level was already
  clamped to 1..6, so it was never actually exploitable, but a `new Array(n)`
  sized from block data is a flagged pattern. Replaced with a fixed six-entry
  lookup table indexed by the clamped level — there is now **no input-sized
  allocation at all**, so the pattern is gone by construction.
- **CodeQL #54 — DOM-text navigation (`safeNav`).** The reading-pane navigation
  guard now rebuilds its destination from the URL parser
  (`new URL(u, origin)` → `pathname + search + hash`) and returns that
  reconstructed value instead of the raw `data-back`/`data-next` attribute, so
  nothing tainted from the DOM reaches `location`. Cross-origin,
  protocol-relative (`//host`), scheme (`javascript:`) and back-slash
  (`/\host`) values all resolve to empty and fall back to a constant path —
  verified with a navigation-target test matrix.

## [3.11.18] — 2026-07-10

### Added
- **Post editor: paste a whole Markdown draft and it becomes real blocks**
  (writing-experience audit). Pasting multi-line text onto an empty paragraph
  now parses it into the matching typed blocks instead of dumping everything
  into one textarea: `#`..`######` headings, grouped bullet / numbered / task
  lists (with checked state), block quotes (consecutive lines joined), fenced
  code with its language, horizontal rules, and paragraphs (blank lines split
  them; wrapped lines join). Bring a draft over from anywhere and it lands
  structured and editable. Guardrails: only fires on an empty paragraph with a
  genuine multi-line paste (single-line and mid-paragraph pastes are unchanged),
  the lone-URL→embed flow still takes precedence, and the parse is capped at 500
  lines so an enormous paste can never jank the editor. Verified with a
  block-by-block parser test over a mixed document.

## [3.11.17] — 2026-07-10

### Fixed
- **Editor focus mode: typewriter padding CSS cascade bug** (mobile audit). The
  distraction-free "typewriter room" (large top/bottom padding so the first and
  last lines can reach the vertical centre) was defined by an unscoped rule that
  a later, unconditional `padding-top` silently overrode — so on **desktop** the
  centring room was lost, and on **mobile** a competing rule could leave roughly
  40% of the screen as empty space above and below the text. The padding is now
  a single authoritative pair: a small base value everywhere, and the tall
  typewriter value only inside `@media (min-width: 901px)`, placed last so it
  wins on wide screens where the canvas scrolls and never applies on phones
  (where the page scrolls instead). Desktop regains proper caret centring;
  mobile focus mode no longer wastes vertical space.

## [3.11.16] — 2026-07-10

### Added
- **Post editor: keyboard inline formatting** (writing-experience audit). Text
  blocks now respond to the shortcuts prose writers expect, so you never leave
  the keyboard to format:
  - **⌘/Ctrl + B** wraps the selection in `**bold**`
  - **⌘/Ctrl + I** wraps it in `*italic*`
  - **⌘/Ctrl + E** wraps it in `` `inline code` ``
  - **⌘/Ctrl + K** wraps it as a `[link](url)` and leaves the `url` placeholder
    selected so you type the destination immediately
  With no selection the marks are dropped at the caret with the cursor placed
  between them. Each change fires the normal input path, so live word/character
  count, reading time and autosave all update exactly as when typing. Purely
  additive to the existing block shortcuts (a leading `#` for headings, `>` for
  a quote, `-` for a list, a triple backtick for code, `---` for a divider),
  which are unchanged.

## [3.11.15] — 2026-07-10

### Added
- **Editor: typewriter scrolling in focus mode** — the biggest "feel" upgrade
  to the writing experience. In distraction-free focus mode (toggle with the
  focus button or Cmd/Ctrl+`.`), the line you are typing now floats at the
  vertical middle of the canvas instead of drifting toward the bottom edge, so
  your eyes stay put while you write (the iA Writer / Ghost "zen" feel). It
  acts only on a collapsed caret, so dragging a text selection is never fought,
  and only inside the editor canvas, so the command menu and modals are
  untouched. Pure scroll math coalesced into a single animation frame, so even
  the fastest typing never thrashes layout; scrolling is intentionally instant
  (smooth-scroll would make caret tracking feel floaty). Focus mode gains
  generous top/bottom room so the first and last lines can also reach centre.
- **Editor: active-line spotlight in focus mode** — companion to typewriter
  scrolling. Once the caret is in a block, that line glows at full opacity and
  the surrounding blocks gently recede (to ~32%), so the eye rests on what you
  are writing without losing context. The dimming is gated on an
  `is-spotlight` state set only when the caret is actually in a block, so
  editing the title (or focus mode before any click) never greys the canvas.
  This also activates a CSS `transition: opacity` rule that was previously
  defined but never triggered — dead styling, now live.
- **Editor: zen writer is mobile-correct.** Typewriter centring only runs when
  the canvas is its own scroll container; on phones the page scrolls instead,
  so the caret-centring is a clean no-op (mobile browsers already keep the
  caret above the on-screen keyboard) and the desktop-only 40vh focus padding
  is reduced to normal spacing on small screens so there is no dead space.

## [3.11.14] — 2026-07-09

### Fixed
- **CI: markdown-lint gate was failing on `CHANGELOG.md`** — the real reason
  the last few pushes showed a red ✗ (the `go-native`/deadcode gate itself was
  already green). A wrapped prose line in the v3.11.12 entry began with a
  literal `+`, which markdownlint reads as a plus-style list bullet (MD004,
  which requires dash bullets). Reworded so no line starts with `+`. This gate
  is not part of `go build`/`go test`, which is why local verification kept
  passing while CI failed; markdownlint is now run locally before each release.

## [3.11.13] — 2026-07-09

### Security
- **Reflected-XSS barrier extended to the mail POST action handlers** — the
  remaining reason CodeQL #36 / #49 stayed open. v3.11.12 sanitised the GET
  readers (`mailUserParam`/`mailFolderParam`/`mailIDParam`) at the source, but
  two POST handlers — `handleVayuOSInboxAction` (bulk/row actions) and
  `handleVayuOSMessagePaneAction` (reader-pane actions) — read `user`/`folder`/
  `id` straight from the form and re-rendered them into the refreshed
  fragment / reader card (`writeOSFragment` / `writeOSHTML` sinks) with no
  sanitiser on the admin path. The pure barriers are now factored out
  (`sanitizeMailLocalPart` / `sanitizeMailFolder` / `sanitizeMailID`) and
  applied at every read — query string **and** form. They are runtime no-ops
  on valid input (the allowed charsets contain no HTML metacharacters, so
  mailbox lookups and comparisons are byte-identical), but route every value
  through `html.EscapeString`, so no tainted value can reach an HTML sink.
  Covered by new `sanitizeMail*` unit tests (valid values unchanged, hostile
  values rejected/escaped).

## [3.11.12] — 2026-07-09

### Security
- **CodeQL alerts #36 / #49 / #52(#53) — fixed at the source this time.** My
  previous attempts escaped the wrong layer (the `hx-vals` builder) while
  CodeQL traced the taint from `?user=`/`?folder=` all the way to the shared
  `writeOSHTML` / `writeOSFragment` write sinks through a *different* helper
  path. Rather than chase each path, the barrier now lives at the **source**:
  - `mailUserParam` / `mailFolderParam` / the new `mailIDParam` validate their
    strict charset and then return the value through `html.EscapeString`. On
    those charsets (no HTML metacharacters) it is a **no-op — byte-identical
    output**, so nothing about the rendered pages changes; but the value now
    flows through a sanitiser static analysis recognises, clearing
    go/reflected-xss on *every* downstream sink at once instead of one at a
    time. `?id=` is now validated + escaped the same way.
  - `admin-os-mail.js` `safeNav`: the earlier version had a real weakness — its
    *fallback* was itself a tainted `data-*` attribute value, so an invalid
    input still reached `location`. It now routes both the candidate and the
    fallback through a `localPath()` guard (a regexp test recognised as a
    local-URL sanitiser: must start with a single `/`, never `//` or `/\`,
    which also excludes any `scheme:` URL) and falls back to a **hardcoded
    constant** — so a `data-*` value can never reach `location` unvalidated.

## [3.11.11] — 2026-07-09

### Fixed
- **CI green again — the Governance Constitution `go-native` gate was failing.**
  The `deadcode` gate flagged `calibrate.BiasedForTest` (a cross-package
  test-suite helper added in v3.11.6) as unreachable from `main`. It is a
  legitimate test seam, like the already-baselined `challenge.Solve`, so it is
  now recorded in `scripts/deadcode-allow.txt`. This is why the last few
  releases showed a red ✗ on the Governance check even though they merged.

### Security
- **The three open CodeQL alerts are now actually fixed** (the v3.11.9 attempts
  used custom escapers/guards CodeQL does not model, so the alerts persisted).
  Each now uses a barrier static analysis recognises:
  - **Reflected XSS #36 / #49** (`admin_os_ui.go`, `vayushield_integration.go`
    write sinks) — the real path was the `hx-vals` builder, which used a bespoke
    `jsonAttrEscape` replacer. `hxVals` now JSON-escapes each key/value and runs
    it through `html.EscapeString`. Because the mail `?user=`/`?folder=` params
    are already charset-restricted and the other values are literals,
    `html.EscapeString` is a no-op on real input — the emitted attribute is
    byte-for-byte identical — but the tainted value now provably passes through
    a recognised sanitiser.
  - **DOM-text-as-HTML #52** (`admin-os-mail.js`) — `safeNav` replaced its
    `charAt` character checks (which CodeQL cannot follow) with the
    `new URL()` same-origin-check pattern: it navigates only to a destination
    **reconstructed** from the parsed URL's own `pathname`/`search`/`hash`,
    never the raw attribute text, closing any `javascript:` / cross-origin /
    protocol-relative redirect.
  - Covered by new tests: `hxVals` leaves safe values untouched and escapes
    hostile ones; the existing HTMX button tests confirm the byte-identical
    output.

## [3.11.10] — 2026-07-09

### Fixed
- **Deep Bot Shield audit — five real bugs found and fixed.** A full pass over
  the whole VayuShield engine for correctness, hot-path cost and the "never
  slow VayuOS/the blog, never block a real user" invariants.
  - **Signature lookup no longer contends on the SQLite writer (major).** The
    per-request classification lookup (`botdb.Lookup`) ran a `SELECT` on the
    single *writer* connection — and the fingerprint hash is always populated,
    so with bot protection enabled *every* unverified public request (and every
    analytics beacon) serialised a read on the one writer, contending with all
    writes and with itself. It now runs on the dedicated public read pool;
    writes stay on the writer. This is the biggest throughput fix in the set.
  - **Save / Tier / Verify buttons no longer silently 403 after a deploy.** The
    Bot Shield panel's page and its 10-second auto-poll did not refresh the
    `vp_csrf` cookie, so after a restart (CSRF secret rotates every deploy) or
    the 1-hour cookie lifetime, an open panel held a stale token and every POST
    was rejected until a hard reload — the "Save does nothing" report. The page
    and the poll route now re-issue the token (via `CSRFTokenMiddleware`), so it
    self-heals within 10 seconds with no reload.
  - **Jail is no longer an amplification vector.** The redeemable-challenge path
    added in v3.11.9 issued a proof-of-work + rendered the interstitial +
    recorded telemetry for *every* request from a jailed IP — far more
    expensive than the O(1) rejection it replaced, and a jailed bot hammers.
    The solvable challenge is now offered at most ~once per 30 s per IP (a
    dedicated redeem budget); every other request gets the cheap flat 429. A
    real user behind a jailed/shared IP still redeems within seconds.
  - **Bot protection no longer silently disabled for look-alike URLs.** The
    bypass check used a raw prefix match, so a *public* post at
    `/oslo-travel-guide` or `/static-site-generators` matched the `/os` /
    `/static` bypass prefixes and skipped classification entirely. Matching is
    now path-boundary-aware (`/os` matches `/os` and `/os/…`, never `/oslo…`).
  - **Operator-immunity check taken off the DB hot path.** The trusted-operator
    session lookup ran a SQLite read for every cookie-bearing request. It is now
    served from a 30-second TTL cache (bounded, sharded), so a logged-in
    operator browsing many pages — or an attacker spamming a garbage session
    cookie — costs at most one read per token per window, on the isolated read
    pool. Cookieless bot traffic still does zero work.

## [3.11.9] — 2026-07-09

### Fixed
- **Operator lock-out & dead Save/refresh under attack** — the critical
  availability regression from the Aegis rollout. When an operator load-tested
  their own site, their own IP got jailed, and every public page plus every
  `/static` asset then returned `429`/`503` — so the admin panel's JavaScript
  never loaded and Save/refresh silently did nothing. Root cause was three
  gaps working together; all three are now closed:
  - **Operator immunity (structural).** A valid admin login session is now
    exempt from *every* gate on *every* path — blocklist, reputation jail,
    load-shed, rate-limit, fair-shed and the challenge ladder — via a new
    `TrustedFn`. The operator can never be locked out of their own site, not
    even after jailing their own IP. Their IP is additionally marked
    *protected* in the L1 kernel exporter (a kernel drop is the one gate
    app-level immunity can't override), so it is never exported to nftables/XDP
    and any pending ban is withdrawn.
  - **Static assets exempt.** `/static` joined the bypass list: a challenge or
    `429` served in place of a stylesheet or `htmx.js` breaks the very page
    that loads it (including the admin console). Assets are cheap and their
    volumetric abuse is still bounded by the L0 lane.
  - **Jail is now redeemable, not a dead end.** A jailed source (blocklist or
    reputation) is served the *silent proof-of-work interstitial* instead of a
    flat `429` (except during an active flood, where the cheap rejection still
    applies). Solving it pardons the reputation sentence **and** lifts the
    blocklist jail **and** withdraws the kernel ban — so a false positive,
    including a shared/NAT IP, self-heals in one page load with no human
    interaction.
- **L0 public-concurrency cap right-sized.** The sovereignty lane's default
  ceiling dropped from 64/core (floor 128) to **16/core (floor 32)**. Public
  requests are the expensive kind (classification + render + SQLite); 128+
  concurrent renders starved the scheduler badly enough that even priority
  admin requests crawled. 32 concurrent renders is ample legitimate capacity
  while leaving real CPU headroom under a flood.

### Security
- **Reflected-XSS hardening (CodeQL #36, #49).** VayuMail's `?user=` and
  `?folder=` parameters are now strictly validated at read time —
  mailbox-local-part and folder-name character whitelists, rejecting anything
  that could carry an HTML/JS metacharacter — giving static analysis a clean
  sanitiser barrier in addition to the existing per-use escaping.
- **DOM-navigation hardening (CodeQL #50, #51).** The VayuMail message-action
  script routes every `location.href` navigation through a new `safeNav()`
  guard that permits only same-origin absolute paths, closing any DOM-based
  open-redirect / `javascript:`-URL vector even though the server-built
  targets were already URL-encoded.

## [3.11.8] — 2026-07-09

### Added
- **VayuShield 2.0 "Aegis" — live console** (step 6, completing the
  Cloudflare-free bot-shield rebuild). The Bot Shield panel now shows the
  whole defense pipeline as it runs.
  - **Aegis layer map:** a new always-visible card renders one chip per layer
    in the order a request actually traverses them — **L0 Sovereignty lane**
    (public in-flight / cap, shed), **L1 Kernel offload** (agent state,
    in-kernel ban count), **L2 Fair-shed pre-filter** (window request rate,
    shed), **L4 Silent challenges** (served/passed, current loosen bias),
    **L5 Reputation brain** (suspects, jailed, pardons) — each with a state
    accent (idle / live / tuned / hot). Refreshes in place every 10 s and
    after every save; every read is an in-memory counter, so polling is free.
  - **Save feedback:** saving settings now flashes an ephemeral "✓ Settings
    applied" toast (CSP-nonce'd, no framework). The Save button itself rides
    the L0 sovereignty lane, so it keeps working during a flood — the exact
    failure this rebuild set out to kill.
  - Pure HTMX + lightweight CSS additions (`vs-aegis`, `vs-layer`,
    `vs-toast`); no new JS dependencies, no page reloads anywhere.

## [3.11.7] — 2026-07-09

### Added
- **VayuShield 2.0 "Aegis" — L1 live kernel offload (nftables + XDP)** (step 5
  of the Cloudflare-free bot-shield rebuild). The shield's own live verdicts —
  confirmed bad actors and L5 reputation sentences — are now pushed down into
  the Linux kernel, so a banned attacker's packets are dropped at line rate
  **before a TCP connection, TLS handshake, goroutine or byte of userspace
  work exists for them**.
  - **Privilege separation preserved (ADR-0123):** the unprivileged app never
    touches the firewall. `internal/vayushield/offload` maintains a plain-text
    ban file in the app-owned control dir (atomic tmp+rename writes, debounced
    to one write per 2 s, expired entries pruned, hard-capped at 10k with the
    most persistent offenders kept). The root reconcile agent revalidates
    every line against a strict character whitelist — app-written content can
    never become command syntax — and reconciles a dedicated
    `vayushield_dyn` nftables table with **timeout sets** (bans expire in
    kernel on their own, capped at 24 h) plus an `xdp-filter` mirror where the
    XDP tooling is installed.
  - **Pure acceleration, never a dependency:** with no agent installed the
    file is simply never consumed and the in-binary gates (L0/L2/L5 +
    blocklist) keep enforcing exactly as before. Dynamic offload follows the
    operator's Tier 2 switch — no Tier 2, no kernel changes — and turning
    Tier 2 off removes the dynamic table entirely.
  - **Panel:** the Network-hardening section now shows a read-only "L1 · Live
    kernel offload" row with live state and the in-kernel ban count.
  - Covered by exporter tests: canonicalization + strict-input rejection,
    expiry pruning, longest-sentence-wins extension, cap retention of the
    most persistent offenders, and concurrent race tests; agent script
    syntax-validated.

## [3.11.6] — 2026-07-09

### Added
- **VayuShield 2.0 "Aegis" — L4 self-calibrating, silent-first challenges**
  (step 4 of the Cloudflare-free bot-shield rebuild). The challenge ladder now
  tunes itself from observed outcomes — no operator input — and scales its
  difficulty to each source's reputation.
  - **The feedback signal:** a solved proof-of-work is overwhelming evidence
    of a real browser. `internal/vayushield/calibrate` watches the pass rate
    of issued challenges in 10-minute windows: ≥ 90% solved means the
    thresholds are biting humans → a bias raises the effective PoW/JS
    thresholds so borderline visitors are allowed instead of challenged;
    ≤ 50% solved means challenges are absorbing bots → the slack is walked
    back. Quiet windows drift the bias to zero so stale looseness never
    lingers.
  - **Loosen-only safety invariant:** the bias is clamped to [0, +0.2] — the
    calibrator can only make the shield MORE permissive than the operator's
    settings, never stricter, so it can never be the reason a real user gets
    challenged. It also never touches the block threshold — self-heal must
    not open the door to confirmed bots. Automatic tightening remains
    exclusively the under-attack controller's job (which relaxes the moment a
    flood subsides).
  - **Reputation-scaled difficulty (L5→L4):** an unknown client gets the
    light, silent PoW; a source already under L5 suspicion (standing < 0.3)
    works the hard variant. Real users never notice; suspect automation pays
    an escalating compute price.
  - **SEO/real-user guarantees unchanged and structural:** search engines,
    AI assistants, verified sessions and human-classified clients are allowed
    before any threshold is consulted; challenges themselves remain
    silent-first (invisible PoW before any interactive interstitial).
  - `calibration_bias`, `challenges_served`, `challenges_passed` in the
    status JSON. Covered by loosen/restore/clamp/drift/min-sample window
    tests, race tests, and integration tests (bias allows a borderline human;
    block threshold immune to bias; serve/verify feed the calibrator).

## [3.11.5] — 2026-07-09

### Added
- **VayuShield 2.0 "Aegis" — L5 Brain v1: continuous online reputation**
  (step 3 of the Cloudflare-free bot-shield rebuild). Where the fingerprint
  learning database learns *what a bot looks like* over days, the brain learns
  *who is currently misbehaving* within minutes — and forgives them just as
  automatically. Fully autonomous: no cron, no operator commands, no settings.
  - **Online scoring:** every enforcement event lowers a source's standing
    immediately (hard block/tarpit −0.25, rate-limit breach −0.10) and every
    positive proof raises it (solved challenge +0.40, sustained human-scored
    browsing +0.02). Reputation decays toward neutral with a ~1 h half-life,
    computed lazily on access — no sweeper goroutine.
  - **Auto-jail with escalating sentences:** a collapsed standing jails the
    source — first offense 5 minutes, doubling per re-offense, capped at 6 h.
    A persistent attacker converges to cheap O(1) rejections at the first
    middleware gate (reputation collapse also escalates straight into the
    blocklist jail when auto-block is on — 4× faster than the legacy
    20-breach violation meter); a one-off mistake costs almost nothing.
  - **Rehabilitation & false-positive self-heal:** release restores a workable
    standing; a solved challenge is an **instant pardon** — so the real user
    behind a shared/NAT IP that hosted one bad actor is never locked out, and
    verified sessions always bypass the jail entirely.
  - **Suspicion-only memory (privacy + bounded):** entries are created ONLY by
    negative events — well-behaved visitors are never tracked at all. Sharded
    and hard-capped (≤ ~256k suspects), evicting the most irrelevant entries
    first, so memory stays bounded under any flood.
  - New hero metric **Rep-jailed / suspects (L5)** and `suspects`,
    `rep_jailed`, `pardons` in the status JSON. Covered by escalation, decay,
    pardon, sentence-cap, bounded-memory and race tests, plus middleware
    integration tests (flooding source ends reputation-jailed; verified
    session and challenge-solvers always pass).

## [3.11.4] — 2026-07-09

### Added
- **VayuShield 2.0 "Aegis" — L2 probabilistic fair-shed pre-filter** (step 2
  of the Cloudflare-free bot-shield rebuild). Identifies and fair-sheds heavy
  hitters during an attack in **fixed memory** — before the per-IP limiter
  map, classification, rendering or SQLite are touched.
  - **How it works:** `internal/vayushield/prefilter` keeps a windowed
    Count-Min Sketch — 256 KiB of atomic counters, memory that never grows no
    matter how many attacking IPs there are (the answer to spoofed/botnet
    floods that would thrash any per-IP map). Every public request is a few
    lock-free atomic increments; estimates cover a ~10–20 s sliding window so
    a burst is forgotten moments after it stops.
  - **Fairness (never harms real users):** every client gets a fair per-window
    budget derived from the operator's rate setting (default ≈ 3× the
    sustained per-IP rate). At or under budget the shed probability is exactly
    **0** — a reader, a search-engine crawler or an AI assistant can never be
    shed here. Above budget, shedding ramps as `1 − budget/estimate`, so the
    heaviest sources shed the most of their own traffic; it is capped at 98%
    so even the worst offender still trickles through to the classifier
    (false positives self-heal instead of hard-failing).
  - **Subnet aggregation:** both the IP and its /24 (IPv6 /48) group are
    tracked, so a botnet spreading load across one subnet is caught at the
    group level even when each address stays under the per-IP budget.
  - **Self-defense floor, zero configuration:** the pre-filter sheds only
    under genuine pressure — the under-attack meter tripping, **or the new
    L0→L2 pressure link**: when the Aegis sovereignty lane passes 75%
    occupancy, fair-shedding starts automatically (even with every operator
    toggle off) and eases the lane back down before it saturates and must
    shed blindly. Heavy hitters are shed *fairly* first; the L0 hard cap
    remains the last-resort backstop.
  - New Bot Shield hero metric **Fair-shed (L2)** and `fair_shed` in the
    status JSON. Covered by sketch accuracy/decay/rotation tests, fairness and
    subnet-flood tests, race tests, and middleware integration tests
    (heavy hitter sheds under pressure, light client and verified sessions
    never shed).

## [3.11.3] — 2026-07-09

### Added
- **VayuShield 2.0 "Aegis" — L0 Admin Sovereignty Lane** (first step of the
  built-in, Cloudflare-free bot-shield rebuild). A lock-free admission
  controller mounted *before* every other middleware that guarantees the admin
  control plane and verified readers always keep CPU/goroutine headroom during
  a volumetric flood.
  - **The bug it fixes:** during a big bot hit the Save button and page refresh
    would hang even though `/os` is bot-exempt. Root cause was resource
    starvation, not blocking — unbounded concurrent public requests exhausted
    the Go scheduler and CPU, so even bypassed admin requests couldn't get a
    time slice. Isolating the admin DB read pool (ARDB) was not enough; the
    process still needs CPU to run the handler.
  - **How it works:** `internal/vayushield/sovereign` caps how many PUBLIC
    requests may be in flight at once (CPU-derived default: 64 per core, floored
    at 128) and sheds the overflow with a cheap `503 + Retry-After` **before**
    any classification, rendering or SQLite work. Admin/API/health paths and any
    visitor holding a verified signed session are the "sovereign lane": always
    admitted, never counted against the public budget. The hot path is a couple
    of atomic ops — zero allocation, bounded memory regardless of attacker count,
    no measurable latency for normal traffic.
  - **Never harms real users or SEO:** the overflow shed is a `503` (retry), not
    a `4xx` block, so crawlers back off politely; verified/logged-in readers and
    the whole admin console are structurally exempt.
  - **Live-tunable** via `VAYUSHIELD_MAX_PUBLIC` (0/unset = auto CPU-derived
    cap); no restart, no operator commands required. New hero metrics on the Bot
    Shield panel show the public lane occupancy (`in-flight / cap`) and the
    cumulative `Shed (L0)` count so the operator can see the lane holding the
    line through a flood.

## [3.11.2] — 2026-07-09

### Added
- **VayuMail conversation threading + snooze** (Phase 3, final part — the
  VayuMail redesign roadmap is complete).
  - **Threading** — messages sharing a subject (Re:/Fwd:/Fw: prefixes stripped,
    any depth, case-insensitive) group into one conversation in every folder
    list: the newest message is the visible row with a **count badge**; tapping
    the badge expands/collapses the older messages in place (delegated JS, so
    it survives HTMX swaps). Single messages are untouched, and grouping never
    changes what actions do — every row is still individually actionable.
  - **Snooze** — from the reading pane, snooze a received message until
    **Tomorrow 8:00** or **Next week (Mon 8:00)**. The message physically moves
    to a new **Snoozed** folder (visible in the panel and to IMAP clients) and
    a background sweeper returns it to its original folder at wake time, where
    it **resurfaces as unread** — Gmail's model. Guard rails: Sent/Drafts and
    Snoozed itself can't be snoozed; a failed wake-row write rolls the move
    back rather than strand mail asleep; a message moved out of Snoozed by hand
    leaves only a stale row that the sweeper discards (it can never resurrect a
    deleted message); manual move menus and filter rules deliberately exclude
    the Snoozed folder, so nothing can be put to sleep without a wake time.
    Backed by a `vayumail_snooze` wake table (idempotent).

## [3.11.1] — 2026-07-09

### Added
- **VayuMail server-side filter rules** (Phase 3, part 3). Per-mailbox delivery
  rules applied to every inbound message at the server — *when From/To-Cc/Subject
  contains X, then move to folder / mark as read / pin*. Evaluation is
  **first-match-wins** in creation order (one predictable action per message);
  matching is case-insensitive and confined to the header block, so body text
  can't trigger a rule. Rules filing into **Junk or Trash suppress auto-forward
  and the autoresponder** (same discipline as the junk filter); moves to other
  folders keep both working. Managed from a new **Filter rules** card on the
  Accounts page (per-mailbox, HTMX in-place add/delete, audit-logged; capped at
  50 rules per mailbox). Best-effort by design — a rules-storage problem can
  never fail delivery. Backed by a new `vayumail_filters` table (idempotent).

## [3.11.0] — 2026-07-09

### Added
- **VayuMail vacation autoresponder** (Phase 3, part 2). Per-mailbox
  out-of-office replies, managed from a new **Vacation autoresponder** card on
  the Accounts page (subject, message, optional first/last day — the window
  opens at the start of the first day and closes at the end of the last).
  Implemented to **RFC 3834** so it can never become a mail-loop generator:
  - each correspondent gets **one reply per week** (persistent dedupe log);
  - **never answers** auto-generated or suppressed mail (`Auto-Submitted`,
    `X-Auto-Response-Suppress`), bulk/list/junk `Precedence`, mailing-list
    mail (`List-Id`/`List-Unsubscribe`/`List-Post`), bounces and machine
    senders (mailer-daemon, postmaster, no-reply, bounce), the mailbox
    itself, or copies relayed by the auto-forwarder;
  - our replies are tagged `Auto-Submitted: auto-replied` +
    `X-Auto-Response-Suppress: All` and threaded via `In-Reply-To`, so two
    autoresponders can never converse — and ours refuses to answer such mail;
  - replies honour `Reply-To`, are DKIM-signed, header-injection-safe, and
    sent through the outbound queue **best-effort** (a responder problem never
    affects delivery). Junk-filtered mail is never answered.
  Changes are audit-logged. Backed by idempotent account-column migrations and
  a `vayumail_autoreply_log` dedupe table.

## [3.10.0] — 2026-07-09

### Added
- **VayuMail aliases & auto-forwarding** (Phase 3, part 1). Two new
  enterprise mail capabilities, managed from a new **Aliases & forwarding**
  card on the Accounts page (HTMX — every add/delete/save swaps in place):
  - **Aliases** — extra receive-only addresses (e.g. `sales@domain`) that
    deliver straight into an existing mailbox. Accepted at SMTP RCPT time and
    resolved at delivery. Single-level by construction: an alias must target a
    real mailbox (never another alias) and cannot shadow an existing address,
    so resolution can never chase chains or loops. Deleting an alias makes the
    address bounce again.
  - **Auto-forwarding** — a per-mailbox forward address: inbound mail is filed
    locally as normal AND a copy is relayed through the outbound queue.
    **Loop-protected**: each forwarded copy is tagged with an
    `X-VayuMail-Forwarded` header and tagged mail is never forwarded again, so
    two servers forwarding at each other can't bounce a message forever
    (header detection is confined to the header block, so body text can't
    spoof it). Junk-filtered mail is never forwarded, forwarding is
    best-effort (a relay problem never fails local delivery), and
    self-forwarding is rejected.
  Alias and forward changes are audit-logged. Backed by a new
  `vayumail_aliases` table and a `forward_to` account column (idempotent
  migrations; existing installs are unaffected until the features are used).

## [3.9.26] — 2026-07-09

### Added
- **VayuMail split reading pane** (completes the Phase-2 redesign). The Mailbox
  now shows the message list and an in-place reader side by side: clicking a
  message opens it in the right-hand pane instead of navigating to a separate
  page. Reader actions (Reply/Forward, Mark unread, Pin, Junk/Trash/Restore,
  Delete) and prev/next run **entirely in the pane via HTMX** — a move or delete
  clears the pane and refreshes the list in place (`HX-Trigger:vm-mail-changed`),
  with no full-page reload. The pane reader is pure HTMX/native (a `<details>`
  raw-source toggle, HTMX nav) so it works after any swap without page-load JS.
  A delegated highlighter marks the open row. On narrow screens the layout
  collapses to one column and the reader overlays the list with a Close button.
  The standalone message page is unchanged (middle-click / no-JS fallback via the
  row's `href`). No backend change.

## [3.9.25] — 2026-07-08

### Added
- **Undo Send.** Clicking Send now holds the message for a few seconds behind an
  **Undo** control with a live countdown, so a mistaken send can be called back
  before it goes out. If you navigate away during the hold, the message is sent
  anyway (best-effort) so nothing is lost, and a guard prevents double-sending.

## [3.9.24] — 2026-07-08

### Added
- **Recipient autocomplete in the composer.** The To, Cc and Bcc fields now
  suggest addresses as you type — drawn from the mail-account directory and,
  for administrators, recent sent-history recipients. Suggestions use a native
  browser list (no extra scripts), and picking one drops it straight into a
  recipient chip. Non-administrators only see the internal address directory,
  never the organisation's outbound history.

## [3.9.23] — 2026-07-08

Phase 2 of the VayuMail redesign begins: **per-account email signatures**.

### Added
- **Signatures.** Each mail account can have a plain-text signature that is
  appended to messages you send from that address. The composer previews it,
  swaps it live when you change the sender, and offers a per-message **Append
  signature** toggle.
- **Self-service editing.** Edit your own signature directly from the composer;
  administrators can edit any account's signature. It is stored per account
  (`vayumail_accounts.signature`, migrated in idempotently).

### Changed
- On send, the signature is inserted **after your reply and before any quoted
  history** (using the standard RFC 3676 dash-dash-space delimiter) — never
  baked into drafts, so it can't double up.

See [ADR-0125](docs/adr/ADR-0125-vayumail-per-account-signatures.md).

## [3.9.22] — 2026-07-08

Final Phase 1 screen of the VayuMail redesign: **keyboard shortcuts** and a
help overlay bring power-user speed to the whole mail experience, and the list
gets a keyboard cursor.

### Added
- **Keyboard shortcuts** across VayuMail — `c` compose, `/` search, `j`/`k` move
  the list cursor (or next/previous in the reader), `Enter`/`o` open, `x` select,
  `u` toggle read, `s` pin, `e` archive, `!` junk, `#` delete, and `r`/`f` reply/
  forward in the reader. Shortcuts are ignored while typing in a field.
- **Help overlay.** Press `?` to see all shortcuts in a lightweight overlay
  (native popover where supported, a positioned fallback otherwise).
- A **keyboard cursor** highlights the focused message row, and rows now have a
  hover state.

## [3.9.21] — 2026-07-08

Fourth screen of the VayuMail redesign: **mail search** gains a real filter bar
and instant, as-you-type results.

### Added
- **Filter bar** — refine a search by folder, sender (“From contains”), date
  range (after / before) and unread-only, all combinable.
- **Instant results.** Results update as you type or change a filter
  (debounced) and swap in place over HTMX — no page reload or extra click.
- **Match highlighting.** Search terms are highlighted in the sender and
  subject of each result.
- Results now show sender avatars, names and relative times, with a live
  result count.

## [3.9.20] — 2026-07-08

Third screen of the VayuMail redesign: the **composer** becomes a modern,
forgiving writing surface with recipient chips, a drag-and-drop attachment tray
and automatic draft saving.

### Added
- **Recipient chips.** Type an address and press Enter (or comma) to turn it
  into a chip; invalid addresses are flagged, chips are removable, and Backspace
  deletes the last one. Applies to To, Cc and Bcc.
- **Drag-and-drop attachments.** Drop files onto the composer (or Browse); each
  shows as a chip with its size and a one-click remove — including the ability
  to remove a single file before sending (a plain file input can't do that).
- **Autosave drafts.** The message is saved to Drafts automatically as you
  write and replaces the previous autosave, so Drafts never fills with copies;
  sending clears the autosaved draft.
- **Encryption indicator.** A live hint shows whether the message is PGP-
  eligible (a single recipient, no Cc/Bcc, no attachments) or will be sent
  DKIM-signed.

### Changed
- The Send button now shows a sending state and reports success or failure with
  a toast.

### Fixed
- Removed an inline `style` attribute on the composer that the strict admin
  Content-Security-Policy blocked, so the Cc/Bcc row now spaces correctly.

## [3.9.19] — 2026-07-08

Second screen of the VayuMail redesign: the **message reader** is rebuilt into a
clean, modern reading view with real attachment downloads, quote folding and
keyboard-free next/previous navigation.

### Added
- **Attachment downloads.** Messages now list their attachments (name, type and
  size) as chips; clicking one streams a safe, forced download. Attachments are
  PGP-decrypted first, so encrypted mail's files download in the clear. New
  endpoint `GET /os/vayumail/attachment`.
- **Previous / next navigation** between messages in the folder, without
  returning to the list.
- **Collapsible quoted text.** Reply history is folded behind a “Show quoted
  text” toggle so the new content is what you read first.
- **PGP badge** on messages that still carry PGP armor (encrypted or signed).
- **Print** button for a clean printout of the message.

### Changed
- **Redesigned reader.** A sender avatar and tidy header card (From, To, Cc,
  Date), an icon action toolbar (Reply, Forward, Mark unread, Pin, Junk, Trash,
  Move, Delete), and a readable body column.
- **Smoother actions.** Move, Junk, Trash and Delete now show a toast and
  advance to the next message (or back to the folder) instead of always jumping
  back; Pin toggles in place; Mark unread returns to the folder.

## [3.9.18] — 2026-07-08

First screen of the VayuMail world-class redesign: the **Mailbox (Inbox)** is now
fully seamless. Every action swaps in place over HTMX — no more full-page
reloads — and the list gains sender avatars, names, relative times and live
per-folder unread badges.

### Changed
- **Seamless mailbox actions (HTMX).** Mark read/unread, pin, move and delete —
  per-row and in bulk — now update the list in place instead of reloading the
  whole page, matching the Outbox. Folder switching is also an in-place swap.
- **Redesigned message rows.** Each row shows a colored initials avatar, the
  sender's display name, a compact relative time (`2h`, `3d`) with the full
  timestamp on hover, and unread mail is bolded with an accent dot.
- **Live new mail.** The open mailbox refreshes itself on a background poll, so
  newly arrived mail appears without a manual reload.
- **Sticky toolbar** with the mailbox identity, a one-click **Compose** button
  and search that stays in view while scrolling.

### Added
- **Per-folder unread badges** on the folder tabs (Inbox, Junk) and on the
  all-mailboxes list.

### Fixed
- Bulk selection now survives the in-place refresh (event-delegated), so the
  selection toolbar keeps working after every swap.

## [3.9.17] — 2026-07-08

Enterprise polish for the VayuMail **Accounts** tab: account actions now give
non‑blocking feedback and update the table in place instead of firing browser
`alert()` dialogs and hard page reloads.

### Changed
- **Account actions use toast notifications, not `alert()`.** Create, role
  change, quota, password, enable/disable and 2FA actions now report success and
  failure through the VayuOS shell toast (`vpToast`), falling back to `alert()`
  only if the toast helper is unavailable.
- **Delete removes the row in place.** Deleting a mailbox now drops its table
  row immediately after a successful call instead of reloading the whole page.
- **Role change reverts on failure.** If a role update is rejected, the role
  select snaps back to its previous value so the UI never shows a change the
  server did not accept.

### Fixed
- **Stale `admin-os-mail.js` after upgrades.** All four VayuMail script tags
  (Inbox, Message, Compose, Accounts) now carry a content‑hash cache‑buster, so
  browsers pick up the latest account‑management script immediately after a
  release instead of serving a cached copy.

## [3.9.16] — 2026-07-08

Simplifies the **Connect** tab: the device‑setup‑QR system is removed (it was the
source of a broken "Rotate" button), the tab now recommends the official
**VayuMail mobile app**, and manual setup + auto‑config are the supported path.

### Removed
- **Device‑setup‑QR system (and the broken Rotate button).** The rotating
  app‑password QR and the "scan‑to‑import account" QR are gone. Their **Rotate**
  button POSTed a server‑rendered form whose embedded CSRF token could diverge
  from the live cookie (the page's CSRF middleware rotated it), so clicking it
  navigated to a raw `{"error":"csrf_invalid"}` page and never rotated the QR.
  Manual server settings and auto‑config cover setup, so the whole QR flow —
  including the `POST /os/vayumail/connect/qr` endpoint and the app‑password
  minting it performed — was removed rather than patched.

### Changed
- **Connect tab redesigned around the VayuMail mobile app.** The third‑party
  "Recommended mail apps (K‑9 / Thunderbird)" card is replaced by an **official
  VayuMail Mobile** card (download + source links). "Instant setup" now points at
  both the Mozilla autoconfig XML and the `/.well-known/vayumail/autoconfig.json`
  the app reads, and notes any standard client (Apple Mail, Gmail app, Outlook,
  Thunderbird) also works with the manual settings. Service status, the TLS‑trust
  guidance, Recommended settings, and Per‑mailbox setup are unchanged.

### Upgrade Notes
- Mail apps connect with the **mailbox's own password** (shown under Accounts) —
  the per‑device app‑password QR is no longer issued. If you had enabled the
  opt‑in `VAYUMAIL_2FA_ENFORCE`, note that device app passwords are no longer
  minted here; connect with the mailbox password (2FA still protects the web
  console and interactive sign‑in).

## [3.9.15] — 2026-07-08

Applies the pending dependency update surfaced on the Security tab, and fixes the
misleading "Latest" column that could show a version *older* than the one built.

### Security
- **Updated `go-chi/chi` v5.3.0 → v5.3.1.** This is the dependency the
  Security-updates panel flagged as "update available"; it now ships in the
  binary, so a one-click update applies it.

### Fixed
- **Security tab "Latest" column is now accurate.** The watcher read GitHub's
  `releases/latest`, which lags for dependencies that tag versions without
  cutting a GitHub Release — so it displayed a "Latest" *older* than "Current"
  (e.g. `go-sqlite3` latest `v1.14.16` vs current `v1.14.47`, `bluemonday`
  `v1.0.26` vs `v1.0.27`). It now reads the **highest semantic-version tag** (the
  real source of truth for Go modules), so "Latest" is always ≥ "Current" and the
  up-to-date / update-available status is correct. Falls back to the release API
  when a repo publishes no version tags.

## [3.9.14] — 2026-07-08

First step of the VayuMail enterprise overhaul: the Outbox becomes a real
delivery cockpit (resend, retry-until-sent, live HTMX status), and dependency
security patches are one click to apply. See ADR-0124.

### Added
- **Outbox: one-click Resend + Retry-all-failed.** The Outbox now shows each
  message's delivery **status, try count and last error**, with a **Resend**
  button per failed/pending message and a **Retry all failed** action. A Resend
  requeues the message and triggers an immediate delivery attempt (it no longer
  waits for the 30s worker tick). Backed by new durable-queue operations
  (`Requeue`, `RequeueAllFailed`, `Delete`) — a message that exhausted its
  retries during a transient outage can be sent again with one click.
- **The Outbox is now live (HTMX).** The table auto-refreshes every 15s and
  re-renders in place after any action — no full-page reload — reusing the same
  lightweight, CSP-safe HTMX pattern as the Bot Shield console.
- **One-click dependency update on the Security tab.** Because VayuPress is a
  single statically-linked binary (dependencies compiled in), security patches to
  the PGP/crypto libraries ship *inside a signed release* — the Security-updates
  panel now has an **Update VayuPress now** button that runs the verified
  one-click self-updater (SHA-256 + Ed25519, atomic swap, auto-rollback) to apply
  them. It also makes clear there is no separate `go get`/rebuild step to run on
  the server.

### Changed
- **"Keep trying until it sends" auto-retry is more generous.** The outbound
  queue's default max attempts rose from 12 to **20**; with the existing
  exponential backoff (2m → … → capped at 6h) a transient delivery problem is
  retried automatically for **~4+ days** before the message is marked failed and
  offered for one-click Resend — so a brief recipient/DNS/greylisting outage no
  longer needs any manual action.

## [3.9.13] — 2026-07-08

Makes Tier 2 (kernel firewall) actually turn on with one click and, when it
can't, says exactly why — diagnosed from a real deployment where the toggle
errored simply because `nftables` wasn't installed and the reason was hidden.

### Fixed
- **Tier 2 now auto-installs its dependency (`nftables`).** Enabling the kernel
  firewall inherently needs `nft`; on a host without it the apply failed with a
  hidden `nft: command not found`. `vayushield-firewall.sh` now installs nftables
  automatically (best-effort across apt/dnf/yum/apk/zypper) before applying, so
  turning Tier 2 on from the panel just works. If it truly can't be installed, it
  prints the exact manual command instead of failing silently.
- **The panel now shows *why* a tier failed.** The reconcile agent captures the
  script's output and records a short reason; the Network hardening section shows
  it inline (e.g. "Error: nftables … is not installed") instead of a generic
  "check the agent log", and offers a **Retry** button.
- **Per-IP concurrent-connection rule hardened.** The firewall now validates the
  whole ruleset with `nft -c` before applying (so a bad rule is reported and
  nothing is half-applied), and the concurrent-connection cap uses a keyed meter
  (`ip saddr ct count over N`) — a portable, genuinely per-IP form — instead of a
  bare `ct count over N` that some `nft` versions reject and that wasn't per-IP.

### Added
- `vayushield-firewall.sh` and the reconcile agent now write a per-tier log
  (`<control>/tierN.log`) and a short reason (`tierN.reason`) that the panel reads.

## [3.9.12] — 2026-07-08

Makes the Tier 2/3 helper install obvious and one-command, and fixes the
confusing "installed automatically the next time you update" message (which did
not account for the **in-app one-click updater being unable to install a root
service**). Follow-up to v3.9.11 / ADR-0123.

### Added
- **`deploy/vayushield-agent.sh install|uninstall|status` subcommands.** The
  agent can now bootstrap itself from your checkout with a single root command —
  `sudo bash deploy/vayushield-agent.sh install` — instead of only via the shell
  updater. It copies the vetted scripts to `/usr/local/lib/vayushield`, installs
  the systemd unit, and starts it. `uninstall` removes the unit; `status` shows
  it. (The default, no-arg invocation still runs the daemon, so the unit is
  unchanged.)

### Changed
- **Clearer "one-time setup" panel message.** The Network hardening section now
  states plainly that the **in-app one-click updater cannot install the helper**
  (it is unprivileged by design), and shows a single copy-paste command to enable
  the in-panel switches — `cd <checkout> && git pull && sudo bash
  deploy/vayushield-agent.sh install` — with a hint for locating the checkout and
  an uninstall command. The manual per-tier fallback commands remain.

## [3.9.11] — 2026-07-08

Tier 2/3 network hardening is now a **real on/off switch inside VayuOS** — no
terminal — while VayuPress itself stays completely unprivileged. This is done
with a tiny root **reconcile agent** that is installed automatically the next
time you update; see ADR-0123 for the privilege-separation design.

### Added
- **In-panel Tier 2/3 toggles (VayuOS → Bot Shield → Network hardening).** When
  the helper agent is present, each tier shows a live status pill (Inactive /
  Applying… / Active / Error) and an on/off button; flipping it applies or
  reverts the hardening within a few seconds and the section refreshes itself in
  place (HTMX, no page reload). No terminal, no copied commands.
- **Privileged reconcile agent (`deploy/vayushield-agent.sh` +
  `vayushield-agent.service`).** A minimal root service — deliberately separate
  from the unprivileged `vayupress` service — that watches an intent flag the web
  app writes and runs **only** the fixed, vetted scripts
  (`vayushield-firewall.sh`, the nginx edge conf). It takes **no argument or
  content** from the web app (it acts purely on a flag file's presence), so there
  is no command-injection surface. It writes back a status + heartbeat the panel
  reads. It does nothing until you flip a toggle.
- **Automatic, zero-extra-terminal install.** `scripts/update-vayupress.sh` now
  installs/refreshes the agent (to `/usr/local/lib/vayushield` + a systemd unit)
  on every run — so after your next normal update the toggles are live. Set
  `VAYUSHIELD_AGENT=0` to skip. Until the agent is present, the panel clearly
  says so and still offers the copy-paste commands as a fallback.

### Security
- **The web app gains no new privilege.** VayuPress stays unprivileged
  (`ProtectSystem=strict`, no `CAP_NET_ADMIN`); its only action is creating or
  removing an empty flag file in a directory it owns. All privileged work
  (nftables, nginx reload) happens in the isolated root agent, which trusts none
  of the web app's input. A bad nginx conf is validated (`nginx -t`) and rolled
  back before reload, so a toggle can never take the site down. Fully reversible
  from the panel. See ADR-0123.

## [3.9.10] — 2026-07-08

Bot Shield console polish: the stray floating Refresh button is gone, the Refresh
control is redesigned, and the Network hardening section now explains Tier 2/3 in
plain language with copy-paste commands. See ADR-0122.

### Changed
- **Engagement analytics is now a single, tidy collapsible card.** Its four
  reports (Engagement, Traffic sources, AI-assisted discovery, Top pages) were
  previously separate cards with the section's Refresh button floating *outside*
  any card. They are now flattened into one "Engagement analytics" disclosure
  with a light rule between reports, and the Refresh control sits neatly inside
  it — consistent with every other section.
- **Redesigned the per-section Refresh control.** It is now a subtle rounded pill
  whose ↻ icon spins while the fetch is in flight (pure CSS `htmx-request`
  state), instead of a plain button — clearer feedback, cleaner look. Still HTMX,
  still no page reload.
- **Network hardening (Tier 2 & 3) is now self-explanatory.** The section spells
  out what each tier is, and — importantly — that they **improve** performance
  rather than degrade it: Tier 2 (nftables) drops floods **in the Linux kernel**
  before a packet reaches VayuPress, and Tier 3 (nginx) shapes abuse **at the
  edge**, so both *reduce* load on VayuOS and the site under attack while leaving
  legitimate visitors untouched. Each tier shows its exact command with a
  **one-click Copy** button (same-origin, nonce-gated — strict admin CSP intact).

### Notes
- **Why Tier 2/3 aren't a one-click toggle in the panel.** VayuPress runs as an
  *unprivileged* service by design and deliberately cannot touch the kernel
  firewall or reload nginx — that isolation is what stops a web-app compromise
  from escalating to root. Applying them needs root, so it stays an operator
  action (copy the command, paste once over SSH; both are idempotent and
  reversible — `… remove` for the firewall, delete the conf + reload for nginx).
  A genuine in-panel switch is possible via a small one-time **privileged helper**
  (a root service that runs only these vetted, argument-free scripts on request);
  the section now says so, and it can be added on request. This performance-and-
  privilege rationale is recorded in ADR-0122.

## [3.9.9] — 2026-07-08

GDPR-safe visitor location across the operator console: **no raw IP is ever
stored or shown** for contact messages or comments any more. Instead, every
message, comment, and member now carries a coarse, privacy-safe location —
country (with a self-hosted flag) plus city/region where the reverse proxy
supplies it — the same no-PII approach VayuAnalytics already uses. Historical
IPs are purged on upgrade. See ADR-0121.

### Security / Privacy
- **Contact messages no longer store or display an IP address.** The Messages
  inbox previously showed the sender's raw IP on the detail view — personal data
  under GDPR. That is removed. At submit time VayuPress now resolves a coarse
  location (country offline via the embedded table; region/city only from
  trusted proxy headers) and stores **only** that — the IP is used purely
  in-process for rate limiting and is never persisted. Migration `058` **purges
  the IP from every existing contact message and comment** (data minimisation),
  and the CSV export drops the `ip` column in favour of `country,region,city`.
- **Comments store a coarse location instead of an IP.** The comments table's
  raw `ip` is replaced by `country/region/city`, captured (no IP stored) when a
  member posts.

### Added
- **Country, city and flag on Messages, Comments, and Members.** All three VayuOS
  consoles now show a GDPR-safe **Location** column/row — a self-hosted SVG flag
  with the full country name, plus the city when known (identical rendering to
  the analytics dashboard, so nothing extra is downloaded and no emoji-font gap
  on Windows). A new member's join location is captured once, at sign-in
  (magic-link or VayuMail portal), and never overwritten.
- Reusable `geoDisplayHTML(country, city)` helper (renders the flag, country and
  city, or a muted "Unknown"), shared across the three consoles.

### Notes
- **Analytics region/city was already wired** (captured from proxy headers via
  the same path as country, and rendered in the Locations card). When a region
  or city shows blank it is because the proxy isn't sending those headers:
  VayuPress does no city-level GeoIP lookup (privacy by design, and no city
  database is shipped). The dashboard's Regions/Cities empty state already names
  the exact fix — in Cloudflare enable **Rules → Settings → Add visitor location
  headers** (adds `cf-region` and `cf-ipcity`); CloudFront/Vercel/Fastly and
  generic `X-Geo-*` headers are also recognised.
- Collapsible `<details>` + per-section HTMX refresh remains best suited to the
  multi-section Bot Shield console (v3.9.8); Messages, Comments, and Members are
  single-table views, so this release focuses their improvement on the location
  data itself rather than adding disclosure panels that would not reduce clutter.

## [3.9.8] — 2026-07-08

A cleaner, calmer, and more responsive Bot Shield & Analytics console: every
section below the status hero is now a click-to-expand panel, and each section
updates **individually via HTMX** — no more reloading the whole page. Both the
collapse mechanism (native HTML `<details>/<summary>`, the same one the "Network
hardening" section already used) and the partial updates (HTMX, already loaded
on every admin page) add **no new JavaScript**, so the strict admin CSP
(`script-src 'self'`, `style-src 'self'`, ADR-0036) is untouched. See ADR-0120.

### Changed
- **Collapsible sections on the Bot Shield & Analytics page.** The header and the
  live status hero (protection state + real-time metrics) stay always visible;
  **Protection & settings**, **Bot signatures**, **Review queue**, **Recent
  blocks**, and each engagement-analytics card (Engagement, Traffic sources,
  AI-assisted discovery, Top pages) — as well as the existing **Network
  hardening** panel — are now collapsed by default and expand on click. It is a
  native `<details>` element reusing the existing `.vs-summary` styling — no
  JavaScript.
- **Each section now updates in place via HTMX — the whole page never reloads.**
  - The **live status hero** polls itself every 10 seconds (and immediately after
    a settings save), so "Visitors now", requests/sec, in-flight and blocked-IP
    counts stay current on their own.
  - **Bot signatures**, **Review queue**, **Recent blocks**, and the
    **Engagement** group each carry a small **↻ Refresh** control that reloads
    only that section's body.
  - **Saving Protection & settings** no longer forces a full-page reload
    (`HX-Refresh`); the server now fires a targeted `HX-Trigger` so only the
    status hero and the settings body refresh in place to show the applied state.
  - **Confirming or dismissing** a review-queue candidate removes its row and
    refreshes the signature counts + queue in place (via an `HX-Trigger`), so the
    "Pending review" figure stays accurate without a reload.
  - Backed by a new `GET /os/shield/section/{name}` fragment endpoint (admin-gated)
    that returns just one section's HTML; the heavy engagement analytics still
    come from the off-request background cache, so a refresh can never block into
    a 502.

## [3.9.7] — 2026-07-08

Hot-path latency calibration and tail-latency hardening. This is a deliberately
**incremental** release: profiling confirmed the request hot path was already
lean (a cached page serve does no per-request database work), so rather than
overclaim a dramatic speed-up, this trims fixed per-request overhead, closes a
slow-client denial-of-service vector that could inflate the P95/P99 tail under
attack, and adds a permanent benchmark regression guard. See ADR-0119 for the
latency budget, the measurements, and the levers deliberately **not** pulled
(and why).

### Changed
- **Leaner per-request tracing on the always-on observability middleware.** The
  root HTTP span that wraps every request previously recorded a
  `runtime.NumGoroutine()` probe (which briefly locks the Go scheduler) and
  formatted two attributes with reflection-based `fmt.Sprintf` — on *every*
  request. The goroutine probe is removed from the per-request span (live
  goroutine count is still exposed as a gauge at
  `/api/v1/admin/resource/stats`), and status formatting now uses `strconv.Itoa`.
  Measured effect on the request-ID + tracing/logging middleware:
  **~4 210 ns/op → ~4 020 ns/op (~4–5% faster)** on the benchmark box, with no
  loss of observability. Fewer per-request allocations also means marginally
  less GC pressure, which is what smooths the rare P95/P99 spikes.

### Security
- **Slow-loris / oversized-header hardening on the HTTP server.** The server now
  sets `ReadHeaderTimeout` (10s) so a client cannot hold a connection — and a
  server goroutine — open indefinitely during the header phase (a classic
  slow-loris vector that starves legitimate requests and inflates tail latency),
  and `MaxHeaderBytes` (1 MB) to reject oversized header floods before they
  allocate. Normal clients on a bad network are unaffected.

### CI / Build
- **Benchmark regression guard for the hot path.** A new
  `BenchmarkObservabilityMiddleware` measures the fixed per-request overhead of
  the request-ID + tracing/logging middleware with a reusable, allocation-free
  `ResponseWriter`, so a future change that reintroduces per-request allocations
  or an expensive probe on the hot path shows up as a measurable regression
  rather than a silent tail-latency creep.

## [3.9.6] — 2026-07-08

VayuShield goes from "detect and block per request" to "detect once, block
thereafter": a bad bot is now jailed the moment it is caught, so its very next
request is dropped instantly — and every detection grows the signature database
and is recorded to an operator-visible block list. Blocked/bad-bot traffic is
kept out of analytics. All of it stays GDPR-clean (hashed IPs, in-memory jail,
no PII).

### Added
- **Auto-block detected bad bots ("detect once, block thereafter").** When
  VayuShield blocks (or tarpits) a bad actor and auto-block is enabled, the
  source IP is now jailed in the O(1) in-memory blocklist, so the very next
  request from it is dropped immediately by the cheap blocklist gate — without
  re-running classification. GDPR: the jailed IP lives only in memory on a
  short, auto-expiring TTL (a security legitimate-interest measure, never written
  to disk).
- **The signature database now grows from real detections.** Every block folds
  the client's fingerprint into `vayushield_signatures` as an auto-learned
  `bad_bot` candidate (operator-reviewable), so the bot knowledge base is no
  longer limited to the static seed list + the previous narrow "unknown,
  score>0.75" learning path (which could sit idle for days). Only derived
  sub-hashes and a coarse UA family are stored — never an IP or the full UA.
- **Operator-visible bot block list.** A new "Recent blocks" card on the Bot
  Shield & Analytics panel lists recent hard blocks (time, truncated UA, path,
  reason, score, country, and the salted **hashed** IP) from `vayushield_blocked`.

### Changed
- **Blocked / bad-bot traffic is excluded from analytics.** The engagement
  beacon now records nothing when a request classifies as a bad bot (blocked
  bots already never render a beacon; this also drops any bad bot that still
  fired one, e.g. a headless scraper). The block itself is recorded to the
  VayuShield block list, not the engagement analytics.

### Fixed
- **Trending & pinned widget not appearing under existing posts (operational,
  not a code change).** The widget markup and its content-versioned script are
  present in the article template for every post, the feature toggle purges the
  render cache, and the cache schema was already bumped so posts re-render with
  it. When it still does not show, the cause is a stale copy cached **upstream**
  (browser or CDN) — the same class of issue as the historical Cloudflare-cached
  `trending.js` 404. Resolve it by hard-refreshing (Ctrl/Cmd+Shift+R), purging
  the CDN/Cloudflare cache, and confirming "Trending & pinned posts" is enabled
  in VayuOS → Tools & Plugins.

## [3.9.5] — 2026-07-08

The Analytics and Bot Shield panels no longer compute their heavy reports on the
request goroutine — so they can never block into a 502, no matter how large the
analytics tables grow or how much load the box is under. This is the same
principle that already keeps the Monitoring tab instant (a background snapshot),
generalised to every heavy operator dashboard.

### Fixed
- **Analytics & Bot Shield 502 on large analytics tables (heavy reports moved
  off the request path).** Even after the dedicated admin DB lane (v3.9.4), these
  two panels could still time out: the Analytics tab fires ~a dozen aggregate
  scans (`COUNT(DISTINCT)`, `GROUP BY`, `AVG`) and the Bot Shield tab several
  more, all **synchronously on the request goroutine**. On a site with millions
  of analytics rows the combined scan time exceeded the 30s server write timeout
  → 502 — isolation can't fix a query that is simply slow. These sections are now
  computed by a **background, single-flighted, TTL-cached fragment builder**
  (`admin_dashcache.go`) with a bounded compute deadline, and a **startup warmer**
  keeps the default (30-day) window hot. A page request now returns the
  last-known fragment instantly (or a lightweight "assembling…" placeholder only
  in the brief cold window right after a restart) and never waits on a scan. The
  Bot Shield status, settings, signature and review-queue controls stay fully
  synchronous, so the operator can always see status and change settings
  instantly.

### Changed
- Analytics/Bot Shield report freshness is now bounded by a short cache TTL
  (default 90s, kept hot by the warmer). This is intentional — a report tolerates
  being a few seconds stale — and collapses repeated tab opens to at most one
  heavy scan per window per TTL instead of one per page load.

## [3.9.4] — 2026-07-08

VayuOS is now **hard-isolated from public load**: the admin console runs on its
own reserved database connections, so no bot flood, cold-cache render storm, or
public read burst can ever starve it. Combined with the WAL/read-pool fixes from
this cycle, the control plane stays responsive under any load — while the bot
shield sheds abusive traffic before it can touch the render/DB path. This is the
release that turns the "VayuOS 502 under load" saga into a closed chapter.

### Added
- **Dedicated admin database lane (VayuOS isolation) — the core guarantee.**
  VayuOS now reads through its OWN reserved WAL connection pool
  (`AdminReader()`/`ARDB`), physically separate from the public read pool. The
  per-request auth gate (session validation + user lookup, on every `/os`
  request) and the operator dashboards (Analytics, Bot Shield & Analytics,
  monitoring) all draw from this lane. So no matter how saturated the public pool
  gets — a bot flood, a diffuse crawler mix, a cold-cache render storm — the
  control plane keeps guaranteed capacity and VayuOS always authenticates and
  loads. It is small and reserved by design (default `max(8, NumCPU)`, capped at
  16, recycled like the public pool), and tunable via `VAYU_ADMIN_POOL_SIZE`.
  Together with `/os` being exempt from VayuShield challenges, the admin surface
  is now doubly isolated from public traffic.
- **Load-shedding now protects out of the box.** Enabling VayuShield load
  shedding without setting an explicit "max concurrent" previously left the gate
  disabled (`0 = unlimited = no protection`). It now derives a generous,
  capacity-based ceiling (`max(256, NumCPU×64)`) so a saturating flood is shed
  with a cheap 503 *before* the render/SQLite path collapses, while normal
  bursts pass untouched. Verified (signed-session) visitors and bypassed
  prefixes (`/os`, `/api`, health/metrics) are never shed.

### Fixed
- **Analytics & Bot Shield admin panels 502 (dashboard reads ran on the single
  writer).** Both analytics stores were constructed on the writer connection
  (`analytics.New(dbpkg.DB)`, `vastore.New(dbpkg.DB)`), so opening the Analytics
  or Bot Shield & Analytics panel fired a dozen heavy aggregate scans
  (`GROUP BY`, `COUNT(DISTINCT)`, `AVG`) over the ever-growing
  `analytics_daily`/`analytics_pageviews`/`vayuanalytics_sessions` tables — all
  serialised on the one writer connection, behind the pageview/beacon write
  stream — and blew past the 30s server timeout into a 502. Both stores now route
  their **read/report queries** at the WAL read pool via a new `UseReader`
  (writes stay on the writer; WAL preserves read-your-writes). Verified no
  read-your-writes hazard: neither store uses explicit transactions, so every
  `Query`/`QueryRow` is a plain SELECT safe on the read pool.
- **VayuOS 502 under read-pool starvation (capacity + checkpoint stalls).**
  Follow-up hardening after the WAL-growth fix, from production diagnosis:
  - **Read pool enlarged and made tunable.** The pool was `NumCPU` connections
    (e.g. 6). Because the bottleneck under load is read *concurrency* (not CPU),
    a modest burst of uncached requests — a diffuse crawler mix, or a cold cache
    right after restart — drained the pool, and every uncached read then queued
    behind it, including the VayuOS auth gate, which timed out into 502 while the
    cache-served public site stayed fast. The pool now defaults to `NumCPU × 4`
    (min 24, max 64) with a smaller per-connection page cache (8 MB) to offset
    the memory, and is tunable via `VAYU_READ_POOL_SIZE`.
  - **Background WAL checkpoint is now always `PASSIVE`.** The adaptive path
    escalated to `TRUNCATE`/`RESTART` whenever the WAL exceeded the threshold —
    but those modes take an exclusive lock that, under continuous read traffic,
    stalls every reader for up to `busy_timeout` (5s) on each checkpoint tick.
    `PASSIVE` never blocks; connection recycling still lets it reclaim the WAL in
    reader-free windows, and `journal_size_limit` caps the file size.
  - **Cache warmer no longer holds a read connection across the whole pass.** It
    streamed a cursor over every published article while rendering each page with
    inter-page pauses, pinning one pool connection (and a WAL read snapshot) for
    minutes on a large catalog. It now materialises the slug list up front and
    releases the connection immediately.

### Fixed (WAL growth — prior)
- **Unbounded WAL growth / checkpoint starvation (root cause of the VayuOS
  502s).** The read pool (`RDB`) was created with `SetConnMaxLifetime(0)`, so its
  `NumCPU` (min 4) connections were **never recycled**. In WAL mode a checkpoint
  can only reset/truncate the `-wal` file when *no* connection is holding an
  older read snapshot; with a pool of permanent readers under continuous traffic
  there is almost always a live reader, so the background checkpoint — which ran
  in `PASSIVE` mode and therefore never truncated the file — could essentially
  never reclaim the WAL. The `-wal` file grew monotonically; reads got steadily
  slower (each read scans an ever-larger WAL) until the dynamic VayuOS admin
  exceeded its request timeout and nginx returned **502**, while the public blog
  (served from the on-disk HTML cache, touching no DB) stayed fast. This is also
  why every previous "move another read off the writer" release made it *worse*,
  not better — each one added load to the very pool that was starving the
  checkpoint. Two changes fix it at the source:
  - Read-pool connections are now recycled on a bounded lifetime/idle window
    (`SetConnMaxLifetime`/`SetConnMaxIdleTime`), guaranteeing recurring moments
    with no live reader so the checkpoint can complete.
  - The background checkpoint now runs `wal_checkpoint(TRUNCATE)` (resets the
    `-wal` file to zero bytes) on a tighter cadence instead of `PASSIVE`.

  In a sustained read+write reproduction the previous configuration drove the
  WAL to multiple gigabytes within seconds; with this fix it stays bounded to a
  few dozen megabytes.

### Changed
- **CI: `sqlclosecheck` added to the lint gate.** The lint config already ran
  `rowserrcheck` (unchecked `rows.Err()`) but not `sqlclosecheck` (unclosed
  `sql.Rows`/`sql.Stmt`). An unclosed rows leaks a pooled connection and, in WAL
  mode, pins a read snapshot — precisely the failure class above — so it is now
  enforced.

### Upgrade Notes
- **Updater parity (built-in one-click vs. manual script).** The two update
  paths now converge on this release: the in-app one-click updater installs the
  published, checksum/signature-verified release binary, and
  `scripts/update-vayupress.sh` rebuilds the same source — both land on v3.9.4.
  The earlier divergence (a manual `git pull` "recreating" files the one-click
  updater never applied) happened because `main` moved forward without a version
  bump, so no release was cut and the one-click updater had nothing newer to
  install. Going forward, shipping = bumping `.release-version`, which triggers
  the self-contained signed release workflow; keep using that and both updaters
  stay in lockstep.
- **New tunables (all optional, safe defaults):** `VAYU_ADMIN_POOL_SIZE`
  (reserved admin read connections, default `max(8, NumCPU)`),
  `VAYU_READ_POOL_SIZE` (public read pool, default `NumCPU×4`). No config or
  migration changes are required to benefit from the isolation and load-shed
  fixes.

## [3.9.3] — 2026-07-07

Final read-routing pass: the remaining public and background reads no longer use
the single SQLite writer connection.

### Changed
- **Public API and background reads moved to the WAL read pool.** The public
  comment API (article-exists checks, approved-comment listing), the article
  table-of-contents endpoint, the health `stats`/`metrics` counters, the
  comment-approval notification lookup, and the background search-reindex and
  social-share reads all read via the single writer connection. They now use the
  read pool (`dbpkg.Reader()`), so public traffic and post-publish background
  work no longer contend with writes on the one writer. Writes stay on the
  writer; WAL preserves read-your-writes. (Maintenance paths that must observe
  the primary — e.g. `PRAGMA integrity_check` before a vacuum — deliberately
  remain on the writer.)

## [3.9.2] — 2026-07-07

Completes the VayuOS enterprise-grade hardening: VayuShield's per-request writes
are now batched off the request path, and the remaining admin-panel reads no
longer touch the single writer connection. Combined with v3.9.0–v3.9.1, VayuOS
is fully decoupled from writer contention — for both authentication and page
loads — even under a bot flood.

### Changed
- **VayuShield writes are now batched and asynchronous.** Bot-protection
  telemetry — adaptive-signature learning (`Observe`), plus challenge and block
  records — was written synchronously on the single SQLite writer for each
  suspicious request. Under a bot flood (exactly when the shield is working
  hardest) that storm of tiny transactions could saturate the writer and take
  VayuOS down with it. These writes now go through a bounded, batched background
  writer (new `internal/dbbatch`): one transaction per flush, non-blocking on
  the request path, and best-effort (shed and counted under an extreme flood
  rather than allowed to block or overwhelm the database). Bot protection is
  now a net-zero cost to the writer under attack.
- **Remaining VayuOS admin reads moved to the read pool.** The block editor
  (blocks/tags/version lookups), the Messages inbox (list, detail, unread count,
  CSV export), the per-post publishing-options load, and the observability
  console (outbox stats, event list/detail, correlation trace) read via the
  single writer connection, so a busy writer made those pages slow. They now use
  the WAL read pool (writes stay on the writer; WAL preserves read-your-writes),
  so no admin page serialises behind writes.

### Reliability series
This closes the update/restart-resilience work: v3.8.9 (page-cache
stale-while-revalidate) → v3.8.10 (covering index + backup rotation) → v3.8.11
(single-flight trending) → v3.9.0 (admin auth reads off the writer) → v3.9.1
(analytics beacons batched) → v3.9.2 (VayuShield writes batched + admin reads off
the writer).

## [3.9.1] — 2026-07-07

The real fix for the post-restart VayuOS 502s: **the always-on analytics
beacons were doing synchronous writes on the single SQLite writer for every
public page view.** They are now batched and written off the request path.

### Fixed
- **Engagement beacons no longer saturate the writer after a restart.** Every
  public page view fires two beacons — a page-enter (on load) and an engagement
  event (on exit) — and each was persisted **synchronously on the single writer
  connection** (`RecordEnter` = a `SELECT COUNT` + `INSERT`; `RecordBeacon` = an
  `UPDATE`). Under real traffic that is a storm of tiny fsync'd transactions
  which, right after a cold restart, pegs the one writer and bloats the WAL — so
  the whole database slows and the *dynamic* VayuOS pages exceed the 30s server
  write timeout and return **502**, while the public site (served from the HTML
  cache, no database) stays fast. Beacons are now **enqueued on a bounded buffer
  and flushed by a single background goroutine in batched transactions** (~1/sec
  or every 256 events), collapsing hundreds of tiny writes into a handful and
  keeping the writer free for everything else. The write is off the request path
  entirely (the beacon returns immediately), and it is best-effort: under an
  extreme flood, events past the buffer are dropped (and counted) rather than
  allowed to block a request or overwhelm the database. This is the write-side
  companion to v3.9.0 (which moved the admin *auth reads* to the read pool).

## [3.9.0] — 2026-07-07

Resolves the last post-update symptom: **VayuOS was returning 502 for the first
several minutes after a restart while the public blog stayed fast.** Root-caused
to the admin authentication gate reading from the single SQLite writer
connection.

### Fixed
- **VayuOS no longer 502s after a restart while the writer warms up.** Every
  admin request passes through an auth gate that did *two* reads — session
  validation and the account lookup — on the **single writer connection**
  (`SetMaxOpenConns(1)`). Right after a restart the writer is briefly saturated
  (cold page cache, the analytics INSERTs driven by live public traffic, the
  write-queue drain, WAL checkpoints, the one-time tag backfill), so under
  `busy_timeout` each of those auth reads could wait seconds. With the server's
  30s write timeout, admin requests that piled up behind the writer were cut
  off mid-response and surfaced as **502 Bad Gateway** — whereas the public site
  never hit it, because pages are served from the on-disk cache and the separate
  WAL read pool. The auth gate now reads sessions and accounts from the **read
  pool** (`dbpkg.Reader()`), so admin authentication is decoupled from writer
  load and the panel stays reachable during the warm-up window. Writes still go
  to the writer, and WAL guarantees read-your-writes, so a fresh login validates
  immediately. This completes the update-reliability series (v3.8.9 page-cache
  stale-while-revalidate, v3.8.10 covering index + backup rotation, v3.8.11
  single-flight trending, and now the admin auth gate).

## [3.8.11] — 2026-07-07

The missing piece of the post-update 502 fix: the public "Trending" JSON
endpoint now single-flights its compute, so a restart can no longer trigger a
read-pool herd.

### Fixed
- **`/api/trending` no longer stampedes the database after a restart.** Every
  public page (the homepage and every post) fetches this endpoint client-side,
  and its payload is memoised in-process. But the memo is lost on every restart,
  and the compute held no single-flight — so immediately after an update, a
  burst of concurrent widget fetches each ran the trending queries at the same
  time and saturated the SQLite read pool, stalling *all* reads (public pages
  and VayuOS alike) and returning 502s for several minutes until the storm
  cleared. This recurred on every restart because the memo starts empty. The
  endpoint now:
  - **single-flights** the recompute (exactly one runs at a time);
  - serves the **last good payload** immediately while a refresh runs in the
    background (stale-while-revalidate);
  - on a truly cold start, serves a **cheap "warming" payload** (pinned +
    most-recent posts, using only indexed lookups — no analytics aggregation)
    to everyone except the single computing request, so the widget still renders
    and nobody queues behind the heavy query.
  A generation counter ensures a pin/unpin during an in-flight compute is never
  masked. Together with the v3.8.9 page-cache stale-while-revalidate and the
  v3.8.10 covering index, updates are now smooth end to end.

## [3.8.10] — 2026-07-07

Two enterprise follow-ups to the v3.8.9 performance work: the public "Trending"
query is now index-only, and pre-update backups no longer accumulate without
bound.

### Fixed
- **"Trending" no longer full-scans the pageview log.** The public trending
  widget (and the same trailing-window aggregation behind the admin "Top pages"
  panel) ranked posts by scanning `analytics_pageviews` and, lacking a covering
  index, fetched a table row per pageview just to read `url_path` — slow on a
  large event log and especially under a cold cache. A new composite index
  `idx_apv_trending(event_type, created_at, url_path)` (migration 057) plus a
  rewritten query that aggregates per path first, then joins the (unique,
  indexed) article slug, make the hot scan **index-only** (`SEARCH … USING
  COVERING INDEX`). Results are identical; a regression test asserts the plan
  stays index-only. Purely additive — no data change.
- **Pre-update backups are now rotated.** The self-update engine writes a
  `backup-<db>-<timestamp>.tar.gz` snapshot before replacing the binary but
  never pruned old ones, so they accumulated on disk across updates. It now
  keeps the newest N per database (default 5, `VAYU_UPDATE_BACKUP_KEEP`),
  scoped per-database so it can never touch another DB's backups, and
  best-effort so pruning can never fail the backup it follows. `deploy/migrate.sh`
  gets the same rotation for its `.bak.<timestamp>` snapshots
  (`VAYUPRESS_BACKUP_KEEP`, default 5). Existing files remain deletable in one
  click from VayuOS → Storage.

## [3.8.9] — 2026-07-07

Performance/reliability fix: eliminates the cache-invalidation "thundering herd"
that made routine updates briefly slow the whole site (VayuOS included) and
return intermittent 502s. Updates are now latency-neutral by design.

### Fixed
- **Stale-while-revalidate for the public page cache — no more re-render herd
  after a deploy.** The render cache is invalidated lazily: any global change (a
  deploy that edits templates/CSS or bumps the cache schema, or a theme/identity
  save) marks every pre-rendered page stale on its next request. Previously the
  serve path re-rendered a stale page *synchronously, on the request goroutine*.
  That is fine for one page, but after a global invalidation the entire catalog
  is stale at once: under real traffic (and crawlers walking the long tail)
  hundreds of cold renders fired concurrently, saturated the CPU and serialised
  on the single SQLite writer — tail latency blew out to tens of seconds
  (HTTP p95 pinned at the histogram ceiling) and nginx returned 502s until the
  cache re-warmed. The serve path now returns the stale bytes **immediately** and
  re-renders the page **off the request path**, single-flighted per page and
  globally bounded (default `max(2, NumCPU/2)`, env `VAYU_SWR_MAX`). A
  cache-invalidating update is now latency-neutral: visitors keep getting instant
  (briefly stale) pages while the cache catches up a few renders at a time.
  Truly-missing entries (a freshly published/edited post, whose cache file is
  deleted) still render synchronously, so content edits appear immediately.
- **Public post reads no longer serialise on the single writer connection.** The
  article handler read the post row via the writer connection (`MaxOpenConns=1`)
  instead of the WAL reader pool, so a cold-cache render competed with — and
  stalled behind — writes, analytics inserts, WAL checkpoints and the VayuOS
  admin path on the one writer. It now uses the reader pool, matching the
  homepage. This is why the admin panel specifically felt slow under a
  cold-cache storm.

### Notes
- Recommended follow-ups (not in this release): the public "Trending" query
  joins the pageview log on a substring of the URL path, which cannot use an
  index — worth normalising to an indexed slug on a large analytics table; and
  the on-disk backup files created by the deploy/cron scripts should be rotated
  (the in-binary write-queue already prunes itself).

## [3.8.8] — 2026-07-07

A cache-invalidation fix so the Trending strip (and the always-show related
section) finally appear under posts that were published before those features
shipped.

### Fixed
- **Trending & related now show on existing posts after an upgrade.** Public
  post pages are cached to disk and only re-render when the renderer
  fingerprint changes (CSS hashes + a cache schema), not on template markup
  alone. The "Trending & pinned posts" strip and the always-show related
  section were added as markup-only template edits, so posts cached by an older
  binary kept serving HTML without them — the sections appeared only on newly
  edited posts. Bumping the cache schema (v3 → v4) flips the fingerprint, so on
  the next deploy every cached post re-renders lazily (one per request, no
  site-wide re-render herd) and the Trending/Related sections show up under
  posts that were published earlier.

## [3.8.7] — 2026-07-07

A Theme Studio control for the Trending strip, and fully responsive post
content — tables, code, and images now adapt cleanly to any screen.

### Added
- **"Trending posts" toggle in Theme Studio → Article pages.** Sitting right
  next to "Related posts", it lets you show or hide the trending & pinned strip
  at the end of a post per theme (Theme default / Show / Hide). Pure scoped CSS,
  no JavaScript, no server round-trip.

### Changed
- **Post content is now fully responsive on every screen.** Wide tables scroll
  horizontally within the reading column instead of overflowing the page, and
  gain readable borders, padded cells, a header tint and zebra rows; images
  keep their aspect ratio (`height:auto`); long URLs, inline code and
  unbreakable words wrap instead of forcing a horizontal scrollbar; code blocks
  stay inside the viewport. A tablet/mobile breakpoint (≤768px) tunes the base
  font, heading sizes, code padding and table cell padding, and stacks the
  related-articles list for small screens. Reader typography and speed are
  unchanged on desktop.

## [3.8.6] — 2026-07-07

Post-page polish and a trending-source correction so the public "Trending"
widget matches the admin analytics the operator actually sees.

### Fixed
- **Post byline now respects the chosen title alignment.** When a post/theme is
  set to centre-align, the author byline and meta row centre with the title
  instead of hanging to one side. (#224)
- **Related and Trending sections always render after a post.** They previously
  disappeared when a post had no same-tag siblings; they now fall back
  gracefully (related → recent posts, trending → recent posts) so every article
  ends with onward-reading links. (#224)
- **Trending is now sourced from the same analytics as the admin "Top pages"
  panel.** The widget previously ranked posts from the daily aggregate
  (`analytics_daily`), which could diverge from VayuOS → Analytics → Pages. It
  now computes the trailing-7-day most-viewed posts from the per-pageview event
  log (`analytics_pageviews`, `event_type=1`) — the exact source and window of
  that panel — joined to published, non-page articles, and falls back to the
  daily aggregate (then recent posts) only when the pageview log is empty.

### Changed
- Trending reflects the last 7 days of real page views and auto-refreshes daily
  (24h cache; 1h while a fallback source is in use), so "Trending" means
  "top pages, last 7 days" and stays current without a scheduler.

## [3.8.5] — 2026-07-07

Theme release: a premium blog theme that matches the vayupress.com website, plus
a Website preview fix. See ADR-0116.

### Added
- **Vayu — a flagship blog theme that matches vayupress.com.** Deep cosmic
  “ink” canvas with teal + saffron accents, gradient display headings, glassy
  post cards with a teal glow-lift, gradient-bordered code blocks and a refined
  reading experience, in light and dark. Deploy it in one click from VayuOS →
  Theme Store (Flagship) so your blog and website share one identity. Pure CSS,
  no JavaScript.
- **Self-hosted display font (sovereign, fast).** Vayu’s Space Grotesk headings
  are served from the binary at `/static/fonts` (latin-subset woff2, ~13 KB per
  weight, `font-display: swap`) — no Google Fonts, no CDN, no external request —
  so the theme stays 100/100-fast and privacy-preserving under `font-src 'self'`.
  Space Grotesk is OFL-1.1; attributed in `NOTICE` with the license at
  `static/fonts/OFL.txt`. (ADR-0116)

### Fixed
- **Website design “Preview” now shows the design you selected**, not always the
  default. `/site` honours `?preview=<design>` and the studio’s Preview button
  follows the selected card, so you can preview a design before saving.

### Security
- **Harden the custom-website deploy against path traversal / Zip Slip.** The
  `.zip` extractor and the static file server now confine every filesystem
  operation to an OS-level directory root (`os.Root`, Go 1.24+): the kernel
  refuses any read or write that would escape the deployed bundle — via `..`, an
  absolute path, or a symlink — in addition to the existing string-level checks.
  Resolves CodeQL alerts #46, #47 and #48. No behavioural change.

### Upgrade Notes
- No action required. Apply the Vayu theme from VayuOS → Theme Store when you
  want the website look on your blog; existing themes are unchanged.

## [3.8.4] — 2026-07-07

Website release: a fixed design picker, a new single-domain website+blog layout,
and one-click custom-website deploys. See ADR-0114 and ADR-0115.

### Added
- **Website at the root, blog at `/blog` (new site mode).** A business website
  can now own the domain root with the blog homepage at `<domain>/blog` on the
  **same** domain — no subdomain, no extra certificate — and **every existing
  post keeps its `<domain>/slug` URL unchanged**. Pagination lives at
  `/blog/page/N`; the legacy `/page/N` redirects there. (ADR-0115)
- **One-click custom website deploy.** Upload a static site as a `.zip` in
  VayuOS → Website and serve it at the domain root — built by hand or with an AI
  assistant, no build step. The upload is validated with defence in depth
  (path-traversal/zip-slip proof, symlink rejection, a static-asset extension
  allowlist, per-file/total/file-count caps, zip-bomb-resistant extraction, a
  required root `index.html`), activated atomically, and revertible with
  one-click **Roll back**. A downloadable **AI build guide** states exactly the
  rules so any assistant can produce a compliant bundle. The blog stays at
  `/blog` and posts at `/slug`. (ADR-0114)

### Fixed
- **The Website design picker could not select a design.** The studio's script
  (`admin-os-website.js`) was referenced but its serve route was never
  registered, so it 404'd — design cards did not respond, the content fields did
  not hydrate, and Save did nothing. The route is now registered.
- **Switching design now applies immediately.** The business-site stylesheet is
  cache-busted per template (`/site.css?v=<template>`), so changing the design no
  longer keeps serving the previous design's CSS from cache.

### Upgrade Notes
- No action required. New modes are opt-in from VayuOS → Website; existing
  installs keep their current layout and all post URLs are unchanged.

## [3.8.3] — 2026-07-07

Search memory + client-payload release, completing the memory-footprint work
started in 3.8.2 (see ADR-0113). The instant-search index is now a bounded
recent window instead of the whole corpus, so the browser download and the
resident server snapshot stay small on large sites — with no post made
unreachable and no change to ranking.

### Changed
- **Instant-search index ships only the most-recent posts (default 5,000).** The
  Ctrl/⌘-K modal still filters entirely in the browser with zero per-keystroke
  server round-trips, now over a recent window rather than every post — so the
  downloaded index drops from tens of MB to a few MB on large sites. The
  server-side index stays complete.
- The client search payload now reports the total post count so the modal knows
  when the corpus is larger than the window it holds.

### Added
- **"Search the full archive" escalation.** When the corpus is larger than the
  local window (or a recent-only search finds nothing), the modal shows a
  keyboard-navigable row that jumps to the server-rendered `/search` page, which
  searches every post. The server is only touched when a visitor chooses it, so
  as-you-type search stays instant and costs the server nothing.
- **ADR-0113 — Memory-Footprint Budget & Search-Index Windowing**, recording the
  SQLite page-cache sizing, search-index trim, single-pass snapshot, cgroup soft
  memory limit (3.8.2) and the client-index windowing (3.8.3).

### Upgrade Notes
- No action required. `/search`, `/api/v1/search` and GraphQL search remain
  full-corpus; only the downloadable instant-search index is windowed. The
  window size is a build-time constant (`clientSnapshotMax`).

## [3.8.2] — 2026-07-07

Memory-footprint release. A RAM audit found that steady-state RSS on large
stores was dominated by SQLite's per-connection page cache (multiplied across
the reader pool) and by redundant copies in the in-memory search index — not by
any leak. This release cuts both with no change to features, reliability, search
results, UI, or measured latency. On a large store the steady RSS drops by
roughly 200–400 MB depending on core count; the in-memory search index and the
resident client-search snapshot remain and are the subject of a later change.

### Changed
- **SQLite page cache right-sized (largest saving).** SQLite's page cache is
  per-connection and the read pool opens one connection per CPU, so a 64 MB
  cache multiplied to 256–512 MB across the pool. The writer now uses 32 MB and
  each reader 16 MB. `mmap_size` (512 MB) is unchanged, so hot database pages
  stay resident in the shared OS page cache and query latency is unaffected —
  the private per-connection caches were largely redundant with mmap.
- **Search index no longer stores a redundant lowercase copy.** Each indexed
  document kept a `title + tags + excerpt` lowercase haystack in addition to the
  separate lowercase title and tags. Since the excerpt fallback only runs after
  title and tags have already been checked, the document now keeps only a
  lowercase *excerpt*. Ranking is byte-for-byte identical.
- **Client search-index snapshot builds in a single pass.** The content version
  is now computed with a streaming hash while the payload is assembled, instead
  of serialising the whole index to JSON a second time purely to hash it. This
  removes a large short-lived allocation (and the hash over it) from every
  snapshot rebuild. The version stays fully content-sensitive.

### Added
- **cgroup-aware soft memory limit.** On startup the process now sets a Go
  memory soft-limit at 90% of the detected cgroup memory limit (containers /
  `systemd MemoryMax=`), keeping steady RSS bounded and reducing OOM-kill risk
  under load. An explicit `GOMEMLIMIT` is always honoured; bare hosts with no
  cgroup limit are unaffected.
- The boot-time search index build now returns its large, short-lived scan
  working set to the OS immediately once the live index is in place, removing
  the post-restart RSS spike.

## [3.8.1] — 2026-07-06

Maintenance micro release. No behavioural changes — it reissues the v3.8.0 line
as a fresh signed build so operators can pull it through the in-app updater and
so the Bot Shield & Analytics console-styling fix is guaranteed to be present in
the running binary.

### Fixed
- Ensures the Bot Shield & Analytics console renders with its full stylesheet
  (real toggle switches, status hero, stat cards, CSS-only `:has()` disclosure)
  by shipping the v3.8.0 CSP-safe external-stylesheet fix in a clean tagged
  build. See 3.8.0 for the details of that fix.

## [3.8.0] — 2026-07-06

### Changed
- **Redesigned the Bot Shield & Analytics console.** The VayuOS security &
  analytics panel is rebuilt from a dense wall of raw checkboxes into a clean,
  Cloudflare-style console: a status hero with a live state dot
  (Off / Protected / Under attack) and live metrics (visitors now, requests/sec,
  in-flight, blocked IPs); real toggle switches, each a titled row with a
  one-line description; and the advanced numeric knobs (thresholds, requests/min,
  jail minutes, …) tucked into a sibling panel that reveals only when its feature
  is switched on. Controls are grouped into Protection, Availability & anti-DDoS,
  and Analytics, with a collapsed section for server-level Tier 2/3 hardening.
- **No custom JavaScript in the console.** All interactivity is now HTMX +
  CSS-only progressive disclosure (`:has()`): the settings form posts via
  `hx-post` and the server replies `HX-Refresh` so the panel re-renders in its
  applied state, and the signature review-queue Confirm/Dismiss actions post via
  `hx-post` with `hx-swap="delete"`. The inline `fetch` script was removed and
  the shield settings/verify/dismiss handlers now parse url-encoded form values.
- **Console styles moved to the external stylesheet.** The redesigned console's
  styles now live in `admin-os.css` (served same-origin), using the VayuOS design
  tokens, so they satisfy the strict admin CSP (`style-src 'self'`, ADR-0036). The
  first cut shipped them in an inline `<style>` block, which the policy blocks —
  the panel rendered unstyled (raw checkboxes, no cards). All inline `style="…"`
  attributes were removed too, and the toggle switches, stat cards, status hero
  and `:has()` disclosure now render correctly in both light and dark themes.

### Fixed
- **Engagement beacon page-enter fetch.** Added a `.catch` so a transient
  network/HTTP failure on the analytics page-enter beacon can never surface as an
  unhandled promise rejection (which would appear as a browser console error and
  could lower a Lighthouse best-practices score). The endpoint returns 204
  normally; this is pure defense-in-depth.

### CI / Build
- **GitHub Pages now deploys via the official Actions Pages flow.** The
  marketing/docs site publishes with `actions/upload-pages-artifact` +
  `actions/deploy-pages` straight to the `github-pages` environment, replacing
  the older `gh-pages` branch push whose auto "pages build and deployment" stage
  was rejected by the environment's branch-protection rule. Requires the one-time
  repository setting **Settings → Pages → Source = "GitHub Actions"**. The
  `vayupress.com` custom domain is carried through `docs/site/CNAME` in the
  uploaded artifact and keeps working.
- **Theme-editor settings-coverage test** now allowlists the VayuShield `shield.*`
  and `analytics.beacon` console keys (managed in `/os/shield`, not the theme
  editor), and `main.go`'s `Version` is kept in lockstep with `.release-version`.

## [3.7.1] — 2026-07-06

### Fixed
- **CI Native-Go gate (staticcheck).** Cleared the two staticcheck findings that
  were failing the gate: `ST1023` (redundant explicit type in a `var`
  declaration in the bot scorer) and `SA4000` (a test asserted two acquisitions
  with an identical expression on both sides of `||`).

### Security
- **Hardened challenge key derivation (CodeQL: weak hashing on sensitive data).**
  The VayuShield challenge signer now derives its key with HMAC — the secret is
  the HMAC key, never hashed as message data — instead of `sha256.Sum256(secret)`.
  This is the correct construction for deriving a MAC key from a high-entropy
  application secret and clears the alert.
- **Removed manual HTML quoting in the challenge interstitial (CodeQL: unsafe
  quoting).** The signed proof-of-work challenge is now injected with
  `html/template` (context-aware auto-escaping) instead of hand-quoted attribute
  concatenation. The value was already server-generated, so this is
  defense-in-depth, and it clears the Critical alert.

## [3.7.0] — 2026-07-06

### Added
- **VayuShield Tier-1 resilience (in-binary DDoS / abuse defense).** New
  `internal/vayushield/resilience` package adds cheap, O(1)-per-request
  availability defenses that run *before* any expensive work (fingerprinting,
  rendering, SQLite), so legitimate traffic stays fast while abuse is shed early:
  a sharded per-IP token-bucket **rate limiter**, an **in-flight concurrency
  gate** for load shedding (cheap 503 when saturated), an auto-expiring TTL
  **blocklist** that jails IPs which relentlessly breach the limit, and a
  lock-free **RPS controller** that drives an **adaptive under-attack mode**
  (auto-tightening challenge thresholds during a flood and relaxing when it
  passes). The middleware is ordered blocklist → load-shed → rate-limit →
  classify, and **verified visitors (signed session token) are never rate-limited
  or shed** — a real reader is never blocked.
- **Live, no-restart operator controls in VayuOS.** The **Bot Shield & Analytics**
  panel now has on/off toggles and tunables for every defense — bot protection
  and its PoW/JS/block thresholds, tarpit, per-IP rate limiting, load shedding,
  auto-blocklist, adaptive under-attack mode, and the engagement beacon — all
  persisted to settings and applied to the live engine atomically with no
  restart. A live status line shows under-attack state, in-flight requests,
  blocklisted IPs and current RPS. Everything defaults **off**.
- **Engagement analytics now populate.** The VayuAnalytics engagement beacon
  (time-on-page, scroll depth) is injected on public pages by piggybacking the
  already-loaded analytics script (cookieless, no PII, operator-toggleable), so
  the dashboard's engagement, source-breakdown and AI-vs-search reports fill in.
- **Tier 2 & 3 network hardening (sovereign scripts).** `deploy/vayushield-firewall.sh`
  (nftables per-IP connection/rate limits + SYN-flood cookies) and
  `deploy/nginx-vayushield.conf` (edge per-IP request/connection shaping +
  slow-loris timeouts). The panel documents them and is honest that a true
  volumetric flood needs anycast/scrubbing capacity no single host can provide.

### Notes
- All new protections are opt-in and default off; the legacy `VAYUSHIELD=on`
  env var still force-enables bot protection. See ADR-0112 for the tiered design
  and the deliberate capability boundary.

## [3.6.0] — 2026-07-06

### Added
- **VayuShield — sovereign bot protection.** A pure-Go, zero-CGo, zero-dependency
  bot-defense engine built into the single binary, replacing Cloudflare Bot
  Management without any third-party service or external network call. It
  fingerprints each client from its TLS ClientHello (JA3 and JA4), the presence
  of a 2026 post-quantum key share (X25519MLKEM768), the HTTP/2 SETTINGS frame
  (Go's default 65535 window vs a real browser's), and header/pseudo-header
  order, then combines those with a static and self-learning User-Agent database
  into an explainable composite `BotScore` and a `ClientType`
  (Human / GoodBot / BadBot / AIAgent / Headless / Unknown). Suspicious traffic
  is met with an escalating challenge ladder — a silent SHA-256 proof-of-work,
  then a JavaScript interstitial, then a hard block, then an optional tarpit —
  while verified visitors carry a signed, PII-free session token. Search-engine
  crawlers and AI assistants (GPTBot, ClaudeBot, PerplexityBot, …) are always
  allowed and counted separately, never blocked. A background reporter promotes
  recurring bot-like fingerprints to a review queue, and signature sets can be
  exported/imported to share with the community. Off by default (`VAYUSHIELD=on`
  to enable).
- **VayuAnalytics Enterprise — cookieless engagement analytics.** Extends the
  privacy-first analytics with real engagement quality — time on page, scroll
  depth, engagement and bounce rates — and classifies every visit by traffic
  source, including a first-class **AI-assisted discovery** category that shows
  how readers arriving from ChatGPT, Claude, Perplexity, Copilot and Gemini
  compare to organic search. Sessions are a daily-rotating salted hash of
  request attributes (never a stored IP or cookie), so the whole system remains
  GDPR-compliant by design; a machine-readable disclosure is published at
  `/.well-known/privacy-report.json`. A new VayuOS **Bot Shield & Analytics**
  panel surfaces the signature review queue, engagement metrics, top pages,
  live activity and the AI-vs-search comparison.
- **Governance.** New `bot-attack-intensity` error-budget (WARN) so a sustained
  bot attack is visible to the governance layer, charged with throttling so
  normal traffic never exhausts it.

### Upgrade Notes
- Two new migrations (`055`, `056`) add the `vayushield_*` and
  `vayuanalytics_sessions` tables; they apply automatically on first start and
  require no action. Bot protection stays disabled until you set `VAYUSHIELD=on`.
  See ADR-0111 for the full design and rationale.

## [3.5.0] — 2026-07-06

### Security
- **Brute-force throttling on mail authentication.** IMAP, SMTP and POP3 logins
  now share a per-mailbox throttle (`vmail.AuthThrottle`) that defeats online
  password guessing: each failed attempt on an account accrues a short,
  time-decaying delay before the next attempt (capped at 2s), so a guessing
  attack drops from thousands of tries per second to well under one — while a
  legitimate user with a typo waits only a few hundred milliseconds. It is
  deliberately a **delay, never a hard lockout**: a correct password always still
  authenticates (throttling is cleared instantly on success), and failures decay
  on their own, so an attacker can never use it to lock a real user out. The
  tracked set is bounded so a flood of distinct usernames cannot exhaust memory.
  Combined with the opt-in app-password-only 2FA enforcement (v3.4.x), this makes
  a stolen or guessed password effectively useless for reaching a mailbox.

## [3.4.1] — 2026-07-06

### Fixed
- **Mail sync restored — the v3.4.0 2FA enforcement is now off by default and
  lockout-proof.** In v3.4.0 the "app-password only" rule rejected the mailbox
  password for IMAP/SMTP/POP3 as soon as 2FA was enabled — which silently broke
  sync for anyone whose mail app still logged in with that password (it could not
  authenticate, so no mail arrived and manual sync failed). That was too
  aggressive. The enforcement is now **opt-in** (`VAYUMAIL_2FA_ENFORCE=1`, off by
  default, so passwords authenticate exactly as before) **and lockout-proof even
  when enabled**: it only retires the password once an app password actually
  exists for the mailbox, so there is always a working credential. To adopt the
  app-password model deliberately: mint a Device setup QR (VayuOS → Connect,
  which shows an app password and requires your 2FA code), set that app password
  in your mail app, then set `VAYUMAIL_2FA_ENFORCE=1`.

## [3.4.0] — 2026-07-06

### Security
- **2FA is now enforced on mail-app connections (app-password-only model).** When
  two-factor authentication is enabled for an identity — on the VayuMail mailbox
  *or* the CMS account behind it — the plain login/mailbox password **no longer
  authenticates IMAP, SMTP or POP3**. Those protocols cannot prompt for a second
  factor, so the enforcement point is the credential: the client must connect
  with a **rotating setup-QR app password**, and minting one already requires a
  fresh 2FA code (shipped in v3.2.0). This is the model Gmail and Outlook use,
  and it makes "connecting the app requires 2FA" actually true end-to-end. The
  check is conservative and fail-open: an account **without** 2FA is completely
  unaffected (its password works exactly as before), and any lookup error falls
  back to allowing the password rather than locking anyone out.

### Upgrade Notes
- **If you use 2FA and connect a mail app with your mailbox password, it will
  stop authenticating after this update — this is intended.** Reconnect the app
  using a **Device setup QR / app password**: VayuOS → Connect → "Device setup
  QR", enter your 2FA code, and scan (or paste the app password). Accounts
  without 2FA need no change.

## [3.3.0] — 2026-07-06

### Added
- **Continuous, polite cache warmer — drives the render cache toward a 100% hit
  ratio.** The public render cache is populated lazily, so the first visitor to
  any new or edited page pays the render cost and every page has a cold window.
  A new background warmer (`startCacheWarmer`) closes that window: it walks
  published pages and, for any whose cache entry is **missing or stale**, drives
  the *real* render handler once to prime the file — so the next real visitor is
  a cache hit. It is deliberately **incremental** (only missing/stale entries;
  never a full rebuild — a page already cached is skipped, and after an
  invalidation entries are re-warmed a few at a time), **polite** (one page at a
  time with a pause between each, on the read pool, off the hot path; a
  steady-state pass stops early once it meets a run of already-fresh pages, so it
  does almost nothing), and **invisible** (warmer requests record no analytics
  and do not count toward the hit/miss ratio, so the numbers reflect real
  visitors). Unlike the existing boot-time warm it runs **continuously** (default
  every 5 min), covers the **homepage** as well as posts, has **no 1000-article
  cap**, and produces cache files identical to a real request (it goes through
  the handler, not a separate render path). Tunable via `VAYUPRESS_CACHE_WARM=0`
  (disable), `VAYUPRESS_CACHE_WARM_DELAY_MS` (per-page pause, default 250ms), and
  `VAYUPRESS_CACHE_WARM_INTERVAL` (re-scan period, default 5m, min 1m).

## [3.2.0] — 2026-07-05

### Security
- **2FA step-up before minting a device credential.** When a mailbox has
  two-factor authentication enabled, generating or rotating its **device setup
  QR / app password** now requires a fresh authenticator code — even from an
  authenticated admin session. App passwords sign in to IMAP/POP3/SMTP and
  deliberately bypass the account's interactive 2FA (those protocols cannot
  prompt for a second factor), so the enforcement point is credential
  *issuance*: the same model Google and Microsoft use. The code is verified with
  `totp.Validate` **before** any existing credential is revoked, so a wrong or
  missing code never kills the current QR, and a successful step-up is recorded
  in the audit log (`vayumail.qr.2fa_ok`). Revoking a credential never needs a
  code. Mailboxes without 2FA are unchanged.

## [3.1.0] — 2026-07-05

### Added
- **Rich mail composer — Cc, Bcc, Reply-To, and file attachments.** The VayuOS
  Compose form gains show-on-demand **Cc/Bcc** and **Reply-To** fields and a
  **multiple-file attachment** picker. Attachments are assembled into a proper
  `multipart/mixed`, DKIM-signed message; Bcc recipients receive the mail without
  appearing in the headers; the exact assembled message (with attachments) is
  filed in Sent. Total attachment size is capped at a **generous 50 MB by
  default** — above Gmail's 25 MB and Outlook's 20 MB — tunable with
  `VAYUMAIL_MAX_ATTACH_MB`. Filenames are sanitised against header injection, and
  the send endpoint accepts multipart uploads (with the same CSRF + per-mailbox
  sender rules) or the legacy JSON body. PGP auto-encryption still applies to a
  single-recipient, no-attachment message.

### Note
- The **"CSRF token missing or invalid"** error when refreshing the setup QR (or
  clicking Update) is resolved by the persisted CSRF secret shipped in v3.0.0 —
  update to v3.0.0+ so tokens survive the restart.

## [3.0.0] — 2026-07-05

**VayuPress is a complete sovereign platform — website, blog, and private PGP
mail with an official mobile app — and the update system is now enterprise-grade
end to end.** This milestone consolidates the 2.9.x line: it hardens self-update
into something that works reliably on real, large, older servers, and closes the
last operational gaps found in production.

### Fixed
- **Admin actions no longer fail with "CSRF token missing or invalid" after a
  restart or self-update.** The CSRF secret was regenerated on every process
  start and never persisted, so every restart — including the one the in-app
  updater performs — invalidated all outstanding tokens, and the next click (e.g.
  **Update now**) was rejected. The secret is now **persisted beside the database**
  (`.vayu-csrf-secret`, override with `VAYU_CSRF_SECRET_FILE`) so tokens stay
  valid across restarts. Falls back to a per-process secret if the file can't be
  written.

### Changed
- **Release workflows publish semver pre-release tags** (those with a hyphen,
  e.g. `v3.0.1-rc1`) as GitHub pre-releases, so they appear only in the updater's
  "Include pre-release & development builds" channel, never in stable.

### Summary of the 2.9.x hardening rolled into 3.0.0
- **2.9.2** — offline, embedded IP→country lookup so live analytics show real
  countries on CDN-less deployments (no external GeoIP, IP never stored).
- **2.9.3–2.9.4** — one-click update runs the binary from a service-owned writable
  directory (and owns the *directory*, not just the file), so the atomic self-swap
  works under systemd hardening; plus an opt-in pre-release/development channel.
- **2.9.5** — release binaries are **fully static**, so a downloaded update runs
  on any Linux glibc (fixed a 502 on Ubuntu 20.04).
- **2.9.6** — the updater writes a **known-good systemd unit** every run
  (healing drift), **auto-rolls-back** a genuinely failed start, and logs to
  journald.
- **2.9.7–2.9.8** — the search index loads in the **background from the read
  pool**, so the web listener binds in ~1s regardless of database size (fixed a
  multi-minute startup 502 on a 12.7 GB database).
- **2.9.9** — the search index is **persisted and restored incrementally**, so a
  restart/update reconciles only what changed instead of rebuilding from scratch.

## [2.9.9] — 2026-07-05

### Added
- **The search index is now persisted and restored incrementally — no full
  rebuild on restart or update.** Previously the in-memory VayuFind index was
  rebuilt from scratch on every boot by scanning every published article (minutes
  on a large store). It now saves a snapshot (`$CACHE_DIR/search-index.gob`) and,
  on start, **restores it and reconciles only what changed** — re-indexing just
  new/modified articles and dropping removed ones via a cheap id+timestamp scan
  (no content re-read). A full rebuild happens only when there is no usable
  snapshot. Snapshots refresh every `VAYU_SEARCH_SAVE_MIN` minutes (default 10)
  and on shutdown. Safe by construction: any snapshot problem falls back to the
  same full rebuild as before, so persistence can never make search worse. See
  [ADR-0110](docs/adr/ADR-0110-incremental-search-index-persistence.md).

### Note
- This completes the "only process what's new on update" goal alongside the parts
  that were already incremental: migrations apply only new migrations, the render
  cache invalidates lazily per-page (ADR-0094), the search index is maintained
  incrementally by article event handlers, and the Go build recompiles only
  changed packages. Sitemap/RSS are generated on demand from an indexed query.

## [2.9.8] — 2026-07-05

### Fixed
- **Large sites now actually bind the listener at startup** (completes the
  v2.9.7 fix). Backgrounding the search-index load was not enough: `search.Load`
  read from the **single writer connection** (`db.SetMaxOpenConns(1)`), so its
  multi-minute full scan of every published article monopolised the one writer
  connection and the main startup thread then **blocked on its next write**
  (`UPDATE write_jobs`) — the process hung at "mode journal open" and the web
  listener never came up. Search now reads from the **read pool**
  (`db.Reader()`, several `query_only` connections), so the scan runs without
  blocking writes and the listener binds in about a second on a database of any
  size. Search is read-only, so this is safe.

## [2.9.7] — 2026-07-05

### Fixed
- **Large sites no longer 502 for minutes on every start (and no longer look
  like a crash).** The built-in search index (VayuFind) was loaded
  **synchronously at startup by scanning every published article** — on a large
  database (e.g. 12.7 GB) that took *minutes*, and it ran **before the HTTP
  listener bound**, so the site returned 502 the whole time and a perfectly
  healthy, slow-starting process looked like a failed update. The index now loads
  in a **background goroutine**, so the web listener binds within a second
  regardless of database size; search returns empty for the brief moment until
  the first load finishes, then serves normally (it is maintained incrementally
  thereafter). Diagnosed from a real deployment where the process sat at "mode
  journal open" for 6 minutes and was killed by `systemctl` (signal=TERM), not by
  OOM — RAM was 90% free.
- **The updater no longer rolls back a slow-but-healthy start.**
  `scripts/update-vayupress.sh` now distinguishes a process that has genuinely
  **exited** (a real crash → roll back) from one that is **still running but not
  yet answering** `/health` (a large DB warming up → leave it running). It only
  auto-rolls-back on an actual exit, and otherwise reports that the service is
  coming up.

## [2.9.6] — 2026-07-05

### Fixed
- **Update no longer leaves a site crash-looping when the systemd unit has
  drifted.** A binary that runs fine by hand but exits under systemd is almost
  always a stale/hand-edited unit whose `ProtectSystem` sandbox omits a
  directory the current binary writes at startup — most often `STATIC_DIR`,
  where the binary syncs its embedded admin assets on boot. The sandbox denied
  that write and the process exited right after "mode journal open", so the site
  502'd even though the binary was healthy. `scripts/update-vayupress.sh` now
  writes a **complete, known-good systemd unit on every update** (correct
  `ReadWritePaths` incl. `STATIC_DIR`, `CAP_NET_BIND_SERVICE`,
  `StateDirectory=vayupress`), backing up the previous one, so the runtime can
  never drift away from what the binary needs.

### Added
- **Automatic rollback on a failed update.** After restarting, the shell updater
  polls `/health/live` for up to 45s; if the new version never becomes healthy it
  **restores the previous binary and restarts automatically**, bringing the site
  back on the last-known-good version instead of crash-looping, then reports the
  failure for offline investigation. An update can no longer leave a site down.
- **Service logs now go to journald** (`StandardOutput/Error=journal`,
  `SyslogIdentifier=vayupress`) in both the deploy and update units, so a startup
  failure is visible with `journalctl -u vayupress` instead of hidden in a file —
  the observability gap that made this class of failure hard to diagnose.

## [2.9.5] — 2026-07-05

### Fixed
- **In-app one-click update no longer takes the site down with a 502 on older
  Linux.** Release binaries were built on the CI runner's new glibc (2.39) with
  **dynamic** CGO linking, so the binary the in-app updater downloads crashed on
  startup on an older server (e.g. Ubuntu 20.04 / glibc 2.31) — nginx then
  returned 502. Release binaries are now **fully statically linked**
  (`-linkmode external -extldflags -static` with the `netgo`/`osusergo` tags so
  DNS and user lookups use Go's pure-Go resolvers, plus
  `sqlite_omit_load_extension`), so a downloaded release runs on **any** Linux
  regardless of glibc. Both release workflows now also fail the build if the
  produced binary is not static.
- **Shell updater reported a stale version** (e.g. "Building version 2.8.1").
  `scripts/update-vayupress.sh` derived the version from the fallback default in
  `main.go`, which lags behind. It now reads the canonical `.release-version`
  file — the same source of truth CI stamps releases from — and the `main.go`
  fallback default is kept in sync.

## [2.9.4] — 2026-07-05

### Fixed
- **One-click update failed with "permission denied writing to
  /var/lib/vayupress/bin" even after moving to the writable layout.** The v2.9.3
  installers chowned the binary *file* to the service user but not its
  *directory*. The atomic swap creates a temp file in that directory and renames
  it over the binary, so it needs write permission on the **directory** — a
  root-owned directory blocks the swap regardless of the file's owner.
  `scripts/deploy-vayupress.sh` and `scripts/update-vayupress.sh` now chown the
  binary **directory** (`/var/lib/vayupress/bin`) to the service user, the
  reference `deploy/vayupress.service` gains `StateDirectory=vayupress` so
  systemd guarantees that ownership, and the in-app error now prints the exact
  one-line fix (`sudo chown -R <user>:<user> <dir>`) instead of generic advice.

### Added
- **Pre-release / development update channel.** A new *"Include pre-release &
  development builds"* checkbox on the Update & Backup panel makes the update
  check and one-click apply also consider the newest **unreleased** GitHub
  pre-release, not just stable releases — useful for early testing. Off by
  default; verification is unchanged (SHA-256 checksum always, Ed25519 signature
  when a release key is pinned). Backed by `update.CheckLatestChannel` and
  `ApplyOptions.IncludePrerelease`. See
  [ADR-0109](docs/adr/ADR-0109-writable-binary-location-for-self-update.md).

## [2.9.3] — 2026-07-04

### Fixed
- **One-click updates now work on a hardened, non-root deployment** — the
  "Cannot install the update because the binary location is not writable:
  permission denied writing to /usr/local/bin" failure is resolved. The binary
  ran from root-owned `/usr/local/bin`, which the non-root service can never
  write: `ReadWritePaths=/usr/local/bin` only relaxes systemd's sandbox (MAC),
  not the Unix ownership (DAC), so the atomic self-swap was always denied.

### Changed
- **The binary now runs from a service-owned writable directory**
  (`/var/lib/vayupress/bin/vayupress`), with `/usr/local/bin/vayupress` kept as a
  convenience symlink. The atomic self-swap needs no elevated permission and no
  sandbox exception. `scripts/deploy-vayupress.sh` and
  `scripts/update-vayupress.sh` install into this layout and migrate legacy
  installs (replace the real `/usr/local/bin` binary with the symlink and, via a
  systemd drop-in, repoint `ExecStart` at the writable path). The reference
  `deploy/vayupress.service` and its `ReadWritePaths` were updated to match.
- **The updater is symlink-aware** (`update.ResolveInstallPath`): the swap, the
  `.bak` rollback artifact, the writability preflight, and the post-update
  re-exec all target the resolved real file, never a launch-time symlink — so
  the symlink layout updates in place with no ownership or sandbox change.
- **Clearer failure guidance.** When the binary still sits in a directory the
  service cannot write, the admin UI now states the real cause and the permanent
  fix (run from a service-owned directory / re-run the installer) instead of the
  incomplete "add ReadWritePaths" advice. See
  [ADR-0109](docs/adr/ADR-0109-writable-binary-location-for-self-update.md).

## [2.9.2] — 2026-07-04

### Fixed
- **Live analytics now shows real countries on CDN-less sovereign
  deployments** (previously every visitor read as *Unknown*). Country was only
  ever populated from a reverse-proxy geo header (`CF-IPCountry` and friends);
  a bare-VPS deployment with no CDN injects none, so the live map stayed empty.
  It is not a regression — the geo code was byte-identical across releases — but
  it made the sovereign default look broken.

### Added
- **Offline, embedded IP→country resolution** (`internal/geoip`, backed by the
  compiled-in `github.com/phuslu/iploc` table). When no proxy header supplies a
  country, the visitor country is resolved in-process from the trusted-proxy-aware
  client IP. It performs **no network I/O and contacts no third party** — the
  no-telemetry / no-phone-home posture is unchanged — and the visitor IP is used
  **only** for the lookup and **never stored** (analytics keeps the country code
  alone). Proxy headers still take precedence when present; region/city remain
  header-only. Operators who want nothing derived from the IP set
  `ANALYTICS_GEOIP=off`, restoring the prior behaviour exactly. See the
  *Amendment 2026-07-04* to [ADR-0097](docs/adr/ADR-0097-vayuanalytics-no-pii.md).

## [2.9.1] — 2026-07-04

### Security
- **Reflected-XSS fully closed on the HTMX comment-moderation fragment
  (CodeQL #43).** The moderation `status` was still written into the row's
  `data-status="…"` attribute without escaping — CodeQL does not credit the
  `status != "…"` equality guard, so `r.FormValue("status")` traced to the
  response as reflected XSS. The handler now maps the status to a compile-time
  constant (`canonicalCommentStatus`) — so the reflected value is provably not
  request-derived — and `osCommentPill` escapes it in both the attribute and the
  text. Combined with the existing hex-only comment-id allowlist, no request
  input reaches the fragment unescaped.

### Fixed
- **CI: `golangci-lint` step restored.** `go.mod` pinned `toolchain go1.26.4`,
  which `golangci-lint@latest` (built with Go 1.25) refuses to lint —
  "the Go language version used to build golangci-lint is lower than the targeted
  Go version (1.26.4)" — failing the lint job outright. Lowered the toolchain to
  `go1.25.1` (the module only requires `go 1.25.0`; build and full test suites
  pass unchanged), and cleared the `errcheck` findings the linter then surfaced
  (unchecked `crypto/rand.Read` across collections, preview, webmention,
  scheduler, members, webhooks, newsletter). `golangci-lint run ./...` is clean.

## [2.9.0] — 2026-07-04

Self-hosted HTMX in the binary with CSP-clean progressive enhancement across the
VayuOS admin panel; a first-party mail-autoconfig endpoint plus verified/hardened
WKD interop with VayuMail-Mobile; and a round of security and enterprise-grade
hardening (reflected-XSS fix, discovery-endpoint rate limiting, WKD caching,
double-submit guards, accessibility announcements, operator metrics, and a
race-clean integration suite in CI).

### Changed

- **Comments manager: moderation is now an in-place HTMX update (no page
  reload).** Approve/Reject/Spam were a `fetch()` that reloaded the whole page.
  They now POST to a new `POST /os/api/comments/{id}/status-fragment` that
  moderates the comment and returns an HTML fragment — the row's new action
  buttons (main swap) plus **out-of-band** updates of its status pill and the
  pending/approved counts. The status filter reads each row's live status from
  its pill (`data-status`) and re-applies on `htmx:afterSwap`, so a moderated
  row moves to the right tab instantly. Because the buttons/pill depend only on
  `(id, status)`, the endpoint re-renders them without re-fetching the comment.
  The JSON `PUT /os/api/comments/{id}/status` endpoint remains for API clients;
  CSRF and the strict CSP are unchanged.
- **Posts manager: pin/unpin is now an in-place HTMX update (no page reload).**
  The Pin/Unpin control matched the publish toggle's old behaviour (`fetch()` +
  full-page reload). It now POSTs to `POST /os/api/posts/{slug}/pin-fragment`,
  which flips the `featured` flag and returns the flipped button plus an
  **out-of-band** update of the row's "📌 Pinned" badge — so every single-post
  action on the page (publish, pin) updates its row in place. The JSON
  `/os/api/posts/pin` endpoint remains for the editor/bulk paths; CSRF and the
  strict CSP are unchanged.
- **Posts manager: publish/unpublish is now an in-place HTMX update (no page
  reload).** The Publish/Unpublish button on `/os/posts` was a `fetch()` that
  reloaded the entire page on success. It now uses HTMX: the button POSTs to a
  new `POST /os/api/posts/{slug}/status-fragment` endpoint that flips the status
  and returns an HTML fragment — the flipped button plus an **out-of-band** swap
  of that row's status pill — so only the affected row updates, instantly, with
  no full reload. The change is surgical: the JSON `/os/api/posts/status`
  endpoint still backs the bulk actions, and pin/delete/select are untouched. A
  nonce-gated `htmx:configRequest` shim mirrors the existing double-submit CSRF
  cookie into the `X-CSRF-Token` header for every admin `hx-*` request, so HTMX
  writes pass the same `CSRFTokenMiddleware` as the `fetch()` controls — the
  strict admin CSP (`script-src 'self'`, `style-src 'self'`) is unchanged.

- **CI now runs the integration test suite.** The `-tags integration` suite
  (real-DB handler tests) previously never ran in CI — it wasn't even compiled.
  After unbreaking its build, CI gained a dedicated `go test -tags integration
  ./...` step, so the HTMX fragment endpoints, editor save/create paths and other
  DB-backed flows are now guarded on every push. (It runs without `-race`
  because the shared test harness mutates the process-global config per test, a
  harness-only race that never occurs in production; the race-clean unit suite
  still runs under `-race`.)
- **Operator metrics for content operations.** Three new counters —
  `comments_moderated`, `post_status_toggles`, `post_pin_toggles` — are exported
  on the JSON stats and Prometheus `/metrics` endpoints. They count every action
  regardless of path (HTMX fragment, JSON API, or bulk), incremented at the
  chokepoints (`comments.Moderate` for moderation; the post handlers for
  publish/pin), so operator dashboards can track editorial activity. (HTTP-level
  tracing, latency histograms and structured logs already covered the endpoints.)
- **Admin HTMX actions are announced to assistive tech (WCAG 2.2 AA).** A
  visually-hidden `aria-live` region (`#vp-live`) plus an `htmx:afterRequest`
  handler announce the outcome of every `hx-*` action ("Change saved." /
  "Content refreshed."), so screen-reader users get the same confirmation sighted
  users get from the in-place update.
- **Admin HTMX writes are double-submit-safe.** Every `hx-*` action button
  (publish/unpublish, pin/unpin, comment moderation, activity refresh) now
  carries `hx-disabled-elt="this"`, so it disables itself while the request is
  in flight — a fast double-click can no longer fire two writes, and the greyed
  button is a clear "working…" cue.
- **Admin HTMX actions never fail silently.** The admin layout now wires a
  global `htmx:responseError` / `htmx:sendError` handler that surfaces any failed
  `hx-*` write (CSRF expiry, 5xx, dropped connection) as an error toast via the
  existing toast API — restoring the failure feedback the pre-HTMX `fetch()`
  controls had. Nonce-gated alongside the CSRF header shim, so the strict CSP is
  unchanged.

### Added

- **First-party VayuMail autoconfig (JSON) for email-only onboarding.** New
  public endpoint `GET /.well-known/vayumail/autoconfig.json` publishes the mail
  server's own IMAP/POP3/SMTP coordinates (host, ports, TLS mode) as an
  easy-to-parse JSON — the same public settings as the existing Mozilla/
  Thunderbird XML (`/.well-known/autoconfig/mail/config-v1.1.xml`), but shaped
  for the VayuMail app, which fetches it to configure an account from just an
  email address. No secrets (same data as the Connect tab); long-cached; `404`s
  when VayuMail is inactive. The document shape is versioned
  (`vayumail-autoconfig/1`) and pinned byte-for-byte to the VayuMail-Mobile
  client by a shared contract test, so server and app can never silently drift.
- **Self-hosted HTMX in the binary (no CDN).** [htmx](https://htmx.org) 2.0.4
  (0BSD) is now vendored at `static/js/htmx.min.js`, compiled into the binary via
  the module-root `//go:embed static` → `StaticFS` and written to `STATIC_DIR` on
  boot (`syncEmbeddedStatic`), so it ships inside the executable and survives a
  binary-only self-update with no separate asset copy (ADR-0099). It is served
  same-origin at `/static/js/htmx.min.js` by `handleHTMXJS` (mirroring the theme
  scripts: `application/javascript`, long-cached, content-hash cache-buster), so
  it satisfies the strict `script-src 'self'` CSP with **no nonce and no external
  host**. The VayuOS admin layout loads it deferred and sets
  `<meta name="htmx-config" content='{"includeIndicatorStyles":false}'>` so htmx
  never injects an indicator `<style>` — `style-src 'self'` stays intact. First
  use: the Members "Recent activity" card gains an `hx-get` Refresh button that
  live-reloads just the feed fragment (`/os/members/activity`) with no page
  reload and no bespoke JavaScript; without htmx the card still renders the
  server-side feed, so it degrades gracefully. See
  [ADR-0107](docs/adr/ADR-0107-self-hosted-htmx.md).

- **Integration test suite is race-clean and runs under `-race` in CI.**
  `render.CachePurge` fires the sitemap/feed/robots regenerations as
  fire-and-forget goroutines; across the integration suite those could outlive a
  test and race the next test's `config.Load` (and the temp-dir cleanup) — a
  harness-level data race. They are now tracked in a WaitGroup with a
  `render.WaitForPurges()` drain that `newTestHarness` calls on teardown, so
  every test's async writers finish deterministically before the next begins.
  Production behaviour is unchanged (it never waits). The CI integration step now
  runs with `-race`.

### Security

- **Reflected-XSS hardening on the HTMX fragment endpoints (CodeQL #43).** The
  comment-moderation and post publish/pin fragment endpoints echo their path
  parameter (comment id / slug) into the returned HTML. On top of HTML-escaping,
  the id/slug is now validated against a strict allowlist *before* it is used or
  reflected — comment ids must be hex (`^[0-9a-f]{1,64}$`), slugs must pass
  `api.IsValidSlug` — so any value carrying HTML/URL/CSS metacharacters is
  rejected with `400` at the source. Tests assert a `<script>` / `<img onerror>`
  payload is rejected on every fragment endpoint and never reflected.
- **WKD responses are cacheable + conditionally revalidated.** The Web Key
  Directory handler now emits a strong `ETag` (over the key bytes) and a
  `Cache-Control: public, max-age=3600`, and honours `If-None-Match` with a
  `304 Not Modified`. Clients (and other MUAs doing key discovery) stop
  refetching the full key on every lookup; correctness across key rotation is
  preserved because a changed key yields a changed ETag, so a revalidation
  returns the new key rather than a stale one.
- **Public discovery endpoints are rate-limited (DoS hardening).** The
  unauthenticated `.well-known` discovery routes — WKD
  (`/.well-known/openpgpkey/*`), the Mozilla mail autoconfig XML, and the
  first-party `vayumail/autoconfig.json` — are now throttled per client IP via a
  dedicated, generous limiter (default 240 requests / 10 min, override with
  `DISCOVERY_RATE_LIMIT`; trusted IPs bypass). The WKD handler scans the whole
  keystore per request, so an unbounded query rate was a DoS amplifier; the cap
  is high enough that ordinary mail-client key discovery is never affected, and
  over-budget callers get a plain `429` with `Retry-After`. Bucket memory is
  bounded by the existing sweeper.

### Verified

- **WKD PGP interop (VayuPress ↔ VayuMail-Mobile).** Confirmed the Web Key
  Directory **direct method** — `/.well-known/openpgpkey/hu/<hash>?l=<local>`,
  with no domain path segment — is mounted on the public router. The
  `/.well-known/openpgpkey/*` wildcard route reaches `ServeWKD`, which matches on
  the `/hu/` path segment, so both the direct method and the advanced method
  (`/.well-known/openpgpkey/<domain>/hu/<hash>`) resolve, along with the WKD
  `policy` file. Added `TestWKDDirectMethodServesKey`, an end-to-end check that a
  generated key is discoverable at the exact URL VayuMail-Mobile fetches and that
  the served bytes parse back into the expected OpenPGP identity. No app change
  needed on either side.
- **WKD hash contract locked with VayuMail-Mobile.** Added
  `internal/vayuos/pgp/wkd_contract_test.go`, an expanded known-answer vector
  table for the published WKD path hash that is kept byte-for-byte identical to
  the client's `test/wkd_contract_test.go`. VayuPress publishes a key at
  `/hu/<hash>` and the app looks it up at a hash it computes with its own
  z-base-32/SHA-1 code; the shared table turns any drift between the two into a
  red build on whichever side moved, rather than silent key-discovery breakage.

### Changed

- **Docs & site: VayuMail Mobile + one-click-stack showcase.** The README and
  marketing site now feature the official Android app
  ([johalputt/VayuMail-Mobile](https://github.com/johalputt/VayuMail-Mobile)) and
  an elegant "one command → website + blog + PGP mail + mobile app" showcase, and
  the README recommends a VPS with open mail ports (Contabo VPS 10) for clean
  mail deliverability.

### Fixed

- **One-click update no longer fails silently.** Two problems made the in-app
  "Update now" button appear to fail every time. (1) The panel read the error
  message from the wrong JSON field (`detail`/`title`, which never existed), so
  every failure collapsed to a bare "Update failed" with no reason — it now
  reads the real `error.message`, surfacing the actual cause and its fix
  (across check, apply, rollback and restore). (2) The sandboxed service runs
  with `ProtectSystem=full`/`strict` and a `ReadWritePaths` that excluded the
  binary's own directory, so the atomic self-swap into `/usr/local/bin` was
  denied. The service unit and installer now grant the binary directory as
  writable, and `update-vayupress.sh` installs an idempotent systemd drop-in
  (`ReadWritePaths=<bindir>`) so existing installs get seamless in-app updates
  after the next run — no manual terminal steps thereafter.

### Security

- **Backup restore Zip-Slip guard is now inline (CodeQL).** The containment
  check was moved from a helper back into `Extract`, directly next to the file
  operations it protects and gating the exact resolved path — the pattern
  CodeQL's `go/zipslip` recognises as a sanitizer. Behaviour is unchanged (the
  v2.8.1 fix already blocked traversal); this clears the static-analysis alert
  and adds an end-to-end test that drives `Extract` with crafted `..` archive
  entries and asserts nothing is written outside the destination.

## [2.8.1] - 2026-07-02

### Security

- **Backup restore: hardened against path traversal ("Zip Slip").** The
  encrypted-backup extractor now resolves each archive entry against the
  destination and requires the *joined* path to stay strictly inside it
  (`safeJoin`), instead of a string check on the raw entry name. This defeats
  `..` traversal, absolute paths and crafted names alike, so a malicious backup
  can never write outside the restore directory. Regression test added.
  (Reported by CodeQL in `internal/backup/backup.go`.)

## [2.8.0] - 2026-07-02

### Added

- **Multi-author.** Articles can now be attributed to individual staff users
  (`articles.author_id`, migration `054`, indexed). The public byline and author
  page reflect the **per-post author** — the byline links to that author and the
  author page lists that author's posts. A post is attributed to whoever creates
  it (never re-attributed on later edits), and posts with no explicit author fall
  back to the site-wide author, so the existing catalogue is unchanged. The
  primary author's page keeps using the proven, index-friendly query (safe on a
  234k-post site); secondary authors filter on the indexed `author_id` column.
  The editor's Post-settings panel now has an **Author picker** (a select of all
  staff users) so an operator can reassign a post's byline at any time.
- **Themes now transform the whole design.** Every gallery theme changes the
  entire site — navigation, hero, post feed, article pages, and footer — not
  just colours. The five layout archetypes (Minimal / Classic / Magazine /
  Editorial / Bold) were rebuilt as full-site, Ghost-style designs: flat colour,
  hairline rules, generous whitespace — **zero gradients, zero neon**. A new
  **Typography** design option (`fontpair`) gives every theme a selectable
  typographic voice — Elegant (serif headings + sans body), Literary, Modern,
  Humanist, Grotesk, Typewriter — using **system fonts only** (no CDNs), and
  each colour preset ships its own default pairing, so deploying a theme changes
  the type personality along with the palette. Studio + Store previews pick the
  new option up automatically through the existing options pipeline.

- **Ghost-class editor.** The post editor gains a whole-document **Markdown
  mode** (`⌘⇧M`, toolbar button) alongside the HTML source mode: standard blocks
  serialise to clean Markdown and parse back losslessly; rich blocks (galleries,
  embeds, tables, …) ride an inert sentinel so a Markdown round-trip never loses
  anything. **Images by link now work end-to-end** — paste any https image URL
  (Unsplash, Pixabay, …) into an image block and it renders on the live site
  (public CSP `img-src` now admits `https:`; scripts/styles/frames stay locked
  to `'self'`). The writing surface was restyled Ghost-clean: centered 46rem
  measure, larger serif-ready title, book-size text, chrome-free blocks whose
  controls appear only on hover, and an elegant full-canvas drop indicator for
  drag-and-drop image upload (drop/paste-to-upload already built in).

- **VayuMail: clean namespace, rotating setup QR, app passwords.** The whole
  mail panel moved to a clean **`/os/vayumail`** namespace (inbox, compose,
  accounts, connect, sent, PGP, security; DKIM/DNS at `/os/vayumail/dns`) —
  every legacy `/os/vayuos/*` URL 308-redirects so nothing breaks. The Connect
  tab gains a **rotating device setup QR**: one tap mints a per-device **app
  password** (Argon2id-hashed, shown exactly once) wrapped in a scannable QR
  with all server settings — a phone signs in from one scan without ever
  seeing the mailbox's main password, and rotating instantly revokes the
  previous QR, so even a photographed code goes stale. App passwords
  authenticate IMAP/POP3/SMTP alongside the main password and are revocable
  per device. CSRF now also accepts a `csrf_token` form field (same
  double-submit + HMAC checks) so secure no-JS forms work. PGP remains
  automatic end-to-end: keys are auto-generated per mailbox, person-to-person
  sends auto-encrypt when the recipient's key is known, and IMAP decrypts
  transparently for the owner.

- **Business websites (VayuOS → Website).** VayuPress now serves a full
  business website alongside the blog and VayuMail: **11 elegant templates**
  (restaurant, café, shop, portfolio, agency, school, clinic, salon, gym,
  professional firm, hotel) — modern-minimalist, flat colour, system fonts,
  zero gradients. Deploy, edit content (about, offerings with prices, gallery,
  hours, contact) and switch designs entirely from VayuOS, with a live preview
  at `/site`. **Hosting topology is the operator's explicit choice** and
  updates never change it: the default keeps the blog at the root (existing
  installs are untouched); business mode serves the website at `domain.com`,
  the blog at `blog.domain.com`, and mail at `mail.domain.com`. The installer
  now issues one Let's Encrypt certificate covering the root, `www.`, `blog.`
  and `mail.` hosts (with graceful fallbacks) and renews it automatically —
  no terminal needed after setup.
- **Operator-only encrypted backups.** `vayupress backup` / `vayupress
  restore` capture the entire data directory — database, settings, media,
  VayuMail maildirs, PGP key store — as a single file encrypted with
  **AES-256-GCM keyed by Argon2id** from a passphrase only the operator
  knows. Every chunk is independently authenticated: a copied backup is
  unreadable and tamper-evident with any modern tool; the wrong passphrase
  restores nothing. Verified by round-trip, tamper and leak tests.

## [2.7.0] - 2026-06-30

### Added

- **Ghost-style author byline + author page with posts.** Posts now show the
  author as a clean byline (avatar + name) directly **under the title**, linking
  to the author's page; the old author box after the post body is removed. The
  public **author page is redesigned** — a minimalist header (avatar, role, bio,
  socials) followed by a list of that author's posts — so clicking a byline opens
  the author and their writing. A light/dark toggle is available on the author
  page too.

### Changed

- **Public light/dark theme toggle now actually switches the whole page.** The
  toggle button existed but only part of the page responded to it — the article
  tokens (background, text, accents) reacted to the OS setting, not the button.
  Article CSS now responds to the `[data-theme]` attribute the toggle sets, so
  light/dark applies to the entire article. Clean, no gradients.

- **Colourful analytics charts (still zero third-party, GDPR-safe).** The
  Analytics console now visualises the existing no-PII data with server-rendered,
  CSP-safe charts instead of plain tables: a two-series **traffic chart**
  (pageviews + unique visitors with gridlines), a **device donut**, and colour
  **bar charts** for top pages, referrers, browsers, operating systems,
  campaign sources, custom events and geography (with country flags). An 8-colour
  palette that adapts to light/dark, share percentages, and thousands-separated
  counts make the numbers easy to read at a glance. Everything is drawn as static
  SVG/HTML with CSS classes (no external JS, no CDNs, no inline styles), so the
  strict admin CSP and the cookieless/aggregate privacy posture are unchanged.

- **True one-command install.** `deploy-vayupress.sh` now takes `DOMAIN`/`EMAIL`
  from the environment (or prompts for them) and generates the API key
  automatically — no file editing. It obtains a single Let's Encrypt certificate
  covering the site **and `mail.<domain>`** (so the built-in mail server is
  trusted by phones), copies it to a service-readable path with an auto-renewal
  hook, grants the service `CAP_NET_BIND_SERVICE` and opens the firewall for the
  mail ports (so VayuMail works out of the box), and prints the auto-created
  administrator credentials at the end. Installation docs rewritten so a
  non-technical operator can deploy with one command and then run everything from
  the VayuOS web console — no further terminal use. The first admin is
  `admin@<domain>` with a strong random password and a forced change on first
  login.

### Changed

- **Redesigned sign-in — clean, calm, minimalist.** The VayuOS login and
  forced-password-change pages are now a single centered card on a clean
  background (no gradients, no animation), with an unobtrusive **light / dark /
  auto** theme switch (auto follows the operating system, and is the default).
- **The whole VayuOS console now themes correctly, and defaults to auto.**
  Fixed a long-standing bug where the admin theme never applied — `data-theme`
  sat on `<html>` while the `.vp-os` tokens were on `<body>`, so the console was
  stuck dark regardless of the setting. The theme attribute now lives on the
  `.vp-os` element, a **light / dark / auto** toggle was added to the topbar (it
  persists to `admin.theme`), the default is **auto** (follows the OS), and
  "auto" genuinely follows the system instead of always being light. Converted
  the remaining hardcoded dark surface/border colours to theme tokens so light
  mode renders cleanly.

## [2.6.0] - 2026-06-30

### Added

- **Per-mailbox storage quotas.** An administrator can allot how much space each
  mail account may use, from the Mail accounts page — set a quota (in MB; 0 =
  unlimited) when creating an account or inline per account, with live usage
  shown next to it. Enforcement covers every way a mailbox grows: inbound
  delivery over quota is refused (the sending server gets a temporary failure and
  retries/bounces), and sending mail or saving a draft is blocked when the
  mailbox is full (both file a copy into the mailbox). Each user also sees a
  storage usage bar on their own Mailbox page (green → amber → red as it fills),
  with a clear "mailbox full" warning at the limit. New `quota_bytes` column on
  `vayumail_accounts` (defaults to 0/unlimited, so existing accounts are
  unchanged).

### Fixed

- **Thunderbird for Android / K-9 now syncs its inbox.** These clients only sync
  folders they see as **subscribed** and explicitly `SUBSCRIBE` to folders during
  setup. The IMAP server kept no subscription state, so it advertised no
  `\Subscribed` flag (the client treated every folder as unsubscribed and synced
  nothing) and replied `BAD` to `SUBSCRIBE` (which K-9 treats as fatal).
  LIST/LSUB now advertise `\Subscribed` for every standard folder, and
  `SUBSCRIBE`/`UNSUBSCRIBE` are accepted as no-ops. (Gmail and desktop
  Thunderbird were unaffected and already worked.)
- **Mobile mail apps that wouldn't sync now have a clear cause surfaced.** When a
  trusted mail certificate is active but does **not** cover the hostname clients
  are told to connect to, desktop apps let the user accept the mismatch while the
  Gmail app and Thunderbird for Android silently refuse — the "desktop syncs,
  mobile doesn't" trap. The Connect tab now detects this, lists the names the
  certificate is actually valid for, and gives the exact fix (point
  `VAYUOS_MAIL_HOSTNAME` at a covered name, or reissue the cert to include the
  mail host). (Mobile IMAP folder discovery itself was already fixed via
  extended-LIST/ENABLE support, in v2.5.3.)
- **"Check for updates" no longer fails with a bare "unable to check".** The
  update checker now points at the canonical repository name (avoiding a 301
  redirect on every API call that could fail under the SSRF-guarded transport),
  reports GitHub API rate limiting (the 60-requests/hour unauthenticated cap)
  with a clear, actionable message, falls back to the releases list when
  `releases/latest` is unavailable, and accepts an optional `VAYU_UPDATE_TOKEN`
  (or `GITHUB_TOKEN`) to raise the rate limit to 5000/hour.

### Security

- **Mail accounts are mail-only by default (least privilege).** Creating a
  VayuMail account defaulted to the *author* role, which carries author **console**
  access — so a mailbox meant for mail-only use also showed content tabs. New
  accounts now default to the **mailbox** role (mail only, no console, own inbox
  only), an unspecified/invalid role falls back to mailbox instead of author, and
  the create form lists mail-only roles first with a note. Only an explicitly
  chosen administrator/editor/author role grants console access. Existing
  accounts can be downgraded from the role dropdown on the Mail accounts page.
  (The per-mailbox scoping that already stopped one user reading another's mail
  is unchanged; this closes the over-broad *visibility* gap.)

## [2.5.4] - 2026-06-30

### Fixed

- **Mailbox "Mark as read" no longer 500s.** A message id goes stale the instant
  the message is read, flagged, or moved (its Maildir file moves `new/`→`cur/` or
  its flag suffix changes). The panel held the old id, so the next action joined
  a path that no longer existed and failed with a 500. Mark/move/delete/read now
  resolve the message by its stable base name across `new/` and `cur/`.

### Added

- **Mail auto-marks read on open.** Opening a received message marks it read,
  like any mail client (Drafts/Sent are left untouched).
- **Pin (flag) messages.** Pin/unpin from the message view or the list (Maildir
  `F` flag); pinning is independent of read state.
- **Move mail between any folders.** The message view gains a "Move to…" folder
  picker covering every standard folder (not just Junk/Trash/Archive).
- **Bulk mailbox actions.** Select multiple messages in the list to mark
  read/unread, pin, move to a folder, or delete in one go, with a select-all box.
  One stale id no longer fails the whole batch.

## [2.5.3] - 2026-06-29

### Fixed

- **VayuMail IMAP now syncs in Thunderbird for Android / K-9.** Modern clients
  discover folders with RFC 5258 extended-LIST syntax (e.g.
  `LIST (SUBSCRIBED) "" "*" RETURN (SPECIAL-USE)`). The server mistook the
  leading `(SUBSCRIBED)` selection option for the mailbox pattern, so LIST
  returned only the hierarchy delimiter and no INBOX — the client found no
  folders and synced nothing. LIST/LSUB now parse the selection-options and
  `RETURN (...)` groups correctly, and the server advertises `LIST-EXTENDED`.
- **IMAP `ENABLE` is now supported.** Thunderbird for Android / K-9 send `ENABLE`
  during setup; it previously returned `BAD`, which some clients treat as a fatal
  handshake error. The server now acknowledges it (RFC 5161).

## [2.5.2] - 2026-06-29

### Fixed

- **One-click update no longer fails on large databases.** The in-app pre-update
  backup gzips the whole database inside the update request, which on a multi-GB
  database could swap-thrash a small VPS and make every update attempt fail or
  hang. The "Back up the database first" checkbox now defaults **off** when the
  database exceeds 2 GiB (with an inline explanation), and the apply endpoint
  refuses the inline backup fast — before downloading anything — when the DB is
  that large, telling the operator to untick it or snapshot/Export first. A
  binary update never rewrites the database and the previous binary is kept for
  rollback, so updating with the backup off is safe.

## [2.5.1] - 2026-06-29

### Added

- **Threaded comment replies.** Readers can reply to a specific comment; the
  public widget renders one level of indented replies with per-comment reply
  forms. Backed by a new `comments.parent_id` column (migration `052`); a reply
  must point at an existing comment on the same article.
- **Reply notifications.** When a reply is approved, the author of the parent
  comment is emailed (new operator-editable `comment_reply` template) — only if
  they are a member with reply notifications on and it isn't a self-reply. New
  `members.reply_notify` preference (migration `053`, default on).
- **Member Activity tab.** The VayuPortal account panel gains a "Your comments"
  view listing a member's own comments with a live / awaiting-review / not-
  approved status badge, linking back to each thread
  (`GET /api/v1/members/comments`, served from the read pool).
- **Member Notifications settings.** The account portal adds a Notifications
  section: toggle reply-notification emails and the newsletter. Saving the
  newsletter toggle now syncs the public subscriber list (subscribed *confirmed*
  — no double opt-in, since the member is already verified — or unsubscribed) so
  the choice affects real broadcasts.
- **Beautiful welcome & sign-in emails.** The `magic_link` and `welcome` emails
  are redesigned as polished, emoji-rich HTML cards. A brand-new member now
  receives the welcome email **in addition** to their sign-in link.

### Changed

- **Signed-in nav.** When a member is signed in, the public "Sign in / Sign up"
  links collapse to the member's name, which opens the account panel (with
  sign-out). Logged-out behaviour is unchanged.
- **VayuMail sign-in shows the console immediately.** The portal's VayuMail login
  response now carries the mailbox role, so the "Open VayuOS console" (or "Open
  VayuMail") shortcut appears at login instead of only after a page reload.
- **Comments restyled to match the theme.** The comment section now inherits the
  active theme's colours via Pico custom properties — avatars, rounded cards,
  hover lift and indented reply threads — in both light and dark mode.
- **Website decluttered.** The marketing site leads with *why VayuPress exists*,
  drops version stamps from every feature, and trims the feature wall, comparison
  table, tool list and screenshot gallery to what a visitor actually needs.

### Fixed

- **IndexNow only pings published posts.** Auto-submission now verifies an
  article is `published` (via the read pool) before pinging, and a publish
  transition triggers a ping — so drafts and unpublishes never reach IndexNow.

### Upgrade Notes

- Two additive, defaulted migrations apply automatically (`052-comment-parent`,
  `053-member-reply-notify`); no operator action is required. Existing members
  default to reply notifications **on** and can opt out from their account portal.

## [2.5.0] - 2026-06-29

### Added

- **Pages** — standalone pages (About, Contact, Privacy…) managed at `/os/pages`,
  separate from the blog feed. Quick-create with starter templates
  (Blank/About/Contact/FAQ), per-page "in menu" and footer-group placement, an
  inline "＋ Page" button in the editor, and delete.
- **Contact form + Messages inbox.** Opt-in per page via the `[[contact-form]]`
  marker (or `[[contact-form: custom reply]]`). Submissions are validated,
  honeypot-screened, rate-limited, **persisted to a `contact_messages` inbox**,
  emailed to the operator via VayuMail, and acknowledged with an auto-reply.
  `/os/messages` adds search, unread/date filters, bulk actions, CSV export, a
  per-message detail view, a sidebar unread badge and a dashboard card.
- **Delete posts & pages** with bulk publish/unpublish/delete on the Posts
  manager.
- **Media** — search + type filter, bulk delete, and per-asset **alt text**
  (auto-applied as the editor's default alt for `/media/…` images).
- **SEO** — `BlogPosting` JSON-LD now uses the real site author/name + image, a
  live **Google snippet preview** in the editor, and actionable **health checks**
  (sitemap freshness, robots, site-wide noindex, canonical domain).
- **Theme Studio** — Tumblr-style refresh: toggle switches, appearance-first
  layout, a sticky quick-jump section navigator, and an unsaved-changes indicator
  with a leave guard.
- **VayuMail** — recommends K-9 Mail & Thunderbird, serves **Mozilla Autoconfig**
  (`/.well-known/autoconfig/mail/config-v1.1.xml`) so clients set up from just an
  email address, and shows **QR codes** for scan-to-import (Thunderbird/K-9) and
  for **2FA** enrolment.
- **Default administrator on install.** A fresh database auto-creates
  `admin@<domain>` with a strong random password (written to a root-only
  `initial-admin.txt` and logged once), and the console **forces a password
  change on first login** — no CLI needed to start. See
  [ADR-0102](docs/adr/ADR-0102-unified-identity-and-bootstrap-admin.md).
- **Human-readable author URLs** — `/author/<username>` (derived from the email
  local-part, uniquified) instead of the opaque id; old id links still resolve.

### Changed

- **Unified VayuMail + CMS identity.** A staff member who has both a CMS account
  and a VayuMail mailbox at the same address is now **one identity**: signing in
  via the mailbox resolves to the persisted CMS account, so the profile is
  editable and the public author URL is stable (previously a throwaway
  `vmail:<email>` identity blocked profile saves). See
  [ADR-0102](docs/adr/ADR-0102-unified-identity-and-bootstrap-admin.md).
- **One-click update — optional database backup.** The pre-update snapshot is now
  a checkbox (default on); unticking it skips the backup so a large database can
  no longer stall or fail the update, with clearer failure messages.

### Fixed

- **Large-catalogue performance.** Several hot-path queries (homepage, sitemap,
  RSS, dashboard, Posts/Pages) wrapped indexed columns in `COALESCE(...)`,
  defeating `idx_articles_is_page`/`idx_articles_status` and full-scanning the
  whole catalogue on the single writer connection — which thrashed a
  memory-constrained host into swap. They now use the read pool and bare indexed
  columns (`is_page=0`/`status='published'`).
- **VayuMail TLS now works for the non-root service.** Let's Encrypt keys are
  root-only, so the service silently fell back to self-signed; `vayumail-setup.sh`
  now copies the certificate to a service-readable location, VayuMail
  auto-discovers it, and the Connect tab surfaces the precise reason on failure.
- **Reverse-DNS (PTR) check** now passes when any of the host's IPs maps back to
  the mail hostname (it previously tested only the first, mixing IPv4/IPv6).

## [2.4.0] - 2026-06-29

### Added

- **VayuFind — built-in instant search (replaces Meilisearch).** Search is now a
  sovereign, dependency-free engine inside the binary — no external service to
  run. Clicking the nav search box (or pressing `Ctrl`/`⌘`-`K`, or `/`) opens a
  clean, Ghost-style **overlay**: the page dims and blurs behind a centred panel
  that filters results **as you type**, with keyboard navigation and match
  highlighting. It is extremely light: the browser downloads **one compact,
  content-hashed index** the first time the overlay opens and then filters
  entirely client-side, so there is **zero server work per keystroke**. The
  index is maintained **incrementally** — each publish/edit/delete updates only
  that entry rather than rebuilding everything — and is served with an `ETag` so
  a browser/CDN re-fetches it only when posts actually change. Ranking is
  field-weighted (title ≫ tags ≫ excerpt) with prefix/whole-word boosts and
  recency tie-breaking. Strict CSP is preserved (same-origin versioned script,
  no inline styles, no `eval`, results built with `textContent`). The
  server-rendered `/search` page remains as a no-JavaScript fallback. See
  [ADR-0101](docs/adr/ADR-0101-builtin-search-vayufind.md).

### Changed

- **Tools & Plugins:** the "Meilisearch" module is now simply **"Search"** — a
  single on/off switch (`feature.search`, default on) for VayuFind. Turning it
  off hides the search box and modal and makes `/search` return 404.

### Removed

- **External Meilisearch backend and its dependency.** VayuPress no longer
  shells out to a Meilisearch process; the `getmeili/meilisearch` service has
  been removed from `docker-compose.yml`, the `gobreaker` dependency is gone, and
  `MEILI_HOST` / `MEILI_MASTER_KEY` are ignored. No operator action is required;
  the `feature.meili` flag is deprecated. (`vayupress_meili_errors_total` is kept
  for dashboard compatibility and now counts built-in index errors.)

### Fixed

- **Trending and pinned posts never appeared on the public site.** The widget
  markup and `/api/trending` endpoint shipped, but the browser script that
  hydrates them — `/static/js/trending.js` — had **no server route**, so it
  returned 404 and the widget stayed hidden (a CDN had also cached that 404 on
  the bare, unversioned URL). The script is now served from a single source of
  truth (`render.TrendingJS`) at a **content-versioned** URL
  (`/static/js/trending.js?v=<hash>`), so both the Trending (7/30-day) and
  Pinned lists render, and a proxy can never pin a stale copy again. A
  regression test locks in the versioned reference.
- **The public search box now honours the Search toggle.** Turning search
  off in Tools & Plugins hides the nav search box across the
  homepage and post pages and makes `/search` return 404; turning it on restores
  them. The page cache is bumped so cached pages re-render to match the toggle.

- **Mail apps can finally connect to VayuMail behind a reverse proxy (IMAP/SMTP
  TLS).** When nginx (or any proxy) already owns ports 80/443, VayuMail's native
  ACME could not complete its HTTP-01 challenge, so it silently fell back to a
  **self-signed certificate** on the mail ports — which mobile and desktop mail
  apps (the Gmail app, Apple Mail, Thunderbird, Outlook) reject with *"Couldn't
  open connection to server"*, even though 993/143/587 were online. Three changes
  make trusted certificates reliable in this very common topology:
  - **Operator/Let's Encrypt certificates now hot-reload.** A file-based
    certificate (`VAYUOS_MAIL_TLS_CERT`/`VAYUOS_MAIL_TLS_KEY`) is served through a
    reloading loader that picks up a renewed certificate on disk within ~30s, on
    the next handshake — **no service restart required**. Previously a renewed
    cert kept serving the stale copy until the next restart, a silent path back to
    an eventually-expired certificate.
  - **New `deploy/nginx-vayumail.conf`** adds the missing `server_name
    mail.<domain>` :80 vhost that serves the ACME challenge from the shared
    `/var/cache/vayupress` webroot, so `certbot --webroot -d mail.<domain>`
    succeeds and auto-renews with **zero nginx downtime** (the existing site vhost
    only answered for the apex/`www` host, so mail-host challenges 404'd).
  - **`deploy/vayumail-setup.sh` is now webroot-first.** It installs the mail
    vhost, issues the certificate over the webroot with no downtime, and only
    falls back to a brief standalone stop/start if that path is unavailable.

### Added

- **Site search box on the public website.** The public nav now carries a search
  box, and a new `/search` page renders results server-side (crawlable, no JS
  required) using the same engine as the API — Meilisearch when enabled and
  reachable, otherwise the built-in SQLite search. So readers can actually search
  the site, and the Meilisearch toggle now has a visible front-end.
- **Pin a post directly from the Posts manager.** Each row in VayuOS → Posts now
  has a one-click **Pin/Unpin** button and a 📌 Pinned badge (backed by
  `POST /os/api/posts/pin`, flipping the same `featured` flag as the editor).
  Pinned posts immediately surface in the Trending & pinned widget on the
  homepage and under every post (the trending cache + public caches are purged on
  pin/unpin). This is also what makes the trending/pinned widget visible — pin a
  few posts and they appear right away, while the 7/30-day trending list fills in
  from analytics as views accrue.
- **Transactional email now sends through the built-in VayuMail engine.** On a
  deployment with no external SMTP configured, sign-in (magic link), welcome,
  newsletter double-opt-in, comment and payment emails were silently dropped.
  They are now delivered via VayuMail — DKIM-signed and queued on its durable,
  retried outbound queue — whenever a `DOMAIN` is set (external `SMTP_HOST`
  still takes priority when present). A new **welcome email** (its own
  operator-editable template) is sent to each newly signed-up member alongside
  their sign-in link, and the welcome template joins the magic-link and
  newsletter-confirm templates in the admin email-template editor.
- **Trending & pinned posts.** The homepage and the bottom of every post now show
  a Trending widget — the most-viewed posts over the **last 7 and 30 days** (a
  tab switches between them), drawn from the built-in cookieless analytics — plus
  your **Pinned** posts. Pin a post with the editor's "Feature this post" toggle
  (up to 4 are shown). It's served by a cached, public `/api/trending` JSON
  endpoint and hydrated client-side, so it stays fresh without invalidating the
  page cache, and it can be turned on/off from **Tools & Plugins** (on by default).
- **Meilisearch is now an operator toggle in Tools & Plugins.** Search has a new
  "Meilisearch" module switch: turn it **on** to use an external Meilisearch
  engine for instant, typo-tolerant full-text search (used when a host is
  configured and healthy), or **off** to force VayuPress's built-in SQLite
  search even when a Meilisearch host is set. The change applies immediately
  (no restart); the card shows *Inactive* when no `MEILI_HOST` is configured so
  it's clear search is running on the SQLite engine. Defaults on, preserving the
  previous auto-detect behaviour.
- **Homepage pagination.** The public homepage now exposes the full archive
  instead of only the latest posts: a Newer/Older pager appears under the feed
  and deeper pages live at `/page/2`, `/page/3`, … (page 1 stays canonical at
  `/`). Each page emits `rel=prev`/`rel=next` and a page-aware canonical for
  clean SEO, out-of-range pages return the branded 404, and only the hot first
  page is cached so the rest stay fresh with no extra invalidation.
- **VayuOS is now a fully mobile, app-like admin experience.** The panel adapts
  to phones the way a native app would: a wide tap-to-close navigation drawer
  (opened from the topbar hamburger *or* a new **Menu** button in the bottom bar)
  exposes every section, an app-style bottom bar gives one-tap access to Home,
  Posts, Write and Inbox with an active-route highlight, and its quick links are
  filtered to match the signed-in role (you never see a link you can't open).
  Comfortable 44px touch targets on touch devices, safe-area insets so the bottom
  bar clears the iPhone home indicator, dynamic-viewport (`dvh`) heights, and
  16px form fields (no more iOS focus-zoom) round out the feel. Wide data tables
  now fold into clean, labelled cards on small screens instead of forcing a
  horizontal scroll — applied automatically across the whole panel.
- **Native automatic mail TLS certificates (ACME / Let's Encrypt).** VayuMail can
  now obtain and auto-renew a trusted certificate for `mail.<domain>` on its own —
  no external certbot run and no shell script. Set `VAYUOS_MAIL_TLS_ACME=on` (and
  optionally `VAYUOS_MAIL_ACME_EMAIL`) and, once port 80 is reachable for the mail
  hostname, the certificate is issued in the background, cached under
  `<storage>/mail/acme`, and shared by every mail TLS listener (IMAPS 993, POP3S
  995, submission 587, and STARTTLS on 25/143/110). This is the foolproof path for
  real mail apps: mobile/desktop clients (the Gmail app, Apple Mail, Thunderbird,
  Outlook) reject the self-signed fallback but accept the ACME certificate. New
  env vars: `VAYUOS_MAIL_TLS_ACME`, `VAYUOS_MAIL_ACME_EMAIL`,
  `VAYUOS_MAIL_ACME_HTTP_ADDR` (default `:80`), `VAYUOS_MAIL_ACME_CACHE`,
  `VAYUOS_MAIL_ACME_DIRECTORY`, `VAYUOS_MAIL_ACME_HOSTS`. While issuance settles,
  the listeners keep answering with the self-signed fallback so opportunistic
  STARTTLS between mail servers is never interrupted.

### Fixed

- **Sign-in / welcome / newsletter emails are now readable, not a PGP blob.**
  Transactional mail routed through VayuMail was being auto-PGP-encrypted whenever
  the recipient had a published key, so the message arrived as an unreadable
  `-----BEGIN PGP MESSAGE-----` block and the magic sign-in link couldn't be used.
  System mail now sends via a dedicated, never-encrypted path (`SendSystemMail`),
  while person-to-person mail composed in VayuMail still encrypts as before.
  The sender identity is also branded with the site's own name (or domain) for
  uniqueness instead of a generic default.
- **ADR Registry now lists every ADR.** The registry read `docs/adr` only from
  disk, so on a box updated via the one-click binary self-update (which never
  refreshes on-disk docs) the list was frozen at whatever shipped with the
  original install — e.g. stopping at ADR-0046. The ADR docs are now embedded in
  the binary and the registry lists the union of the embedded set and any
  on-disk copies, so every ADR (and each new one) always appears after an update.
- **VayuOS Posts tab no longer returns intermittent 502s.** The `/os/posts`
  handler now runs its list/count queries under an 8s request-bounded context
  and degrades gracefully (a retryable notice on a fully-rendered page) instead
  of hanging until the upstream proxy returns a gateway error when the database
  is briefly busy on a large catalog.
- **"Couldn't open connection to server" is now diagnosable.** The most common
  cause is a reachable mail port serving an untrusted **self-signed** certificate
  that mobile apps silently reject — the connection and TLS handshake succeed, but
  the client refuses the certificate. VayuMail now detects the active certificate's
  provenance and surfaces it: VayuOS → VayuMail → **Connect** shows a prominent
  warning (with exact remediation) when the certificate is self-signed, and the
  server logs a loud, actionable error at startup.
- **Implicit-TLS listener bind failures are no longer swallowed.** Failures to
  bind IMAPS (993), POP3S (995) or submission (587) are now recorded and surfaced
  via the inbound-error channel (alongside the existing SMTP/IMAP/POP3 reporting)
  instead of being silently dropped, so an unreachable SSL port can be explained.

## [2.3.0] — 2026-06-28

### Highlights

A one-click update that reliably activates the new version, a new admin-only
**Storage & System** panel to see RAM/disk usage and clean up backups, logs and
temp files without the shell, and PGP/DNS infrastructure detail locked to
administrators.

### Added

- **Storage & System panel (VayuOS → Storage & System, administrators only).** A
  new admin page shows how much RAM and disk the system is using — system memory
  (used/total), the VayuPress process's own memory, and the disk/NVMe usage of
  the filesystem holding the database, plus the on-disk footprint of the
  database, render cache, media library and pre-update backups. It also lists the
  files VayuPress creates over time — **database backups** (including the
  automatic pre-update snapshots), **log files** and **temporary files** — each
  with its size and age and a one-click **Download** (to keep a copy off-server)
  or **Delete** (single or bulk, to reclaim space). Downloads and deletes are
  validated against the live set of managed files, so the live database and its
  WAL can never be served or removed and path traversal is impossible. The whole
  page and its APIs are administrator-only. See ADR-0100.

### Changed

- **Infrastructure detail is now administrator-only in VayuMail.** PGP keys, the
  DKIM/SPF/DMARC records and live DNS health, the deliverability self-check, the
  dependency security-update watcher, and mail-account management are hidden from
  the four non-admin roles (editor, author, reviewer, mailbox) — both removed
  from the VayuMail tabs/dashboard and blocked server-side (a non-admin is
  redirected to their inbox). Those roles now see only the mail surface they use:
  Overview, Compose, Mailbox, Connect and Outbox. See ADR-0100.

### Fixed

- **One-click update now actually activates the new version.** After installing
  an update from VayuOS the service restarted but could come back on the *old*
  version (then prompt to update again). The cause: immediately after the binary
  was swapped, the in-process restart re-derived its target from
  `os.Executable()`, which the kernel reports as `"…/vayupress (deleted)"` (the
  old, now-unlinked inode) — so it failed to launch the new file. The restart now
  re-execs the exact path the update just wrote (and strips a stray `(deleted)`
  marker as a safeguard), so the new binary takes over in place with the PID
  preserved. In addition, the update now **pre-checks that the binary's directory
  is writable** and, if not (most often a systemd `ProtectSystem` sandbox making
  `/usr/local/bin` read-only), fails fast with the exact fix instead of a
  confusing mid-update failure. See ADR-0100.

## [2.2.0] — 2026-06-28

### Highlights

A truly one-click update — installing from VayuOS now advances the binary, the
embedded migrations **and** the admin assets together — plus automatic Let's
Encrypt TLS for VayuMail so mobile mail apps (the Gmail app, Apple Mail) connect.

### Changed

- **One-click update now updates everything, not just the binary.** The VayuOS
  self-update replaces the running executable and re-execs to activate it — but
  the admin panel's CSS/JavaScript were served from `STATIC_DIR` on disk and
  refreshed by a *separate* file-copy step, so a one-click update could leave the
  new binary paired with stale `admin-os.css`/`admin-os-*.js` until someone
  re-copied them by hand. Those admin assets are now **compiled into the binary**
  and written to `STATIC_DIR` automatically on boot (only when their bytes
  changed), so installing an update from the panel updates the binary, the
  embedded database migrations (already run on start) **and** every admin asset
  together — with no command line and nothing left half-applied. If `STATIC_DIR`
  is missing or read-only, the panel now serves these assets straight from the
  binary as a fallback, so VayuOS always loads correctly after an update. See
  ADR-0099.

### Added

- **Automatic TLS for VayuMail (Let's Encrypt) in the setup script.**
  `deploy/vayumail-setup.sh` now provisions a trusted certificate end to end:
  it resolves your mail hostname (`mail.<domain>` from the service environment,
  or `MAIL_DOMAIN=`), installs certbot if needed, obtains the certificate over
  the HTTP-01 challenge (briefly freeing port 80 when nginx holds it), wires
  `VAYUOS_MAIL_TLS_CERT`/`VAYUOS_MAIL_TLS_KEY` into the service environment, and
  installs a renewal deploy-hook that restarts the service so a renewed cert is
  loaded. This closes the last gap that made mobile mail apps (the Gmail app,
  Apple Mail) refuse to connect — they reject VayuMail's self-signed fallback.
  Best-effort and idempotent; set `MAIL_TLS=off` to skip, or
  `MAIL_CERT_EMAIL=` to set the registration contact.

### Fixed

- **Self-update now always picks the correct release binary.** The updater chose
  the download as "the first release asset that isn't a `.sig`/`.sha256`", which
  could match the `*.cosign.bundle` signature artefact attached to every release
  (the update would then fail checksum verification and never complete). Asset
  selection now explicitly skips checksum/signature/SBOM sidecars and, when a
  release ships builds for multiple platforms, picks the one matching the running
  OS and architecture (with common arch aliases like `amd64`↔`x86_64`,
  `arm64`↔`aarch64`); the matching `.sha256`/`.sig` is resolved to the chosen
  binary. Single-binary releases (VayuPress's own) are unaffected. See ADR-0099.

## [2.1.0] — 2026-06-28

### Highlights

VayuMail becomes a real mail provider you can use from the apps you already
have, and the engine is hardened to run a 1M-post catalogue on a small VPS with
smooth, incremental updates (no site-wide rebuilds).

### Added

- **Role-scoped login for VayuMail accounts (5 roles).** The email accounts you
  create under VayuMail (administrator / editor / author / reviewer / mailbox)
  can now sign in from the website login button and use VayuOS scoped to their
  role — and only their role. A **mailbox** (and reviewer) account is confined to
  the VayuMail surface: it lands on its inbox and the rest of the console is both
  hidden from the sidebar and blocked server-side. **author** sees only content
  authoring (Dashboard, Posts, New Post, Media, Mail, Profile); **editor** adds
  content management (Comments, Pages, SEO, Analytics, Theme, Messages);
  **administrator** gets the full console. The sidebar now renders per role
  (hidden == unreachable: the same policy guards the routes), and deleting or
  deactivating an account immediately invalidates its web session. See ADR-0098.

- **VayuMail "Connect" tab — one-glance mail-app setup + live status.** A new
  Connect tab in the VayuMail console shows the exact IMAP/POP3/SMTP server,
  ports and username needed to add a mailbox to any standard app (Gmail, Apple
  Mail, Thunderbird, Outlook): IMAP `993` SSL / `143` STARTTLS, POP3 `995` SSL /
  `110` STLS, SMTP submission `587` STARTTLS (auth required), username = full
  email, password = the mailbox password. It also reports the live online/offline
  status of every mail listener (and any bind error), and lists per-mailbox
  settings so each account can be set up by copying the values.

- **VayuMail now works with real mail apps over IMAP and POP3.** You can add a
  VayuMail mailbox to the Gmail app, Apple Mail, Thunderbird, Outlook, or any
  standard client and read/send mail like any hosted provider. The IMAP server
  was rebuilt to a full RFC 3501 service: persistent, stable UIDs and per-folder
  UIDVALIDITY (so clients sync incrementally instead of re-downloading on every
  reconnect), all standard folders exposed via LIST/LSUB with SPECIAL-USE
  (Inbox/Sent/Drafts/Archive/Junk/Trash), SELECT/EXAMINE of any folder, full UID
  FETCH (FLAGS, UID, RFC822.SIZE, INTERNALDATE, ENVELOPE, BODYSTRUCTURE, and
  BODY[…] section/partial fetch with a MIME walker), STORE flag updates
  (\Seen/\Answered/\Flagged/\Deleted/\Draft mapped to Maildir flags), APPEND
  (so a client's Sent/Drafts copies are saved server-side), COPY, MOVE (RFC
  6851), EXPUNGE, a SEARCH subset, IDLE (RFC 2177) push, NAMESPACE, and
  AUTHENTICATE PLAIN (SASL-IR) alongside LOGIN. A brand-new **POP3** server
  (STLS on 110, implicit TLS on 995) provides USER/PASS, STAT, LIST, UIDL, RETR,
  TOP, DELE, RSET, NOOP and QUIT for download-style clients. UIDs/UIDVALIDITY are
  persisted in SQLite; transparent PGP decryption is applied on read for both
  protocols. Listen addresses are configurable via `VAYUOS_MAIL_IMAP_LISTEN`,
  `VAYUOS_MAIL_IMAPS_LISTEN`, `VAYUOS_MAIL_POP3_LISTEN`, `VAYUOS_MAIL_POP3S_LISTEN`,
  and all listeners remain best-effort (a bind failure never blocks startup).
  See ADR-0096.

- **The ADR Registry is now readable and shows the true, complete set.** The ADR
  tab (`/os/adr`) previously listed the architecture decision records as static,
  non-clickable rows with no way to open them. Each row is now a link to a read
  view that renders the record's Markdown to HTML (GFM: headings, tables, code,
  links), sanitised with the same UGC policy as published content. The list is
  sorted newest-first, de-duplicated by ADR number, and the registry's own
  `INDEX.md` is no longer counted as a record. The boot-time generator that wrote
  ~13 stub ADRs was removed (it produced duplicate ADR numbers alongside the
  canonical files), and the deploy now mirrors the shipped `docs/adr` exactly
  (pruning renamed/removed/stale files) so the registry reflects precisely the
  ADRs that ship with the running build — no missing entries, no stale leftovers.

- **Startup index self-check guards against full-scan regressions.** VayuPress
  depends on every hot read being index-backed to serve 1M+ posts on a small VPS,
  but a future change could silently reintroduce a full-table scan that only
  surfaces as a 502 once the catalog is large. A read-only self-check now runs
  `EXPLAIN QUERY PLAN` on a curated list of the hottest reads shortly after boot
  and logs a loud warning (and bumps a metric) if any resolves to a full table
  scan instead of an index search/scan. It never blocks startup and skips queries
  whose tables don't exist on a partial schema.

### Changed

- **Updates and theme changes no longer rebuild the whole site.** Previously, a
  deploy that changed the templates/CSS (renderer fingerprint) deleted the entire
  pre-rendered cache (`home/`, `tags/`, `posts/`) on startup, and every global
  theme/identity save wiped it too — so on a large catalog the next wave of
  traffic re-rendered hundreds of thousands of pages at once (a thundering herd
  that pegs CPU and stalls the box). Invalidation is now **lazy and per-page**:
  each cached file is checked against a persisted "staleness cutoff" when it is
  served, and only a page that is actually requested after a renderer/theme change
  is re-rendered (and re-stamped). A plain restart, or a deploy that doesn't touch
  templates/CSS, now invalidates **nothing** (a legacy cache from the previous
  scheme is preserved as-is on first upgrade). The cache-warmer likewise refreshes
  only stale pages, paced in the background. This is the incremental-update
  behaviour VayuPress exists to provide: changing one thing rebuilds one thing,
  not the whole site.

- **Pages manager reads straight from an index (no sort step).** The Pages
  manager (`/os/pages`) lists pages with `WHERE is_page=1 ORDER BY updated_at
  DESC`. The single-column `is_page` index satisfied the filter but then forced a
  temp-b-tree sort of every matching row; `idx_articles_pagefeed` orders by
  `created_at`, not `updated_at`, so it did not help. New migration 049 adds
  `idx_articles_pages(is_page, updated_at DESC)`, which serves both the filter and
  the recency order from the index, so the Pages tab stays fast even with many
  pages. Proven with `EXPLAIN QUERY PLAN` (temp b-tree eliminated).

- **Tag lookups are now indexed instead of full-scanning the catalog.** Tags are
  stored on each article as one comma-separated string, so "find posts with tag
  X" (per-tag page, related posts, the topic index, and the JSON list tag filter)
  could only be answered with `tags LIKE '%X%'` — a predicate that cannot use an
  index and therefore reads **every** row of the (multi-GB) articles table. At
  hundreds of thousands of posts that scan exceeds the request timeout and shows
  up as a 502. New migration 048 adds a normalised `article_tags(article_id, tag,
  tag_norm, created_at)` join table, kept exactly in sync inside the same
  transaction as every article create/update/delete, plus a one-time, resumable,
  **batched** background backfill for existing posts (so a low-RAM VPS holding a
  large database is never blocked). All four lookups were rewritten to resolve
  membership through the indexed table; `EXPLAIN QUERY PLAN` confirms each is now
  an indexed range scan whose cost is bounded by how many posts carry the tag
  (not by the table size) — and a `CROSS JOIN` pins the tag table as the driver
  so the planner can never fall back to a full articles scan when a tag is very
  common. The topic index count became a single `GROUP BY tag` over a covering
  index instead of loading every article's tags into memory. Verified fast for
  both rare and very common tags on a 100k-row synthetic catalog.

### Fixed

- **VayuMail mail-app connectivity ("could not open connection to server").** A
  mail client failing to connect is almost always a blocked/closed port, not a
  password problem: the non-root VayuPress service cannot bind the privileged
  mail ports (25/110/143/587/993/995) without `CAP_NET_BIND_SERVICE`, and the
  host/cloud firewall must allow them. VayuPress now logs a loud, actionable
  warning at startup when the inbound listeners fail to bind (instead of failing
  silently), and a new one-shot `deploy/vayumail-setup.sh` grants the capability
  via a systemd drop-in (works regardless of how old the installed unit is),
  opens the firewall (ufw/firewalld), restarts, verifies the listeners, and
  reminds you to add the `mail.<domain>` DNS record and point VayuMail at a
  trusted TLS cert (mobile clients like the Gmail app require a CA-signed cert).

- **ADR Registry now shows every record (duplicate ADR-0079 resolved).** Two
  different ADRs had both been numbered 0079 (VayuMail Transport Security and
  VayuAnalytics), and the registry de-duplicated by number — so one real decision
  record was hidden, and the visible count looked short. The VayuAnalytics record
  is renumbered to **ADR-0097** (0079 stays the VayuMail transport ADR that
  ADR-0084 already references), INDEX.md is corrected, and the registry no longer
  de-dupes by number, so every distinct ADR file is listed. Note: ADR numbers are
  not contiguous (0003–0031 and 0091 were never assigned), so the highest number
  being 0096/0097 does not imply that many records exist.

- **One-click update no longer shows a scary "Unexpected token '<'" error.** When
  you install an update from VayuOS, the service restarts to activate the new
  binary — so the apply request (and the auto-check that runs when the panel
  reloads) can briefly receive an nginx 502/504 HTML page instead of JSON. The UI
  called `response.json()` on that and surfaced `SyntaxError: Unexpected token
  '<'`, making a successful update look like a failure. The Update panel now reads
  responses defensively and treats a non-JSON or dropped response after
  apply/rollback as the expected restart (waiting for the service to return),
  while genuine pre-restart errors still report their JSON message.

- **Security-updates tab: clearer dependency names and honest status.** The
  tracked-dependencies table showed `chi/v5` as the meaningless component name
  `v5` (it used the last module-path element), and marked every row a green "up
  to date" even when the watcher was disabled and the latest version was unknown.
  Component names now skip the major-version suffix (so it reads `chi`), a row
  whose upstream version hasn't been fetched shows a neutral **"not checked"**
  instead of a false "up to date", and "update available" is now decided by a
  semver-aware comparison (only when the upstream release is actually newer),
  removing false positives on pinned/pseudo-versions.

- **The contact form no longer fails when email delivery isn't configured.** A
  page using the Contact template ([[contact-form]]) rejected every submission
  with "Email delivery is not configured on this site" unless VayuMail/SMTP was
  set up — so visitors on a site without outbound email saw an error and their
  message was lost, even though it could have been stored. Submissions are now
  always saved to the Messages inbox (the durable record the operator reads in
  `/os/messages`); emailing the operator and auto-replying to the visitor are
  best-effort and only happen when a contact address and mailer are configured. A
  submission is refused only if it can be neither stored nor emailed.

- **Opening the Posts tab no longer 502s on a large catalog (the real fix).** The
  Posts manager counts posts with `SELECT status, COUNT(1) FROM articles WHERE
  is_page=0 GROUP BY status`, and the homepage/feeds filter on `is_page` **and**
  `status` together. No existing index covered both columns, so SQLite found the
  rows with one single-column index and then read **every** row to evaluate the
  other column (a temp-b-tree `GROUP BY` over the whole catalog) — a full scan
  that exceeds the request timeout and returns 502 at hundreds of thousands of
  posts. New migration 047 adds a composite index
  `idx_articles_pagefeed(is_page, status, created_at DESC)`; `EXPLAIN QUERY PLAN`
  confirms the counts become **covering, index-only** (no temp b-tree, no table
  reads) and the published/draft listings read straight from the index in
  recency order — so the Posts tab, homepage and feed counts stay fast at 1M+
  posts. The index builds once on the next start.

- **The public site and VayuOS no longer hang after an update on a large
  catalog.** Every public *cold-render* path ran a full-table scan over the
  whole catalog on the single SQLite **writer** connection: the homepage and
  tag-index `COUNT` (via `COALESCE(status,…)`/`COALESCE(is_page,0)`), and the
  `tags LIKE '%…%'` scans behind related-posts, per-tag pages, and the search
  reindex. When an update changes templates the pre-rendered cache is dropped,
  so under real traffic every cold page render hammered that one connection with
  a 234k-row scan — starving sessions, writes and admin requests until the whole
  site (and VayuOS) appeared to hang. These reads now run on the **read pool**
  and use the bare (indexed) `status`/`is_page` columns, so they never block the
  writer connection and pages render without a full-table scan.

- **Opening the Posts or Pages tab no longer 502s on a large catalog.** Both
  manager pages wrapped indexed columns in `COALESCE(...)`
  (`GROUP BY COALESCE(status,'published')` on Posts; `WHERE COALESCE(is_page,0)=1`
  with no `LIMIT` on Pages), which defeats `idx_articles_status` /
  `idx_articles_is_page` and forces a full-table scan of the whole catalog on
  every visit — at hundreds of thousands of posts that exceeds the request
  timeout and the connection is dropped (502). Both columns are `NOT NULL` with
  defaults, so the `COALESCE` was unnecessary: Posts now `GROUP BY status` (and
  filters `status='draft'`/`'published'`) and Pages now query `WHERE is_page=1`
  on the read pool with an explicit cap, so both render in milliseconds
  regardless of catalog size.

- **Large catalogs (100k+ posts) no longer stall VayuOS into 502s.** On a site
  with hundreds of thousands of articles, several read paths scanned the whole
  catalog on the single SQLite *writer* connection, so each scan blocked
  sessions, writes and admin pages until it finished. They now run on the
  read-only connection pool (and use indexes where they didn't):
  - the dashboard metrics `is_page` count uses `WHERE is_page=1`
    (index-backed) instead of `COALESCE(is_page,0)=1` (full scan every 30s);
  - the **Comments** page resolves slugs only for the posts referenced by the
    comments shown, instead of loading every `id,slug` row into memory per view;
  - the **SEO** content-quality scan, the **Posts** manager status counts and
    listing, the dashboard publishing-trend sparkline, and the background
    **sitemap/RSS** generators (including the per-publish tag scan) all moved to
    the read pool, so they never contend with writes.

- **Updating/restarting no longer rebuilds the entire pre-rendered site.** The
  cache fingerprint that decides whether to drop the pre-rendered public HTML
  included the release version, so **every update invalidated all cached pages**
  — on a large catalog (100k–1M+ posts) that meant re-rendering the whole site
  on each deploy. The fingerprint now tracks only what actually affects rendered
  output (stylesheet content hashes + the manual `cacheSchema`), so a new binary
  that doesn't change templates/CSS keeps the existing cache intact and serves
  instantly after a restart. Genuine template/CSS changes still invalidate as
  before. (Pages already render on-demand on a cache miss, so the one-time
  fingerprint change when this ships rebuilds lazily per request rather than all
  at once.)

## [2.0.0] — 2026-06-27

> **The sovereign-publishing 2.0 release.** A ground-up block-editor overhaul
> with full publishing options, a complete in-house monetization system
> (payments, subscriptions, advertising), major outbound/auth security
> hardening, and smoother low-impact deploys — all still a single Go binary,
> SQLite-first, with zero third-party SDKs and zero telemetry.

### Changed

- **Smoother, lower-impact deploys and restarts.** Several changes make an
  in-place update gentle on a small VPS and keep the public site responsive
  throughout:
  - The HTTP listener no longer blocks on the Meilisearch readiness probe at
    startup — readiness now runs in the background and search uses its SQLite
    fallback until Meili confirms ready. This removes the multi-second 502
    window a restart could open while waiting on Meili.
  - The post-deploy cache warm and the search reindex are now **paced** so they
    no longer saturate a CPU after a restart. Tunable via
    `VAYU_WARM_THROTTLE_MS`, `VAYU_WARM_DELAY_SEC`, `VAYU_REINDEX_THROTTLE_MS`,
    and `VAYU_WARM_ON_BOOT=0` to skip boot warming entirely (pages then render
    lazily on first request).
  - `scripts/update-vayupress.sh` now builds at **idle CPU/IO priority with
    capped parallelism** (so the still-running old binary and nginx stay
    responsive during the compile), adds a **disk-space preflight**, and takes a
    **timeout-bounded DB snapshot by default** before restarting (disable with
    `BACKUP_DB=0`).
  - The sample nginx config gains a dual-peer upstream + bounded
    `proxy_next_upstream` retry so an in-flight idempotent request during a
    sub-second restart is retried instead of surfacing a 502 (POSTs are never
    retried).

### Added

- **Editor: image galleries, HTML & Markdown cards, and richer images.** The
  block editor gains the remaining content cards: an **image gallery** (up to 9
  images via upload or URL, rendered as a responsive grid with an optional
  caption), an **HTML card** for custom markup (sanitised on save through the
  same UGC policy as every block — rich markup is kept, scripts/handlers/forms
  are stripped), and a **Markdown card** for authoring a block in full Markdown.
  The image card now also supports a **caption** and a **width** option (regular,
  wide, or full-width). Pasting a bare URL onto an empty line now
  **auto-creates a bookmark/embed card** and unfurls it. Image captions
  round-trip through the HTML importer.

- **Editor: Post settings panel (publishing options).** A slide-out drawer
  (⚙ Settings in the toolbar, or `⌘⇧P`/`Ctrl+Shift+P`) puts everything that
  isn't the post body in one place: a **feature image** (URL or upload), the
  **post URL / slug** (with a safe rename that repoints links), an explicit
  **publish date**, a custom **excerpt**, a **tag** editor, **SEO** meta title
  and description, a **canonical URL**, **social cards** (Open Graph + Twitter
  title/description/image), and **Feature this post** / **Turn into a page**
  toggles. Every field is optional and falls back to the previous derived
  default, so existing posts are unchanged until something is set explicitly.
  These flow into the rendered article `<head>` (title, description, canonical,
  `og:*` and `twitter:*` tags, JSON-LD) and body (hero feature image); pages drop
  the post chrome and are kept out of the home feed, RSS, and sitemap. Backed by
  new `articles` columns (migration 045) and the `POST /os/api/editor/import`
  and `POST /os/api/editor/slug` endpoints.

- **Editor: one-click HTML source mode.** A new **HTML** button in the editor
  toolbar (or `⌘⇧H` / `Ctrl+Shift+H`) switches the writing surface to a raw HTML
  source view and back. Switching out parses the HTML back into blocks through
  the server-side importer, which now re-encodes inline `**bold**`, `*italic*`,
  `` `code` ``, `~~strike~~` and `[links](url)` as Markdown — so a
  visual → HTML → visual round-trip is lossless for common formatting. Saving
  while in HTML mode applies the source first so no edit is lost. Backed by a new
  `POST /os/api/editor/import` endpoint.

### Changed

- **VayuOS one-click update now works without pre-arming.** The **Update now**
  button used to be disabled (showing a not-allowed cursor) unless both
  `VAYU_SELFUPDATE_ENABLED=true` and a pinned `VAYU_RELEASE_PUBKEY` were set, so
  an operator could see an available update but not install it. An authenticated
  admin clicking *Update* is now treated as the explicit opt-in: the release is
  **always SHA-256 checksum verified**, and the Ed25519 **signature is still
  required whenever a release key is pinned** (`VAYU_RELEASE_PUBKEY`) — pinning a
  key upgrades verification rather than being a prerequisite. Applies are still
  admin-gated, CSRF-protected, refused in read-only/quarantined/maintenance
  modes, and audit-logged. The strict CLI path (`vayupress update apply`) is
  unchanged and still requires the opt-in flag and a pinned key. See ADR-0089.

### Fixed

- **Monitoring: a single-event budget no longer shows "at-risk" with zero
  events.** The governance error-budget state used `consumed >= limit-1`, which
  for a limit-1 budget (`integrity-exhaustion`, CRITICAL) is `0 >= 0` — so it
  always read **at-risk** even with nothing recorded. A budget now reads
  **healthy** until at least one event lands; `at-risk` still means "one event
  from exhaustion." These budgets remain advisory — mode transitions are
  operator-gated and never auto-applied.

- **Backup export no longer downloads an empty (0-byte) archive.** The export
  now builds the snapshot to a temporary file *before* sending any response, so
  a failure returns a clear error instead of a truncated download; it serves the
  archive with a real `Content-Length` (accurate browser progress) and picks a
  writable temp location (falling back from `TMP_DIR` to the OS temp dir to the
  database directory). If `VACUUM INTO` is unavailable it falls back to a
  checkpointed file copy, so the export always produces a valid archive.

- **VayuOS: the "Update now" button is no longer stuck disabled.** It now enables
  as soon as a newer release is detected (and the system mode allows applying),
  so updates can be installed in one click.

- **Intermittent hangs / 502s under load, and an ever-growing database.** Two
  compounding issues are fixed. (1) The `write_jobs` queue table was never
  pruned, so completed jobs — each holding a full article snapshot — accumulated
  indefinitely and could bloat the database to many gigabytes. A new retention
  sweeper now deletes completed jobs after `QUEUE_JOB_RETENTION_HOURS` (default
  24h) and dead-letter/quarantined jobs after `QUEUE_DEAD_JOB_RETENTION_DAYS`
  (default 7d), in small batches. (2) The admin-metrics collector ran a
  full-table `SUM(...) FROM write_jobs` scan every 30 seconds on the single
  writer connection; on a large queue table that scan monopolised the connection
  for seconds, stalling session lookups, writes and admin pages and surfacing as
  intermittent gateway timeouts. The collector now runs index-only counts on the
  read pool, so it never blocks writers. Operators with an already-bloated
  database should prune completed jobs once and `VACUUM` to reclaim disk (see
  UPGRADING notes).

- **Editor: the writing canvas now scrolls.** The block canvas was clipped at
  the viewport edge, so a long post — or a single tall block — could not be
  scrolled into view. The editor shell is now a fixed-height grid with a correct
  `min-height: 0` chain (shell → main → workspace → canvas), giving the canvas a
  smooth internal scroll with generous trailing space, plus the focus-mode and
  drag-and-drop highlight styles that were previously missing.

### Security

- **SSRF hardening — single, rebind-safe outbound path.** All server-side
  outbound HTTP (webhooks, self-update downloads, AI/search service calls) now
  flows through the consolidated `internal/safefetch` dialer, which validates
  the host and **pins the resolved public IP at connect time** (closing the
  DNS-rebinding window), never honours an environment proxy, and refuses the
  full set of private/reserved ranges (RFC1918/ULA, loopback, link-local, cloud
  metadata, CGNAT `100.64/10`, benchmarking, Class-E). The weaker re-resolving
  transport has been removed and the duplicate IP-classification helper
  de-duplicated.
- **Spoof-resistant client-IP resolution.** Rate limiting, brute-force lockout,
  and the `TRUSTED_IPS` allowlist now derive the client IP through a
  trusted-proxy-aware resolver: `X-Forwarded-For` / `X-Real-IP` are honoured
  **only** when the immediate peer is a configured `TRUSTED_PROXIES` entry
  (default: loopback, matching the shipped same-host nginx). Direct clients can
  no longer spoof these headers to evade limits or impersonate a trusted IP.
  Replaces chi's spoofable `RealIP` middleware (GHSA-3fxj-6jh8-hvhx).
- **Argon2id work factor raised** from t=1 to t=3 (OWASP-aligned) using a new
  parameterised encoding (`argon2id$v=2$t=N$…`). Existing hashes remain valid —
  legacy `salt$hash` values are verified with the original cost — so no
  credential migration is required.
- **Admin session cookie is now `SameSite=Strict`** (the reader/member
  magic-link cookie stays `Lax` so email-link sign-in still works).

### Changed

- **SQLite read concurrency.** Read-only queries on hot public paths now run
  against a dedicated WAL reader pool (`query_only`, sized to CPU count) while
  writes stay on the single serialized writer, so reads no longer queue behind
  the writer. In-memory test databases transparently fall back to the writer.
- **CI now enforces the constitution's quality gates.** `golangci-lint` (zero
  errors, via a committed `.golangci.yml`), `gosec` (fail on HIGH severity +
  HIGH confidence), and a `deadcode` baseline gate (blocks *new* unreachable
  code) run in CI alongside the existing `go vet` / `staticcheck` /
  `govulncheck` checks. Numerous previously-unchecked database-write and
  row-iteration errors are now handled or explicitly documented.

### Fixed

- **Flaky `apikeys` test under `-race`** caused by an unpinned shared `:memory:`
  database handing queries a separate empty connection; the test DB is now
  pinned to a single connection.

### Added

- **Monetization — a sovereign system for getting paid, with optional
  advertising.** A new **Monetization** section in VayuOS (sidebar + command
  palette) turns VayuPress into a complete earning platform without surrendering
  the single-binary, zero-third-party-SDK posture. Everything is off by default
  and only acts once the operator switches the relevant module on in *Tools &
  Plugins*. See ADR-0090 (payments) and ADR-0091 (advertising).
- **Payments & subscriptions — collect money with no embedded gateway.** A built
  -in **direct / offline gateway** is the dependency-free way to get paid: the
  operator publishes payment instructions (bank transfer, UPI, a payment link —
  anything), the reader checks out and receives a short, quotable order
  reference, pays out of band, and the operator confirms receipt with one click
  in the **Monetization** console. Confirmation upgrades the member to the
  chosen tier, records the subscription at its true cadence/amount, and emails
  the payer a receipt — all idempotently. A new sovereign **order ledger**
  (`payment_orders`) is the single source of truth for every checkout.
- **Connect any third-party processor by signed webhook.** A generic, gateway
  -agnostic webhook (`POST /api/v1/payments/webhook/{gateway}`) lets any external
  payment processor confirm an order with a signature-verified event
  (`X-VayuPress-Signature`, HMAC-SHA256). No card data ever touches the server
  and no payment SDK is linked — the same pattern as the existing Stripe webhook,
  generalised so no provider is special-cased.
- **Confirmation emails to the payer.** Two new operator-customisable
  transactional templates — *payment pending* (instructions + reference at
  checkout) and *payment confirmed* (receipt on fulfilment) — are sent through
  the existing sovereign SMTP sender, with safe built-in defaults.
- **Public checkout page.** A clean, JavaScript-free `/checkout` flow (so it
  satisfies the strict CSP and works for signed-out readers) collects the
  payer's details, opens the order, and shows the payment instructions +
  reference. The public pricing page's paid plans now link straight to it when
  Payments is enabled.
- **Advertising — operator-managed ad slots that only render when activated.** A
  new **Advertising** console manages ad *slots* targeting page placements
  (header, above/below the post, sidebar, footer) with three creative kinds:
  same-origin **image + link** house ads, sanitised **HTML** creatives, and
  **Google AdSense** units. Nothing renders until the **Advertising** module is
  on *and* the individual slot is enabled, so a fresh install never shows an
  advert unexpectedly.
- **Google AdSense as an optional integration.** Surfaced as a toggleable module
  in *Tools & Plugins*; set your publisher id in the Advertising console and add
  slots of kind *AdSense*. Pages that actually render an AdSense unit
  automatically widen **only that page's** Content-Security-Policy to admit the
  vetted Google ad origins — every other page keeps the strict baseline.
- **More monetization modules in Tools & Plugins.** *Affiliate disclosure* (an
  FTC-style banner shown above posts) and a *Sponsor banner* placement join
  Payments, Advertising and Google AdSense under a new **Monetization** category
  in the plugin panel, each independently toggleable.

### Security

- All monetization writes are CSRF-protected and admin-gated; the connected
  -gateway webhook is verified with a constant-time HMAC-SHA256 comparison and
  its signing secret is stored encrypted at rest (AES-256-GCM) in the existing
  credential store. Ad image/link URLs are scheme-validated (same-origin or
  http(s) only) and HTML creatives are sanitised before emit, so a crafted
  creative can never inject script. Order fulfilment is idempotent, so a
  duplicate confirmation or replayed webhook never upgrades or emails twice.

## [1.19.0] — 2026-06-26

### Added

- **Newsletter console (`/os/newsletter`) — a full operator console where the
  sidebar item previously dead-ended.** Clicking *Newsletter* now opens a real
  dashboard instead of a missing page. See ADR-0090.
- **Audience health at a glance** — total subscribers, active (confirmed),
  pending double-opt-in, unsubscribed, 30-day new signups, and a confirmation
  rate, with a dependency-free 30-day growth sparkline.
- **Subscriber management** — a searchable table with status-segment tabs
  (All / Active / Pending / Unsubscribed), instant client-side search, per-row
  delete (GDPR erasure / spam cleanup), and one-click CSV export of the full
  list.
- **Broadcast composer** — compose a subject, plain-text body and optional HTML,
  send a **test message** to any address first, then send to every confirmed
  subscriber. An unsubscribe link is appended to every message automatically.
- **Persisted broadcast history** — each send is recorded with its audience
  size and final sent/failed tallies (table `newsletter_broadcasts`), surfaced
  as a delivery-history table so operators can see what went out and how it did.
- **SMTP status awareness** — the console clearly indicates when SMTP is not
  configured and disables sending until it is, while still allowing signups and
  confirmations.
- New session-authed endpoints under `/os/api/newsletter/*`
  (`stats`, `subscribers`, `broadcasts`, `export.csv`, delete, `test`,
  `broadcast`) so a browser operator no longer needs an API key.

### Upgrade Notes

- Migration `044-newsletter-broadcasts` adds the broadcast-history table. It is
  additive and backward-compatible; existing subscribers are untouched.

## [1.18.0] — 2026-06-26

### Added

- **Update & Backup — a one-click VayuOS surface for software updates and full
  site backups.** A new **Update & Backup** page (under *System* in the sidebar
  and the command palette) brings two operator tasks that previously required
  shell access into the admin panel. See ADR-0089.
- **One-click software update — no command line, nothing left half-done.** Check
  GitHub for the latest signed release and install it in a single click: the
  release is downloaded, its SHA-256 checksum **and** Ed25519 signature are
  verified against the pinned release key, your database is backed up
  automatically, the binary is swapped atomically, and the service re-launches
  itself to activate the new version. A **Roll back** action restores the
  previous binary and restarts. Every step is recorded in the update history and
  the tamper-evident audit log.
- **Full backup, export & import — with no size limit.** Download your entire
  site — the database and every setting — as a single consistent, checksummed
  `.tar.gz`, and restore it on this or another server. The database is copied
  with SQLite `VACUUM INTO` for a clean, point-in-time snapshot (no torn pages).
  Export streams straight to the download and import streams the upload to disk,
  both in constant memory with the request timeouts lifted, so multi-gigabyte
  sites move without trouble. A restore validates the archive (checksum +
  integrity check + schema check), backs up the current database first, then
  swaps the restored data in atomically at startup and restarts.

### Security

- One-click apply keeps every guarantee of the CLI updater (ADR-0064): it still
  requires the operator opt-in (`VAYU_SELFUPDATE_ENABLED=true`) and a pinned
  release public key (`VAYU_RELEASE_PUBKEY`), refuses in
  read-only/quarantined/maintenance modes, and verifies signatures before
  writing anything. All update, restart, rollback, export and import actions are
  admin-role gated, CSRF-protected, and written to the WORM audit log.

## [1.17.0] — 2026-06-26

### Added

- **API Keys console — a dedicated VayuOS surface for credential management.** A
  new **API Keys** page (under *System* in the sidebar, and in the command
  palette) brings two long-missing capabilities into the admin panel. See
  ADR-0088.
- **Issue, rotate and revoke your own API keys from the UI.** Previously the
  VayuPress API accepted a single static key set via the `API_KEY` environment
  variable. You can now mint as many labelled keys as you need at runtime —
  for a deploy bot, CI, a third-party integration — and **rotate** or **revoke**
  any of them instantly without a restart. Only a SHA-256 hash of each token is
  stored; the full key is shown exactly once at creation/rotation and is never
  recoverable afterwards. The `API_KEY` env value still works as a bootstrap
  credential.
- **Encrypted third-party service credentials.** Store the secrets VayuPress
  uses to talk to other services directly in the admin panel, **sealed with
  AES-256-GCM at rest**. Secrets are shown only as a masked hint after saving
  and can be revealed on demand. First-class cards ship for:
  - **IndexNow** — set the submission key in the UI (no more env-only key, and
    no manual file upload). The ownership-verification file at
    `/.well-known/<key>.txt` is now served automatically whenever a key is
    configured, so instant search-engine indexing works out of the box.
  - **OpenRouter** — hosted access to a wide range of AI models via a single key.
  - **Local AI (Ollama)** — point VayuPress at a self-hosted model runtime so AI
    features run on infrastructure you control; no data leaves your server.
  - **n8n** — wire VayuPress events into automation workflows via a webhook.
  - **Custom** — store an API key for any other service by name.
- **Rotation is 100% automated — nothing to re-enter.** Stored credentials are
  encrypted with a persistent, randomly-generated **Data Encryption Key (DEK)**
  held in a new keyring, deliberately decoupled from any authentication
  credential. Rotating an API key (or even the bootstrap `API_KEY`) therefore
  never makes a stored secret undecryptable — there is no manual migration step.
  Optionally set `VAYU_SECRET` to wrap the DEK for defence-in-depth; the
  encryption secret can be introduced or changed in place without re-entering a
  single credential.
- **Auto-managed internal/system key.** VayuPress provisions a dedicated
  **System** key for internal use automatically — you never have to create or
  configure it. Internal automation always reads the live value, so rotating it
  propagates instantly with no manual step; it can be rotated but never revoked
  or deleted. Operator-issued keys remain explicitly managed.

### Changed

- The API authentication middleware now accepts keys issued from the API Keys
  console in addition to the static `API_KEY`, with the same constant-time /
  lockout protections.

### Security

- Third-party secrets and issued API keys are never persisted in clear text:
  service credentials are AES-256-GCM encrypted under a dedicated keyring DEK,
  and API keys are stored as one-way SHA-256 hashes.

## [1.16.0] — 2026-06-26

### Added

- **Subscription Engine v2 — the Members console becomes a revenue cockpit that
  goes well beyond what a hosted newsletter platform shows you.** The Members
  page now opens on a dashboard with eight headline metrics — Monthly Recurring
  Revenue (with a coloured 30-day net-movement trend), annual run-rate, paid
  members (with how many are trialing), total members and 30-day new signups,
  free-to-paid **conversion rate**, 30-day **churn rate**, **ARPU** (average
  revenue per paying member) and an estimated **lifetime value (LTV)** — none of
  which existed before. See ADR-0087.
- **Growth & revenue at a glance.** A dependency-free inline SVG sparkline plots
  new members per day for the last 30 days, and a "revenue by tier" panel breaks
  MRR down across your plans with proportional bars.
- **Member activity feed.** Signups, subscription starts, trials, upgrades,
  cancellations, complimentary grants and failed payments are recorded to a new
  per-member activity log and surfaced both as a site-wide recent-activity feed
  on the dashboard and as a timeline on each member's detail record.
- **Free trials per tier.** A tier can grant an N-day free trial: the member
  gets full paid access immediately but contributes nothing to MRR until the
  trial converts, and trialing members are counted separately.
- **Graceful cancellations.** Operators (and the Stripe webhook) can now cancel a
  member at the end of their paid period — keeping access until then — instead of
  only revoking access immediately. A per-member **Cancel** action sits in the
  console (hold Shift to cancel immediately).
- **One-click CSV export** of your entire audience (email, name, tier, status,
  newsletter opt-in, monthly value, labels, joined/last-seen) for backups or for
  migrating an audience in from another platform.
- **Member search.** An instant client-side filter over email, name and labels
  on the members table.
- **Optional Stripe price wiring per tier.** Tiers can store a monthly/yearly
  Stripe Price id so a hosted Stripe Checkout can be linked without any embedded
  payment SDK, and the webhook now also reconciles
  `customer.subscription.updated` (scheduled cancel), `customer.subscription.deleted`
  (cancellation) and `invoice.payment_failed` (at-risk flagging).

### Changed

- Membership MRR now excludes subscriptions still inside a free trial, matching
  how trialing members are reported, so the figure reflects truly recurring
  revenue.

### Upgrade Notes

- Migration `040-subscription-engine` adds trial/Stripe-price columns to tiers,
  trial/cancel-at-period-end columns to subscriptions, and a new `member_events`
  activity log. It is additive and backward-compatible; existing members, tiers
  and subscriptions are untouched.

## [1.15.0] — 2026-06-26

### Added

- **Theme Studio overhaul — applying a theme now restyles the *whole* public
  site, not just colours.** Every built-in design theme (Gale, Zephyr, Dispatch,
  Vivid, Beacon, Ripple, Maverick, Agora, Apex) now styles the real `vayu-*`
  public markup across every section — navigation, homepage hero, post
  list/cards (including cover-image cards), article body and headings, the
  author box, related posts, the comments section, and the multi-column footer —
  in that theme's own visual language. Switching themes visibly transforms the
  entire blog. See ADR-0086.
- **Layout archetypes for colour presets.** Each colour-only preset is assigned
  a reusable layout archetype (Minimal / Classic / Magazine / Editorial / Bold)
  carried as the new `archetype` customization option, so even a palette swap
  changes structure, spacing, cards and hero — not just colour.
- **Opt-in homepage hero for a clean homepage.** A new `home.hero` setting
  (off by default) opens the homepage straight into the post list; the hero is
  toggled from **Theme Studio → Hero section**.
- **Theme Studio consolidation + uploads.** Design settings (logo, brand
  colours, layout, fonts, hero, article-page options, and the social/OG share
  image) are now edited in one place — the Theme Studio — with a live preview
  and same-origin uploads; the Settings → Design tab points operators there.
- **VayuPortal — a clean, minimalist membership overlay for the public site.**
  A floating launch button plus an accessible slide-in panel now lets readers
  sign up, sign in, and manage their account without leaving the page they are
  reading. It is a self-contained, dependency-free, same-origin script that
  renders only when membership is enabled, transparently upgrades the existing
  nav "Sign in" / "Sign up" links to open the panel in place, and reuses the
  existing passwordless magic-link backend. Served at `/static/js/portal.js`
  with styles in `/static/css/portal.css`; capability/auth state comes from the
  new `GET /api/v1/members/me` endpoint.
- **"Sign in with VayuMail" for readers.** Members who hold a VayuMail mailbox
  can now sign in directly with their mailbox email and password via
  `POST /api/v1/members/vayumail-login`, as an alternative to the emailed
  sign-in link. Bad credentials return a uniform response so the endpoint cannot
  enumerate addresses.
- **Optional two-factor authentication (TOTP) for VayuMail accounts.**
  Administrators can enrol any mail account in 2FA from
  **VayuOS → Mail accounts** (scan/enter a secret, verify a code to turn it on,
  or disable it). When enabled, the "Sign in with VayuMail" flow requires a
  valid 6-digit code as a second factor. Uses the existing stdlib-only TOTP
  implementation; secrets are stored per account and 2FA is only switched on
  after a code is verified, so a half-finished enrolment can never lock anyone
  out.

- **Role-based VayuMail access after portal sign-in, plus a new mail-only
  role.** When a reader signs in with their VayuMail mailbox, they can now open
  VayuMail according to that account's role: `administrator`, `editor`, and
  `author` mailboxes reach the VayuOS console with the matching capabilities
  (admin / editor / author), while `reviewer` and the new mail-only **mailbox**
  role are confined to the VayuMail surface only. The new **mailbox** role joins
  the existing administrator/editor/author/reviewer roles for handing out
  pure-email identities with no console access. The portal account view surfaces
  an "Open VayuOS console" (or "Open VayuMail") shortcut, and signing out clears
  both sessions at once.

- **Commenting is now members-only.** Posting a comment requires an
  authenticated member session — readers sign in via the membership portal
  (magic link) or with a VayuMail mailbox — and the commenter's identity is
  taken from that session rather than the request body, so comments can no
  longer be posted anonymously or under a spoofed identity. The comment widget
  shows a "Sign in to comment" prompt to signed-out readers.

### Security

- **VayuMail is now role-scoped: non-admin staff can only operate their own
  mailbox.** Previously any signed-in user could browse, read, search, and act
  on any mailbox via the `?user=` parameter, and could open the mail-account
  management screens. The webmail (inbox, message, search, compose, send, draft,
  message actions) now locks non-admins to their assigned mailbox and rejects
  cross-mailbox access; mail-account management (create/update/delete/list) is
  restricted to administrators.

- **Hardened the profile avatar preview against DOM-based injection (CodeQL:
  "DOM text reinterpreted as HTML").** The live avatar preview assigned the raw
  text field value to an element attribute; it now passes the value through a
  protocol allowlist (`new URL` + http/https check) before touching the DOM, so
  only safe image URLs are ever loaded. The server already validated avatar URLs
  as `http(s)` on save — this closes the client-side path too.

### Fixed

- **Theme Studio gallery now always renders.** The Theme Studio page
  (`/os/theme`) dereferenced the settings store without a nil-guard, so if the
  store was not ready (startup race / init failure) the page panicked (HTTP 500)
  and the theme gallery "did not show anywhere" while every other VayuOS page
  kept working. The handler now falls back to defaults and always renders the
  full Studio, including the gallery.
- **VayuOS stylesheet/script updates now reach the browser immediately.** The
  admin CSS/JS were cache-busted only by the release version, so iterative
  changes within a build could be masked by a stale cached `admin-os.css`
  (most visibly: the profile avatar preview rendering at full size). Assets are
  now versioned by a short content hash, so an updated file is refetched as soon
  as its content changes, while unchanged files stay cached.

### Added

- **Admins can assign a custom VayuMail mailbox to each team member, and staff
  operate it from their own panel.** From **Members → Team & roles**, an admin
  assigns a mailbox (`name@yourdomain` + password) to any admin/editor/author;
  the address is linked to their account and the mailbox + PGP keypair are
  provisioned automatically. Signed-in staff then use the VayuMail panel scoped
  to their own mailbox — inbox, read, compose, reply/forward, and send all run
  as their assigned address. The role↔mailbox mapping is admin→administrator,
  editor→editor, author→author. New endpoint:
  `POST /api/v1/admin/users/{email}/mailbox` (mirrored at `/os/api/...`),
  admin-only and CSRF-protected. Migration `039-user-mailbox` links the address.

- **Posts manager now paginates the full archive instead of capping at 500.**
  The VayuOS Posts screen previously rendered a single hard `LIMIT 500` list, so
  older posts beyond the 500th were unreachable. It now serves the archive in
  pages of 100 (newest first on page 1) with a premium pager:
  - **Page navigation** — First / Last, Prev / Next, a windowed run of numbered
    pages around the current one, and **jump ±10 pages** controls for skating
    through large archives. A "Go to page N of M" box jumps directly to any page.
    Every control is a plain GET link/form, so navigation works without
    JavaScript and stays within the strict CSP.
  - **Find any post by time** — a time-range filter with quick presets (last 7 /
    30 / 90 days, last 12 months) plus a custom **From / To** date range filters
    posts by their created date so you can pull up a specific period instantly.
  - **Server-side search & status tabs** — title/tag search and the
    All / Published / Drafts tabs now filter across the entire archive (not just
    the current page), and the tab counts reflect the active search/date filter.
    A "showing X–Y of Z posts" summary keeps the position clear. All filters are
    preserved in the URL, so a filtered view is shareable and survives reloads.

- **Premium membership system — multi-tier plans, a member portal, and revenue
  insight.** VayuPress memberships grow from a free/paid switch into a complete
  membership product, all still sovereign and passwordless:
  - **Priced subscription tiers** — operators define named plans with monthly
    and/or yearly pricing, a description, and a benefit list. Two tiers ship
    seeded (Free and Premium); add, edit, hide, or archive more from the Members
    console. The built-in Free and Premium plans are protected from deletion.
  - **Public pricing page (`/pricing`)** — a themed, responsive plan grid built
    from the published tiers, with a JSON catalogue at `GET /api/v1/tiers` for
    themes and integrations. Plans, prices, and benefits render server-side with
    no inline styles, so the strict CSP is untouched.
  - **Member portal (`/members/account`)** — signed-in readers see their plan
    (with price/cadence), edit their display name, toggle the members newsletter,
    and sign out. A dedicated sign-in page (`/members`) replaces the previously
    dead "Sign in" link, and verifying a magic link now lands members in their
    portal.
  - **Richer member records** — members gain a display name, an operator note,
    a newsletter preference, last-seen activity, and free-form **labels** for
    segmentation. The Members console shows each member's tier (changeable
    inline), labels (add/remove), last-seen, and join date.
  - **Subscription state + MRR** — every paid grant or Stripe upgrade records a
    subscription (tier, cadence, amount), so the console surfaces **Monthly
    Recurring Revenue**, annual run-rate, paid/free split, active subscriptions,
    and 30-day signups. Yearly plans are normalised to monthly; complimentary
    (operator-granted) plans never inflate revenue.
  - **Tier-aware paywall** — gated posts now show the cheapest paid plan's price
    and benefits with a clear call to action, alongside the inline passwordless
    sign-in form.
  - **New admin/JSON API** — `GET/POST/PUT/DELETE /api/v1/admin/tiers[/{id}]`,
    `GET /api/v1/admin/members/stats`, `GET /api/v1/admin/members/{email}`, and
    `POST/DELETE /api/v1/admin/members/{email}/labels[/{label}]`, all
    CSRF-protected on writes and mirrored under `/os/api/members/*` for the
    session-authenticated console. The Stripe webhook continues to upgrade paid
    members with no embedded payment SDK — it now also records the subscription.
- **Team roles, staff mailboxes, and author profiles.** Membership now spans the
  people who run the site, not just readers:
  - **Roles** — accounts are **admin**, **editor**, or **author**. Admins manage
    the team from the Members console: create accounts, change roles inline, and
    remove people. The last remaining admin cannot be demoted, so a site can
    never lock itself out of administration.
  - **Sovereign staff mailboxes** — creating a team member auto-provisions a
    VayuMail mailbox (`name@yourdomain`) and a PGP keypair when VayuMail is
    active, giving authors and editors a real, self-hosted email address with no
    third-party provider.
  - **Author profiles** — every staff member edits their own public profile from
    **My Profile**: a display name, a short bio (capped at 250 characters), an
    avatar image, and social links (website, X, GitHub, LinkedIn, Mastodon,
    Instagram, YouTube). Profiles render at **`/author/{id}`** as a themed,
    indexable page. Social/avatar URLs are validated as `http(s)` and the public
    page is escaped end to end. The signed-in member's avatar now appears in the
    VayuOS sidebar (linking to the profile editor), and the editor shows a live,
    fixed-size cropped circular thumbnail so large images never display at full
    resolution.
  - New CSRF-protected API: `PUT /api/v1/admin/users/{email}/role`, mirrored
    (with the existing user create/list/delete) under `/os/api/users/*` for the
    session-authenticated console, plus `POST /os/api/profile` for self-service
    profile edits. Migration `038-user-profiles` adds the profile columns.

### Added

- **Browsable tag pages — clicking a tag now opens a real page.** Tag links on
  posts pointed at `/tags/<tag>`, but no route served that path, so every tag
  click fell through to the 404 page. VayuPress now ships a complete public
  taxonomy:
  - **`/tags` topic index** — a premium tag cloud listing every tag with its
    published-post count, most-used first, styled with the existing theme tokens
    so it adapts to every preset and light/dark mode.
  - **`/tags/<tag>` listing** — each tag opens its own page listing the matching
    posts (most recent first) with the same card layout as the homepage. Tag
    matching is exact and case-insensitive (so `go` never collides with
    `golang`), drafts are excluded, and a tag with no published posts returns a
    proper 404 instead of an empty indexed page.
  - Per-tag pages are disk-cached at `tags/<tag>.html` and invalidated
    automatically when an article carrying that tag is created, updated, or
    deleted (reusing the existing cache-purge path). The topic index renders
    live so newly introduced tags appear immediately.
  - Tag URLs are path-escaped end to end (links, canonical tags, and sitemap),
    and both `/tags` and every `/tags/<tag>` page are now emitted in
    `sitemap.xml` for discovery.

- **VayuAnalytics gets a tabbed dashboard + a richer Live view.** The analytics
  page was one long scroll; it is now split into client-side tabs (no reload):
  Overview, Live, Pages, Audience, Geography, Campaigns, Events, Goals, Journey
  and Export. The selected tab is remembered in the URL hash so refreshes and
  shared links reopen it.
  - **Live view is now real-time situational awareness:** a large active-visitor
    counter plus three live panels — **Countries** (with flag + full name),
    **Active pages**, and **Referrers** — all refreshing every 10s and pausing
    when the browser tab is hidden. Backed by an extended `Store.Realtime`
    (active countries + referrers over the last 5 minutes).
  - Country flags are now **real self-hosted SVG images** (the MIT-licensed
    flag-icons set under `static/flags/`, served on demand from
    `/os/static/flags/<cc>.svg`), so they render identically on every platform
    — including Windows, which omits flag emoji from its system font. The full
    country name is always shown alongside. No third-party requests; flags load
    only when shown.

- **Optional outbound smarthost relay (deliverability without losing
  sovereignty).** When `VAYUOS_MAIL_RELAY_HOST` is set, VayuMail delivers
  outbound mail through an authenticated SMTP relay instead of direct-to-MX —
  the pragmatic remedy for a fresh self-hosted IP that Gmail/Outlook still
  spam-file for lack of sending reputation. The relay's established IP reputation
  carries deliverability, while **inbound receive, IMAP, local delivery and DKIM
  signing all remain self-hosted**, and VayuMail still DKIM-signs with the domain
  key so DMARC stays aligned. STARTTLS (587) and implicit TLS (465) are
  supported with `AUTH PLAIN`/`LOGIN`; encryption before AUTH is required by
  default (`VAYUOS_MAIL_RELAY_TLS=off` to opt out on a trusted private network).
  Credentials are read from the environment only and never persisted. Direct-to-
  MX remains the default when no relay is configured. The deliverability panel
  shows when a relay is active. See ADR-0085.

- **Mailbox usability: drafts, mark-as-read, and a deliverability self-check.**
  - **Drafts** — Compose now has a **Save as draft** button that files the
    message into the sender's Drafts folder; opening a draft from the mailbox
    reloads it in the composer to finish and send (`Engine.SaveDraft`).
  - **Mark as read / unread** — the reader view has ✓ **Mark read** / **Mark
    unread** actions and each Inbox row has a per-message read toggle, backed by
    proper Maildir Seen-flag moves (`Engine.MarkRead` / `MarkUnread`).
  - **Deliverability self-check** — the Mail & DNS panel now flags the common
    reasons mail is marked as spam: a **DKIM key published in DNS that does not
    match VayuMail's signing key**, and a **reverse-DNS (PTR) mismatch** against
    the mail hostname (`Engine.Deliverability`).
- **VayuAnalytics dashboard polish.** The privacy-first analytics page now reads
  far more clearly at a glance:
  - **Full country names + flag emoji.** Country breakdowns show e.g.
    "🇺🇸 United States" instead of the raw `US` code. The mapping is render-only
    (ISO 3166-1 alpha-2 → name, flag derived from Regional Indicator Symbols);
    nothing extra is stored, so the no-GeoIP / no-PII guarantee is unchanged.
  - **Period-over-period deltas** on the headline metrics (Unique visitors /
    Visits / Pageviews / Bounce rate) — an up/down badge comparing the selected
    window to the immediately-preceding window of equal length, powered by the
    new `Store.OverviewBetween`. Colour semantics are inverted for bounce rate
    (lower is better).
  - **Cleaner page URLs** in Top pages and Visitor journey: query strings
    stripped, percent-encoding decoded, long paths truncated with the full value
    kept in a tooltip.
  - **Friendlier empty states** for campaigns, countries, page views, referrers
    and visitor journeys (actionable guidance instead of a bare "No data yet").
  - **"Last updated" timestamp** in the page header, a **local-only privacy
    footer**, a **loading cue** when switching the time range, and **mobile
    single-column layout** with horizontally swipeable tables.
- **TLS for mail (STARTTLS + IMAPS + authenticated submission).** The mail
  listeners now offer encryption: **STARTTLS** on SMTP `:25`, the new
  **submission** service `:587` (STARTTLS **required** before `AUTH PLAIN`/
  `LOGIN`, then authenticated relay), and IMAP `:143`; plus implicit-TLS
  **IMAPS** on `:993`. A CA-signed certificate can be supplied via
  `VAYUOS_MAIL_TLS_CERT` / `VAYUOS_MAIL_TLS_KEY`; when unset, VayuMail generates
  an in-memory self-signed certificate so opportunistic STARTTLS works
  immediately. All TLS listeners are best-effort (a bind/cert failure is
  surfaced in the health panel but never blocks outbound/local mail). The health
  row now shows which secure listeners are active (`STARTTLS`, `submission:587`,
  `IMAPS:993`).
- **Inbound SPF / DKIM / DMARC verification.** Received mail is now
  authenticated during the SMTP transaction: **SPF** (connecting IP vs the
  envelope sender), **DKIM** (signature verification), and **DMARC** (policy +
  identifier alignment with the From domain). The outcome is stamped as a
  standard `Authentication-Results` header, and a DMARC failure under an
  enforcing policy (`p=quarantine`/`p=reject`) is routed to **Junk** via the
  existing local filter. All lookups are best-effort — a DNS error degrades to
  `none`/`temperror` and never blocks delivery. Implemented with the vetted
  `github.com/emersion/go-msgauth` (DKIM/DMARC) and `blitiri.com.ar/go/spf`
  libraries (completes the ADR-0078 follow-up).
- **Clean reader view for received mail.** The message page now shows a decoded
  view — From / To / Cc / Subject / Date summary plus the rendered `text/plain`
  body (or sanitised HTML when that's all a message carries) — instead of raw
  MIME. A **"View raw source"** toggle reveals the full original headers/MIME on
  demand. HTML is sanitised through a bluemonday UGC policy so it respects the
  console's strict CSP. New `mail.ParseMessage` decodes multipart/alternative,
  quoted-printable, base64, and RFC 2047 encoded-word headers.

### Changed

- **Inbound mail is now enabled by default.** Once a `DOMAIN` is configured, the
  SMTP-receive + IMAP read listeners start automatically so the instance can
  actually receive external mail; previously this required the easily-missed
  `VAYUOS_MAIL_INBOUND=on` opt-in. Set `VAYUOS_MAIL_INBOUND=off` to run
  outbound-only. Binding the mail ports is best-effort: a failed bind (e.g.
  `:25` without privileges, or a port already in use) is recorded
  (`Engine.InboundError`), surfaced in the VayuOS health panel **with an
  actionable hint** (grant `CAP_NET_BIND_SERVICE`, or stop a conflicting MTA
  like Postfix), and **never** fails engine startup — outbound and local
  delivery stay available. Amends ADR-0078. The shipped `deploy/vayupress.service`
  now grants `CAP_NET_BIND_SERVICE` so the non-root service can bind `:25`/`:143`.
  (Receiving external mail still also requires port 25 reachable and MX/A DNS
  records pointing at the host.)

- **Outbound deliverability hardening (fewer messages in Gmail/Outlook spam).**
  - **DKIM signing now uses the vetted `github.com/emersion/go-msgauth/dkim`
    library** instead of a hand-rolled canonicalizer. A subtle canonicalization
    bug is one of the most common reasons a message that "looks" signed still
    fails verification at the receiver and is filed as spam; delegating to the
    same battle-tested implementation already used for inbound verification
    removes that entire class of risk. Signing remains relaxed/relaxed,
    rsa-sha256, `d=` aligned to the From domain.
  - **Well-formed MIME.** Messages with both a text and an HTML body are now sent
    as a proper `multipart/alternative` (text part first, HTML second) — the
    shape mainstream mail clients send and spam filters expect — with explicit
    `Content-Transfer-Encoding` and canonical CRLF line endings throughout. The
    inline PGP path is unchanged (a single ASCII-armored `text/plain` part).
  - **Deliverability self-check** now also flags a **mail hostname that is not a
    fully-qualified domain name** (announced in EHLO/HELO), alongside the
    existing DKIM-key and reverse-DNS (PTR) checks.

### Fixed

- **Home and topic post cards now show a clean title and excerpt instead of raw
  markup.** A post whose body began with a `<style>` or `<script>` block leaked
  its CSS/JS as the card "excerpt", because the plain-text helper stripped tags
  but kept their inner text. A new `render.PlainText` removes non-rendered blocks
  (`<style>`, `<script>`, `<head>`, `<noscript>`, `<template>`, `<svg>`) and HTML
  comments in full before stripping the remaining tags, unescapes entities, and
  tidies whitespace — so only readable body text reaches the card. The cards were
  also redesigned: each is now a clean grid card showing the post's cover image
  (the first image in the body, when present), a `date · author` line, the title,
  and a three-line excerpt. Inline tag chips were removed from the cards for a
  calmer, more readable feed. The same treatment applies to tag listing pages.
  - **The home page no longer lags behind after a redeploy.** Pre-rendered public
    HTML (`home/index.html`, `tags/*.html`, `posts/*.html`) is now fingerprinted
    with the renderer version and stylesheet hashes; on startup any cache produced
    by an older renderer is cleared, so a redeploy always serves the current
    design rather than a stale cached home page.
  - **Broken cover images are hidden.** A small same-origin script (CSP-safe,
    `script-src 'self'`) removes a card's cover image if it fails to load (or was
    blocked), so an expired/broken image link never shows a broken-image icon.

- **Theme Studio: deploying a theme now restyles the whole public site, not just
  colours.** The token compiler bridges the active theme onto the variables the
  public templates actually read (`--bg`, `--surface`, `--text`, `--accent`,
  `--font`, `--max-w`, `--radius`), with explicit `[data-theme]` blocks so the
  manual light/dark toggle re-themes the site too. The built-in design themes
  (Gale, Zephyr, Dispatch, Vivid, Beacon) now style the real `vayu-*` markup, so
  each visibly changes layout and typography rather than only recolouring. The
  Theme Store "Customize" action no longer reverts to the active theme and
  carries the selected theme's design through to Apply.
- **Compose Send / Save-as-draft no longer fail with `403` after a while.** The
  VayuOS panel pages did not re-issue the `vp_csrf` cookie, so once it expired
  (1h) every panel POST (send, save draft, message/account actions) was rejected
  as a CSRF failure. The VayuOS GET pages are now wrapped in the CSRF middleware
  so each page load re-seeds the token, and a `403` now shows a clear
  "reload the page" hint instead of a bare error.
- **Outbound mail now carries the sender's display name.** Messages put a
  friendly `From: "Full Name" <addr>` header (from the mail account's full name,
  or the CMS user's name) so recipients see a name instead of a bare address.
  The SMTP envelope (MAIL FROM) and the outbound queue still use the bare
  address, and the DKIM signature is unaffected.

- **Incoming mail now lands in the recipient's Inbox (local delivery loopback).**
  Mail addressed to a mailbox served by this instance was only ever enqueued for
  external MX relay, so it never appeared in the local recipient's Inbox even
  though outbound delivery to remote servers worked. `Engine.SendMail` now
  splits recipients: local-domain mailboxes (a CMS user or an admin-managed mail
  account, resolved through the new `Bridge.IsLocalRecipient`) are delivered
  straight into their Maildir via the existing inbound path — honouring the
  heuristic junk filter — while only genuinely remote recipients are queued for
  MX relay. When no bridge is wired the engine falls back to a domain-only check
  matching the inbound SMTP relay policy.
- **PGP keys now show for every mailbox.** Keypairs were auto-generated only for
  CMS users at registration, so admin-managed mail accounts (and accounts that
  pre-dated auto-keygen) had no key and the VayuPGP panel listed nothing for
  them. A new idempotent `Bridge.EnsureKeypair` mints a key the first time and
  reuses the existing one by email thereafter; it is invoked on mail-account
  creation and from a non-blocking boot-time backfill that covers existing CMS
  users and mail accounts. Transparent inbox decryption now resolves the
  recipient through the key store (new `Bridge.DecryptForEmail`) rather than the
  CMS user table, so it works for mail-only accounts too. Private keys remain
  AES-256-GCM encrypted at rest.

---

## [1.14.0] — 2026-06-25

**The Post Editor becomes the most powerful sovereign writing studio — without
breaking the constitution (single binary, lightweight, privacy-first, strict CSP).**

### Added

- **Five new editor blocks**, each rendered and re-sanitised server-side by
  `internal/blockrender` (bluemonday UGC policy — no raw-HTML escape hatch):
  - **Table** — optional heading row plus body rows; cell text supports inline
    Markdown (bold/italic/code/links).
  - **Toggle** — a collapsible `<details>`/`<summary>` with an "expanded by
    default" option.
  - **Task list** — a checklist with per-item done states, rendered as a static
    glyph (never a live `<input>` on the public page).
  - **Math** — a LaTeX/expression block stored verbatim and shown in a styled,
    dependency-free element (a theme may progressively enhance `.vp-math`).
  - **Audio** — a self-hosted `<audio>` player whose `src` is **restricted to the
    site's own `/media` path** (double-guarded by `safeMediaURL` and a bluemonday
    `Matching` rule), so audio never triggers a third-party request.
- **Drag-and-drop block reordering** plus keyboard `↑`/`↓` move buttons.
- **Undo / redo** history for structural edits (native per-field text undo is
  preserved — `Ctrl/Cmd+Z` is only intercepted outside an editable field).
- **Live word count, character count and reading time** in the editor sidebar and
  topbar.
- **Focus mode** (`Ctrl/Cmd+.`) for distraction-free writing, and a **split-screen
  live preview** that renders the sanitised published look beside the draft.
- **Command palette** — the slash (`/`) menu is now grouped by category and fully
  keyboard-navigable (↑/↓/Enter/Esc), and a global **`Ctrl/Cmd+K`** opens it from
  anywhere.
- **Markdown shortcut** — typing `- [ ]` or `* [ ]` (then a space) converts a paragraph into a task list.
- Legacy **HTML import** now maps `<table>` → table blocks and `<details>` →
  toggle blocks (`internal/blockrender/importer.go`), keeping "Convert to blocks"
  lossless for more content.

### Changed

- The `osEditorBody` editor shell gained CSP-safe controls (focus/split buttons,
  word-count chip, document-stats panel, undo/redo) — markup stays class-only with
  no inline styles, scripts or external hosts (verified by `TestOSEditorBodyCSPSafe`).
- The editor frontend (`static/js/admin-os-editor.js`) is rebuilt around the new
  block model while preserving the save/preview/AI/history network contract and the
  on-disk **block storage format (fully backward compatible)**.

### Security

- New blocks add **no new XSS surface**: the bluemonday policy was widened only to
  the structural elements the renderer emits (tables, `details`/`summary`, a
  local-only `<audio>`), and audio sources are constrained to the `/media` origin.
  Regression tests cover table/cell XSS, task-list `<input>` suppression, math
  escaping, and rejection of external/`javascript:` audio sources.

### Tests

- Added `internal/blockrender` unit tests for every new block type and its
  sanitisation, and importer round-trip tests for tables and toggles.

---

## [1.13.0] — 2026-06-25

**VayuMail towards Gmail-like usability — roles, Archive, and search.**

### Added

- **Role-based mail accounts.** Each admin-managed mailbox now carries a role —
  **Administrator**, **Editor**, **Author**, **Reviewer** (read-only), or a
  custom role. Roles are set on creation and editable inline in the Accounts
  table. Permission helpers (`RoleCanSend`, `RoleCanDelete`,
  `RoleCanManageAccounts`) gate per-account capabilities; account creation and
  deletion remain restricted to the VayuPress admin session.
- **Archive folder.** A first-class `Archive` folder alongside
  Inbox/Sent/Drafts/Junk/Trash, with a one-click Archive action on any message.
- **Mailbox full-text search.** A search box over a mailbox scans From / To /
  Subject (with a body fallback) across all folders. The scan is bounded and
  fully local — no external index, no extra services.

### Notes

- This is the foundational slice of the Gmail-like VayuMail roadmap. Threaded
  conversations, rich HTML compose with attachments and scheduling, server-side
  filters, vacation responder, and real-time notifications are tracked for
  v1.14.0.

---

## [1.12.5] — 2026-06-25

**Security: close the reflected-XSS path exposed by v1.12.4.**

### Security

- **VayuMail panel link parameters are now HTML-context safe.** Mailbox links
  embedded `user`/`folder`/`id` values with `url.QueryEscape` only; once the
  html/template passthrough was removed in v1.12.4, CodeQL traced those values
  to the page sink (`go/reflected-xss`). A new `qparam` helper wraps the
  query-escaped value with `html.EscapeString` (a no-op on that output) so it is
  safe in both the URL and the surrounding HTML attribute, clearing the finding
  without changing behaviour.

---

## [1.12.4] — 2026-06-25

**Security: resolve the last CodeQL XSS finding.**

### Security

- **VayuOS trusted-HTML passthrough no longer routes through html/template.**
  `renderTrustedHTML` previously executed a `{{.}}` template with a
  `template.HTML` argument, which CodeQL flagged as an escaping bypass
  (`go/html-template-escaping-bypass`, alert in admin_os_ui.go). The function is
  a verbatim passthrough — every interpolated user value is already escaped via
  html.EscapeString at construction — so it is now a direct string conversion
  with byte-identical output, removing the html/template sink entirely.

---

## [1.12.3] — 2026-06-25

**Security: fix CodeQL path-traversal and SSRF findings.**

### Security

- **Path traversal in the Maildir store (CWE-22).** Untrusted mailbox domain and
  username values now pass through a single-segment sanitiser (filepath.Base of
  a cleaned path) before being joined to the storage base, so a hostile value
  can never escape it; message ids are additionally reduced with filepath.Base.
  Resolves nine CodeQL "uncontrolled data in path expression" alerts under
  internal/vayuos/mail.
- **Server-side request forgery in WKD key discovery (CWE-918).** External
  public-key lookup now validates the recipient domain against a strict
  public-hostname allowlist — rejecting IP literals, localhost and numeric TLDs
  — and URL-escapes the local part before building the request, so a crafted
  recipient domain cannot point the request at internal hosts. Resolves the
  critical CodeQL alert in internal/vayuos/pgp/wkd.go.

### Tests

- Added path-traversal and WKD-domain-validation regression tests.

---

## [1.12.2] — 2026-06-25

**Dependency updates (clear security alerts).**

### Security

- **Upgraded dependencies to their latest published patch releases**:
  `cloudflare/circl` v1.6.3 to v1.6.4, `dlclark/regexp2/v2` v2.2.1 to v2.2.2,
  and `mattn/go-sqlite3` v1.14.46 to v1.14.47. Combined with the pinned
  `go1.26.4` toolchain from v1.12.1, `govulncheck ./...` reports **no
  vulnerabilities**. All other modules were already at their latest versions.

---

## [1.12.1] — 2026-06-25

**CI fix + supply-chain hardening.**

### Fixed

- **CI markdown lint.** The v1.10.0 changelog entry used an empty link and code
  spans with trailing spaces, failing the `lint-markdown` gate. Rewritten to
  satisfy markdownlint (MD038/MD042) so CI is green again.

### Security

- **Pinned a patched Go toolchain** (`toolchain go1.26.4` in `go.mod`). Builds —
  including the in-place `update-vayupress.sh` path on a server — now link the
  fixed standard library, clearing the `crypto/tls`, `crypto/x509`,
  `encoding/pem`, `net/url`, and `net/mail` advisories. `govulncheck ./...`
  reports **no vulnerabilities**. Dependencies were already at their latest
  published versions.

---

## [1.12.0] — 2026-06-25

**Theme import / export in Theme Studio.**

### Added

- **Export theme.** Download the full active theme — design tokens
  (palette/typography/layout) plus the site-wide custom CSS and head/SEO meta —
  as a single portable JSON file from Theme Studio (`GET /os/api/theme/export`).
- **Import theme.** Upload a previously exported theme JSON to apply it
  everywhere (`POST /os/api/theme/import`). Imported tokens are **validated by
  compiling them** before going live, custom CSS is capped at 16 KB, and head
  meta is checked against the same escaped allowlist as the editor — so a bad
  file can never break the site or bypass the CSP.

---

## [1.11.0] — 2026-06-25

**Tumblr-style theme code editing in Theme Studio.**

### Added

- **Custom CSS editor in Theme Studio.** The VayuOS Theme Studio (`/os/theme`)
  now has a full Custom CSS editor (monospace, 16 KB). Styles are served
  same-origin via `/theme.css` — **CSP-safe** (`style-src 'self'`), no external
  origins, no script execution — and apply to every public page on save.
- **Head & SEO meta controls in Theme Studio.** Keywords, theme-colour,
  robots directive, and Google/Bing verification tokens are editable inline.
  Raw `<head>` HTML is deliberately rejected (it could smuggle redirects or
  beacons past the CSP); fields render to a validated, escaped `<meta>`
  allowlist. Saved via a new dedicated `POST /os/api/theme/code` endpoint that
  only touches these keys (never the identity/palette settings).

---

## [1.10.0] — 2026-06-25

**A Ghost-style writing experience for the VayuOS editor.**

### Added

- **Inline rich text.** Block text now renders Markdown inline — **bold**,
  *italic*, `inline code`, links, and ~~strikethrough~~ — across
  paragraphs, headings, quotes, callouts and list items. Output is still run
  through the bluemonday UGC sanitizer (no new XSS surface).
- **Selection formatting toolbar.** Select text in the editor and a floating bar
  appears with Bold / Italic / Code / Strikethrough / Link, wrapping the
  selection in the matching Markdown.
- **Markdown shortcuts while typing.** At the start of a paragraph: `##` then a
  space becomes a heading, `-` or `*` a bullet list, `1.` a numbered list, `>` a
  quote, a triple-backtick fence a code block, `---` a divider — converted
  instantly as you type.
- **Continuous writing flow.** <kbd>Enter</kbd> creates the next block and
  focuses it; <kbd>Shift+Enter</kbd> inserts a soft line break;
  <kbd>Backspace</kbd> on an empty block removes it and returns focus to the
  previous one. New/converted blocks autofocus.
- **Filterable slash menu.** The `/` block palette now has a search box — type
  to narrow the list and press <kbd>Enter</kbd> to insert the first match.
- **Image paste & drag-and-drop.** Paste an image from the clipboard or drop an
  image file onto the canvas to upload it (via the existing media pipeline) and
  insert it inline.

---

## [1.9.3] — 2026-06-25

**Fix: admin panel pages could show stale content after an update.**

### Fixed

- **VayuOS admin pages are now served with `Cache-Control: no-store`.** Admin
  HTML previously carried no cache directives, so a browser (especially mobile)
  or proxy could keep showing an old panel — e.g. the Analytics page appearing
  "unchanged" after a deploy even though the new version was live. Admin pages
  are dynamic and cheap to render, so they are now always served fresh; combined
  with the v1.9.1 versioned (`?v=`) CSS/JS, deploys take effect immediately.

---

## [1.9.2] — 2026-06-25

**Fix: SEO dashboard 502 on large sites.**

### Fixed

- **SEO page no longer times out (502) on large sites.** The content-quality
  tallies (healthy / thin / missing-title) previously scanned every article
  body (`LENGTH(content)` across all rows) on each page load — far too slow on
  a 234k-post database, causing an nginx **502 Bad Gateway**. The scan now runs
  as a single aggregate query in a **background goroutine**, cached for 15
  minutes with throttled refresh, so the SEO page renders instantly and never
  blocks the request path. Numbers show `…` on the very first view and fill in
  within a few seconds.

---

## [1.9.1] — 2026-06-24

**Analytics & mail polish — deeper insights and a more complete mailbox.**

### Added

- **VayuAnalytics — reporting period selector.** Choose any window from 24
  hours up to **3 years** (24h / 7d / 30d / 90d / 6mo / 1y / 2y / 3y) on the
  Analytics page; the selection flows through every card, the goals/journey
  sections, and the export links.
- **VayuAnalytics — conversion goals.** Define named goals as either a page
  view (path, with a trailing-`*` prefix match) or a custom event; the panel
  shows completions, unique converters, and conversion rate over the window.
- **VayuAnalytics — visitor journey / path analysis.** Most common
  page-to-page transitions with synthetic `(entry)`/`(exit)` markers; computed
  on a bounded scan so it stays cheap on large datasets.
- **VayuAnalytics — report export.** Download any report (overview, pages,
  referrers, browsers, devices, OS, countries, regions, cities, UTM, events,
  sessions, goals, journey) as **CSV or JSON**. Exports are computed locally
  and contain no PII.
- **VayuAnalytics — country/region/city.** Coarse location captured
  **server-side from trusted reverse-proxy headers** (Cloudflare
  `CF-IPCountry`/`CF-IPCity`, generic `X-Geo-*`, App Engine). VayuPress performs
  **no GeoIP lookup, bundles no GeoIP database, and never stores an IP**;
  Cloudflare `XX`/`T1` placeholders are dropped.
- **VayuAnalytics — live panel.** A realtime card showing active visitors and
  active pages, refreshing every 10s (pauses on a hidden tab); CSP-safe.
- **VayuMail — built-in junk filter.** Fully-local heuristic scorer files
  obvious spam straight into the recipient's Junk folder on inbound delivery
  (no external services, no network calls); operator-toggleable.
- **VayuMail — account management.** Set a new password or enable/disable an
  existing mail account from the panel (disabled accounts keep their mailbox
  but cannot authenticate).
- **VayuMail — reply & forward.** Compose pre-filled server-side from the
  selected message (original PGP-decrypted for the owner and quoted).

### Fixed

- **Admin-OS asset caching.** Versioned `?v=` query on the VayuOS CSS/JS so a
  deploy always serves fresh panel assets instead of a stale 1-hour browser
  cache.

---

## [1.9.0] — 2026-06-24

**"Stable Private Email" — the inbound half of VayuMail.**

### Added (v1.9.0 — "Stable Private Email")

- **Inbound mail — receive side complete.** Local delivery into Maildir
  (`Engine.DeliverInbound`), mailbox listing/read with path-traversal protection
  (`Maildir.List` / `ReadRaw`), per-account inbox summaries (`Engine.Mailboxes`),
  and a `/os/vayuos/mail/inbox` panel view.
- **SMTP-receive server** (`smtpd.go`) — RFC 5321 listener (EHLO/MAIL/RCPT/DATA/
  RSET/NOOP/QUIT), no-open-relay (only local-domain recipients accepted),
  dot-unstuffing, size caps. Opt-in via `VAYUOS_MAIL_INBOUND=on`.
- **IMAP read server** (`imapd.go`) — RFC 3501 subset (CAPABILITY, LOGIN via
  VayuPress accounts, LIST, SELECT, FETCH incl. BODY[]/FLAGS/SIZE/INTERNALDATE,
  STORE \Seen, NOOP, LOGOUT) so standard clients can read the Maildir.
- **Transparent PGP decryption on read** — IMAP serves decrypted bodies to the
  owning account when VayuPGP holds its key; best-effort, never blocks delivery.

> The inbound listeners are a long-running daemon and therefore strictly
> opt-in (`VAYUOS_MAIL_INBOUND=on`) per the Operational Simplicity Doctrine.

---

## [1.8.0] — 2026-06-24

**Sovereignty release — VayuAnalytics, VayuOS Phase 2 (VayuMail + VayuPGP), and the Theme Studio Gallery.**

The constitution evolves: _Complete digital sovereignty in one binary. Own your
content. Own your communication. Own your infrastructure._ Publishing remains
the core identity; VayuMail is the native sovereignty layer, VayuPGP the native
privacy layer, and VayuOS the native control layer — all in the single binary.

### Added

- **VayuAnalytics** — privacy-first, cookieless, no-PII web analytics stored in
  SQLite: overview, daily pageview series, top pages, referrers (reduced to
  host), browsers/devices/OS buckets, UTM campaigns, custom events, realtime,
  sessions, funnels, weekly retention cohorts, and revenue. Visitor/session
  identity is derived **server-side** from a daily-rotating, crypto-random
  salted hash of (IP + User-Agent + host); the raw IP and User-Agent are
  **never stored**. Public tracking script (`/static/vp-analytics.js`) sets no
  cookies and writes nothing to `localStorage`. Protected JSON API under
  `/api/v1/analytics/*`.
- **VayuPGP** (`internal/vayuos/pgp`) — native PGP on ProtonMail go-crypto:
  Ed25519 (sign) + Curve25519 (encrypt) keypairs, 2-year expiry, private keys
  **AES-256-GCM encrypted at rest** under a master-secret-derived key,
  encrypt/decrypt/sign/verify, encrypt-and-sign, key rotation preserving old
  messages (archived keys), revocation, import/export, and a **WKD server**
  (RFC, advanced method) at `/.well-known/openpgpkey/`.
- **VayuMail** (`internal/vayuos/mail`) — native outbound mail: RFC 6376 DKIM
  signing (relaxed/relaxed, RSA-2048/SHA-256), direct-to-MX delivery with
  opportunistic STARTTLS, durable SQLite retry queue with exponential backoff,
  Maildir storage, MX/SPF/DKIM/DMARC record generation, live DNS health checks,
  and automatic PGP encryption of outgoing mail via WKD discovery.
- **VayuOS kernel** (`internal/vayuos/kernel`) — typed event bus
  (`UserCreated → PGP keypair + mailbox`), ordered boot orchestrator (critical
  steps abort, others degrade), and a health monitor.
- **VayuOS panel** — `/os/vayuos` dashboard plus `/os/vayuos/pgp`,
  `/os/vayuos/mail` (queue + DNS records + live health), `/os/vayuos/security`,
  and `/os/api/vayuos/health` (JSON). All session-protected.
- **Security-update watcher** (`internal/vayuos/secwatch`) — opt-in
  (`VAYUOS_SECURITY_UPDATES=on`) advisory that compares the built versions of
  security-critical crypto dependencies (go-crypto, CIRCL, …) against upstream
  GitHub releases. Disabled by default; sends nothing about the site.
- **Theme Studio Gallery** — 20+ presets including the new **Gale** (editorial
  magazine) and **Zephyr** (bright creative) themes; per-preset embedded CSS now
  reaches `/theme.css` via the CSP-safe Pico bridge; WCAG-AA contrast and ≥44px
  touch targets.
- Migrations `031`–`035` for the analytics session/pageview/event/funnel/revenue
  tables (all indexed).
- Dependencies: `github.com/ProtonMail/go-crypto` (Apache-2.0) and its transitive
  `github.com/cloudflare/circl` (BSD-3-Clause).

### Changed

- Account creation now publishes a `UserCreated` event; with a domain configured
  VayuOS auto-provisions the PGP keypair and Maildir mailbox.
- Analytics retention janitor now also prunes detailed session/pageview/event
  rows beyond `AnalyticsRetainDays` (data minimisation).

### Security

- No cookies, no `localStorage` identifiers, and no IP/User-Agent retention in
  analytics. Public ingest is body-capped (8 KB) and per-IP rate-limited.
- PGP private keys are AES-256-GCM encrypted at rest and never logged; logs
  record only fingerprints. SMTP delivery uses STARTTLS (TLS ≥ 1.2).

### Upgrade Notes

- VayuMail activates only when a real `DOMAIN` is set (not `localhost`); until
  then VayuOS runs in degraded mode and the rest of VayuPress is unaffected.
- The PGP at-rest key is derived from `API_KEY`; keep it stable to retain access
  to stored keypairs.

### Scope

- VayuMail v1.8.0 is the **outbound** sovereignty path. A full inbound MX + IMAP
  server is a governed future milestone (Operational Simplicity Doctrine).

---

## [1.7.0] — 2026-06-22

**VayuOS — unified operator powerhouse, draft/publish workflow, and member signup.**
All operator tools now live in the VayuOS shell (no separate admin panels).
Drafts are a first-class concept with full public-surface isolation. Readers can
sign up through a branded page. Three security vulnerabilities were closed.

### Added

- **Draft/publish workflow** — articles carry a `status` column (`published` |
  `draft`, migration 030). The VayuOS post manager lists all posts with status
  pills; each row has Publish / Unpublish and Edit buttons. The block editor's
  top bar shows the current status and an action button.
- **VayuOS post manager** (`/os/posts`) — single page listing all articles
  (published + draft), filterable by tab, with inline status toggling that
  purges render caches immediately on transition.
- **Six operator consoles unified into VayuOS** — System Modes (Ω7), Policy
  Engine / Provenance Inspector (Ω11), Runtime Topology (Ω9), Replay Explorer
  (Ω10), Fault Manager, and ADR Registry all render inside the VayuOS chrome.
  Old `/admin/{modes,policy,topology,replay,faults,adr}` paths 301-redirect.
- **Member/reader signup page** (`/signup`) — branded, nonce-gated; integrates
  with the existing magic-link member auth flow.
- **Ghost-style homepage auth buttons** — optional Sign in / Sign up links in
  the public nav, controlled by the `site.membership_buttons` toggle in VayuOS
  → Members settings.
- **Draft regression tests** — `TestDraftNotLeakedViaArticleAPI`,
  `TestDraftNotLeakedViaListAPI`, `TestDraftNotLeakedViaCommentAPI` (integration
  build tag) permanently guard the three previously-patched leak paths.

### Security

- **LEAK-1 (Critical)** — `GET /api/v1/articles/{slug}` now returns 404 for
  draft articles to unauthenticated callers, preventing content enumeration via
  the public API. (Only authenticated operators receive the draft payload.)
- **LEAK-2 (High)** — The write-queue worker now verifies the article's DB
  status before writing the rendered HTML to the on-disk cache, preventing a
  draft from being served as a cached public page after a status transition.
- **LEAK-3 (Low)** — Comment submission (`POST /api/v1/comments/{slug}`) and
  comment listing (`GET /api/v1/comments/{slug}`) reject requests whose slug
  resolves to a draft article, preventing slug-existence probing via the comment
  API.

### Changed

- **Internal rename: Admin v3 → VayuOS** — `admin_v3_ui.go` →
  `admin_os_ui.go`, `admin_v3_editor.go` → `admin_os_editor.go`,
  `admin-v3.css` → `admin-os.css`. All 450 `.vp-v3` CSS selectors renamed to
  `.vp-os`. All `/admin/v3` references removed from source; 301-redirects
  preserved for existing bookmarks.
- **`site.membership_buttons` added to settings allowlist** — the key was
  previously silently dropped by `SetMany`.
- Public surfaces (list API, article page, sitemap, RSS, search index, related
  articles) filter `COALESCE(status,'published')='published'` so drafts are
  invisible outside the operator console.

### Upgrade Notes

- Run migrations on upgrade — migration **030** adds the `status` column and
  index to `articles`. All existing rows default to `published`; no content is
  hidden after the upgrade.
- The `/os` shell is the only admin. Old `/admin/v3` and operator-page URLs
  continue to 301-redirect.

---

## [1.6.0] — 2026-06-21

**One admin, for real — Admin v2 removed (ADR-0069 Stage 3).** VayuOS at `/os` is
now the only admin. The block editor owns every authoring flow, so the legacy
Admin v2 surface, its assets and its escape hatch are gone.

### Added

- **Native create path in the `/os` block editor.** Brand-new posts open the
  native block editor (no slug) and are created on first Save through the
  authoritative article service — `handleV3EditorSave` derives a unique slug from
  the title, creates the article, persists the block document, and the editor
  adopts the new slug / URL in place.
- **Native legacy-post editing.** Opening an existing legacy (non-block) post now
  loads it in the block editor, pre-seeded with an in-memory import of its HTML
  (`blockrender.ImportHTML`). The import is **not** persisted and the published
  content is untouched until you Save, so opening a post is non-destructive.

### Removed

- **Admin v2 (`/admin/v2`) and its assets** — `admin_ui.go`, the v2 login
  handlers, `static/css/admin-v2.css`, `static/js/admin-v2.js`, and the v2 e2e
  specs are deleted. The block editor no longer depends on any v2 code.
- **The `ADMIN_LEGACY` escape hatch** and the deprecation banner.

### Changed

- **Legacy admin routes now redirect permanently (301).** `/admin`,
  `/admin/v2[/...]` and `/admin/v3[/...]` 301-redirect into the `/os` equivalent
  (previously 302), still emitting a deprecation warning to the server log.

### Upgrade Notes

- The admin lives at **`/os`**. Old `/admin`, `/admin/v2` and `/admin/v3` URLs
  redirect there automatically (now 301). Update any bookmarks or automation that
  hard-coded `/admin/v2`. There is no configuration to change and no data
  migration; legacy posts keep their stored HTML until you edit and save them.

---

## [1.5.0] — 2026-06-21

**VayuOS — One Admin.** The v1/v2/v3 admin surfaces consolidate into a single,
fast Admin v3. The block editor gains AI-assist and an inline version-history
diff; the Theme Studio becomes native to v3; legacy posts can be adopted into
blocks losslessly; and Admin v2 enters soft deprecation. Still a sovereign
single binary — zero CDNs, strict CSP (no `unsafe-eval`, no `unsafe-inline`,
per-request nonces). See ADR-0069 and ADR-0073.

### Added

- **AI-assist slash commands (opt-in).** When `VAYU_AI_URL` is configured, the
  block editor's slash palette gains an AI section (continue, rewrite, summarise)
  with an inline Accept/Discard overlay. Disabled and invisible by default.
- **Inline version-history diff.** A History panel in the v3 editor lists recent
  versions and renders a word-level LCS diff against the working draft.
- **Native Theme Studio in Admin v3.** Preset gallery + design-token editor with
  CSP-clean live preview via scripted CSSOM custom-property writes (no `<style>`
  injection). Session-gated API mirrors under `/admin/v3/api/theme/*`.
- **Convert-to-blocks (ADR-0073).** An explicit, confirmed, non-destructive
  action imports a legacy article's HTML into a block document (`blocks_json`
  side-car) via `blockrender.ImportHTML` — `articles.content` is never touched,
  so the action is reversible by simply not saving.
- **Governance panel (`/os/governance`).** A dedicated control surface for the
  adaptive-governance runtime: current system mode + full transition lineage, the
  severity-classified error-budget ledger, and a live policy-engine evaluation
  (pass / warning / fail). Server-rendered, CSP-clean; wired into the sidebar and
  command palette.
- **Formal plugin interface specification (ADR-0074).** `docs/plugins/SPEC.md` —
  a normative, RFC-2119, independently versioned (v1.0) contract covering plugin
  kinds, the manifest schema, the deny-by-default capability model, the
  line-oriented JSON IPC protocol, hook events, lifecycle and conformance. The
  Tools panel gains a live registry of sandboxed out-of-process plugins.
- **"About the Developer" page** on the marketing site.

### Changed

- **Legacy admin routes log a deprecation warning.** Every hit on `/admin`,
  `/admin/v2` or `/admin/v3` emits a structured `warn` log line (component
  `admin-legacy`) naming the `/os` target and the removal release.

- **VayuOS — the admin moves to `/os`.** The canonical admin surface is now
  mounted at `/os`. The three historical surfaces — the classic console
  (`/admin`), Admin v2 (`/admin/v2`), and Admin v3 (`/admin/v3`) — are legacy and
  302-redirect into the `/os` equivalent (ADR-0069).
- **Admin v2 soft-deprecated (ADR-0069 Stage 2).** The deprecated v2 pages can be
  kept reachable with the `ADMIN_LEGACY=1` escape hatch, which also shows a
  dismissible deprecation banner naming the removal release (`v1.6.0`).
- **CI concurrency control.** Heavy workflows (`ci`, `race`, `e2e`, `lighthouse`,
  `sbom`) now cancel superseded runs on the same ref, so rapid pushes no longer
  stack redundant runs.

### Upgrade Notes

- Operators who still rely on Admin v2 must set `ADMIN_LEGACY=1`; otherwise v2
  URLs redirect to Admin v3. Admin v2 is scheduled for removal in `v1.6.0`.

---

## [1.4.0] — 2026-06-21

**Sovereign Rich Media & Theme Studio** — diagrams, privacy-first embeds, and a
design-token theme system that surpasses Tumblr's customiser, all as a sovereign
single binary with zero CDN dependencies and a strict CSP (no `unsafe-eval`, no
`unsafe-inline`). See ADR-0070.

### Added

- **Sovereign rich media — embeds & click-to-load video (ADR-0070, Phase 1–2).**
  - New `embed` block: paste any URL and the server unfurls it into a self-hosted
    link card (OpenGraph metadata fetched via the SSRF-hardened `safefetch`
    client; the thumbnail is imported into the media library, never hotlinked).
  - **Video embeds are privacy-first click-to-load facades.** YouTube and Vimeo
    URLs render as a poster + play button with **no third-party request until the
    reader clicks**. On click, same-origin `video-facade.js` injects a sandboxed
    iframe pointed at the cookie-free privacy origin (`youtube-nocookie.com`,
    `player.vimeo.com`).
  - **Per-page CSP builder.** The reader's baseline CSP never carries a
    third-party `frame-src`. A page that contains a video facade narrowly extends
    `frame-src` to exactly the vetted privacy origin(s) it needs — validated
    against a closed allowlist, so a crafted block or tampered cache sidecar can
    never widen the policy. Admin and non-embed pages stay fully locked. The
    extension is re-applied on cache-hit serves via a tiny CSP sidecar.
  - Migration 027 adds `embed_cache` for resolved metadata + provenance.
- **Sovereign diagrams — pure-Go Mermaid→SVG (ADR-0070, Phase 3).**
  - New `diagram` block compiles a useful Mermaid subset — **flowcharts**
    (`flowchart`/`graph`, directions TD/TB/LR/RL/BT, rect/rounded/diamond nodes,
    labelled solid/dashed edges) and **sequence diagrams** (`sequenceDiagram`,
    participants, solid/dashed messages, notes) — to a static, themeable SVG
    entirely on the server. No headless browser, no Node, no client JavaScript,
    no `eval`; the strict reader CSP is untouched and pages stay light.
  - The SVG uses `currentColor`/CSS classes so it inherits the page theme and
    prints perfectly; it is sanitised through a closed SVG allowlist (no
    `<script>`, no `<foreignObject>`, no event handlers).
  - Unsupported/malformed sources degrade gracefully to an annotated code block.
  - Editor gains a live preview via a debounced server endpoint
    (`POST /api/v1/admin/diagram/preview`); results are content-addressed in
    `diagram_cache` (migration 028). No Mermaid library ever reaches the browser.
- **Expanded diagram grammar (ADR-0070, Phase 4).** The pure-Go engine now also
  compiles **pie charts** (`pie`, arc geometry + themeable legend), **state
  diagrams** (`stateDiagram`/`-v2`, `[*]` pseudo-states as filled circles,
  layered layout), **class diagrams** (`classDiagram`, member compartments,
  inheritance/composition/aggregation markers), and **Gantt charts** (`gantt`,
  sections, `done`/`active`/`crit`/`milestone` styles, `after <id>` sequencing).
  Six grammars total, all server-rendered to sanitised SVG with graceful
  fallback — still zero client JavaScript.
- **Theme Studio — sovereign design-token system (ADR-0070, Phases 5–6).**
  - New `internal/theme` package: a typed 23-field token schema (dark/light
    colour ramps, typography, spacing, radii), a CSS-variable compiler that
    validates every hex value before emission (injection-proof), and SQLite
    persistence (`theme_tokens`, migration 029, singleton row).
  - **Eight built-in presets** — Default, Aurora, Slate, Terminal, Sepia,
    Carbon, Ocean, Sakura — using system fonts only, so a theme switch makes
    **zero external requests**.
  - REST API (auth + CSRF gated): `GET …/theme/presets`, `GET …/theme/tokens`,
    `POST …/theme/preview` (compiled CSS + sanitised sample HTML), and
    `POST …/theme/apply` (validates, persists, recompiles, purges the render
    cache). Applied token CSS is served live via `/theme.css` with no restart.
  - **Studio tab** in the admin theme editor: a preset gallery with colour
    swatches and a live preview pane that re-themes instantly. The preview
    applies colours via CSSOM `setProperty` (no inline `<style>`, no `style=`
    attributes), so the strict `style-src 'self'` CSP stays intact.

### Security

- **CodeQL barrier recognition.** The v3 block-editor body builder now calls
  `html.EscapeString` directly instead of through a function-typed alias, so the
  escaping is recognised as a sanitiser barrier (clears the `go/reflected-xss`
  finding; the value was already escaped). Email subjects are now emitted as
  RFC 2047 base64 encoded-words — correct UTF-8 subject handling plus a
  CR/LF-free transformation that clears the `go/email-injection` finding. Both
  were defence-in-depth false positives; the mail path was already CRLF-stripped,
  base64-encoded and HTML-sanitised.
- **Anchored video-embed host matching.** YouTube/Vimeo detection now parses the
  URL and matches the provider by **exact host equality** with fully-anchored ID
  validators, instead of unanchored substring regexes. A URL that merely contains
  a provider host as a path/query fragment (e.g. `evil.com/youtube.com/embed/ID`,
  `youtube.com.evil.com/…`) is refused. Clears two `go/regex/missing-regexp-anchor`
  findings; covered by `TestDetectVideoEmbed`.
- **Pre-flight SSRF host barrier in `safefetch`.** Every guarded fetch now
  resolves and validates the request host (public, non-reserved address required)
  *before* any connection is opened, in addition to the authoritative dial-time
  pinned-IP guard that re-runs on each redirect hop. Fail-fast and an explicit
  allow-check on previously-raw input (`go/request-forgery`).

### Upgrade Notes

- Migrations **027–029** apply automatically on first boot (embed cache, diagram
  cache, theme tokens). No manual steps; downgrades are not supported once the
  new tables exist.
- No configuration changes are required. Embeds, video facades, diagrams, and the
  Theme Studio are available immediately; the reader-facing CSP stays strict by
  default and only narrows `frame-src` per-page when a video facade is present.

---

## [1.3.0] — 2026-06-20

**Admin v3** — a ground-up admin & editor that surpasses Ghost/WordPress/Substack
in design, depth, and security, while staying a sovereign single binary with zero
CDN dependencies and a strict CSP (no `unsafe-eval`, no `unsafe-inline`). Mounted
at `/admin/v3` alongside `/admin/v2`, so the upgrade is fully non-breaking
(ADR-0068).

### Added

- **Design system & shell.** Hand-authored `admin-v3.css` scoped to `.vp-v3`,
  CSS-custom-property theming with dark/light/auto, grouped sidebar, mobile
  bottom-nav, command palette (⌘K), toasts — all same-origin, no inline styles.
- **Dashboard intelligence.** Real 14-day publishing-trend sparkline
  (server-rendered SVG), live stat cards, storage + activity feed, quick-compose.
- **Block editor.** Canonical block document in `articles.blocks_json`
  (migration 025); `internal/blockrender` renders blocks → sanitised HTML
  (HTML-escape + bluemonday UGC, no raw-HTML escape hatch). Vanilla-JS editor
  with 9 block types, slash (`/`) command palette, debounced autosave, ⌘S, and a
  server-rendered + DOMPurify-guarded live preview. Legacy and new posts keep the
  lossless v2 editor so no content can be wiped on save.
- **Media library.** Responsive grid with drag-and-drop upload reusing the
  hardened backend (content-addressed, type-allowlisted, **SVG refused**, CSRF).
  Listing only surfaces server-generated content-addressed names.
- **Members.** Tier counts and roster.
- **Native SEO dashboard.** Per-article readiness (healthy / thin / missing-title)
  and artefact freshness (sitemap / feed / robots) with one-click regenerate.
- **Privacy-preserving analytics page.** 30-day views sparkline, top pages, and
  referrers — sourced only from the local DB, no third-party services.

### Security

- **Two-factor authentication (TOTP).** New `internal/totp` implements RFC 6238
  over RFC 4226 using only the standard library (no new dependency), validated
  against the official RFC test vectors, with constant-time comparison and clock-
  skew tolerance. Migration 026 adds `users.totp_secret` / `users.totp_enabled`.
  Enrolment is a verify-before-enable ceremony; sign-in enforcement is wired into
  **both** the v2 and v3 login flows so an enrolled account cannot bypass 2FA via
  the older surface. The password is never echoed back on a failed second factor.
- Strict CSP maintained throughout: the only inline `<script>` is the nonce-gated
  bootstrap; all DOM mutation uses `createElement`/`textContent`; SVG uploads
  remain refused (script-carrier XSS vector).

### Upgrade Notes

- Additive, non-breaking. Migrations 025 and 026 apply automatically with safe
  defaults. `/admin/v2` continues to work unchanged; `/admin/v3` is the new
  recommended surface.

---

## [1.2.0] — 2026-06-20

Four tiers of new capability — all single-binary, sovereign, and CSP/governed-write safe.

### Added

- **Tier 1 — Sovereign foundations:** standard-library SMTP email + double-opt-in
  newsletter (`internal/email`), durable scheduled publishing (`internal/scheduler`,
  migration 019), multi-author accounts with Argon2id + server-side sessions
  (`internal/users`, `internal/auth`, migration 020), and stdlib-only automatic
  image optimization (`internal/imageproc`, no CGO).
- **Tier 2 — Reach & insight:** cookieless zero-PII analytics (`internal/analytics`,
  migration 021), HMAC-SHA256 outbound webhooks with retry + delivery audit
  (`internal/webhooks`, migration 022), Mastodon auto-posting (`internal/social`),
  Ghost/WordPress importers, a local-Ollama AI writing assistant (`internal/aiassist`,
  suggest-only), and memberships & paywalls with passwordless magic-link sign-in and
  an optional signature-verified Stripe webhook (`internal/members`, migration 023).
- **Tier 3 — Reading polish (ADR-0066):** server-side syntax highlighting (chroma,
  `style-src 'self'`-safe via a highlight-before-sanitise placeholder pipeline),
  related articles with precise comma-token tag matching, reading-time, PDF/document
  uploads (≤32 MB, magic-number validated), comment-approval emails, and an
  installable PWA (`/manifest.json`, `/sw.js`) with offline service worker.
- **Tier 4 — Enterprise interfaces (ADR-0067):** read-only GraphQL content API
  (`/api/v1/graphql`, query-only — no mutations), internationalisation with
  `Accept-Language` negotiation and operator-editable catalogs (`internal/i18n`),
  customisable transactional email templates (`internal/emailtmpl`), and a real-time
  SSE event stream (`/api/v1/stream`). Migration 024 adds `email_templates` +
  `i18n_messages`. Cloudflare edge-purge + IndexNow CDN push confirmed on every mutation.

### Fixed

- Syntax highlighting: bluemonday stripped the `language-*` class before chroma ran,
  so code never highlighted — reworked into a highlight-before-sanitise placeholder
  pipeline (regression-tested, including placeholder-forgery).
- Related articles: query referenced a non-existent `status` column and returned nil.
- PDF uploads were truncated to ~8 MB (wrong read limit) producing corrupt files.
- Related-article tag matching no longer matches substrings (`go` ≠ `golang`) or
  treats tag `%`/`_` as LIKE wildcards.
- GraphQL `articles(offset:)` now honours non-page-aligned offsets exactly.

### Security

- GraphQL is deliberately query-only so writes never get a second path around the
  governed REST API. SSE stream is API-key-gated. SVG uploads remain refused.
  Service worker never caches `/admin` pages.

### Upgrade Notes

No breaking changes. Start the server once and migrations 019–024 apply
automatically. Every new capability is opt-in and a safe no-op until configured.

---

## [1.1.0] — 2026-06-19

### Added

- **`vayupress migrate` CLI subcommand** (built into the main binary) — import
  Markdown folders directly into VayuPress without a separate binary.
  Supports `--dry-run`, `--recursive`, `--skip-drafts`, YAML frontmatter
  (title/slug/date/tags/draft), falls back gracefully on missing fields.
  Writes both the sanitised HTML article row **and** an `article_sources`
  side-car row (`format=markdown`) so the Admin v2 editor reopens posts in
  Markdown mode. `INSERT OR IGNORE` makes re-runs idempotent.
  Subcommands: `migrate markdown`, `migrate list`, `migrate info`.
- **Multi-format post editor** (`/admin/v2/editor`) — Markdown ⇄ raw HTML
  toggle via a segmented control; `[data-format-state]` hidden input persists
  the chosen format across saves. `computeHTML()` converts Markdown to HTML or
  passes raw HTML through; the public renderer always receives sanitised HTML
  regardless of authoring format. The editable source and format are stored in
  the `article_sources` side-car (migration 018) so round-tripping is lossless.
- **`article_sources` side-car table** (migration 018) — stores `(slug, format,
  source, updated_at)` separate from the write queue; never rendered
  server-side, zero XSS surface.
- **New-post create flow** — when the editor has no slug yet, the first save
  `POST`s to `/api/v1/articles`, then redirects to the permanent
  `/admin/v2/editor/{slug}` URL so autosave can continue.
- **Dual-write autosave** — each save fires two CSRF-protected requests in
  parallel: `PUT .../source` (editable source + format) and
  `PUT /api/v1/articles/{slug}` (rendered, sanitised HTML).
- **`docs/MIGRATION.md`** — comprehensive migration guide covering all 8
  platforms and the new built-in Markdown import.
- **`vayupress migrate rollback`** (already in `vayupress update rollback` —
  documented in UPGRADING.md).
- **`github.com/yuin/goldmark`** added as a direct dependency for the built-in
  Markdown importer.

### Fixed

- **HTML-escaping gap in admin snapshot** — article `title` and `slug` values
  emitted in the admin v2 dashboard's recent-articles table were not
  HTML-escaped; fixed with `html.EscapeString`.
- **XML-injection in sitemap / RSS** — `slug` values in `<loc>` tags were
  written unescaped; now escaped with `xml.EscapeText`. CDATA title/body
  content defensively strips embedded `]]>` sequences to prevent CDATA
  injection.
- **Test signature mismatch** — `admin_ui_test.go` calls to `editorBodyHTML`
  updated to match the 5-parameter signature (`slug, heading, title, format,
  source`).

### Security

- All user-originated string fields emitted in HTML contexts in the operator
  console now use `html.EscapeString` or `template.HTMLEscapeString` (audit
  finding from security review 2026-06-19).

### Upgrade Notes

- Run the server once; migration 018 (`article_sources`) is applied
  automatically on startup.
- No breaking API changes. Legacy `/admin` is unaffected.
- Existing posts open in the editor in HTML mode (the side-car is empty until
  a save in Markdown mode creates it).

---

### Fixed — Critical: migrations 011–016 broke fresh installs
- The migration runner (`internal/db/db.go`) executes each migration **one
  statement per line**. Migrations `011`–`016` (article-versions, redirects,
  comments, collections, newsletter, webmentions) were authored as multi-line
  `CREATE TABLE` statements, so a fresh database failed at `011` with
  `incomplete input` and never reached the later schema. Rewrote `011`–`017` as
  single-statement-per-line to match the runner's contract; a fresh DB now
  migrates all 17 cleanly (verified end-to-end). Existing databases that already
  applied these are unaffected (checksums recomputed on next deploy).

### Added — Sovereign self-update (ADR-0064)
- **`internal/update`**: check-only service + signature-verified, CLI-only apply.
  - `vayupress update check|apply|history` CLI.
  - Read-only HTTP: `GET /admin/api/updates/check`, `GET /admin/api/updates/history`.
    There is **no** web endpoint that downloads, replaces, or restarts the binary.
  - Apply gates (all enforced before any disk write): opt-in
    `VAYU_SELFUPDATE_ENABLED=true`, pinned `VAYU_RELEASE_PUBKEY` (Ed25519),
    mode not in {read-only, quarantined, maintenance}, SHA-256 checksum **and**
    Ed25519 signature over the digest, DB backup first, atomic binary swap with
    `.bak` kept. Never auto-restarts — prints `systemctl` instructions.
  - Audit trail in `update_history` (migration `017`).

### Added — Modern admin UI `/admin/v2` (ADR-0065)
- CSP-compliant, fully vendored (no CDNs). Tailwind precompiled to
  `static/css/admin-v2.css`; Alpine via its CSP build; eval-free `admin-v2.js`;
  per-request nonce on every inline script; self-hosted fonts.
- Editor-first: split-view live preview, distraction-free mode, slash-command
  palette, formatting toolbar, word count / reading time, SEO preview, debounced
  autosave (reusing `/api/v1/articles`), version-history access.
- **Non-breaking**: served alongside the untouched legacy `/admin`.

### Security & dependencies
- Bumped all modules (core + every tool) to latest and re-tidied:
  `chi v5.3.0`, `go-sqlite3 v1.14.46`, `golang.org/x/crypto v0.53.0`,
  `golang.org/x/net v0.56.0`, `golang.org/x/sys v0.46.0`.
- Fixed `internal/preview.Issue()` negative-TTL bug (now yields an expired token).
- New docs: `docs/UPGRADING.md`, `docs/ADMIN-UI.md`, `docs/SECURITY.md`;
  ADR-0064, ADR-0065 added to the registry.

### Added — Full tool ecosystem & plugin API wiring

**8 migration tools** (all standalone Go modules, no API keys required):
- **`ghost-to-vayu`**: Ghost CMS → VayuPress (MySQL/SQLite direct DB)
- **`wordpress2vayu`**: WordPress MySQL → VayuPress (posts, pages, categories, featured images)
- **`hugo2vayu`**: Hugo site → VayuPress (YAML + TOML frontmatter, goldmark GFM)
- **`jekyll2vayu`**: Jekyll `_posts` → VayuPress (YAML frontmatter, date-in-filename)
- **`substack2vayu`**: Substack CSV export → VayuPress
- **`notion2vayu`**: Notion HTML export → VayuPress
- **`medium2vayu`**: Medium HTML export (ZIP) → VayuPress (new)
- **`markdownfolder2vayu`**: Any Markdown folder with YAML frontmatter → VayuPress

**3 operational tools:**
- **`vayu-backup`**: compressed backup archives, verify, restore, retention scheduling
- **`vayu-export`**: render all articles to a static HTML site for CDN or archiving
- **`vayu-validate`**: content integrity checker — slugs, duplicates, bad dates, CI-safe exit codes (new)

**Plugin API routes wired into VayuPress core** (`cmd/vayupress/plugin_handlers.go`):
- **Comments**: `POST/GET /api/v1/articles/{slug}/comments`, admin moderation (`PUT /api/v1/admin/comments/{id}/status`)
- **Article Versions**: `GET /api/v1/admin/articles/{slug}/versions[/{id}]`
- **Collections / Series**: `GET/POST /api/v1/collections`, admin membership management
- **Newsletter**: `POST /api/v1/newsletter/subscribe`, confirm/unsubscribe links, admin subscriber list
- **Webmention receiver**: `POST /webmention` (W3C standard), admin list + moderation
- **Draft Preview Links**: `POST /api/v1/admin/preview` (issues HMAC token + shareable URL)
- **Redirect Manager**: `GET/POST/DELETE /api/v1/admin/redirects` + redirect middleware wired into chi router
- **Table of Contents**: `GET /api/v1/articles/{slug}/toc`

**Built-in SEO Optimizer** (`internal/seo`): per-article meta description and
Open Graph / Twitter Card image, Article JSON-LD, sitemap generation.

**Bug fix**: `internal/preview.Issue()` with negative TTL now correctly produces
an already-expired token instead of silently substituting the default 48-hour TTL.

**Website**: Tools section updated with all 8 migration tools + 3 operational tools.

---

## [1.0.0] — 2026-06-15 — First Stable Release

VayuPress 1.0.0 is the first tagged release: a sovereign, single-VPS publishing
engine with an adaptive governance runtime. It consolidates phases P1–P28 and
Ω1–Ω12 into a stable line.

### Added (1.0.0 release highlights)
- **Custom favicon/logo upload** (`/admin/theme` → Branding tab): PNG/ICO,
  magic-number validated, ≤ 256 KB, stored base64 in `site_settings` and served
  over the existing favicon routes (overrides the embedded default everywhere
  without template edits). CSRF-protected, mode-gated, with live preview + remove.
- **Gated governance budget actuation (Ω12)** (`internal/budget.Actuator`): when
  `GOVERNANCE_ACTUATION=true`, an exhausted governance budget drives an automatic,
  graph-respecting mode escalation. Opt-in (off by default), one-shot/debounced,
  and audited. Surfaced via `GET /api/v1/admin/budgets` (`actuation_enabled`,
  `actuations[]`, `last_applied`). See **ADR-0063**.
- **`trace-tap` example plugin**: demonstrates participating in the distributed
  trace substrate — reads `correlation_id`/`causation_id`/`trace_id` and echoes
  them so plugin work stitches into the host trace waterfall.
- **ADR Registry HTML console** (`/admin/adr`): the architecture decision records
  now render as a styled console page instead of a raw JSON endpoint.
- **CI screenshot pipeline** (`.github/workflows/screenshots.yml`): boots a live
  instance, seeds content, and captures the public + operator-console pages via
  Playwright, committing refreshed PNGs back to the branch.

### Security (1.0.0)
- **Federation inbox replay protection**: `InboxHandler` consults an optional
  durable `ReplayStore`, so a captured signed activity (or a benign retry) is
  recorded once by id and a duplicate is accepted idempotently without being
  processed twice; id-less activities are refused. `MarkOrReject` is now atomic
  (single `INSERT OR IGNORE` + rows-affected), closing the prior check-then-mark
  TOCTOU window.
- **CSRF cookie seeding on `/admin/theme`**: the editor GET now issues its own
  CSRF cookie, so Save/Reset/favicon writes work when the page is opened directly.

### Added
- **Theme & Site Settings control panel** (`/admin/theme`): operator-editable site
  identity (name, tagline, description, author), light/dark palette, custom CSS, and
  declarative head/SEO capabilities. CSRF-protected, mode-gated (blocked in
  read-only/quarantined), audit-logged (`component: "theme"`).
- **`internal/settings`** package: thread-safe key/value store over the new
  `site_settings` table (migration **006**, content-checksummed), 30 s read cache,
  transactional `SetMany`, allowlisted keys.
- **`/theme.css`**: dynamic per-site palette + custom CSS served same-origin
  (ETag + `max-age=60`) so it satisfies `style-src 'self'` — no inline `<style>`.
- **Public theme toggle**: sun/moon switch in the site header, preference persisted
  to `localStorage`, served as a same-origin script (`/static/js/theme-toggle.js`)
  so it needs no CSP nonce.
- **CSP violation reporting**: `report-uri /csp-report` + `POST /csp-report`
  endpoint, `vayupress_csp_violations_total` metric, structured per-violation logs.
  Hardened against abuse: per-IP rate limit (`auth.AllowCSPReport`, 30/min,
  over-limit dropped before counting/logging), 16 KB body cap, strict structured
  parsing, and short-window duplicate suppression on `(directive|blocked-uri)`.
- **Report-Only CSP mode**: `CSP_REPORT_ONLY=true` sends
  `Content-Security-Policy-Report-Only` instead of the enforcing header, so a
  candidate policy can be observed via `/csp-report` in staging before enforcing.
  Enforcement posture is now operationally visible (not hidden in an env var): a
  `csp.policy` boot entry in the Unified Operational Timeline, a `csp_mode` field
  on `/api/v1/admin/timeline` and `/api/v1/stats`.
- **CSP report attribution**: violation logs are tagged with the receiving
  deployment build version (`build=`) for release attribution — browser CSP
  reports carry no session/correlation context, so build version is the
  meaningful debugging anchor for a frontend change.
- **CSP violations in the Unified Operational Timeline**: accepted violations are
  recorded in a bounded process-local ring and rendered as `csp.violation` entries
  in the live timeline (Ω8/Ω10), placing frontend-governance signals in the same
  causal narrative as mode transitions and faults — visible spatially, not just as
  a metric counter.
- **Timeline event provenance**: every timeline entry now carries structured
  provenance (`source` subsystem, `actor`, causal `cause`, `correlation_id`,
  `build`, `policy_rev`) in the `/api/v1/admin/timeline` JSON, plus an
  envelope-level `provenance` (build + policy revision). Fields are populated only
  where genuinely known — synthesized governance entries leave `correlation_id`
  empty rather than fabricate one — so the timeline becomes honest, machine-readable
  runtime memory rather than a flat string log.
- **Formal operational severity taxonomy** (`internal/severity`): a fixed, totally
  ordered vocabulary — OBSERVE · NOTICE · WARN · VIOLATION · ESCALATION ·
  CONTAINMENT · CRITICAL — where each level defines its meaning, operator
  expectation, escalation behavior, timeline class, topology colour, and policy
  interaction. Timeline events now carry a `severity` taxonomy name (single
  auditable classifier in `timelineSeverity`); the CSP violation log adopts the
  `VIOLATION` level; and `GET /api/v1/admin/severity` publishes the full taxonomy
  so the vocabulary is self-documenting and auditable.
- **Causal lineage on the timeline**: each event now carries a deterministic,
  render-stable `provenance.id` and a `provenance.parent_id`, turning the flat
  narrative into a traversable operational graph (boot chain → governance arming →
  fault/CSP/mode escalation ancestry → posture). Links are structural and honest —
  derived from genuine subsystem relationships, computed over the full set before
  display truncation so ancestors keep stable identity.
- **Event retention doctrine** (`docs/governance/event-retention.md`): explicit
  classification of every event store as ephemeral / durable / replayable /
  audit-grade / operator-cognition, with the governing rule that a signal's
  retention class must match its purpose (the timeline is a projection, not a
  ledger; the CSP ring is ephemeral with a durable log/metric shadow).
- **Governance error budgets** (`internal/budget`): severity-classified events
  accumulate against bounded, rolling-window budgets that imply a defined
  escalation when exhausted — `5 WARN/10m → NOTICE debt`, `3 VIOLATION/10m →
  ESCALATION`, `1 CRITICAL/1h → CONTAINMENT`. CSP violations charge the breach
  budget; budget posture surfaces in the timeline (`governance.budget` entries,
  severity = the recommended escalation), via `GET /api/v1/admin/budgets`, and as
  the `vayupress_governance_budgets_exhausted` metric. Deliberate scope boundary:
  the engine **accounts and recommends only** — it does not auto-drive mode
  transitions (that control-loop actuation is gated behind its own safety design).
- **WCAG AA contrast warnings**: saving the palette returns advisory (non-blocking)
  warnings when a primary colour falls below 4.5:1 on its page background. The
  shipped **default light primary changed from `#0d9488` (3.6:1) to `#0f766e`
  (teal-700, 5.2:1)** so the defaults themselves clear AA.

### Security
- **Declarative head capabilities replace raw `<head>` HTML**: head/SEO inputs are
  an allowlisted, validated, escaped `<meta>` subset (keywords, theme-color, robots,
  Google/Bing verification). Raw head HTML is no longer accepted — meta-refresh
  redirects, external beacons, and `<base>` hijacks (which CSP does not fully cover)
  are structurally impossible.
- **Dynamic theme served as a stylesheet, not inline** — preserves the strict
  `style-src 'self'` CSP (no `unsafe-inline`).
- Palette colours and verification tokens are validated server-side
  (`#rgb`/`#rrggbb`, allowlists, token regex) before persistence.

---

## [1.0.0-p26] — 2026-06-13

### Added (Prompt 26 — Security Sandboxing & Capability Enforcement)
- **`internal/sandbox` capability enforcement**: subprocess plugins now run with explicitly
  dropped Linux capabilities via `PR_SET_SECCOMP` and namespace isolation (ADR-0057)
- **`plugins.RegisterSubprocess`**: registers sandboxed subprocess plugins via `sandbox.Manifest`;
  launches isolated worker processes using the subprocess IPC pool
- **`plugins.ShutdownSubprocesses`**: clean teardown of all subprocess pools during graceful shutdown
- **`subprocess_linux.go` / `subprocess_other.go`**: platform-conditional sandbox application
  (`//go:build !linux` guard on non-Linux stub)
- **ADR-0057** — Security Sandboxing & Capability Enforcement

---

## [1.0.0-p25] — 2026-06-13

### Added (Prompt 25 — Process Isolation & Runtime Sandboxing)
- **`internal/sandbox` package**: subprocess IPC pool for out-of-process plugin execution (ADR-0056)
- **`sandbox.Pool`**: manages a pool of sandboxed worker processes with health checking and restart
- **`sandbox.Manifest`**: declarative plugin manifest (name, binary path, allowed syscalls, run-as user)
- **Linux seccomp filtering**: `applyProcAttr` wires seccomp allowlist to subprocess `exec.Cmd`
- **`SubprocessStats`**: runtime stats for all registered subprocess pools
- **ADR-0056** — Process Isolation & Runtime Sandboxing

---

## [1.0.0-p24] — 2026-06-13

### Added (Prompt 24 — Resource Governance & Execution Isolation)
- **`internal/resource` package**: named semaphore-based concurrency limiters (ADR-0055)
- **`resource.Register`**: registers a named limiter (`articles.write`, `plugin.exec`) with a cap
- **`resource.Watchdog`**: periodic goroutine monitoring limiter saturation; logs warnings
- **`resource.Global`**: package-level watchdog wired in `main.go`
- Plugin worker `run()` enforces `plugin.exec` concurrency ceiling via `resource.Get`
- **ADR-0055** — Resource Governance & Execution Isolation

---

## [1.0.0-p23] — 2026-06-13

### Added (Prompt 23 — Structured Tracing & Execution Spans)
- **`internal/trace` package**: span-based tracing with `Start`, `SetAttribute`, `End` (ADR-0054)
- **Correlation and causation IDs on every span**: `WithCorrelationID`, `WithCausationID` context helpers
- **Outbox dispatch tracing**: every outbox event dispatch opens a `outbox.dispatch.<type>` span
- **Span attributes**: `event_id`, `event_type`, `causation_id` recorded on dispatch spans
- **ADR-0054** — Structured Tracing & Execution Spans

---

## [1.0.0-p22] — 2026-06-13

### Added (Prompt 22 — Observability & Correlation Architecture)
- **`internal/logging` structured fields**: `LogFields` struct with `CorrelationID`, `CausationID`,
  `Level`, `Component`, `Msg`, `Error` — all logs emit valid JSON (ADR-0053)
- **Correlation IDs propagated end-to-end**: from HTTP middleware through write queue, outbox
  dispatch, and event bus handlers
- **`logging.LogJSON`**: type-safe structured log emission replacing ad-hoc `fmt.Sprintf` chains
- **ADR-0053** — Observability & Correlation Architecture

---

## [1.0.0-p21] — 2026-06-13

### Added (Prompt 21 — Event Envelopes, Idempotent Dispatch, Versioned Event Types)
- **`events.Envelope`**: wrapper struct with `EventID` (UUID), `EventType` (versioned string),
  `CorrelationID`, `CausationID`, `OccurredAt`, and `Payload` (raw JSON) (ADR-0052)
- **Idempotent dispatch**: `delivered_events` table deduplicates events by `event_id`;
  replayed outbox rows are ignored instead of double-dispatched
- **Versioned event type strings**: `article.created.v1`, `article.updated.v1`,
  `article.deleted.v1` — forward-compatible via envelope type routing
- **`events.Bus` type dispatch**: outbox relay unmarshals envelope, routes by `EventType`,
  publishes typed event to the in-process event bus
- **ADR-0052** — Idempotency & Event Evolution

---

## [1.0.0-p20] — 2026-06-13

### Added (Prompt 20 — Transactional Outbox, Queue Writer Interface, Lifecycle Manager)
- **`internal/outbox` package**: transactional outbox relay — polls `outbox_events` table,
  dispatches events atomically written alongside article mutations (ADR-0051)
- **`outbox.NewRelay`**: wires dispatch function and done channel; started via `lifecycle.Manager`
- **`internal/lifecycle` package**: ordered startup/shutdown with named components;
  `lc.Register(name, startFn, stopFn)` — components start in order, shut down in reverse
- **`queue.Writer` interface**: swappable queue backend; `queue.NewSQLiteWriter` is the
  default production implementation
- **`outbox_events` migration**: events table written transactionally with article mutations
- **ADR-0051** — Transactional Consistency & Event Reliability

---

## [1.0.0-p19] — 2026-06-12

### Added (Prompt 19 — Repository Pattern, Typed Events, Search Service, httputil)
- **`internal/api` package**: `ArticleService` with `Repo` (interface), `Queue` (`queue.Writer`),
  and `StorageCheckFn` — fully injectable, no direct DB references in handlers (ADR-0050)
- **`db.ArticleRepo`**: concrete SQLite implementation of the `Repo` interface
- **`internal/events` package**: typed domain events (`ArticleCreated`, `ArticleUpdated`,
  `ArticleDeleted`) and `Bus` (in-process pub/sub)
- **`internal/search`**: `MeiliService` with circuit breaker, `WaitReady`, `ConfigureIndex`,
  `Ping` — SQLite fallback activates when Meilisearch is unavailable
- **`internal/httputil`**: `WriteJSON`, `WriteError`, `DecodeJSON` — thin HTTP primitives
  eliminating duplication across handlers (ADR-0049)
- **`a.registerEventHandlers()`**: domain event handlers wired after all services are ready
- **ADR-0050** — Persistence & Transport Maturity

---

## [1.0.0-p18] — 2026-06-12

### Added (Prompt 18 — Thin Handlers, Service Error Layer, Integration Test Harness)
- **Thin handler contract**: handlers call service, marshal response, set status code —
  no business logic or direct SQL (ADR-0049)
- **Service-layer typed errors**: `api.ErrNotFound`, `api.ErrConflict`, `api.ErrStorageQuota`,
  `api.ErrValidation` — handlers map errors to HTTP status codes centrally
- **Integration test harness**: `go test -race ./...` passes; per-package test files cover
  happy-path and error scenarios without test databases
- **ADR-0049** — Thin Handlers & Service Boundaries

---

## [1.0.0-p17] — 2026-06-12

### Added (Prompt 17 — Route Domains, ArticleService, Centralised Validation)
- **Route domain separation**: `handlers_articles.go`, `handlers_infra.go`, `handlers_admin.go`
  — each file owns one domain; `routes.go` wires chi router (ADR-0048)
- **`ArticleService`** extracted from `main.go`: create/update/delete/get with validation,
  storage quota check, and write-queue dispatch
- **Centralised validation**: slug format (regex), required fields, tag sanitization —
  all in the service layer, not scattered across handlers
- **ADR-0048** — Route Domains & Service Extraction

---

## [1.0.0-p16] — 2026-06-12

### Added (Prompt 16 — App Container & Handler Refactor)
- **`App` struct**: 10 package-level mutable globals replaced by explicit fields on `*App`; all runtime state is owned and auditable
- **28 handlers as `*App` methods**: route registration uses method values (`a.handleXxx`); handlers depend on explicit fields, not implicit globals
- **Filesystem migrations**: SQL extracted to `internal/db/migrations/*.sql`, loaded via `embed.FS`, checksums preserved
- **`staticcheck` in CI**: static analysis on every push; two numeric HTTP status literal issues fixed on introduction
- **ADR-0047** — App Container & Handler Refactor

---

## [1.0.0-p15] — 2026-06-12

### Added (Prompt 15 — Runtime Architecture & Service Boundaries)
- **`internal/plugins` package**: plugin pool (ADR-0032 hardening) extracted from `main.go`
  into a standalone, independently testable package with `Registry`, `Manager`, `HookFunc`.
  `main.go` plugin section reduced from ~150 lines to ~15 lines.
- **Unit tests for all internal packages** (`go test -race ./internal/...` passes):
  `metrics`, `auth`, `logging`, `config`, `plugins`, `health`, `queue`.
- **ADR-0046** — Runtime Architecture & Service Boundaries.

### Fixed
- SQLite migration compatibility: removed `IF NOT EXISTS` from `ALTER TABLE ADD COLUMN`
  in migrations 003 and 004 (not supported on older SQLite versions present in CI).

---

## [1.0.0-p14] — 2026-06-12

### Added (Prompt 14 — Internal Package Decomposition)
- Split `cmd/vayupress/main.go` into 8 `internal/` packages with compiler-enforced boundaries.
- **ADR-0045** — Internal Package Decomposition.

---

## [1.0.0-p13] — 2026-06-12

### Added (Prompt 13 — Repository Decomposition & Tooling Maturity)
- **Real Go source tree**: the application is now committed at `cmd/vayupress/main.go`
  with committed `go.mod`/`go.sum` (pinned, Go 1.23). `git clone && go build ./...`
  works; IDEs index the code; `go vet`/`go test`/`gofmt`/`govulncheck` all run.
- **Source parity enforcement**: `scripts/sync-source.sh` mirrors the canonical deploy
  heredoc to `cmd/vayupress/main.go`; `--check` mode runs in CI and fails on drift.
- **Native Go CI** (`go-native` job): `go vet`, `gofmt -l`, `go build -race`,
  `go test -race`, and `govulncheck` on every push.
- **Constitution Prompt 13** added; `check-governance` now verifies Prompts 1–13.
- **ADR-0044** — Repository Decomposition & Source Parity.

### Changed
- Canonical Go source normalized with `gofmt` (deploy script grew ~4.3k → ~5.5k lines
  as compact one-liners were expanded for tool-compatibility).
- Deploy script pins exact dependency versions (no `@latest`): `chi@v5.1.0`,
  `go-sqlite3@v1.14.45`, `bluemonday@v1.0.27`, `gobreaker@v1.0.0`, `cors@v1.11.1`,
  `x/crypto@v0.39.0`, `x/net@v0.41.0` — reproducible and govulncheck-clean.
- **Toolchain moved to latest stable Go 1.25.11** (deploy `GO_VERSION`, CI
  `setup-go: '1.25'`) so the build carries the newest standard-library security
  fixes; `go.mod` keeps a `go 1.23.0` minimum directive.
- `Makefile`: `build`/`dev` target `./cmd/vayupress`; added `sync` and `sync-check`
  targets; `build` now depends on `sync-check`; `check-adrs` requires ADR-0044;
  `check-governance` verifies Prompt 13.

### Fixed
- **Reachable vulnerabilities (govulncheck)**: flagged `golang.org/x/net/html` (via
  bluemonday) and several standard-library symbols (`crypto/x509.Verify`,
  `html/template.Execute`, `net/textproto.ReadMIMEHeader`, `net.Listen`,
  `net.Resolver.LookupIPAddr`). Fixed by bumping `x/net`→v0.41.0 / `x/crypto`→v0.39.0
  and building with the latest stable Go (1.25.11). Security outranks Simplicity per
  the Constitution priority order.
- **Latent deploy failure**: deploy script previously used `go get ...@latest`, which
  would pull `chi v5.3.0` onto the install unpredictably. Now pinned to exact versions.

---

## [1.0.0-p12.1] — 2026-06-12

### Fixed

#### Engine (`scripts/deploy-vayupress.sh` — bug fixes)
- **Plugin pool shutdown ordering**: `close(pluginQueue)` now precedes `workerPluginWg.Wait()` — range-loop workers exit cleanly instead of blocking indefinitely
- **Memory leak — bucket sweeper**: `startBucketSweeper()` goroutine evicts stale entries from `authFailBuckets`, `rateBuckets`, `pprofLimiters`, and `purgeLimiters` every 10 minutes; bounds memory on long-running instances with rotating IPs
- **CSP `style-src 'unsafe-inline'` removed**: `style-src` is now `'self'` only — all styles must be served from static files; inline style injection vector eliminated
- **Health contract schema versioning**: all `/health/*` responses now include `"schema_version": "1"` — automation consumers can detect breaking API shape changes
- **Lifecycle manager formalized**: shutdown sequence now has six named phases: (1) stop ingress, (2) drain queue, (3) stop plugins, (4) WAL checkpoint, (5) flush metrics, (6) close DB
- **Version header corrected**: all stale `v1.0.0-p8` references in banner, step labels, and header comments updated to `v1.0.0-p12`

#### Documentation
- `README.md` — CI/Security/Go/License/Constitution badges added; ASCII architecture diagram; performance targets table; expanded docs links
- `UPGRADING.md` — new file: version-specific upgrade notes, rollback procedure, zero-downtime upgrade steps, full health verification checklist
- `docs/operations/disaster-recovery.md` — new file: DR-01 through DR-06 runbooks (server loss, DB corruption, migration drift, TLS expiry, search failure, backup verification)
- `Makefile` — fixed `SRC_DIR` from hardcoded `/var/www/vayupress/src` to `SRC_DIR ?= .`
- `.gitignore` — added `coverage.out`, `coverage.html`, `*.coverprofile`, `bin/`

---

## [1.0.0-p12] — 2026-06-12

### Added (Prompts 9–12)

#### Engine (`scripts/deploy-vayupress.sh` → v1.0.0-p12)
- **SSRF protection**: all outbound HTTP now dials through a guarded `DialContext`
  (`ssrfSafeTransport`/`isPrivateOrReservedIP`) that blocks loopback, link-local
  (169.254.169.254 cloud metadata), and RFC-1918/ULA private ranges
- **Argon2id** credential hashing helpers (`hashSecretArgon2id`/`verifySecretArgon2id`)
  with constant-time comparison
- **Immutable WORM audit log**: migration `005-audit-log-worm` adds an `audit_log`
  table with `BEFORE UPDATE`/`BEFORE DELETE` triggers that `RAISE(ABORT)`; all
  admin article create/update/delete mutations now call `auditLog()`
- **Magic-number file verification** (`verifyMagicNumber`) for JPEG/PNG/GIF/WebP/PDF
- **`/health/ethics`** endpoint exposing machine-readable ethics compliance
  (no-tracking, privacy-by-design, audit-log present, charter version)
- Verified: full `go build ./...` + `go vet ./...` pass with real dependencies

#### Security (Prompt 9)
- Dedicated `security.yml` CI workflow: supply-chain scan, 7 security header checks, CSRF, SSRF, auth lockout, audit log, rate limit, threat model verification
- `docs/THREAT-MODEL.md` — Trust Boundaries, Entry Points, Assets, Threat Actors, Mitigations
- SSRF protection: 169.254.169.254 + private IP ranges blocked on all outbound fetches
- Immutable audit log (WORM): `audit_log` table insert-only, no UPDATE/DELETE grants
- Magic number file type verification on all media uploads
- `/health/ethics` endpoint returning ethics compliance status
- All 7 security headers verified in deploy script and CI

#### Automated Governance (Prompt 10)
- Complete rewrite of `ci.yml`: 13 jobs, `ci-pass` gate, all 12 Prompts + 14 ADRs verified
- `check-governance` job: verifies all 12 Prompts in Constitution
- `check-adrs` job: verifies ADR-0001, 0002, 0032–0043 all exist
- `check-docs` job: 19 required documentation files verified
- `check-ethics` job: Ethical AI Charter sections verified
- `check-community` job: RFC template, CODEOWNERS verified

#### Community (Prompt 11)
- `docs/MAINTAINERS.md` — 7 maintainer roles, nomination process, burnout prevention
- `docs/rfc-template.md` — full RFC template with ethical impact assessment
- `CONTRIBUTING.md` updated with all 7 maintainer roles and burnout prevention policy

#### Ethics (Prompt 12)
- `docs/ETHICAL-REVIEW-PROCESS.md` — ERB process, decision types, annual metrics, incident response
- `ETHICS.md` expanded with 7-point Ethical AI Charter
- Annual ethics metrics publication process defined

#### Documentation
- `docs/OPERATIONS.md` — runbooks RB-01 through RB-09, monitoring metrics, incident classification
- `docs/RELEASES.md` — release types, pre-release checklist, hotfix process, SemVer rules
- `docs/CI-GOVERNANCE.md` — workflow documentation, constraint budgets, governance enforcement matrix
- `docs/SUSTAINABILITY.md` — financial model, environmental footprint, long-term viability
- ADR-0036: CSP nonce centralized template helpers
- ADR-0037: Pprof explicit handler + rate limit + audit log
- ADR-0038: VACUUM cooldown + write-threshold guard
- ADR-0039: Deploy sourced component architecture
- ADR-0040: Config versioning + compatibility contracts
- ADR-0041: Structured health contracts (6 endpoints)
- ADR-0042: Backup restore automation + checksum registry
- ADR-0043: Integration test suite (8 test files)

### Changed
- `Makefile` — added: `test-integration`, `test-migrations`, `test-storage`, `test-api-contracts`, `bench`, `check-adrs`, `check-governance`, `check-ethics`, `check-security`, `check-complexity`, `check-threat-model`
- `scripts/README.md` — updated compliance table to ADR-0043

### Governance
- Constitution: v6.0 Prompts 1–12 fully implemented and CI-enforced
- All 14 required ADRs present and accepted

### SHA-256 Checksums
- To be published with binary release artifact

---

## [1.0.0-p8] — 2026-06-12

### Added
- Plugin pool WaitGroup drain + context cancellation propagation (ADR-0032)
- WAL adaptive checkpoint with size threshold triggers >32 MB (ADR-0033)
- Migration checksum drift verification at startup — halts boot on tampering (ADR-0034)
- Dead-letter replay limits (max 100/call), poison-job quarantine after MAX_REPLAY_COUNT (ADR-0035)
- CSP nonce centralized template helpers — `CSPNonce(r)` exported (ADR-0036)
- Pprof explicit handler registration, localhost-only binding, rate limiting, audit logging (ADR-0037)
- VACUUM cooldown window (10 min) + active write threshold guard (ADR-0038)
- Deploy scaffold sourced components (`deploy/install.sh` etc.) (ADR-0039)
- Config versioning + compatibility validation, deprecated setting warnings (ADR-0040)
- Structured health contracts: `/health/dependencies`, `/health/storage`, `/health/search`, `/health/queue` (ADR-0041)
- Backup restore automation: nightly restore validation cron, integrity check, checksum registry (ADR-0042)
- 8 new integration test files covering shutdown race, WAL recovery, plugin panic flood, migration corruption, replay abuse, CSP nonce, vacuum rate-limit, health contracts (ADR-0043)
- Repository governance structure aligned to Constitution v6.0
- `ETHICS.md` — Ethical AI Charter and principles
- `GOVERNANCE.md` — Governance overview and amendment process
- `SECURITY.md` — Vulnerability disclosure policy
- `CONTRIBUTING.md` — Contributor guide
- `docs/ARCHITECTURE.md`, `docs/INSTALLATION.md`, `docs/API-REFERENCE.md`, `docs/DEVELOPMENT.md`, `docs/TROUBLESHOOTING.md`
- `docs/EMAILS.md` — Official contact addresses
- `docs/adr/` — Architecture Decision Records directory

### Security
- Automated CSP nonce per request for all inline scripts
- Pprof rate-limited and localhost-only by default
- Migration tampering detection halts startup

### Upgrade Notes
- `QUEUE_MAX_RETRIES` env var deprecated — use `MAX_REPLAY_COUNT` instead
- `ConfigVersion=1.0` validation added; incompatible configs log a warning

---

## [0.9.0-p7] — Previous

### Added
- Decomposition, reliability, and operational contracts (Prompt 7 compliance)
- Deploy script modularisation

---

*Older entries omitted for brevity. Full history available via `git log`.*
