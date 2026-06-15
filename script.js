'use strict';

/* ── Alpine.js root app ── */
function app() {
  return {
    scrolled: false,
    activeShot: 0,
    termLine: 0,

    features: [
      {
        icon: '⚡',
        iconBg: 'bg-teal-900/60 border border-teal-800',
        title: 'Adaptive governance runtime',
        desc: 'Six system modes with a validated transition graph, severity-classified error budgets, and an append-only mode journal. The gated budget actuator (Ω12) drives automatic escalation when opted in.',
        tags: ['mode-graph', 'error-budgets', 'Ω12'],
      },
      {
        icon: '🗄️',
        iconBg: 'bg-blue-900/60 border border-blue-800',
        title: 'Durable by design',
        desc: 'Append-only SQLite write queue with retry, dead-letter, and replay. Transactional outbox relay, WAL with adaptive checkpointing, and migration checksum drift verification.',
        tags: ['SQLite', 'WAL', 'outbox'],
      },
      {
        icon: '🔭',
        iconBg: 'bg-purple-900/60 border border-purple-800',
        title: 'Observable end to end',
        desc: 'Structured health contracts, distributed tracing with correlation/causation IDs, Prometheus metrics, and a unified operational timeline visible in the admin console.',
        tags: ['Prometheus', 'tracing', 'health'],
      },
      {
        icon: '🎨',
        iconBg: 'bg-pink-900/60 border border-pink-800',
        title: 'Operator theme console',
        desc: 'Site identity, light/dark palette, custom CSS, declarative head/SEO, favicon and logo upload (PNG/ICO, magic-number validated), portable JSON export/import, one-click reset.',
        tags: ['branding', 'favicon', 'export/import'],
      },
      {
        icon: '🔌',
        iconBg: 'bg-orange-900/60 border border-orange-800',
        title: 'Sandboxed plugins',
        desc: 'Subprocess plugins under a capability model — filesystem, network, and write allowlists. Five worked examples including trace-tap and seo-stamp.',
        tags: ['sandbox', 'plugins', 'capabilities'],
      },
      {
        icon: '🌐',
        iconBg: 'bg-green-900/60 border border-green-800',
        title: 'Federation substrate',
        desc: 'Minimal ActivityPub server with HTTP-signature verification and durable, atomic inbox replay protection to prevent duplicate delivery from hostile or retrying peers.',
        tags: ['ActivityPub', 'federation', 'replay'],
      },
    ],

    screenshots: [
      {
        label: 'Homepage',
        path: '/',
        src: 'screenshots/homepage.png',
        caption: 'Public-facing homepage — clean, fast, no third-party scripts.',
      },
      {
        label: 'Admin dashboard',
        path: '/admin',
        src: 'screenshots/admin-dashboard.png',
        caption: 'Operator dashboard — runtime health, mode status, and quick actions at a glance.',
      },
      {
        label: 'Theme panel',
        path: '/admin/theme',
        src: 'screenshots/admin-panel.png',
        caption: 'Theme console — identity, palette, favicon upload, Export/Import, and one-click Reset.',
      },
      {
        label: 'Policy modes',
        path: '/admin/policy/modes',
        src: 'screenshots/policy-modes.png',
        caption: 'Six system modes: normal → degraded → read-only → quarantined → recovery → maintenance.',
      },
      {
        label: 'Policy inspector',
        path: '/admin/policy/inspector',
        src: 'screenshots/policy-inspector.png',
        caption: 'Governance inspector — live error budgets with severity classification and actuation status.',
      },
      {
        label: 'Runtime topology',
        path: '/admin/runtime/topology',
        src: 'screenshots/runtime-topology.png',
        caption: 'Runtime topology view — subsystem graph with health and dependency edges.',
      },
      {
        label: 'Replay explorer',
        path: '/admin/replay',
        src: 'screenshots/replay-explorer.png',
        caption: 'Inbox replay explorer — view and re-drive dead-letter activities from the SQLite outbox.',
      },
    ],

    principles: [
      {
        title: 'Single-tenant by default',
        body: 'VayuPress is designed for one operator, one VPS, one SQLite database. No multi-tenant complexity, no shared infrastructure. Your data never leaves your machine.',
      },
      {
        title: 'Operations as first-class surfaces',
        body: 'Modes, budgets, faults, traces, and ADRs are observable, governable entities — not log lines buried in a sidecar. Every runtime decision is auditable.',
      },
      {
        title: 'No invisible dependencies',
        body: 'Zero third-party fonts. Zero analytics scripts. Zero CDN trackers on your readers. The only external calls are ones you explicitly configure.',
      },
      {
        title: 'Decisions have records',
        body: '63 Architecture Decision Records document every significant design choice — from WAL checkpointing to the inbox replay protocol. The codebase comes with its own archaeology.',
      },
    ],

    steps: [
      { label: 'Clone the repository', cmd: 'git clone https://github.com/johalputt/vayupress && cd vayupress' },
      { label: 'Build the binary (requires CGO for SQLite)', cmd: 'CGO_ENABLED=1 go build -o vayupress ./cmd/vayupress' },
      { label: 'Run the tests', cmd: 'CGO_ENABLED=1 go test ./...' },
      { label: 'Start the server', cmd: 'STATIC_DIR=./static VAYU_DOCS_DIR=./docs ./vayupress --port 8080' },
    ],

    adrTicker: Array.from({ length: 63 * 2 }, (_, i) => (i % 63) + 1),

    onScroll() {
      this.scrolled = window.scrollY > 20;
    },

    init() {
      // animate terminal lines in sequence
      const total = 9;
      let i = 1;
      const tick = () => {
        if (i <= total) {
          this.termLine = i++;
          setTimeout(tick, i < 4 ? 600 : 400);
        }
      };
      setTimeout(tick, 800);

      // intersection observer for feature cards
      if ('IntersectionObserver' in window) {
        const cards = document.querySelectorAll('.feature-card');
        const obs = new IntersectionObserver(
          (entries) => entries.forEach((e) => {
            if (e.isIntersecting) {
              e.target.style.opacity = '1';
              e.target.style.transform = 'none';
              obs.unobserve(e.target);
            }
          }),
          { threshold: 0.1 }
        );
        cards.forEach((c) => {
          c.style.opacity = '0';
          c.style.transform = 'translateY(20px)';
          c.style.transition = 'opacity 0.5s ease, transform 0.5s ease';
          obs.observe(c);
        });
      }
    },
  };
}
