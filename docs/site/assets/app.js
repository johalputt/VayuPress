'use strict';

/* ═══════════════ Wind particle field (Vayu) ═══════════════
   Slow, drifting teal/saffron streaks evoking air across a cosmic field.
   Capped count, DPR-aware, pauses when tab hidden, disabled for reduced-motion. */
(function wind() {
  const reduce = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  const canvas = document.getElementById('wind');
  if (!canvas || reduce) return;
  const ctx = canvas.getContext('2d', { alpha: true });
  let w, h, particles, raf, running = true;

  function resize() {
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    w = canvas.width = innerWidth * dpr;
    h = canvas.height = innerHeight * dpr;
    canvas.style.width = innerWidth + 'px';
    canvas.style.height = innerHeight + 'px';
    const count = Math.min(64, Math.floor(innerWidth / 24));
    particles = Array.from({ length: count }, () => spawn(dpr));
  }
  function spawn(dpr) {
    const teal = Math.random() > 0.32;
    return {
      x: Math.random() * w, y: Math.random() * h,
      len: (40 + Math.random() * 130) * dpr,
      vx: (0.35 + Math.random() * 1.1) * dpr,
      vy: (Math.random() - 0.5) * 0.22 * dpr,
      a: 0.04 + Math.random() * 0.10,
      c: teal ? '13,148,136' : '245,158,11',
    };
  }
  function frame() {
    if (!running) return;
    ctx.clearRect(0, 0, w, h);
    for (const p of particles) {
      const g = ctx.createLinearGradient(p.x, p.y, p.x - p.len, p.y);
      g.addColorStop(0, `rgba(${p.c},${p.a})`);
      g.addColorStop(1, `rgba(${p.c},0)`);
      ctx.strokeStyle = g; ctx.lineWidth = Math.min(window.devicePixelRatio || 1, 2);
      ctx.beginPath(); ctx.moveTo(p.x, p.y); ctx.lineTo(p.x - p.len, p.y); ctx.stroke();
      p.x += p.vx; p.y += p.vy;
      if (p.x - p.len > w) { p.x = -p.len * 0.2; p.y = Math.random() * h; }
    }
    raf = requestAnimationFrame(frame);
  }
  addEventListener('resize', resize, { passive: true });
  document.addEventListener('visibilitychange', () => {
    running = !document.hidden;
    if (running) { cancelAnimationFrame(raf); frame(); }
  });
  resize(); frame();
})();

/* ═══════════════ Cursor aura (desktop) ═══════════════ */
(function aura() {
  if (window.matchMedia('(pointer: coarse)').matches) return;
  const el = document.getElementById('aura');
  if (!el) return;
  let tx = innerWidth / 2, ty = innerHeight / 2, cx = tx, cy = ty;
  addEventListener('pointermove', (e) => { tx = e.clientX; ty = e.clientY; }, { passive: true });
  (function loop() {
    cx += (tx - cx) * 0.12; cy += (ty - cy) * 0.12;
    el.style.transform = `translate(${cx - 230}px, ${cy - 230}px)`;
    requestAnimationFrame(loop);
  })();
})();

/* ═══════════════ Magnetic hover for buttons/links ═══════════════ */
(function magnetic() {
  if (window.matchMedia('(pointer: coarse)').matches) return;
  addEventListener('load', () => {
    document.querySelectorAll('[data-magnetic]').forEach((el) => {
      el.addEventListener('pointermove', (e) => {
        const r = el.getBoundingClientRect();
        const x = e.clientX - r.left - r.width / 2;
        const y = e.clientY - r.top - r.height / 2;
        el.style.transform = `translate(${x * 0.22}px, ${y * 0.28}px)`;
      });
      el.addEventListener('pointerleave', () => { el.style.transform = ''; });
    });
  });
})();

/* ═══════════════ Scroll reveal ═══════════════ */
(function reveal() {
  if (!('IntersectionObserver' in window)) {
    document.querySelectorAll('[data-reveal]').forEach((el) => el.classList.add('revealed'));
    return;
  }
  const obs = new IntersectionObserver((entries) => {
    entries.forEach((e) => { if (e.isIntersecting) { e.target.classList.add('revealed'); obs.unobserve(e.target); } });
  }, { threshold: 0.12, rootMargin: '0px 0px -8% 0px' });
  addEventListener('DOMContentLoaded', () => document.querySelectorAll('[data-reveal]').forEach((el) => obs.observe(el)));
})();

/* ═══════════════ Alpine root ═══════════════ */
function app() {
  return {
    scrolled: false,
    t: 0,
    lightbox: null,
    typed: '',
    copied: false,
    stars: '★',

    deployScript:
`git clone https://github.com/johalputt/vayupress && cd vayupress
CGO_ENABLED=1 go build -o vayupress ./cmd/vayupress
STATIC_DIR=./static VAYU_DOCS_DIR=./docs ./vayupress --port 8080`,

    features: [
      { icon: '⚡', iconBg: 'bg-teal-900/50 border border-teal-800/60', orb: 'rgba(13,148,136,0.4)',
        title: 'Adaptive governance runtime',
        desc: 'Six system modes on a validated transition graph, severity-classified error budgets, append-only mode journal, and the gated budget actuator (Ω12) for opt-in automatic escalation.',
        tags: ['mode-graph', 'budgets', 'Ω12'] },
      { icon: '🗄️', iconBg: 'bg-sky-900/50 border border-sky-800/60', orb: 'rgba(56,189,248,0.35)',
        title: 'Durable by design',
        desc: 'Append-only SQLite write queue with retry, dead-letter and replay. Transactional outbox relay, WAL with adaptive checkpointing, migration checksum drift verification.',
        tags: ['SQLite', 'WAL', 'outbox'] },
      { icon: '🔭', iconBg: 'bg-violet-900/50 border border-violet-800/60', orb: 'rgba(167,139,250,0.35)',
        title: 'Observable end to end',
        desc: 'Structured health contracts, distributed tracing with correlation/causation IDs, Prometheus metrics, and a unified operational timeline in the console.',
        tags: ['tracing', 'metrics', 'health'] },
      { icon: '🎨', iconBg: 'bg-pink-900/50 border border-pink-800/60', orb: 'rgba(244,114,182,0.32)',
        title: 'Operator theme console',
        desc: 'Identity, light/dark palette, custom CSS, declarative head/SEO, favicon & logo upload (magic-number validated), portable JSON export/import, one-click reset.',
        tags: ['branding', 'favicon', 'export'] },
      { icon: '🔌', iconBg: 'bg-orange-900/50 border border-orange-800/60', orb: 'rgba(251,146,60,0.32)',
        title: 'Sandboxed plugins',
        desc: 'Subprocess plugins under a capability model — filesystem, network and write allowlists. Five worked examples including trace-tap and seo-stamp.',
        tags: ['sandbox', 'capabilities'] },
      { icon: '🌐', iconBg: 'bg-emerald-900/50 border border-emerald-800/60', orb: 'rgba(52,211,153,0.32)',
        title: 'Federation substrate',
        desc: 'Minimal ActivityPub server with HTTP-signature verification and durable, atomic inbox replay protection against hostile or retrying peers.',
        tags: ['ActivityPub', 'replay'] },
    ],

    screenshots: [
      { label: 'Homepage',         path: '/',                       src: 'screenshots/homepage.png',         caption: 'Public homepage — clean, fast, no third-party scripts.' },
      { label: 'Admin dashboard',  path: '/admin',                  src: 'screenshots/admin-dashboard.png',  caption: 'Operator dashboard — runtime health, mode status and quick actions.' },
      { label: 'Theme console',    path: '/admin/theme',            src: 'screenshots/admin-panel.png',      caption: 'Theme console — identity, palette, favicon upload, export/import and reset.' },
      { label: 'Policy modes',     path: '/admin/policy/modes',     src: 'screenshots/policy-modes.png',     caption: 'Six modes: normal → degraded → read-only → quarantined → recovery → maintenance.' },
      { label: 'Policy inspector', path: '/admin/policy/inspector', src: 'screenshots/policy-inspector.png', caption: 'Live error budgets with severity classification and actuation status.' },
      { label: 'Runtime topology', path: '/admin/runtime/topology', src: 'screenshots/runtime-topology.png', caption: 'Subsystem graph with health and dependency edges.' },
      { label: 'Replay explorer',  path: '/admin/replay',           src: 'screenshots/replay-explorer.png',  caption: 'Inspect and re-drive dead-letter activities from the SQLite outbox.' },
      { label: 'ADR registry',     path: '/admin/adr',              src: 'screenshots/adr-registry.png',     caption: 'All 63 architecture decision records, browsable in-console.' },
    ],

    principles: [
      { title: 'Single-tenant by default', body: 'One operator, one VPS, one SQLite database. No multi-tenant complexity, no shared infrastructure. Your data never leaves your machine.' },
      { title: 'Operations as first-class surfaces', body: 'Modes, budgets, faults, traces and ADRs are observable, governable entities — not log lines buried in a sidecar. Every decision is auditable.' },
      { title: 'No invisible dependencies', body: 'Zero third-party fonts on your readers. Zero analytics. Zero CDN trackers. The only external calls are ones you explicitly configure.' },
      { title: 'Decisions have records', body: '63 ADRs document every significant choice — from WAL checkpointing to the inbox replay protocol. The codebase ships with its own archaeology.' },
    ],

    steps: [
      { label: 'Clone the repository',            cmd: 'git clone …/vayupress && cd vayupress' },
      { label: 'Build the binary (CGO + SQLite)', cmd: 'CGO_ENABLED=1 go build ./cmd/vayupress' },
      { label: 'Run the test suite',              cmd: 'CGO_ENABLED=1 go test ./...' },
      { label: 'Start the server',                cmd: 'STATIC_DIR=./static ./vayupress --port 8080' },
    ],

    footer: [
      { head: 'Project', links: [
        { label: 'GitHub',    href: 'https://github.com/johalputt/VayuPress' },
        { label: 'Changelog', href: 'https://github.com/johalputt/VayuPress/blob/main/CHANGELOG.md' },
        { label: 'Releases',  href: 'https://github.com/johalputt/VayuPress/releases' },
      ]},
      { head: 'Docs', links: [
        { label: 'Installation', href: 'https://github.com/johalputt/VayuPress/blob/main/docs/INSTALLATION.md' },
        { label: 'Architecture', href: 'https://github.com/johalputt/VayuPress/blob/main/docs/ARCHITECTURE.md' },
        { label: 'Operations',   href: 'https://github.com/johalputt/VayuPress/blob/main/docs/OPERATIONS.md' },
      ]},
      { head: 'Decisions', links: [
        { label: 'ADR registry', href: 'https://github.com/johalputt/VayuPress/tree/main/docs/adr' },
        { label: 'Threat model', href: 'https://github.com/johalputt/VayuPress/blob/main/docs/THREAT-MODEL.md' },
        { label: 'Plugins',      href: 'https://github.com/johalputt/VayuPress/tree/main/docs/plugins' },
      ]},
    ],

    // duplicated list for the seamless infinite marquee
    get galleryLoop() {
      const tagged = this.screenshots.map((s, idx) => ({ ...s, idx }));
      return [...tagged, ...tagged];
    },

    onScroll() { this.scrolled = scrollY > 24; },

    tilt(e) {
      const el = e.currentTarget;
      const r = el.getBoundingClientRect();
      const px = (e.clientX - r.left) / r.width;
      const py = (e.clientY - r.top) / r.height;
      el.style.setProperty('--mx', `${px * 100}%`);
      el.style.setProperty('--my', `${py * 100}%`);
      el.style.transform = `perspective(900px) rotateX(${(0.5 - py) * 6}deg) rotateY(${(px - 0.5) * 6}deg) translateY(-3px)`;
    },
    untilt(e) { e.currentTarget.style.transform = ''; },

    ripple(e) {
      const btn = e.currentTarget;
      const circle = document.createElement('span');
      const d = Math.max(btn.clientWidth, btn.clientHeight);
      const r = btn.getBoundingClientRect();
      circle.className = 'ripple';
      circle.style.width = circle.style.height = d + 'px';
      circle.style.left = e.clientX - r.left - d / 2 + 'px';
      circle.style.top = e.clientY - r.top - d / 2 + 'px';
      btn.appendChild(circle);
      setTimeout(() => circle.remove(), 700);
    },

    runType() {
      if (this._typing) return;
      this._typing = true;
      const text = this.deployScript;
      let i = 0;
      const tick = () => {
        if (i <= text.length) {
          this.typed = text.slice(0, i++);
          setTimeout(tick, text[i - 1] === '\n' ? 170 : 16 + Math.random() * 28);
        }
      };
      tick();
    },

    copyDeploy() {
      navigator.clipboard?.writeText(this.deployScript).then(() => {
        this.copied = true;
        setTimeout(() => (this.copied = false), 1800);
      });
    },

    async fetchStars() {
      try {
        const res = await fetch('https://api.github.com/repos/johalputt/VayuPress');
        if (!res.ok) return;
        const data = await res.json();
        if (typeof data.stargazers_count === 'number') {
          this.stars = data.stargazers_count.toLocaleString();
        }
      } catch (_) { /* offline / rate-limited — keep the star glyph */ }
    },

    init() {
      addEventListener('scroll', () => this.onScroll(), { passive: true });
      this.fetchStars();

      // hero terminal boot sequence
      let i = 1;
      const tick = () => { if (i <= 9) { this.t = i++; setTimeout(tick, i < 4 ? 520 : 360); } };
      setTimeout(tick, 700);

      // type deploy script once the quick-start terminal enters view
      this.$nextTick(() => {
        const term = this.$refs.deployTerm;
        if (!term || !('IntersectionObserver' in window)) { this.runType(); return; }
        const obs = new IntersectionObserver((entries) => {
          entries.forEach((e) => { if (e.isIntersecting) { this.runType(); obs.disconnect(); } });
        }, { threshold: 0.3 });
        obs.observe(term);
      });
    },
  };
}
