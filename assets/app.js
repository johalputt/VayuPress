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
    feature:    null,       // open feature-detail index
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
      'own your content · mail · PGP',
      'native mail + E2E PGP',
      'Apache-2.0 licensed',
    ],

    features: [
      { icon:'✍️', iconBg:'bg-teal-900/60 border border-teal-800/60',   orb:'rgba(45,212,191,0.55)',
        title:'A writing studio you\'ll love',
        desc:'A calm, Ghost-clean block editor with tables, toggles, task lists, math, callouts, code, self-hosted audio and privacy-first video. Write in blocks, switch to whole-document Markdown or raw HTML and back — losslessly — and drop, paste or link any image (Unsplash, Pixabay, anywhere) straight in. Drag to reorder, undo anything, and watch live word-count as you type; a slash menu, a ⌘K palette, focus mode and split preview keep you in flow.',
        tags:['blocks · Markdown · HTML','image drag/drop/link','slash menu · ⌘K'] },
      { icon:'✉️', iconBg:'bg-sky-900/60 border border-sky-800/60',     orb:'rgba(56,189,248,0.5)',
        title:'VayuMail — your own mail server',
        desc:'Send and receive from your own domain without renting a mail provider. Outbound mail is DKIM-signed and delivered straight to the recipient\'s server; an inbound SMTP receiver and IMAP inbox let Thunderbird, K-9 and Apple Mail read your mail. Connect a phone in one scan with a rotating setup QR — it carries a per-device app password you can revoke any time, never your real password. It even writes your MX, SPF, DKIM and DMARC records and checks the DNS is healthy.',
        tags:['DKIM-signed','rotating setup QR','IMAP inbox'] },
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
      { icon:'📦', iconBg:'bg-amber-900/60 border border-amber-800/60',   orb:'rgba(245,158,11,0.4)',
        title:'Update & back up in one click',
        desc:'Install the latest signed release from inside VayuOS — the download is checksum- and signature-verified, your database is backed up first, and the binary swaps in atomically. Export your entire site — database, settings, media, mailboxes and PGP keys — as one file encrypted with AES-256-GCM under a passphrase only you hold: a stolen backup is useless and tamper-evident with any tool, yet restores anywhere in seconds. No shell required, fully reversible.',
        tags:['signed updates','encrypted backups','reversible'] },
      { icon:'🏪', iconBg:'bg-orange-900/60 border border-orange-800/60', orb:'rgba(251,146,60,0.45)',
        title:'A business website, not just a blog',
        desc:'Need a real website for a restaurant, shop, studio, school, clinic or portfolio? Pick from eleven elegant, modern-minimalist templates and deploy a full site — hero, offerings with prices, gallery, hours and contact — editing every word from VayuOS with a live preview. Your domain shows the website, the blog moves to blog.yourdomain.com and mail to mail.yourdomain.com; the installer gets Let\'s Encrypt certificates for all of them automatically. It\'s your choice, and an update never changes it.',
        tags:['11 templates','website + blog + mail','edit from VayuOS'] },
      { icon:'🔁', iconBg:'bg-indigo-900/60 border border-indigo-800/60', orb:'rgba(129,140,248,0.4)',
        title:'Bring your whole archive',
        desc:'Move in from Ghost, WordPress, Substack, Medium, Hugo, Jekyll, Notion or a plain folder of Markdown — slugs, tags, dates, images and drafts preserved. The importers are resumable and gentle enough to migrate a 200,000-post site onto a small VPS without falling over.',
        tags:['Ghost · WP · Substack','Medium · Hugo · Jekyll','resumable'] },
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
      { f:'Your data in your own SQLite file',  v:['yes','partial','partial','no'] },
      { f:'Native mail server built in (DKIM)', v:['yes','no','no','no'] },
      { f:'Inbound IMAP mailbox (read your mail)', v:['yes','no','no','no'] },
      { f:'End-to-end PGP encryption',          v:['yes','no','no','no'] },
      { f:'Zero reader-side trackers / cookies', v:['yes','no','partial','no'] },
      { f:'Privacy-first analytics built in',   v:['yes','plugin','partial','partial'] },
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
