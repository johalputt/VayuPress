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
      '63 architecture decisions',
      'MIT licensed',
      'ActivityPub federation',
    ],

    features: [
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
    ],

    screenshots: [
      { label:'Homepage',         path:'/',                       src:'screenshots/homepage.png',         caption:'Public homepage — clean, fast, no third-party scripts.' },
      { label:'Admin dashboard',  path:'/admin',                  src:'screenshots/admin-dashboard.png',  caption:'Operator dashboard — runtime health, mode status and quick actions.' },
      { label:'Theme console',    path:'/admin/theme',            src:'screenshots/admin-panel.png',      caption:'Theme console — identity, palette, favicon upload, export/import and reset.' },
      { label:'Policy modes',     path:'/admin/policy/modes',     src:'screenshots/policy-modes.png',     caption:'Six modes: normal → degraded → read-only → quarantined → recovery → maintenance.' },
      { label:'Policy inspector', path:'/admin/policy/inspector', src:'screenshots/policy-inspector.png', caption:'Live error budgets with severity classification and actuation status.' },
      { label:'Runtime topology', path:'/admin/runtime/topology', src:'screenshots/runtime-topology.png', caption:'Subsystem graph with health and dependency edges.' },
      { label:'Replay explorer',  path:'/admin/replay',           src:'screenshots/replay-explorer.png',  caption:'Inspect and re-drive dead-letter activities from the SQLite outbox.' },
      { label:'ADR registry',     path:'/admin/adr',              src:'screenshots/adr-registry.png',     caption:'All 63 architecture decision records, browsable in-console.' },
    ],

    principles: [
      { title:'Single-tenant by default',          body:'One operator, one VPS, one SQLite database. No multi-tenant complexity, no shared infrastructure. Your data never leaves your machine.' },
      { title:'Operations as first-class surfaces', body:'Modes, budgets, faults, traces and ADRs are observable, governable entities — not log lines buried in a sidecar. Every decision is auditable.' },
      { title:'No invisible dependencies',          body:'Zero third-party fonts on your readers. Zero analytics. Zero CDN trackers. The only external calls are ones you explicitly configure.' },
      { title:'Decisions have records',             body:'63 ADRs document every significant choice — from WAL checkpointing to the inbox replay protocol. The codebase ships with its own archaeology.' },
    ],

    steps: [
      { label:'Clone the repository',              cmd:'git clone github.com/johalputt/vayupress' },
      { label:'Build the binary (CGO + SQLite)',   cmd:'CGO_ENABLED=1 go build ./cmd/vayupress' },
      { label:'Run the test suite',                cmd:'CGO_ENABLED=1 go test ./...' },
      { label:'Start the server',                  cmd:'STATIC_DIR=./static ./vayupress --port 8080' },
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
