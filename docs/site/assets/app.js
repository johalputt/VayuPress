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
    activeProduct: 0,       // selected product tab
    prodAnim:   0,          // bump on tab change to retrigger panel animation
    rot:        0,          // rotating hero subject index
    world:      'clearnet', // 'clearnet' | 'tor' — the whole page repaints per world
    heroKey:    0,          // bump on world switch to replay hero entrance
    typed:      '',
    copied:     false,
    stars:      '★',
    version:    '',
    _typing:    false,

    deployScript:
`git clone https://github.com/johalputt/vayupress && cd vayupress
CGO_ENABLED=1 go build -o vayupress ./cmd/vayupress
STATIC_DIR=./static VAYU_DOCS_DIR=./docs ./vayupress --port 8080`,

    /* ── Two worlds, one binary (ADR-0141 · VayuOS Spaces) ──
       The hero toggle flips `world`; every world-bound surface — headline,
       rotating subject, lead copy and the boot terminal — swaps to match, and
       the whole page repaints via [data-world] on <body>. Content only; no
       screenshotting in the sandbox, so this stays structurally verifiable. */
    worlds: {
      clearnet: {
        label:  'Clearnet',
        icon:   '🌐',
        kicker: 'The public web — fast, encrypted, and entirely yours.',
        title:  ['Your website, blog,', 'mail &amp; chat —', 'one sovereign binary.'],
        lead:
          'Point a domain at one VPS, run one command, and own your whole online presence — ' +
          '<span class="hl">website</span>, <span class="hl">blog</span>, ' +
          '<span class="hl">PGP mail</span>, <span class="hl">encrypted chat</span>, ' +
          'cookieless analytics and one control panel. Publish every site to a ' +
          '<span class="hl">Tor .onion</span>, connect <span class="hl">Claude</span> in one click, ' +
          'and <span class="hl">get paid</span> through your own Stripe &amp; PayPal. ' +
          'SQLite, zero telemetry, nothing rented.',
        cta:    'Deploy in one command',
        rotWords: [
          'a business website.',
          'a Ghost-class blog.',
          'a PGP mail server.',
          'encrypted VayuTalk chat.',
          'paid memberships &amp; ads.',
          'a one-click MCP connector.',
          'cookieless analytics.',
          'your whole stack.',
        ],
        termDone: 'VayuOS online · your whole stack is live',
        term: [
          { cls:'',                 mark:'❯', markCls:'text-teal-400', body:' deploy-vayupress.sh · yourdomain.com', host:'', hostCls:'', tail:'' },
          { cls:'text-gray-600',    mark:'',  markCls:'', body:"Provisioning · Let's Encrypt × 3 …", host:'', hostCls:'', tail:'' },
          { cls:'text-emerald-400', mark:'✓', markCls:'', body:' Website · ',    host:'yourdomain.com',       hostCls:'text-teal-300', tail:'' },
          { cls:'text-emerald-400', mark:'✓', markCls:'', body:' Blog · ',       host:'blog.yourdomain.com',  hostCls:'text-teal-300', tail:'' },
          { cls:'text-emerald-400', mark:'✓', markCls:'', body:' VayuMail · ',   host:'mail.yourdomain.com',  hostCls:'text-teal-300', tail:' · DKIM + IMAP' },
          { cls:'text-emerald-400', mark:'✓', markCls:'', body:' VayuTalk · ',   host:'talk.yourdomain.com',  hostCls:'text-teal-300', tail:' · E2E chat relay' },
          { cls:'text-emerald-400', mark:'✓', markCls:'', body:' VayuShield + VayuAnalytics · bot protection · cookieless', host:'', hostCls:'', tail:'' },
          { cls:'text-emerald-400', mark:'✓', markCls:'', body:' VayuTor · ',    host:'every domain now on .onion', hostCls:'text-purple-300', tail:' · one click' },
          { cls:'text-emerald-400', mark:'✓', markCls:'', body:' VayuMCP · ',    host:'yourdomain.com/mcp',  hostCls:'text-indigo-300', tail:' · connect Claude' },
          { cls:'text-emerald-400', mark:'✓', markCls:'', body:' Monetization · Stripe + PayPal · ', host:'your keys, your revenue', hostCls:'text-saffron-300', tail:'' },
        ],
      },
      tor: {
        label:  'Tor',
        icon:   '🧅',
        kicker: 'The anonymous web — no IP, no trace, and still yours.',
        title:  ['Your onion site, webmail', '&amp; anonymous chat —', 'one sovereign binary.'],
        lead:
          'Flip one switch and run a <span class="hl">fully separate Tor world</span> — a site on its own ' +
          '<span class="hl">v3 .onion</span>, <span class="hl">webmail over Tor</span>, and an ' +
          '<span class="hl">anonymous, rotatable VayuTalk</span> that delivers ' +
          '<span class="hl">onion-to-onion</span>. Anti-leak by construction: no clearnet callbacks, ' +
          'no CA-TLS, no cookies that follow you. Its own database, its own identity — ' +
          '<span class="hl">nothing crosses back</span>. SQLite, zero telemetry, nothing rented.',
        cta:    'Enter the Tor world',
        rotWords: [
          'a v3 .onion for every site.',
          'webmail over Tor.',
          'anonymous VayuTalk chat.',
          'onion-to-onion delivery.',
          'obfs4 bridges to beat blocks.',
          'a world that never leaks.',
          'your anonymous stack.',
        ],
        termDone: 'Tor world online · anonymous, and it never leaks',
        term: [
          { cls:'',                 mark:'❯', markCls:'text-purple-300', body:' VAYUOS_MODE=tor deploy-vayupress.sh', host:'', hostCls:'', tail:'' },
          { cls:'text-gray-600',    mark:'',  markCls:'', body:'Starting own tor · publishing v3 onions …', host:'', hostCls:'', tail:'' },
          { cls:'text-emerald-400', mark:'✓', markCls:'', body:' Onion site · ',  host:'vayp7xk2n4qy6zj3…onion', hostCls:'text-purple-300', tail:'' },
          { cls:'text-emerald-400', mark:'✓', markCls:'', body:' VayuMail·Tor · webmail only · ', host:'no clearnet MX', hostCls:'text-purple-300', tail:'' },
          { cls:'text-emerald-400', mark:'✓', markCls:'', body:' VayuTalk·Tor · ', host:'anonymous rotatable ID', hostCls:'text-purple-300', tail:'' },
          { cls:'text-emerald-400', mark:'✓', markCls:'', body:' Onion-to-onion delivery · ', host:'over-Tor key fetch', hostCls:'text-purple-300', tail:' · signed' },
          { cls:'text-emerald-400', mark:'✓', markCls:'', body:' Anti-leak · no callbacks · no CA-TLS · no Secure cookie', host:'', hostCls:'', tail:'' },
          { cls:'text-emerald-400', mark:'✓', markCls:'', body:' obfs4 bridges armed · ', host:'routes around blocks', hostCls:'text-purple-300', tail:'' },
          { cls:'text-emerald-400', mark:'✓', markCls:'', body:' Separate database · ', host:'nothing crosses to clearnet', hostCls:'text-purple-300', tail:'' },
        ],
      },
    },

    // Hero pillar chips — the whole platform at a glance.
    pillars: [
      'Website', 'Blog', 'PGP Mail', 'VayuTalk', 'Mobile App', 'Analytics', 'VayuOS',
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
        blurb:'SMTP send + receive, IMAP and POP3, RFC-6376 DKIM signing and direct-to-MX delivery — a real mail server for your domain, with automatic MX / SPF / DKIM / DMARC records and live DNS health checks. Mail never leaves your server unencrypted to a third party. And running your own mail does not mean one forgotten password loses the mailbox: recovery codes, a verified off-server recovery address, a still-syncing phone, or an administrator-approved one-time link all get a holder back in — and every reset revokes the app passwords and sessions an intruder would have kept.',
        points:[
          'Send & receive from your own domain',
          'DKIM-signed, direct-to-MX with STARTTLS',
          'IMAP + POP3 for any client (Thunderbird, K-9…)',
          'Automatic MX / SPF / DKIM / DMARC + DNS health',
          'Native PGP: keys per account, published via WKD',
          'Official Android app — enter your domain, sign in once',
          'Five recovery paths — a forgotten password is not a dead end',
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
        tag:'Self-learning bot shield — application-layer defence, self-hosted',
        host:'/os/shield',
        blurb:'A five-layer, self-learning bot shield built into the same binary. It keeps the console responsive while the public site is under application-layer load, jails offenders in minutes and forgives automatically — search engines and AI assistants are always welcome. It does not absorb a volumetric flood: that is decided by your provider'+String.fromCharCode(39)+'s network, upstream of any software on the host.',
        points:[
          'Admin-sovereignty lane keeps the console responsive under load',
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

      { key:'vayuapi', name:'VayuAPI + VCB', icon:'🔌', rgb:'250,204,21', badge:'',
        tag:'A fine-grained API + an enforceable extension contract',
        host:'/os/apikeys',
        blurb:'A first-class REST API with per-key, per-section scopes, per-key rate limits and a WORM audit trail — paired with the Vayu Compatibility Bible (VCB), a machine-checked contract every theme, plugin and extension must satisfy, gated in CI. Build on VayuPress without ever getting a surprise break.',
        points:[
          'Per-key scopes: posts, media, members, mail…',
          'Read / write / export actions — least privilege',
          'Per-key rate limits, expiry & a WORM audit log',
          'VCB: hooks, tokens & contracts validated in CI',
          'vayu-compat CLI checks an extension before ship',
          'Golden-file contracts — no silent breaking changes',
        ],
        cta:{ label:'Read the VCB →', href:'https://github.com/johalputt/VayuPress/blob/main/docs/adr/ADR-0135-vayu-compatibility-bible.md' } },

      { key:'vayumcp', name:'VayuMCP', icon:'🔗', rgb:'129,140,248', badge:'NEW',
        tag:'Connect any AI — one click, full control',
        host:'yourdomain.com/mcp',
        blurb:'A built-in Model Context Protocol server and OAuth 2.1 authorization server let an AI client connect to your site in one click and run it by chat. Claude and Claude Code connect straight from claude.ai — but that is just one client; any MCP client works the same way, including the agents in a Buzz workspace. No API key to copy, no plugin to install: approve once and it can run your site, scoped to exactly what you allow.',
        points:[
          'One-click Connect on claude.ai — OAuth 2.1 + PKCE',
          'Standard MCP — Claude, Claude Code or any MCP client',
          'Guided setup pages for Claude Code and for Buzz agents',
          'Built-in server at /mcp — no extra service',
          'Read analytics · create, edit & publish posts and pages',
          'Update settings & switch themes just by asking',
          'Scoped, revocable access — no bearer token stored at rest',
        ],
        cta:{ label:'How VayuMCP works →', href:'https://github.com/johalputt/VayuPress/blob/main/docs/compatibility/mcp.md' } },

      { key:'vayutor', name:'VayuTor', icon:'🧅', rgb:'147,112,219', badge:'NEW',
        tag:'One click, every domain gets a Tor .onion',
        host:'*.onion',
        blurb:'Publish every hosted domain as its own Tor v3 onion service, alongside its normal URL — both work at the same time, with no speed or quality trade-off. No ISP, network or observer can see who visits, and the server never learns a visitor’s IP. Activate from VayuOS in a single click.',
        points:[
          'One-click activation — drives Tor’s control port',
          'A stable .onion for every hosted domain',
          'Clearnet + onion serve the same site, together',
          'Onion-Location — Tor Browser discovers it automatically',
          'Count-only stats: no IP, no time, nothing else',
          'Opens no inbound port — zero new attack surface',
        ],
        cta:{ label:'How VayuTor works →', href:'https://github.com/johalputt/VayuPress/blob/main/docs/adr/ADR-0138-vayutor-onion-services.md' } },
    ],

    screenshots: [
      { label:'Homepage',         path:'/',          src:'screenshots/homepage.png',           caption:'Your public homepage — clean, fast, and free of third-party scripts.' },
      { label:'VayuOS dashboard', path:'/os',        src:'screenshots/admin-os-dashboard.png', caption:'VayuOS — one fast, calm admin for everything, with an at-a-glance dashboard.' },
      { label:'Block editor',     path:'/os/editor', src:'screenshots/admin-os-editor.png',    caption:'The block editor — distraction-free writing with a slash menu and live preview.' },
      { label:'Clearnet + Tor',   path:'/os/spaces', src:'screenshots/admin-os-spaces.png',    caption:'Spaces — run an anonymous .onion world beside your public site. Separate databases, so the two can never be linked.' },
      { label:'Monetization',     path:'/os/monetization', src:'screenshots/admin-os-monetization.png', caption:'Cards, PayPal, crypto and direct transfer — funds settle into your own accounts, with no platform cut.' },
      { label:'Bot shield',       path:'/os/shield', src:'screenshots/admin-os-shield.png',    caption:'VayuShield — self-learning bot protection that never blocks a real reader or a search crawler. No Cloudflare needed.' },
      { label:'VayuMail',         path:'/os/vayumail', src:'screenshots/admin-os-vayumail.png', caption:'A real mail server on your domain — SMTP, IMAP, POP3, DKIM, and PGP encryption at rest.' },
      { label:'VayuTalk',         path:'/os/talk',   src:'screenshots/admin-os-vayutalk.png',  caption:'End-to-end encrypted chat, hosted on your own domain.' },
      { label:'VayuMCP',          path:'/os/connector', src:'screenshots/admin-os-connector.png', caption:'A built-in MCP server and OAuth 2.1 provider — connect Claude, or any MCP client, in one click.' },
      { label:'Theme Studio',     path:'/os/theme',  src:'screenshots/admin-os-theme.png',     caption:'Theme Studio — restyle your whole site with a live preview, all self-hosted.' },
      { label:'Members',          path:'/os/members', src:'screenshots/admin-os-members.png',  caption:'Members — tiers, growth, revenue and retention, computed from your own database.' },
      { label:'Post manager',     path:'/os/posts',  src:'screenshots/admin-os-posts.png',     caption:'Post manager — one collapsible card per article, with one-click publish / unpublish.' },
      { label:'Analytics',        path:'/os/analytics', src:'screenshots/admin-os-analytics.png', caption:'Privacy-first analytics — insight for you, no cookies or PII for your readers.' },
      { label:'Website',          path:'/os/website', src:'screenshots/admin-os-website.png',  caption:'Website — pages, navigation and the public shell, all in one place.' },
      { label:'Media library',    path:'/os/media',  src:'screenshots/admin-os-media.png',     caption:'Media library — fast uploads with safe, validated file handling.' },
      { label:'SEO',              path:'/os/seo',    src:'screenshots/admin-os-seo.png',       caption:'Native SEO — sitemap, robots, structured data and per-post readiness.' },
      { label:'Member sign-up',   path:'/signup',    src:'screenshots/member-signup.png',      caption:'Reader sign-up — branded and passwordless; an email gets a one-time link.' },
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

    /* Measured on a live install (johal.in), not a staged benchmark. These are
       the numbers behind the table: anyone can re-run them against the site. */
    compareProof: [
      { n:'234,477', l:'posts in one SQLite file', s:'on a single small VPS' },
      { n:'156 ms',  l:'time to first byte',       s:'measured from Iowa' },
      { n:'100',     l:'PageSpeed, desktop',       s:'all four categories, every run' },
      { n:'13%',     l:'of the machine’s RAM',     s:'while serving all of it' },
    ],

    compareGroups: [
      { g:'Sovereignty & your data', rows:[
        { f:'Single self-contained binary',              v:['yes','no','no','n/a'] },
        { f:'Your data in your own SQLite file',         v:['yes','partial','partial','no'] },
        { f:'Apache-2.0, self-hostable, no SaaS lock-in',v:['yes','yes','yes','no'] },
        { f:'Plain export, no proprietary format',       v:['yes','partial','yes','partial'] },
      ]},
      { g:'Built in — not a plugin, not a tier', rows:[
        { f:'Website + blog + mail on one domain',       v:['yes','partial','no','no'] },
        { f:'Native mail server (SMTP/IMAP/POP3, DKIM)', v:['yes','no','no','no'] },
        { f:'Official mobile mail app',                  v:['yes','no','no','no'] },
        { f:'End-to-end PGP encryption + WKD discovery', v:['yes','no','no','no'] },
        { f:'Ephemeral end-to-end encrypted chat',       v:['yes','no','no','no'] },
        { f:'One-click Tor .onion for every domain',     v:['yes','no','no','no'] },
        { f:'MCP server for AI clients & agents',        v:['yes','no','no','no'] },
        { f:'Bot shield at your own origin, no add-on',  v:['yes','plugin','no','n/a'] },
        { f:'Cookieless analytics — no external GeoIP',  v:['yes','plugin','partial','partial'] },
        { f:'Memberships & paywalls, no SDK lock-in',    v:['yes','plugin','yes','hosted-only'] },
      ]},
      { g:'Running it in production', rows:[
        { f:'Zero reader-side trackers or cookies',      v:['yes','no','partial','no'] },
        { f:'Signed releases + one-click self-update',   v:['yes','partial','partial','n/a'] },
        { f:'Encrypted backups with automatic restore drills', v:['yes','plugin','partial','n/a'] },
        { f:'Services to run alongside it',              v:['none','several','several','n/a'] },
      ]},
    ],

    /* Legend for the cell vocabulary above — rendered under the table so the
       shorthand is never left to inference. */
    compareLegend: [
      { k:'yes',         t:'Built in and on by default' },
      { k:'partial',     t:'Possible, but not out of the box' },
      { k:'plugin',      t:'Needs a third-party extension' },
      { k:'hosted-only', t:'Only on the vendor’s hosted tier' },
      { k:'no',          t:'Not available' },
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
        { label:'Sponsor ♥', href:'https://github.com/sponsors/johalputt' },
        { label:'About the developer', href:'about.html' },
        { label:'VayuWeb',      href:'vayuweb.html' },
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
        { label:'Agency hosting (ADR-0152)', href:'https://github.com/johalputt/VayuPress/blob/main/docs/adr/ADR-0152-agency-hosting.md' },
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

    /* ── World switch (Clearnet ⇄ Tor) ── */
    get w() { return this.worlds[this.world]; },
    setWorld(name) {
      if (name === this.world || !this.worlds[name]) return;
      this.world = name;
      this.rot = 0;                  // restart the rotating subject cleanly
      this.heroKey++;                // re-key hero blocks so entrances replay
      this.bootTerminal();           // re-run the boot log for the new world
    },
    // Boot the hero terminal: reveal one line at a time, then the "online" line.
    bootTerminal() {
      this.t = 0;
      const n = this.w.term.length + 1; // +1 for the trailing "…online" summary
      let i = 1;
      const tick = () => { if (i <= n) { this.t = i++; setTimeout(tick, i < 4 ? 480 : 300); } };
      setTimeout(tick, 320);
    },

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

      /* hero terminal boot (world-aware) */
      this.bootTerminal();

      /* rotating hero subject — indexes into the active world's list */
      setInterval(() => { this.rot = (this.rot + 1) % this.w.rotWords.length; }, 2400);

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
      });
    },
  };
}
