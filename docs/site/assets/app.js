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

/* ═══════════════ Aurora field — living cinematic gradient
   A few large, slowly-orbiting radial blobs blended additively into a soft,
   breathing aurora. Canvas 2D (no WebGL), DPR-capped, ~30fps, visibility-aware,
   and static on prefers-reduced-motion.                                    ═══ */
(function auroraCanvas() {
  const canvas = document.getElementById('aurora');
  if (!canvas) return;
  const reduce = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  const ctx = canvas.getContext('2d', { alpha: true });
  const DPR = Math.min(window.devicePixelRatio || 1, 1.4);
  const palette = [ [13,148,136], [139,92,246], [245,158,11], [56,189,248], [45,212,191] ];
  let w, h, blobs, raf, visible = true, last = 0;

  function resize() {
    w = canvas.width  = Math.floor(innerWidth  * DPR);
    h = canvas.height = Math.floor(innerHeight * DPR);
    canvas.style.width  = innerWidth  + 'px';
    canvas.style.height = innerHeight + 'px';
    const n = innerWidth < 640 ? 4 : 5;
    blobs = Array.from({ length: n }, (_, i) => ({
      c:   palette[i % palette.length],
      ox:  (0.15 + Math.random() * 0.7) * w,
      oy:  (0.10 + Math.random() * 0.7) * h,
      rad: (0.14 + Math.random() * 0.12) * Math.min(w, h),
      r:   (0.34 + Math.random() * 0.24) * Math.min(w, h),
      ang: Math.random() * Math.PI * 2,
      sp:  (0.0011 + Math.random() * 0.0013) * (Math.random() > 0.5 ? 1 : -1),
      a:   0.55 + Math.random() * 0.4,
    }));
  }

  function draw(t) {
    if (!visible) return;
    if (t - last < 32) { raf = requestAnimationFrame(draw); return; } // ~30fps
    last = t;
    ctx.clearRect(0, 0, w, h);
    ctx.globalCompositeOperation = 'lighter';
    for (const b of blobs) {
      if (!reduce) b.ang += b.sp;
      const cx = b.ox + Math.cos(b.ang) * b.rad;
      const cy = b.oy + Math.sin(b.ang * 0.85) * b.rad * 0.72;
      const g = ctx.createRadialGradient(cx, cy, 0, cx, cy, b.r);
      g.addColorStop(0,    `rgba(${b.c[0]},${b.c[1]},${b.c[2]},${0.17 * b.a})`);
      g.addColorStop(0.5,  `rgba(${b.c[0]},${b.c[1]},${b.c[2]},${0.05 * b.a})`);
      g.addColorStop(1,    `rgba(${b.c[0]},${b.c[1]},${b.c[2]},0)`);
      ctx.fillStyle = g;
      ctx.beginPath(); ctx.arc(cx, cy, b.r, 0, Math.PI * 2); ctx.fill();
    }
    ctx.globalCompositeOperation = 'source-over';
    if (reduce) return;
    raf = requestAnimationFrame(draw);
  }

  addEventListener('resize', () => { cancelAnimationFrame(raf); resize(); last = 0; raf = requestAnimationFrame(draw); }, { passive: true });
  document.addEventListener('visibilitychange', () => {
    visible = !document.hidden;
    if (visible) { cancelAnimationFrame(raf); last = 0; raf = requestAnimationFrame(draw); }
  });
  resize();
  raf = requestAnimationFrame(draw);
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
    feature:    null,       // open feature-detail index
    activeProduct: 0,       // selected product tab
    prodAnim:   0,          // bump on tab change to retrigger panel animation
    rot:        0,          // rotating hero subject index
    typed:      '',
    copied:     false,
    stars:      '★',
    version:    '',
    _typing:    false,

    deployScript:
`git clone https://github.com/johalputt/vayupress && cd vayupress
CGO_ENABLED=1 go build -o vayupress ./cmd/vayupress
STATIC_DIR=./static VAYU_DOCS_DIR=./docs ./vayupress --port 8080`,

    /* ── data ── */
    // Rotating hero subject — cycles through what one command stands up.
    rotWords: [
      'a business website.',
      'a Ghost-class blog.',
      'a PGP mail server.',
      'encrypted VayuTalk chat.',
      'cookieless analytics.',
      'your whole stack.',
    ],

    // Hero pillar chips — the whole platform at a glance.
    pillars: [
      'Website', 'Blog', 'PGP Mail', 'VayuTalk', 'Mobile App', 'Analytics', 'VayuOS',
    ],

    trustBadges: [
      'single-VPS deploy',
      'SQLite-durable',
      'zero third-party trackers',
      'website · blog · mail · chat · app',
      'native mail + E2E PGP',
      'Apache-2.0 licensed',
    ],

    features: [
      { icon:'🏪', iconBg:'bg-orange-900/60 border border-orange-800/60', orb:'rgba(251,146,60,0.45)',
        title:'A business website, not just a blog',
        desc:'Need a real website for a restaurant, shop, studio, school, clinic or portfolio? Pick from eleven elegant, modern-minimalist templates and deploy a full site — hero, offerings with prices, gallery, hours and contact — editing every word from VayuOS with a live preview. Your domain shows the website, the blog moves to blog.yourdomain.com and mail to mail.yourdomain.com; the installer gets Let\'s Encrypt certificates for all of them automatically. It\'s your choice, and an update never changes it.',
        tags:['11 templates','website + blog + mail','edit from VayuOS'] },
      { icon:'✍️', iconBg:'bg-teal-900/60 border border-teal-800/60',   orb:'rgba(45,212,191,0.55)',
        title:'A writing studio you\'ll love',
        desc:'A calm, Ghost-clean block editor with tables, toggles, task lists, math, callouts, code, self-hosted audio and privacy-first video. Write in blocks, switch to whole-document Markdown or raw HTML and back — losslessly — and drop, paste or link any image (Unsplash, Pixabay, anywhere) straight in. Drag to reorder, undo anything, and watch live word-count as you type; a slash menu, a ⌘K palette, focus mode and split preview keep you in flow.',
        tags:['blocks · Markdown · HTML','image drag/drop/link','slash menu · ⌘K'] },
      { icon:'✉️', iconBg:'bg-sky-900/60 border border-sky-800/60',     orb:'rgba(56,189,248,0.5)',
        title:'VayuMail — your own mail server',
        desc:'Send and receive from your own domain without renting a mail provider. Outbound mail is DKIM-signed and delivered straight to the recipient\'s server; an inbound SMTP receiver and IMAP inbox let the official VayuMail Android app, Thunderbird, K-9 and Apple Mail read your mail. Connect a phone in one scan with a rotating setup QR — it carries a per-device app password you can revoke any time, never your real password. It even writes your MX, SPF, DKIM and DMARC records and checks the DNS is healthy.',
        tags:['DKIM-signed','Android app','rotating setup QR'] },
      { icon:'🔑', iconBg:'bg-violet-900/60 border border-violet-800/60',orb:'rgba(167,139,250,0.5)',
        title:'VayuPGP — privacy by architecture',
        desc:'Every account gets a modern PGP keypair automatically. Private keys are encrypted at rest and never logged; you can encrypt, decrypt, sign, verify and rotate keys, and a Web Key Directory lets any GPG client discover your public key. End-to-end encryption that just works, with nothing to bolt on.',
        tags:['auto keypairs','encrypted at rest','WKD discovery'] },
      { icon:'🛡️', iconBg:'bg-emerald-900/60 border border-emerald-800/60',orb:'rgba(52,211,153,0.5)',
        title:'VayuOS — one calm control room',
        desc:'Every operator tool lives in one fast, strict-CSP admin at /os: posts, the editor, media, members, SEO, theme studio, mail and security. Creating an account quietly provisions a mailbox and PGP keys for you. One front door, no sprawl, no second weaker path in.',
        tags:['single admin','/os','strict CSP'] },
      { icon:'🎨', iconBg:'bg-pink-900/60 border border-pink-800/60',    orb:'rgba(244,114,182,0.55)',
        title:'Themes that restyle the whole site',
        desc:'Pick a theme and the entire public site changes — navigation, hero, post cards, article pages, author box, comments and footer, each with its own layout and personality. Tune logo, colours, fonts and your social share image with a live preview. All served from your own origin: no inline styles, no CDNs.',
        tags:['whole-site themes','live preview','self-hosted CSS'] },
      { icon:'💳', iconBg:'bg-green-900/60 border border-green-800/60',   orb:'rgba(74,222,128,0.4)',
        title:'Memberships & paywalls',
        desc:'Turn readers into members with passwordless magic-link sign-in — no reader passwords ever stored. Define priced tiers, publish a themed pricing page, and gate any article as public, members or paid. Members get a self-service portal; an optional Stripe webhook handles paid upgrades, with no payment SDK baked in.',
        tags:['magic-link','tiers & paywalls','member portal'] },
      { icon:'📈', iconBg:'bg-fuchsia-900/60 border border-fuchsia-800/60',orb:'rgba(232,121,249,0.4)',
        title:'Analytics without surveillance',
        desc:'See pageviews, top pages, referrers and trends — without cookies, consent banners, or storing a single IP address. Visitor identity is a daily-rotating salted hash that can\'t be traced back to a person, and everything lives in your own SQLite. Insight for you, privacy for your readers.',
        tags:['cookieless','no PII','no consent banner'] },
      { icon:'🛡', iconBg:'bg-rose-900/60 border border-rose-800/60',    orb:'rgba(244,63,94,0.45)',
        title:'VayuShield — bot defense meets deep analytics',
        desc:'A sovereign replacement for Cloudflare Bot Management and Google Analytics, built into the binary. It fingerprints every client from its TLS handshake (JA3/JA4), a 2026 post-quantum key share, HTTP/2 settings and header order to tell real browsers from scrapers, then challenges the suspicious ones with a silent proof-of-work that escalates to a JS check, a block, or a tarpit — learning new bot signatures as it goes. It welcomes and counts good bots and AI assistants (ChatGPT, Claude, Perplexity) separately, and measures real engagement — time on page, scroll depth, and how AI-referred readers compare to search. Under load it also shields availability — per-IP rate limiting, load shedding, an auto-blocklist and an adaptive under-attack mode that tightens the moment a flood starts and relaxes when it passes, with verified readers never throttled. Every defense is a live on/off switch in VayuOS, no restart. Still cookieless, still no PII, still GDPR by design.',
        tags:['JA3/JA4 + PoW','rate-limit + load-shed','live toggles, no Cloudflare/GA'] },
      { icon:'📦', iconBg:'bg-amber-900/60 border border-amber-800/60',   orb:'rgba(245,158,11,0.4)',
        title:'Update & back up in one click',
        desc:'Install the latest signed release from inside VayuOS — the download is checksum- and signature-verified, your database is backed up first, and the binary swaps in atomically. Export your entire site — database, settings, media, mailboxes and PGP keys — as one file encrypted with AES-256-GCM under a passphrase only you hold: a stolen backup is useless and tamper-evident with any tool, yet restores anywhere in seconds. No shell required, fully reversible.',
        tags:['signed updates','encrypted backups','reversible'] },
      { icon:'🔁', iconBg:'bg-indigo-900/60 border border-indigo-800/60', orb:'rgba(129,140,248,0.4)',
        title:'Bring your whole archive',
        desc:'Move in from Ghost, WordPress, Substack, Medium, Hugo, Jekyll, Notion or a plain folder of Markdown — slugs, tags, dates, images and drafts preserved. The importers are resumable and gentle enough to migrate a 200,000-post site onto a small VPS without falling over.',
        tags:['Ghost · WP · Substack','Medium · Hugo · Jekyll','resumable'] },
      { icon:'🔌', iconBg:'bg-cyan-900/60 border border-cyan-800/60',   orb:'rgba(34,211,238,0.5)',
        title:'VayuAPI — fine-grained API control',
        desc:'Drive everything through the API — create posts, apply themes, manage domains, read analytics, run backups — with keys scoped to the exact section and action they need, and nothing more. Mint a key from a 12×6 permission grid, give it an expiry and a per-key rate budget, then rotate, deactivate or revoke it in a click. Every call is metered against the key\'s budget and written to a tamper-evident audit log; raw keys are never stored, only a hash. A script, a CI job or an AI agent can update your site autonomously — without ever holding the keys to everything.',
        tags:['12×6 permission grid','per-key rate + audit','scoped, hashed keys'] },
      { icon:'🧩', iconBg:'bg-blue-900/60 border border-blue-800/60',   orb:'rgba(96,165,250,0.45)',
        title:'Build extensions with confidence (VCB)',
        desc:'The Vayu Compatibility Bible turns \'will this work?\' into a checked contract. A developer — or an AI agent — writes a plugin.json or theme.json, runs vayu-compat, and knows before shipping that every hook, capability, colour, option and CSP rule matches what the host actually enforces — validated by the same code that runs it, so the docs can never drift. Themes that fetch from an external host, plugins that ask for more than they need, or manifests built against a hook that does not exist are refused with a plain, exact reason. Build for VayuPress with certainty, not guesswork.',
        tags:['plugin + theme contracts','vayu-compat validator','one source of truth'] },
    ],

    /* ── The Vayu suite — one binary, showcased product by product ── */
    products: [
      { key:'vayuos', name:'VayuOS', icon:'🛠', rgb:'52,211,153', badge:'',
        tag:'One calm control room for the whole platform',
        host:'/os',
        blurb:'Every surface — publishing, mail, chat, analytics and security — in one fast, strict-CSP admin. No sprawl, no SPA bloat, no second weaker path in. Creating an account quietly provisions a mailbox and PGP keys for you.',
        points:[
          '⌘K command palette + a live 14-day publishing chart',
          'Block editor, media library & Theme Studio',
          'Members, newsletter, SEO & mail in one place',
          'TOTP two-factor, roles & a WORM audit log',
          'One-click signed update & encrypted backup',
          'HTMX + hand-written CSS — negligible RAM/CPU',
        ],
        cta:{ label:'Explore VayuOS →', href:'https://github.com/johalputt/VayuPress' } },

      { key:'vayumail', name:'VayuMail', icon:'✉️', rgb:'56,189,248', badge:'',
        tag:'Your own sovereign PGP mail server',
        host:'mail.yourdomain.com',
        blurb:'SMTP send + receive, IMAP and POP3, RFC-6376 DKIM signing and direct-to-MX delivery — a real mail server for your domain, with automatic MX / SPF / DKIM / DMARC records and live DNS health checks. Mail never leaves your server unencrypted to a third party.',
        points:[
          'Send & receive from your own domain',
          'DKIM-signed, direct-to-MX with STARTTLS',
          'IMAP + POP3 for any client (Thunderbird, K-9…)',
          'Automatic MX / SPF / DKIM / DMARC + DNS health',
          'Native PGP: keys per account, published via WKD',
          'Official Android app — connect in one scan',
        ],
        cta:{ label:'Get the app ↗', href:'https://github.com/johalputt/VayuMail-Mobile' } },

      { key:'vayutalk', name:'VayuTalk', icon:'💬', rgb:'139,92,246', badge:'NEW',
        tag:'Ephemeral, end-to-end-encrypted chat',
        host:'talk.yourdomain.com',
        blurb:'Real-time PGP-encrypted messaging that works seamlessly across the web console and the mobile app over one shared relay — a message typed on the web reaches the phone and vice-versa. Nothing ever touches disk; every message vanishes the moment it is read.',
        points:[
          'PGP end-to-end encrypted, web ⇄ app interop',
          'Read-once — a message self-destructs on read',
          'Short TTL (5 min – 1 h); nothing is persisted',
          'Safety-number verification defeats a MITM swap',
          'Dedicated talk.yourdomain.com relay — bypasses any CDN',
          'Auto-provisioned by the installer — one DNS record',
        ],
        cta:{ label:'How VayuTalk works →', href:'https://github.com/johalputt/VayuPress/blob/main/docs/adr/ADR-0131-vayutalk-ephemeral-messaging.md' } },

      { key:'vayushield', name:'VayuShield', icon:'🛡', rgb:'244,63,94', badge:'',
        tag:'Enterprise bot shield & anti-DDoS — no Cloudflare',
        host:'/os/shield',
        blurb:'A five-layer, self-learning bot & DDoS shield built into the same binary. It keeps Save and refresh working even during a volumetric flood, jails offenders in minutes and forgives automatically — search engines and AI assistants are always welcome.',
        points:[
          'Admin-sovereignty lane survives a volumetric flood',
          'Fixed-memory fair-shed catches spoofed botnets',
          'Reputation brain jails offenders & auto-forgives',
          'Silent, self-calibrating proof-of-work challenges',
          'Search engines & AI crawlers always allowed',
          'Optional kernel-level nftables + XDP offload',
        ],
        cta:{ label:'Read the architecture →', href:'https://github.com/johalputt/VayuPress/blob/main/docs/adr/ADR-0111-vayushield-bot-protection-and-analytics.md' } },

      { key:'vayuanalytics', name:'VayuAnalytics', icon:'📊', rgb:'232,121,249', badge:'',
        tag:'Product analytics without surveillance',
        host:'/os/analytics',
        blurb:'Pageviews, sessions, top pages, funnels, retention, revenue and a live visitor panel — computed entirely from your local SQLite. No cookies, no localStorage, no IP or User-Agent ever stored, and no consent banner required.',
        points:[
          'Cookieless — a daily-rotating salted hash identity',
          'No IP or User-Agent ever stored — nothing to leak',
          'Funnels, retention, revenue & UTM campaigns',
          'Live visitor panel, updated in real time',
          'Offline country table — no external GeoIP call',
          'GDPR by design — no consent banner needed',
        ],
        cta:{ label:'See the dashboard →', href:'https://github.com/johalputt/VayuPress' } },

      { key:'website', name:'Website & Blog', icon:'🌐', rgb:'45,212,191', badge:'',
        tag:'A real website and a Ghost-class blog',
        host:'yourdomain.com',
        blurb:'Eleven elegant, modern-minimalist site templates plus a best-in-class block editor with lossless Markdown and HTML modes, whole-site themes, members, newsletters and SEO — every word in your own SQLite, styled entirely from your own origin.',
        points:[
          '11 modern-minimalist business templates',
          'Ghost-class block editor · Markdown · HTML',
          'Whole-site themes with a live Theme Studio',
          'Memberships, paywalls & newsletters',
          'Server-rendered Mermaid, math & code',
          'SEO, JSON-LD & sitemaps baked in',
        ],
        cta:{ label:'View the templates →', href:'https://github.com/johalputt/VayuPress' } },

      { key:'vayupgp', name:'VayuPGP', icon:'🔑', rgb:'245,158,11', badge:'',
        tag:'Privacy by architecture',
        host:'WKD',
        blurb:'Every account gets a modern PGP keypair automatically. Private keys are AES-256-GCM encrypted at rest and never logged; public keys are published via Web Key Directory so any GPG client can discover them. It is the engine under both VayuMail and VayuTalk.',
        points:[
          'Auto keypair generated per account',
          'Private keys AES-256-GCM encrypted at rest',
          'Encrypt · decrypt · sign · verify · rotate',
          'WKD discovery for any GPG client',
          'Powers both VayuMail and VayuTalk',
          'End-to-end encryption with nothing to bolt on',
        ],
        cta:{ label:'Learn about VayuPGP →', href:'https://github.com/johalputt/VayuPress' } },
    ],

    screenshots: [
      { label:'Homepage',         path:'/',          src:'screenshots/homepage.png',           caption:'Your public homepage — clean, fast, and free of third-party scripts.' },
      { label:'VayuOS dashboard', path:'/os',        src:'screenshots/admin-os-dashboard.png', caption:'VayuOS — one fast, calm admin for everything, with an at-a-glance dashboard.' },
      { label:'Block editor',     path:'/os/editor', src:'screenshots/admin-os-editor.png',    caption:'The block editor — distraction-free writing with a slash menu and live preview.' },
      { label:'Theme Studio',     path:'/os/theme',  src:'screenshots/admin-os-theme.png',     caption:'Theme Studio — restyle your whole site with a live preview, all self-hosted.' },
      { label:'Post manager',     path:'/os/posts',  src:'screenshots/admin-os-posts.png',     caption:'Post manager — every article in one view with one-click publish / unpublish.' },
      { label:'Member signup',    path:'/signup',    src:'screenshots/member-signup.png',      caption:'Reader sign-up — branded and passwordless; an email gets a one-time link.' },
      { label:'Media library',    path:'/os/media',  src:'screenshots/admin-os-media.png',     caption:'Media library — fast uploads with safe, validated file handling.' },
      { label:'SEO',              path:'/os/seo',    src:'screenshots/admin-os-seo.png',       caption:'Native SEO — sitemap, robots, structured data and per-post readiness.' },
      { label:'Analytics',        path:'/os/analytics', src:'screenshots/admin-os-analytics.png', caption:'Privacy-first analytics — insight for you, no cookies or PII for your readers.' },
      { label:'Security (2FA)',   path:'/os/security',  src:'screenshots/admin-os-security.png',  caption:'Security — two-factor authentication, enforced at sign-in.' },
    ],

    principles: [
      { title:'Single-tenant by default',          body:'One operator, one VPS, one SQLite database. No multi-tenant complexity, no shared infrastructure. Your data never leaves your machine.' },
      { title:'Operations as first-class surfaces', body:'Modes, budgets, faults, traces and ADRs are observable, governable entities — not log lines buried in a sidecar. Every decision is auditable.' },
      { title:'No invisible dependencies',          body:'Zero third-party fonts on your readers. Zero analytics. Zero CDN trackers. The only external calls are ones you explicitly configure.' },
      { title:'Decisions have records',             body:'Every significant choice is written down as an architecture decision record — from durability to the draft/publish security model. The codebase ships with its own reasoning.' },
    ],

    /* ── How VayuPress compares ── */
    compareCols: ['VayuPress', 'WordPress', 'Ghost', 'Substack'],
    compareRows: [
      { f:'Single self-contained binary',       v:['yes','no','no','n/a'] },
      { f:'Website + blog + mail on one domain', v:['yes','partial','no','no'] },
      { f:'Your data in your own SQLite file',  v:['yes','partial','partial','no'] },
      { f:'Native mail server built in (DKIM)', v:['yes','no','no','no'] },
      { f:'IMAP/POP3 + official mobile app',    v:['yes','no','no','no'] },
      { f:'End-to-end PGP encryption + WKD',    v:['yes','no','no','no'] },
      { f:'Zero reader-side trackers / cookies', v:['yes','no','partial','no'] },
      { f:'Offline analytics — no external GeoIP', v:['yes','plugin','partial','partial'] },
      { f:'Memberships & paywalls, no SDK lock-in', v:['yes','plugin','yes','hosted-only'] },
      { f:'Local-LLM AI assistant (no cloud)',  v:['yes','no','no','no'] },
      { f:'Apache-2.0, self-hostable, no SaaS lock-in', v:['yes','yes','yes','no'] },
    ],

    steps: [
      { label:'Clone the repository',              cmd:'git clone github.com/johalputt/vayupress' },
      { label:'Build the binary (CGO + SQLite)',   cmd:'CGO_ENABLED=1 go build ./cmd/vayupress' },
      { label:'Run the test suite',                cmd:'CGO_ENABLED=1 go test ./...' },
      { label:'Start the server',                  cmd:'STATIC_DIR=./static ./vayupress --port 8080' },
    ],

    tools: [
      {
        name:'Built-in importers',
        tag:'Migration',
        desc:'Move in from Ghost, WordPress, Substack, Medium, Hugo, Jekyll, Notion or a plain folder of Markdown — titles, slugs, dates, tags, images and draft status preserved. Reads databases and exports directly, with the source platform never left running.',
        points:[
          'Ghost & WordPress read straight from the database — no plugins',
          'Substack, Medium, Notion, Hugo, Jekyll & Markdown exports',
          'Resumable & idempotent — gentle enough for a 200k-post archive',
        ],
        cmd:'vayupress migrate markdown --dir ./posts',
        href:'https://github.com/johalputt/VayuPress/blob/main/docs/MIGRATION.md',
      },
      {
        name:'vayu-backup',
        tag:'Operations',
        desc:'Back up, restore and verify your VayuPress database. Compressed archives carry a checksum manifest, integrity is verified before any restore, and you can schedule automated backups with retention policies.',
        points:[
          'Compressed archives with SHA-256 manifest',
          'Integrity verified before any restore',
          'Schedule automated backups with retention',
        ],
        cmd:'go build -o vayu-backup ./cmd/vayu-backup',
        href:'https://github.com/johalputt/VayuPress/tree/main/tools/vayu-backup',
      },
      {
        name:'vayu-export',
        tag:'Operations',
        desc:'Export your whole site to static HTML — every article a self-contained page with a paginated index. Perfect for archiving, CDN deployment, or zero-server hosting.',
        points:[
          'Every article rendered to standalone HTML',
          'Paginated index with configurable page size',
          'Base-URL rewriting for CDN or subdirectory hosting',
        ],
        cmd:'go build -o vayu-export ./cmd/vayu-export',
        href:'https://github.com/johalputt/VayuPress/tree/main/tools/vayu-export',
      },
      {
        name:'vayu-compat',
        tag:'Extensions',
        desc:'Validate a plugin or theme against the Vayu Compatibility Bible before you ship it. It checks your plugin.json / theme.json with the very code the host enforces — hooks, capabilities, colours, options, sandbox limits and CSP rules — so a package that passes here installs and runs without surprises.',
        points:[
          'Plugin & theme manifests checked against the live contract',
          'Refuses external CSS fetches, over-broad grants & unknown hooks',
          'Exit 1 on any error — drop it straight into CI',
        ],
        cmd:'vayu-compat check --manifest plugin.json --host 3.13.42',
        href:'https://github.com/johalputt/VayuPress/blob/main/docs/compatibility/vcb.md',
      },
    ],

    footer: [
      { head:'Project', links:[
        { label:'GitHub',    href:'https://github.com/johalputt/VayuPress' },
        { label:'About the developer', href:'about.html' },
        { label:'Changelog', href:'https://github.com/johalputt/VayuPress/blob/main/CHANGELOG.md' },
        { label:'Releases',  href:'https://github.com/johalputt/VayuPress/releases' },
      ]},
      { head:'Docs', links:[
        { label:'Installation', href:'https://github.com/johalputt/VayuPress/blob/main/docs/INSTALLATION.md' },
        { label:'Architecture', href:'https://github.com/johalputt/VayuPress/blob/main/docs/ARCHITECTURE.md' },
        { label:'Operations',   href:'https://github.com/johalputt/VayuPress/blob/main/docs/OPERATIONS.md' },
        { label:'API & compatibility', href:'https://github.com/johalputt/VayuPress/blob/main/docs/compatibility/vcb.md' },
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

    /* Magnetic buttons — the element drifts toward the cursor, springs back. */
    magnet(e) {
      if (window.matchMedia('(pointer: coarse)').matches) return;
      const el = e.currentTarget;
      const r  = el.getBoundingClientRect();
      const mx = e.clientX - (r.left + r.width / 2);
      const my = e.clientY - (r.top + r.height / 2);
      el.style.transform = `translate(${mx * 0.22}px, ${my * 0.34}px)`;
    },
    demagnet(e) { e.currentTarget.style.transform = ''; },

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

    /* product tabs — select and retrigger the panel's entrance animation */
    setProduct(i) {
      if (i === this.activeProduct) return;
      this.activeProduct = i;
      this.prodAnim++;               // key change re-runs the x-transition
    },
    get product() { return this.products[this.activeProduct]; },

    /* feature-detail modal */
    openFeature(i) { this.feature = i; document.body.style.overflow = 'hidden'; },
    closeFeature() { this.feature = null; document.body.style.overflow = ''; },
    nextFeature()  { if (this.feature !== null) this.feature = (this.feature + 1) % this.features.length; },
    prevFeature()  { if (this.feature !== null) this.feature = (this.feature - 1 + this.features.length) % this.features.length; },

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

    async fetchVersion() {
      // Same two-stage, always-live pattern as the star count: a same-origin
      // version.json baked by the deploy workflow first (instant, never
      // rate-limited), then the live GitHub releases API to refresh it in the
      // browser. Falls back silently to whichever value it already has.
      const setVer = (t) => {
        if (typeof t === 'string' && /^v?\d/.test(t)) this.version = t.charAt(0) === 'v' ? t : 'v' + t;
      };
      try {
        const b = await fetch('assets/version.json', { cache: 'no-cache' });
        if (b.ok) { const j = await b.json(); setVer(j.tag_name || j.version); }
      } catch (_) { /* baked file absent in dev — fall through to the API */ }
      try {
        const r = await fetch('https://api.github.com/repos/johalputt/VayuPress/releases/latest', { cache: 'default' });
        if (!r.ok) return;
        const d = await r.json();
        setVer(d.tag_name);
      } catch (_) { /* offline / rate-limited — keep the baked value */ }
    },

    async fetchStars() {
      // Two-stage, always-live star count:
      //  1) a same-origin stars.json baked fresh by the deploy workflow (and a
      //     daily schedule) — instant, never rate-limited, works even if the
      //     visitor's network blocks api.github.com;
      //  2) the live GitHub API with default caching (honours GitHub's own
      //     ~60s Cache-Control, so it revalidates instead of pinning a stale
      //     copy the way force-cache did) — refreshes the baked value in-browser.
      const setStars = (n) => {
        if (typeof n === 'number' && n >= 0) this.stars = n.toLocaleString();
      };
      try {
        const b = await fetch('assets/stars.json', { cache: 'no-cache' });
        if (b.ok) { const j = await b.json(); setStars(j.stargazers_count); }
      } catch (_) { /* baked file absent in dev — fall through to the API */ }
      try {
        const r = await fetch('https://api.github.com/repos/johalputt/VayuPress', { cache: 'default' });
        if (!r.ok) return;
        const d = await r.json();
        setStars(d.stargazers_count);
      } catch (_) { /* offline / rate-limited — keep the baked value */ }
    },

    init() {
      /* scroll listener */
      addEventListener('scroll', () => this.onScroll(), { passive: true });

      /* fetch star count + latest release version (both live) */
      this.fetchStars();
      this.fetchVersion();

      /* hero terminal boot */
      let i = 1;
      const tick = () => { if (i <= 9) { this.t = i++; setTimeout(tick, i < 4 ? 540 : 370); } };
      setTimeout(tick, 750);

      /* rotating hero subject */
      setInterval(() => { this.rot = (this.rot + 1) % this.rotWords.length; }, 2400);

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
        if (this.lightbox !== null) {
          if (e.key === 'Escape')     this.lightbox = null;
          if (e.key === 'ArrowLeft')  this.lightbox = (this.lightbox - 1 + this.screenshots.length) % this.screenshots.length;
          if (e.key === 'ArrowRight') this.lightbox = (this.lightbox + 1) % this.screenshots.length;
          return;
        }
        /* feature-detail modal nav */
        if (this.feature !== null) {
          if (e.key === 'Escape')     this.closeFeature();
          if (e.key === 'ArrowLeft')  this.prevFeature();
          if (e.key === 'ArrowRight') this.nextFeature();
        }
      });
    },
  };
}
