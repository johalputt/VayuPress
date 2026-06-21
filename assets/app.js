'use strict';

/* ═══════════════ Wind particle field (Vayu = wind)
   Lightweight: ~48 particles max, visibility-aware, DPR-capped at 1.5,
   disabled on prefers-reduced-motion and small-RAM devices.            ═══ */
(function windCanvas() {
  if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return;
  // Skip on low-memory devices (navigator.deviceMemory < 2 GB)
  if (navigator.deviceMemory !== undefined && navigator.deviceMemory < 2) return;

  const canvas = document.getElementById('wind');
  if (!canvas) return;
  const ctx = canvas.getContext('2d', { alpha: true, desynchronized: true });
  const DPR = Math.min(window.devicePixelRatio || 1, 1.5); // cap for perf
  let w, h, particles, raf, visible = true;

  function resize() {
    w = canvas.width  = innerWidth  * DPR;
    h = canvas.height = innerHeight * DPR;
    canvas.style.width  = innerWidth  + 'px';
    canvas.style.height = innerHeight + 'px';
    // Fewer particles on mobile
    const isMobile = innerWidth < 640;
    const count = Math.min(isMobile ? 32 : 52, Math.floor(innerWidth / (isMobile ? 18 : 22)));
    particles = Array.from({ length: count }, () => spawnAt(Math.random() * w, Math.random() * h));
  }

  function spawnAt(x, y) {
    const teal = Math.random() > 0.32;
    return {
      x, y,
      len: (50 + Math.random() * 110) * DPR,
      vx:  (0.3  + Math.random() * 0.9)  * DPR,
      vy:  (Math.random() - 0.5) * 0.18 * DPR,
      a:   0.035 + Math.random() * 0.085,
      c:   teal ? '13,148,136' : '245,158,11',
    };
  }

  function frame() {
    if (!visible) return;
    ctx.clearRect(0, 0, w, h);
    for (const p of particles) {
      const g = ctx.createLinearGradient(p.x, p.y, p.x - p.len, p.y);
      g.addColorStop(0, `rgba(${p.c},${p.a})`);
      g.addColorStop(1, `rgba(${p.c},0)`);
      ctx.strokeStyle = g;
      ctx.lineWidth = DPR;
      ctx.beginPath();
      ctx.moveTo(p.x, p.y);
      ctx.lineTo(p.x - p.len, p.y);
      ctx.stroke();
      p.x += p.vx; p.y += p.vy;
      if (p.x - p.len > w) {
        Object.assign(p, spawnAt(-p.len * 0.15, Math.random() * h));
      }
    }
    raf = requestAnimationFrame(frame);
  }

  addEventListener('resize', () => { cancelAnimationFrame(raf); resize(); frame(); }, { passive: true });
  document.addEventListener('visibilitychange', () => {
    visible = !document.hidden;
    if (visible) { cancelAnimationFrame(raf); frame(); }
  });
  resize();
  frame();
})();

/* ═══════════════ Cursor aura (desktop pointer only) ═══ */
(function cursorAura() {
  if (window.matchMedia('(pointer: coarse)').matches) return;
  if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return;
  const el = document.getElementById('aura');
  if (!el) return;
  let tx = innerWidth / 2, ty = innerHeight / 2, cx = tx, cy = ty;
  addEventListener('pointermove', (e) => { tx = e.clientX; ty = e.clientY; }, { passive: true });
  (function loop() {
    cx += (tx - cx) * 0.10;
    cy += (ty - cy) * 0.10;
    el.style.transform = `translate(${cx - 210}px,${cy - 210}px)`;
    requestAnimationFrame(loop);
  })();
})();

/* ═══════════════ Scroll reveal ═══════════════ */
(function reveal() {
  const els = () => document.querySelectorAll('[data-reveal]');
  if (!('IntersectionObserver' in window)) {
    els().forEach((el) => el.classList.add('revealed'));
    return;
  }
  const obs = new IntersectionObserver(
    (entries) => entries.forEach((e) => { if (e.isIntersecting) { e.target.classList.add('revealed'); obs.unobserve(e.target); } }),
    { threshold: 0.1, rootMargin: '0px 0px -6% 0px' }
  );
  // Observe after DOM ready
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', () => els().forEach((el) => obs.observe(el)));
  } else {
    els().forEach((el) => obs.observe(el));
  }
})();

/* ═══════════════ Alpine root ═══════════════ */
function app() {
  return {
    /* state */
    scrolled:   false,
    scrollPct:  0,
    t:          0,          // hero terminal line
    lightbox:   null,
    typed:      '',
    copied:     false,
    stars:      '★',
    _typing:    false,

    deployScript:
`git clone https://github.com/johalputt/vayupress && cd vayupress
CGO_ENABLED=1 go build -o vayupress ./cmd/vayupress
STATIC_DIR=./static VAYU_DOCS_DIR=./docs ./vayupress --port 8080`,

    /* ── data ── */
    trustBadges: [
      'single-VPS deploy',
      'SQLite-durable',
      'zero third-party trackers',
      '67 architecture decisions',
      'Apache-2.0 licensed',
      'ActivityPub federation',
    ],

    features: [
      { icon:'🎬', iconBg:'bg-teal-900/60 border border-teal-800/60',   orb:'rgba(45,212,191,0.45)',
        title:'Sovereign rich media & Theme Studio',
        desc:'New in v1.4.0 (ADR-0070): diagrams, video and design — all rendered on your own origin. A pure-Go engine turns Mermaid into sanitised SVG server-side (flowchart, sequence, pie, state, class, gantt) with no browser JS. Video and link embeds are unfurled and self-hosted, never calling the reader’s browser out to a third party. The Theme Studio compiles typed design tokens — colour ramps, typography, spacing — into a single sovereign stylesheet, with an instant live preview that stays inside the strict style-src ’self’ CSP.',
        tags:['Mermaid→SVG','rich media','design tokens'] },
      { icon:'⚡', iconBg:'bg-teal-900/60 border border-teal-800/60',   orb:'rgba(13,148,136,0.45)',
        title:'Adaptive governance runtime',
        desc:'Six system modes on a validated transition graph, severity-classified error budgets, append-only mode journal, and the gated budget actuator (Ω12) for opt-in automatic escalation.',
        tags:['mode-graph','budgets','Ω12'] },
      { icon:'🗄️', iconBg:'bg-sky-900/60 border border-sky-800/60',     orb:'rgba(56,189,248,0.38)',
        title:'Durable by design',
        desc:'Append-only SQLite write queue with retry, dead-letter and replay. Transactional outbox relay, WAL with adaptive checkpointing, migration checksum drift verification.',
        tags:['SQLite','WAL','outbox'] },
      { icon:'🔭', iconBg:'bg-violet-900/60 border border-violet-800/60',orb:'rgba(167,139,250,0.38)',
        title:'Observable end to end',
        desc:'Structured health contracts, distributed tracing with correlation/causation IDs, Prometheus metrics, and a unified operational timeline in the console.',
        tags:['tracing','metrics','health'] },
      { icon:'🎨', iconBg:'bg-pink-900/60 border border-pink-800/60',    orb:'rgba(244,114,182,0.35)',
        title:'Operator theme console',
        desc:'Identity, light/dark palette, custom CSS, declarative head/SEO, favicon & logo upload (magic-number validated), portable JSON export/import, one-click reset.',
        tags:['branding','favicon','export'] },
      { icon:'🔌', iconBg:'bg-orange-900/60 border border-orange-800/60',orb:'rgba(251,146,60,0.35)',
        title:'Sandboxed plugins',
        desc:'Subprocess plugins under a capability model — filesystem, network and write allowlists. Five worked examples including trace-tap and seo-stamp.',
        tags:['sandbox','capabilities'] },
      { icon:'🌐', iconBg:'bg-emerald-900/60 border border-emerald-800/60',orb:'rgba(52,211,153,0.35)',
        title:'Federation substrate',
        desc:'Minimal ActivityPub server with HTTP-signature verification and durable, atomic inbox replay protection against hostile or retrying peers.',
        tags:['ActivityPub','replay'] },
      { icon:'🧱', iconBg:'bg-teal-900/60 border border-teal-800/60',     orb:'rgba(45,212,191,0.45)',
        title:'Next-gen admin & block editor (/admin/v3)',
        desc:'A ground-up redesign (ADR-0068): a cohesive design system, an intelligent dashboard with a 14-day sparkline, and a typed block editor whose document is rendered server-side and re-sanitised — there is no raw-HTML escape hatch. Plus a media library, members management, native SEO and analytics, all on the same strict-CSP, zero-CDN stack. Admin v2 stays available during the staged migration (ADR-0069).',
        tags:['block editor','design system','strict-CSP'] },
      { icon:'🔐', iconBg:'bg-cyan-900/60 border border-cyan-800/60',     orb:'rgba(34,211,238,0.40)',
        title:'Two-factor authentication (TOTP)',
        desc:'RFC 6238 time-based one-time passwords implemented in pure Go standard library (validated against the RFC test vectors) — no third-party 2FA SDK. Two-step enrolment stores the secret disabled until a code is verified, so an abandoned setup can never lock an operator out. Enforced on both the v2 and v3 sign-in surfaces.',
        tags:['TOTP','RFC 6238','stdlib-only'] },
      { icon:'✍️', iconBg:'bg-teal-900/60 border border-teal-800/60',     orb:'rgba(45,212,191,0.40)',
        title:'Editor-first admin (/admin/v2)',
        desc:'A modern, distraction-free writing surface on a fully vendored, CSP-clean stack — no CDNs, no unsafe-eval. Author in Markdown or raw HTML, with split-view live preview, slash commands, drag-&-drop image upload, autosave, an SEO readiness meter and version history. The public renderer always receives server-sanitised HTML.',
        tags:['editor','Markdown / HTML','CSP-safe'] },
      { icon:'⬆️', iconBg:'bg-amber-900/60 border border-amber-800/60',   orb:'rgba(245,158,11,0.35)',
        title:'Sovereign self-update',
        desc:'Check for updates from the panel; apply them from the CLI only — gated by opt-in, system mode, an operator-pinned Ed25519 key, checksum + signature verification, and an automatic backup. No web RCE, ever.',
        tags:['Ed25519','signed','CLI-only'] },
      { icon:'📬', iconBg:'bg-rose-900/60 border border-rose-800/60',     orb:'rgba(251,113,133,0.35)',
        title:'Sovereign email & newsletter',
        desc:'Plain-SMTP delivery built on the Go standard library — no third-party SDKs, no hosted senders, no telemetry. Double opt-in confirmations, one-click broadcasts with auto unsubscribe links, CRLF-injection-safe headers. Unconfigured? Every send is a safe no-op.',
        tags:['SMTP','newsletter','no-SDK'] },
      { icon:'🗓️', iconBg:'bg-indigo-900/60 border border-indigo-800/60', orb:'rgba(129,140,248,0.35)',
        title:'Scheduled publishing',
        desc:'Stage future-dated posts with an RFC3339 publish time. A durable SQLite-backed ticker promotes each through the normal render → index → cache pipeline when due — and catches up anything missed while the server was down.',
        tags:['scheduling','durable','catch-up'] },
      { icon:'👥', iconBg:'bg-cyan-900/60 border border-cyan-800/60',     orb:'rgba(34,211,238,0.35)',
        title:'Multi-author accounts',
        desc:'Per-author email + password sign-in with Argon2id-hashed credentials and server-side SQLite sessions (only token hashes stored, HttpOnly/SameSite cookie). Admin pages accept an API key or a login session — bootstrap the first admin from the CLI.',
        tags:['Argon2id','sessions','roles'] },
      { icon:'🖼️', iconBg:'bg-lime-900/60 border border-lime-800/60',     orb:'rgba(163,230,53,0.32)',
        title:'Automatic image optimization',
        desc:'Oversized PNG/JPEG editor uploads are downscaled and re-encoded by a stdlib-only pipeline — no libvips, no CGO, no third-party scaling libs. GIF and WebP pass through untouched; the smaller of optimized/original always wins.',
        tags:['stdlib-only','downscale','no-CGO'] },
      { icon:'📊', iconBg:'bg-fuchsia-900/60 border border-fuchsia-800/60',orb:'rgba(232,121,249,0.32)',
        title:'Privacy-first analytics',
        desc:'Cookieless, consent-free page-view counting with zero PII — no IP addresses, no user agents, no cookies, no fingerprints, no per-visitor rows. Only daily aggregates per path and referrer host. Nothing to leak, no banner to show.',
        tags:['cookieless','no-PII','aggregate'] },
      { icon:'🪝', iconBg:'bg-orange-900/60 border border-orange-800/60', orb:'rgba(251,146,60,0.32)',
        title:'Outbound webhooks',
        desc:'HMAC-SHA256-signed JSON POSTs on article create/update/delete — wire VayuPress into Zapier, n8n, Make or any custom service. Per-hook secrets, bounded retry/backoff, and a full delivery audit trail.',
        tags:['HMAC-signed','retry','audit'] },
      { icon:'🐘', iconBg:'bg-purple-900/60 border border-purple-800/60', orb:'rgba(192,132,252,0.32)',
        title:'Social auto-posting & easy migration',
        desc:'Auto-share new posts to Mastodon/Pleroma/Akkoma with a single app token — no OAuth dance. Plus built-in Ghost (JSON) and WordPress (WXR) importers move your whole archive across with no external tooling: titles, slugs, dates, tags and draft status preserved.',
        tags:['Mastodon','Ghost import','WP import'] },
      { icon:'🤖', iconBg:'bg-teal-900/60 border border-teal-800/60',     orb:'rgba(45,212,191,0.32)',
        title:'Sovereign AI writing assistant',
        desc:'Summarize, improve, suggest titles, draft SEO metadata or continue a draft — powered by a LOCAL Ollama model you run yourself. Nothing is sent to a hosted third party, and the assistant only suggests: it never auto-edits your content.',
        tags:['local-LLM','Ollama','suggest-only'] },
      { icon:'💳', iconBg:'bg-green-900/60 border border-green-800/60',   orb:'rgba(74,222,128,0.32)',
        title:'Memberships & paywalls',
        desc:'Turn readers into members with passwordless magic-link sign-in (no reader passwords stored). Gate any article as public, members or paid — non-members get a preview plus a sign-in CTA. Optional signature-verified Stripe webhook handles paid upgrades, with no payment SDK embedded.',
        tags:['magic-link','paywall','Stripe-optional'] },
      { icon:'🎨', iconBg:'bg-sky-900/60 border border-sky-800/60',       orb:'rgba(56,189,248,0.32)',
        title:'Server-side syntax highlighting',
        desc:'Code blocks are highlighted at render time by chroma and served from a same-origin stylesheet — class-only, never inline styles, so the strict style-src \'self\' CSP stays intact. A highlight-before-sanitise placeholder pipeline keeps every byte of prose flowing through the sanitiser (ADR-0066).',
        tags:['chroma','CSP-safe','server-side'] },
      { icon:'🔗', iconBg:'bg-violet-900/60 border border-violet-800/60', orb:'rgba(167,139,250,0.32)',
        title:'Related posts, reading time & PDFs',
        desc:'Every article shows estimated reading time and a precise, comma-token "related articles" list (so "go" never matches "golang"). Upload PDFs and documents (magic-number validated, up to 32 MB) alongside images — SVG stays refused as an XSS vector.',
        tags:['related','reading-time','PDF'] },
      { icon:'📲', iconBg:'bg-emerald-900/60 border border-emerald-800/60',orb:'rgba(52,211,153,0.32)',
        title:'Installable PWA + offline',
        desc:'A generated web-app manifest and a hand-rolled service worker make the public site installable and readable offline — cache-first for assets, stale-while-revalidate for pages, and it never caches the admin console. No build step, no third-party SW library.',
        tags:['PWA','offline','service-worker'] },
      { icon:'🕸️', iconBg:'bg-pink-900/60 border border-pink-800/60',     orb:'rgba(244,114,182,0.32)',
        title:'Read-only GraphQL API',
        desc:'A query-only GraphQL endpoint exposes articles, tags and search with computed wordCount, readingMinutes and excerpt fields — built on pure-Go graphql-go. There are deliberately no mutations, so writes never get a second, weaker path around the governed REST API (ADR-0067).',
        tags:['GraphQL','query-only','pure-Go'] },
      { icon:'🌍', iconBg:'bg-indigo-900/60 border border-indigo-800/60', orb:'rgba(129,140,248,0.32)',
        title:'i18n & customisable emails',
        desc:'A BCP-47 message catalog with Accept-Language negotiation and English fallback, editable per-language at runtime. Plus operator-customisable transactional email templates (magic-link, comment-approved, newsletter) — auto-escaped, hot-reloaded, stored in your own SQLite.',
        tags:['i18n','email-templates','hot-reload'] },
      { icon:'📡', iconBg:'bg-amber-900/60 border border-amber-800/60',   orb:'rgba(245,158,11,0.32)',
        title:'Real-time event stream + CDN push',
        desc:'An API-key-gated Server-Sent-Events feed broadcasts article create/update/delete events for live dashboards and multi-editor presence — read-only, no WebSocket dependency. Cloudflare edge-purge and IndexNow submission fire automatically on every mutation.',
        tags:['SSE','real-time','CDN-purge'] },
    ],

    screenshots: [
      { label:'Homepage',         path:'/',                       src:'screenshots/homepage.png',         caption:'Public homepage — clean, fast, no third-party scripts.' },
      { label:'Admin v3 dashboard', path:'/admin/v3',             src:'screenshots/admin-v3-dashboard.png', caption:'Next-gen admin (ADR-0068) — grouped sidebar, stat cards and a CSP-safe 14-day server-rendered sparkline.' },
      { label:'Admin v3 block editor', path:'/admin/v3/editor',   src:'screenshots/admin-v3-editor.png',  caption:'Typed block editor — document rendered server-side and re-sanitised, no raw-HTML escape hatch.' },
      { label:'Admin v3 media',   path:'/admin/v3/media',         src:'screenshots/admin-v3-media.png',   caption:'Media library — magic-number-validated uploads; SVG refused as an XSS vector.' },
      { label:'Admin v3 security (2FA)', path:'/admin/v3/security', src:'screenshots/admin-v3-security.png', caption:'Security — TOTP two-factor enrolment (RFC 6238, pure stdlib), enforced on sign-in.' },
      { label:'Admin v2 editor',  path:'/admin/v2/editor',        src:'screenshots/admin-v2-editor.png',  caption:'Editor-first redesign — split-view live preview, slash commands, autosave. CSP-strict, eval-free (ADR-0065).' },
      { label:'Admin v2 dashboard', path:'/admin/v2',             src:'screenshots/admin-v2-dashboard.png', caption:'Modern admin dashboard — dark-first, teal/saffron, fully vendored, no CDNs.' },
      { label:'Admin dashboard',  path:'/admin',                  src:'screenshots/admin-dashboard.png',  caption:'Classic operator console — runtime health, mode status and quick actions.' },
      { label:'Theme console',    path:'/admin/theme',            src:'screenshots/admin-panel.png',      caption:'Theme console — identity, palette, favicon upload, export/import and reset.' },
      { label:'Theme Studio',     path:'/admin/theme?tab=studio', src:'screenshots/theme-studio.png',     caption:'Theme Studio (v1.4.0) — curated design-token presets with an instant, CSP-safe live preview. Colour, type and spacing compile to a single sovereign stylesheet — no inline styles, no CDNs.' },
      { label:'Policy modes',     path:'/admin/policy/modes',     src:'screenshots/policy-modes.png',     caption:'Six modes: normal → degraded → read-only → quarantined → recovery → maintenance.' },
      { label:'Policy inspector', path:'/admin/policy/inspector', src:'screenshots/policy-inspector.png', caption:'Live error budgets with severity classification and actuation status.' },
      { label:'Runtime topology', path:'/admin/runtime/topology', src:'screenshots/runtime-topology.png', caption:'Subsystem graph with health and dependency edges.' },
      { label:'Replay explorer',  path:'/admin/replay',           src:'screenshots/replay-explorer.png',  caption:'Inspect and re-drive dead-letter activities from the SQLite outbox.' },
      { label:'ADR registry',     path:'/admin/adr',              src:'screenshots/adr-registry.png',     caption:'All 67 architecture decision records, browsable in-console.' },
    ],

    principles: [
      { title:'Single-tenant by default',          body:'One operator, one VPS, one SQLite database. No multi-tenant complexity, no shared infrastructure. Your data never leaves your machine.' },
      { title:'Operations as first-class surfaces', body:'Modes, budgets, faults, traces and ADRs are observable, governable entities — not log lines buried in a sidecar. Every decision is auditable.' },
      { title:'No invisible dependencies',          body:'Zero third-party fonts on your readers. Zero analytics. Zero CDN trackers. The only external calls are ones you explicitly configure.' },
      { title:'Decisions have records',             body:'67 ADRs document every significant choice — from WAL checkpointing to the inbox replay protocol. The codebase ships with its own archaeology.' },
    ],

    /* ── How VayuPress compares ── */
    compareCols: ['VayuPress', 'WordPress', 'Ghost', 'Substack'],
    compareRows: [
      { f:'Single self-contained binary',       v:['yes','no','no','n/a'] },
      { f:'Data in your own SQLite file',       v:['yes','partial','partial','no'] },
      { f:'Zero reader-side trackers / cookies', v:['yes','no','partial','no'] },
      { f:'Privacy-first analytics built in',   v:['yes','plugin','partial','partial'] },
      { f:'Strict CSP, no unsafe-inline/eval',  v:['yes','no','partial','no'] },
      { f:'Sandboxed, capability-scoped plugins', v:['yes','no','no','no'] },
      { f:'Adaptive governance / system modes', v:['yes','no','no','no'] },
      { f:'Transactional outbox + replay',      v:['yes','no','no','no'] },
      { f:'Read-only GraphQL API',              v:['yes','plugin','partial','no'] },
      { f:'Server-side syntax highlighting',    v:['yes','plugin','yes','partial'] },
      { f:'Pure-Go diagrams (Mermaid → SVG, no JS)', v:['yes','plugin','no','no'] },
      { f:'Sovereign rich media (no reader-side calls)', v:['yes','no','no','no'] },
      { f:'Live Theme Studio (design tokens, CSP-safe)', v:['yes','partial','partial','no'] },
      { f:'Memberships & paywalls, no SDK lock-in', v:['yes','plugin','yes','hosted-only'] },
      { f:'Local-LLM AI assistant (no cloud)',  v:['yes','no','no','no'] },
      { f:'ActivityPub federation',             v:['yes','plugin','partial','no'] },
      { f:'Installable PWA + offline reading',  v:['yes','plugin','partial','no'] },
      { f:'Signed, CLI-only self-update (no web RCE)', v:['yes','no','no','n/a'] },
      { f:'Every decision recorded as an ADR',  v:['yes','no','no','no'] },
      { f:'Apache-2.0, self-hostable, no SaaS lock-in', v:['yes','yes','yes','no'] },
    ],

    steps: [
      { label:'Clone the repository',              cmd:'git clone github.com/johalputt/vayupress' },
      { label:'Build the binary (CGO + SQLite)',   cmd:'CGO_ENABLED=1 go build ./cmd/vayupress' },
      { label:'Run the test suite',                cmd:'CGO_ENABLED=1 go test ./...' },
      { label:'Start the server',                  cmd:'STATIC_DIR=./static ./vayupress --port 8080' },
    ],

    tools: [
      /* ── Migration tools ── */
      {
        name:'vayupress migrate (built-in)',
        tag:'Migration',
        desc:'New in v1.1.0 — import Markdown folders straight from the main binary, no separate tool to build. Parses YAML frontmatter, renders GitHub-Flavored Markdown, and writes both the sanitised article and an editable Markdown side-car so the editor reopens each post in Markdown mode.',
        points:[
          'vayupress migrate markdown --dir ./posts (--dry-run to preview)',
          'Idempotent INSERT OR IGNORE — safe to re-run after interruptions',
          'vayupress migrate info prints options for every supported platform',
        ],
        cmd:'vayupress migrate markdown --dir ./posts',
        href:'https://github.com/johalputt/VayuPress/blob/main/docs/MIGRATION.md',
      },
      {
        name:'ghost-to-vayu',
        tag:'Migration',
        desc:'Move an entire Ghost site into VayuPress straight from the database — no Ghost admin or API needed. Reads MySQL or SQLite directly, preserves every slug, tag, image and timestamp, and throttles itself so even a 200k-post migration never overloads your VPS.',
        points:[
          'Direct DB access — MySQL & SQLite, no running Ghost required',
          'Images, links & formatting preserved (HTML passed through, sanitized on render)',
          'Keyset pagination + checkpoints — gentle, resumable, idempotent',
        ],
        cmd:'go build -o ghost2vayu ./cmd/ghost2vayu',
        href:'https://github.com/johalputt/VayuPress/tree/main/tools/ghost-to-vayu',
      },
      {
        name:'wordpress2vayu',
        tag:'Migration',
        desc:'Lift a WordPress site into VayuPress directly from its MySQL database — no plugins, no export files, no running WordPress. Reads wp_posts, categories and tags, recovers featured images, preserves slugs and dates.',
        points:[
          'Direct MySQL access — reads posts, pages, categories & tags',
          'Featured images recovered from postmeta, content HTML preserved',
          'Custom table prefixes, keyset pagination, resumable & idempotent',
        ],
        cmd:'go build -o wp2vayu ./cmd/wp2vayu',
        href:'https://github.com/johalputt/VayuPress/tree/main/tools/wordpress2vayu',
      },
      {
        name:'hugo2vayu',
        tag:'Migration',
        desc:'Import a Hugo site into VayuPress. Parses YAML and TOML frontmatter from Hugo content directories, merges categories into tags, strips the date prefix from filenames, and renders Markdown to HTML with goldmark.',
        points:[
          'YAML and TOML frontmatter support (--- and +++ delimiters)',
          'Merges categories + tags, strips Hugo date-prefixed filenames',
          'Dry-run and resume support',
        ],
        cmd:'go build -o hugo2vayu ./cmd/hugo2vayu',
        href:'https://github.com/johalputt/VayuPress/tree/main/tools/hugo2vayu',
      },
      {
        name:'jekyll2vayu',
        tag:'Migration',
        desc:'Import a Jekyll blog into VayuPress. Reads _posts and _pages, parses YAML frontmatter, extracts the date from the YYYY-MM-DD-slug.md filename format, and renders Markdown to HTML.',
        points:[
          'YAML frontmatter — title, date, slug, categories, tags, layout',
          'Date extracted from Jekyll filename convention',
          'Drafts from _drafts directory optionally included',
        ],
        cmd:'go build -o jekyll2vayu ./cmd/jekyll2vayu',
        href:'https://github.com/johalputt/VayuPress/tree/main/tools/jekyll2vayu',
      },
      {
        name:'substack2vayu',
        tag:'Migration',
        desc:'Import a Substack export into VayuPress. Reads the posts.csv that Substack provides and imports the title, slug, HTML body, and publication date — no Substack API key needed.',
        points:[
          'Reads Substack posts.csv export — no API access required',
          'Subtitle prepended to body, slugs extracted from Substack post URLs',
          'Skip drafts and free/subscriber-only filters',
        ],
        cmd:'go build -o substack2vayu ./cmd/substack2vayu',
        href:'https://github.com/johalputt/VayuPress/tree/main/tools/substack2vayu',
      },
      {
        name:'notion2vayu',
        tag:'Migration',
        desc:'Import a Notion HTML export into VayuPress. Parses the ZIP archive that Notion generates, extracts title and date from each page\'s HTML, and preserves content formatting.',
        points:[
          'Reads Notion ZIP export or flat directory of HTML files',
          'Title from <h1>, date from first <time> element',
          'Idempotent — safe to re-run after partial imports',
        ],
        cmd:'go build -o notion2vayu ./cmd/notion2vayu',
        href:'https://github.com/johalputt/VayuPress/tree/main/tools/notion2vayu',
      },
      {
        name:'medium2vayu',
        tag:'Migration',
        desc:'Import a Medium HTML export into VayuPress. Medium exports a ZIP of HTML files, one per post — this tool extracts title, date, tags and content from each file and inserts them into your VayuPress database.',
        points:[
          'Reads Medium export ZIP or extracted directory',
          'Extracts date from <time datetime="..."> and tags from <a rel="tag">',
          'Slug derived from Medium filename (date-prefix and hash stripped)',
        ],
        cmd:'go build -o medium2vayu ./cmd/medium2vayu',
        href:'https://github.com/johalputt/VayuPress/tree/main/tools/medium2vayu',
      },
      {
        name:'markdownfolder2vayu',
        tag:'Import',
        desc:'Turn any folder of Markdown files into a VayuPress site. Parses YAML frontmatter, renders GitHub-Flavored Markdown to HTML, derives slugs and dates from filenames when missing, and skips drafts.',
        points:[
          'YAML frontmatter — title, slug, date, tags, draft',
          'GitHub-Flavored Markdown → HTML via goldmark (tables, task lists)',
          'Slug & date fallbacks, draft-skipping, recursive folder walk',
        ],
        cmd:'go build -o md2vayu ./cmd/md2vayu',
        href:'https://github.com/johalputt/VayuPress/tree/main/tools/markdownfolder2vayu',
      },
      /* ── Operational tools ── */
      {
        name:'vayu-backup',
        tag:'Operations',
        desc:'Backup, restore, and verify VayuPress SQLite databases. Creates compressed tar.gz archives with a manifest, verifies checksums on restore, and supports scheduled backups with retention policies.',
        points:[
          'Compressed backup archives with SHA-256 manifest',
          'Verify backup integrity before restoring',
          'Schedule automated backups with retention',
        ],
        cmd:'go build -o vayu-backup ./cmd/vayu-backup',
        href:'https://github.com/johalputt/VayuPress/tree/main/tools/vayu-backup',
      },
      {
        name:'vayu-export',
        tag:'Operations',
        desc:'Export a VayuPress database as a static HTML site — every article rendered to a self-contained page, with a paginated index. Perfect for archiving, CDN deployment, or zero-server hosting.',
        points:[
          'Renders every article to standalone HTML with shared CSS',
          'Paginated index page with configurable page size',
          'Base URL rewriting for CDN or subdirectory deployment',
        ],
        cmd:'go build -o vayu-export ./cmd/vayu-export',
        href:'https://github.com/johalputt/VayuPress/tree/main/tools/vayu-export',
      },
      {
        name:'vayu-validate',
        tag:'Operations',
        desc:'Check a VayuPress database for content integrity issues before going live or after a migration. Catches empty titles, invalid slugs, duplicate slugs, bad dates, oversized content, and suspicious tags. Exits 1 on errors — CI-friendly.',
        points:[
          'Detects empty titles, invalid/duplicate slugs, empty content',
          'Flags suspicious dates (pre-2000 — common bad-migration artifact)',
          'Stats subcommand for content analytics — top tags, avg size, date range',
        ],
        cmd:'go build -o vayu-validate ./cmd/vayu-validate',
        href:'https://github.com/johalputt/VayuPress/tree/main/tools/vayu-validate',
      },
    ],

    footer: [
      { head:'Project', links:[
        { label:'GitHub',    href:'https://github.com/johalputt/VayuPress' },
        { label:'Changelog', href:'https://github.com/johalputt/VayuPress/blob/main/CHANGELOG.md' },
        { label:'Releases',  href:'https://github.com/johalputt/VayuPress/releases' },
      ]},
      { head:'Docs', links:[
        { label:'Installation', href:'https://github.com/johalputt/VayuPress/blob/main/docs/INSTALLATION.md' },
        { label:'Architecture', href:'https://github.com/johalputt/VayuPress/blob/main/docs/ARCHITECTURE.md' },
        { label:'Operations',   href:'https://github.com/johalputt/VayuPress/blob/main/docs/OPERATIONS.md' },
      ]},
      { head:'Decisions', links:[
        { label:'ADR registry', href:'https://github.com/johalputt/VayuPress/tree/main/docs/adr' },
        { label:'Threat model', href:'https://github.com/johalputt/VayuPress/blob/main/docs/THREAT-MODEL.md' },
        { label:'Plugins',      href:'https://github.com/johalputt/VayuPress/tree/main/docs/plugins' },
      ]},
    ],

    /* Deduplicate gallery for seamless marquee */
    get galleryLoop() {
      const tagged = this.screenshots.map((s, idx) => ({ ...s, idx }));
      return [...tagged, ...tagged];
    },

    /* ── Methods ── */
    onScroll() {
      const y = scrollY;
      this.scrolled = y > 24;
      const doc = document.documentElement;
      this.scrollPct = y / (doc.scrollHeight - doc.clientHeight);
    },

    smoothTo(id) {
      document.getElementById(id)?.scrollIntoView({ behavior: 'smooth' });
    },

    tilt(e) {
      if (window.matchMedia('(pointer: coarse)').matches) return;
      const el = e.currentTarget;
      const r  = el.getBoundingClientRect();
      const px = (e.clientX - r.left)  / r.width;
      const py = (e.clientY - r.top)   / r.height;
      el.style.setProperty('--mx', `${px * 100}%`);
      el.style.setProperty('--my', `${py * 100}%`);
      el.style.transform = `perspective(1000px) rotateX(${(0.5-py)*5.5}deg) rotateY(${(px-0.5)*5.5}deg) translateY(-3px)`;
    },
    untilt(e) { e.currentTarget.style.transform = ''; },

    ripple(e) {
      const btn = e.currentTarget;
      const el  = document.createElement('span');
      const d   = Math.max(btn.clientWidth, btn.clientHeight);
      const r   = btn.getBoundingClientRect();
      el.className = 'ripple';
      el.style.cssText = `width:${d}px;height:${d}px;left:${e.clientX-r.left-d/2}px;top:${e.clientY-r.top-d/2}px`;
      btn.appendChild(el);
      setTimeout(() => el.remove(), 750);
    },

    runType() {
      if (this._typing) return;
      this._typing = true;
      const text = this.deployScript;
      let i = 0;
      const tick = () => {
        if (i <= text.length) {
          this.typed = text.slice(0, i++);
          setTimeout(tick, text[i-1] === '\n' ? 160 : 14 + Math.random() * 24);
        }
      };
      tick();
    },

    copyDeploy() {
      navigator.clipboard?.writeText(this.deployScript).then(() => {
        this.copied = true;
        setTimeout(() => (this.copied = false), 2000);
      });
    },

    async fetchStars() {
      try {
        const r = await fetch('https://api.github.com/repos/johalputt/VayuPress', { cache: 'force-cache' });
        if (!r.ok) return;
        const d = await r.json();
        if (typeof d.stargazers_count === 'number') {
          this.stars = d.stargazers_count.toLocaleString();
        }
      } catch (_) { /* offline / rate-limited */ }
    },

    init() {
      /* scroll listener */
      addEventListener('scroll', () => this.onScroll(), { passive: true });

      /* fetch star count */
      this.fetchStars();

      /* hero terminal boot */
      let i = 1;
      const tick = () => { if (i <= 9) { this.t = i++; setTimeout(tick, i < 4 ? 540 : 370); } };
      setTimeout(tick, 750);

      /* typing terminal — triggered by IntersectionObserver when in view */
      this.$nextTick(() => {
        const term = this.$refs.deployTerm;
        if (!term) { this.runType(); return; }
        if (!('IntersectionObserver' in window)) { this.runType(); return; }
        const obs = new IntersectionObserver(
          (entries) => entries.forEach((e) => { if (e.isIntersecting) { this.runType(); obs.disconnect(); } }),
          { threshold: 0.3 }
        );
        obs.observe(term);
      });

      /* lightbox keyboard nav */
      addEventListener('keydown', (e) => {
        if (this.lightbox === null) return;
        if (e.key === 'ArrowLeft')  this.lightbox = (this.lightbox - 1 + this.screenshots.length) % this.screenshots.length;
        if (e.key === 'ArrowRight') this.lightbox = (this.lightbox + 1) % this.screenshots.length;
      });
    },
  };
}
