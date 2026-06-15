'use strict';

/* ───────────────────────── Wind particle canvas ─────────────────────────
   "Vayu" means wind. A field of slow, drifting streaks — teal/saffron tinted —
   evoking air moving across the page. Lightweight, capped, reduced-motion aware. */
(function wind() {
  const reduce = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  const canvas = document.getElementById('wind');
  if (!canvas || reduce) return;
  const ctx = canvas.getContext('2d');
  let w, h, particles;

  function resize() {
    w = canvas.width = window.innerWidth * devicePixelRatio;
    h = canvas.height = window.innerHeight * devicePixelRatio;
    canvas.style.width = window.innerWidth + 'px';
    canvas.style.height = window.innerHeight + 'px';
    const count = Math.min(70, Math.floor(window.innerWidth / 22));
    particles = Array.from({ length: count }, spawn);
  }
  function spawn() {
    const teal = Math.random() > 0.35;
    return {
      x: Math.random() * w,
      y: Math.random() * h,
      len: (40 + Math.random() * 120) * devicePixelRatio,
      vx: (0.4 + Math.random() * 1.1) * devicePixelRatio,
      vy: (Math.random() - 0.5) * 0.25 * devicePixelRatio,
      a: 0.04 + Math.random() * 0.10,
      c: teal ? '13,148,136' : '245,158,11',
    };
  }
  function frame() {
    ctx.clearRect(0, 0, w, h);
    for (const p of particles) {
      ctx.beginPath();
      const grad = ctx.createLinearGradient(p.x, p.y, p.x - p.len, p.y);
      grad.addColorStop(0, `rgba(${p.c},${p.a})`);
      grad.addColorStop(1, `rgba(${p.c},0)`);
      ctx.strokeStyle = grad;
      ctx.lineWidth = 1 * devicePixelRatio;
      ctx.moveTo(p.x, p.y);
      ctx.lineTo(p.x - p.len, p.y);
      ctx.stroke();
      p.x += p.vx; p.y += p.vy;
      if (p.x - p.len > w) { p.x = -p.len * 0.2; p.y = Math.random() * h; }
    }
    requestAnimationFrame(frame);
  }
  window.addEventListener('resize', resize);
  resize();
  frame();
})();

/* scroll-reveal observer */
(function reveal() {
  if (!('IntersectionObserver' in window)) {
    document.querySelectorAll('[data-reveal]').forEach((el) => el.classList.add('revealed'));
    return;
  }
  const obs = new IntersectionObserver((entries) => {
    entries.forEach((e) => {
      if (e.isIntersecting) { e.target.classList.add('revealed'); obs.unobserve(e.target); }
    });
  }, { threshold: 0.12 });
  document.querySelectorAll('[data-reveal]').forEach((el) => obs.observe(el));
})();

/* drag-to-scroll on the gallery rail */
(function dragRail() {
  document.addEventListener('alpine:init', () => {});
  window.addEventListener('load', () => {
    const rail = document.querySelector('.gallery-rail');
    if (!rail) return;
    let down = false, startX, scroll;
    rail.addEventListener('pointerdown', (e) => { down = true; startX = e.pageX; scroll = rail.scrollLeft; });
    rail.addEventListener('pointerup', () => { down = false; });
    rail.addEventListener('pointerleave', () => { down = false; });
    rail.addEventListener('pointermove', (e) => {
      if (!down) return;
      rail.scrollLeft = scroll - (e.pageX - startX) * 1.4;
    });
  });
})();

/* ───────────────────────── Alpine root ───────────────────────── */
function app() {
  return {
    scrolled: false,
    t: 0,                 // hero terminal line counter
    lightbox: null,
    typed: '',
    copied: false,

    deployScript:
`git clone https://github.com/johalputt/vayupress && cd vayupress
CGO_ENABLED=1 go build -o vayupress ./cmd/vayupress
STATIC_DIR=./static VAYU_DOCS_DIR=./docs ./vayupress --port 8080`,

    trust: [
      { value: 'v1.0.0', label: 'first stable release' },
      { value: '63',     label: 'architecture decisions' },
      { value: '1 VPS',  label: 'all it takes to run' },
      { value: 'MIT',    label: 'open source, forever' },
    ],

    features: [
      { icon: '⚡', iconBg: 'bg-teal-900/50 border border-teal-800/60',
        title: 'Adaptive governance runtime',
        desc: 'Six system modes on a validated transition graph, severity-classified error budgets, append-only mode journal, and the gated budget actuator (Ω12) for opt-in automatic escalation.',
        tags: ['mode-graph', 'budgets', 'Ω12'] },
      { icon: '🗄️', iconBg: 'bg-sky-900/50 border border-sky-800/60',
        title: 'Durable by design',
        desc: 'Append-only SQLite write queue with retry, dead-letter and replay. Transactional outbox relay, WAL with adaptive checkpointing, migration checksum drift verification.',
        tags: ['SQLite', 'WAL', 'outbox'] },
      { icon: '🔭', iconBg: 'bg-violet-900/50 border border-violet-800/60',
        title: 'Observable end to end',
        desc: 'Structured health contracts, distributed tracing with correlation/causation IDs, Prometheus metrics, and a unified operational timeline in the console.',
        tags: ['tracing', 'metrics', 'health'] },
      { icon: '🎨', iconBg: 'bg-pink-900/50 border border-pink-800/60',
        title: 'Operator theme console',
        desc: 'Identity, light/dark palette, custom CSS, declarative head/SEO, favicon & logo upload (magic-number validated), portable JSON export/import, one-click reset.',
        tags: ['branding', 'favicon', 'export'] },
      { icon: '🔌', iconBg: 'bg-orange-900/50 border border-orange-800/60',
        title: 'Sandboxed plugins',
        desc: 'Subprocess plugins under a capability model — filesystem, network and write allowlists. Five worked examples including trace-tap and seo-stamp.',
        tags: ['sandbox', 'capabilities'] },
      { icon: '🌐', iconBg: 'bg-emerald-900/50 border border-emerald-800/60',
        title: 'Federation substrate',
        desc: 'Minimal ActivityPub server with HTTP-signature verification and durable, atomic inbox replay protection against hostile or retrying peers.',
        tags: ['ActivityPub', 'replay'] },
    ],

    screenshots: [
      { label: 'Homepage',         path: '/',                        src: 'screenshots/homepage.png',         caption: 'Public homepage — clean, fast, no third-party scripts.' },
      { label: 'Admin dashboard',  path: '/admin',                   src: 'screenshots/admin-dashboard.png',  caption: 'Operator dashboard — runtime health, mode status and quick actions.' },
      { label: 'Theme console',    path: '/admin/theme',             src: 'screenshots/admin-panel.png',      caption: 'Theme console — identity, palette, favicon upload, export/import and reset.' },
      { label: 'Policy modes',     path: '/admin/policy/modes',      src: 'screenshots/policy-modes.png',     caption: 'Six modes: normal → degraded → read-only → quarantined → recovery → maintenance.' },
      { label: 'Policy inspector', path: '/admin/policy/inspector',  src: 'screenshots/policy-inspector.png', caption: 'Live error budgets with severity classification and actuation status.' },
      { label: 'Runtime topology', path: '/admin/runtime/topology',  src: 'screenshots/runtime-topology.png', caption: 'Subsystem graph with health and dependency edges.' },
      { label: 'Replay explorer',  path: '/admin/replay',            src: 'screenshots/replay-explorer.png',  caption: 'Inspect and re-drive dead-letter activities from the SQLite outbox.' },
      { label: 'ADR registry',     path: '/admin/adr',               src: 'screenshots/adr-registry.png',     caption: 'All 63 architecture decision records, browsable in-console.' },
    ],

    principles: [
      { title: 'Single-tenant by default', body: 'One operator, one VPS, one SQLite database. No multi-tenant complexity, no shared infrastructure. Your data never leaves your machine.' },
      { title: 'Operations as first-class surfaces', body: 'Modes, budgets, faults, traces and ADRs are observable, governable entities — not log lines buried in a sidecar. Every decision is auditable.' },
      { title: 'No invisible dependencies', body: 'Zero third-party fonts on your readers. Zero analytics. Zero CDN trackers. The only external calls are ones you explicitly configure.' },
      { title: 'Decisions have records', body: '63 ADRs document every significant choice — from WAL checkpointing to the inbox replay protocol. The codebase ships with its own archaeology.' },
    ],

    steps: [
      { label: 'Clone the repository',           cmd: 'git clone …/vayupress && cd vayupress' },
      { label: 'Build the binary (CGO + SQLite)', cmd: 'CGO_ENABLED=1 go build ./cmd/vayupress' },
      { label: 'Run the test suite',             cmd: 'CGO_ENABLED=1 go test ./...' },
      { label: 'Start the server',               cmd: 'STATIC_DIR=./static ./vayupress --port 8080' },
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
        { label: 'ADR registry',  href: 'https://github.com/johalputt/VayuPress/tree/main/docs/adr' },
        { label: 'Threat model',  href: 'https://github.com/johalputt/VayuPress/blob/main/docs/THREAT-MODEL.md' },
        { label: 'Plugins',       href: 'https://github.com/johalputt/VayuPress/tree/main/docs/plugins' },
      ]},
    ],

    onScroll() { this.scrolled = window.scrollY > 24; },

    tilt(e) {
      const el = e.currentTarget;
      const r = el.getBoundingClientRect();
      const px = (e.clientX - r.left) / r.width;
      const py = (e.clientY - r.top) / r.height;
      el.style.setProperty('--mx', `${px * 100}%`);
      el.style.setProperty('--my', `${py * 100}%`);
      el.style.transform = `perspective(900px) rotateX(${(0.5 - py) * 5}deg) rotateY(${(px - 0.5) * 5}deg) translateY(-2px)`;
    },
    untilt(e) { e.currentTarget.style.transform = ''; },

    runType() {
      if (this._typing) return;
      this._typing = true;
      const text = this.deployScript;
      let i = 0;
      const tick = () => {
        if (i <= text.length) {
          this.typed = text.slice(0, i++);
          setTimeout(tick, text[i - 1] === '\n' ? 180 : 18 + Math.random() * 30);
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

    init() {
      window.addEventListener('scroll', () => this.onScroll(), { passive: true });
      // hero terminal sequence
      let i = 1;
      const tick = () => {
        if (i <= 9) { this.t = i++; setTimeout(tick, i < 4 ? 520 : 360); }
      };
      setTimeout(tick, 700);

      // type the deploy script once the quick-start terminal scrolls into view
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
