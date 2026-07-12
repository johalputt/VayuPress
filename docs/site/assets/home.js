'use strict';
/* ═══════════════════════════════════════════════════════════════════════
   VAYUPRESS · SINGULARITY — award-grade motion engine.
   Lenis smooth scroll + GSAP ScrollTrigger + SplitType, over a live WebGL
   shader. Every motion effect is guarded: if a library is missing, content
   stays fully visible and the page works as an ordinary scroll site.
   ═══════════════════════════════════════════════════════════════════════ */
(function () {
  var RM = matchMedia('(prefers-reduced-motion: reduce)').matches;
  var COARSE = matchMedia('(hover: none)').matches;
  var DESKTOP = matchMedia('(min-width: 900px)').matches && !COARSE;
  var HAS_GSAP = !!(window.gsap && window.ScrollTrigger) && !RM;
  var HAS_LENIS = !!window.Lenis && !RM;
  var $ = function (s, r) { return (r || document).querySelector(s); };
  var $$ = function (s, r) { return Array.prototype.slice.call((r || document).querySelectorAll(s)); };
  var el = function (t, c, h) { var e = document.createElement(t); if (c) e.className = c; if (h != null) e.innerHTML = h; return e; };
  var esc = function (s) { return String(s).replace(/[&<>"]/g, function (m) { return ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[m]; }); };

  /* ═══ DATA ═══ */
  var DATA = {
    pillars: ['Website', 'Blog', 'PGP Mail', 'VayuTalk', 'Mobile App', 'Analytics', 'VayuOS'],
    products: [
      { name: 'VayuOS', icon: '🛠', rgb: '52,211,153', badge: '', tag: 'One calm control room', host: '/os',
        blurb: 'Every surface — publishing, mail, chat, analytics and security — in one fast, strict-CSP admin. No sprawl, no SPA bloat. Creating an account quietly provisions a mailbox and PGP keys.',
        points: ['⌘K palette + live 14-day publishing chart', 'Block editor, media library & Theme Studio', 'Members, newsletter, SEO & mail in one place', 'TOTP 2FA, roles & a WORM audit log'],
        cta: { label: 'Explore VayuOS', href: 'https://github.com/johalputt/VayuPress' } },
      { name: 'VayuMail', icon: '✉️', rgb: '56,189,248', badge: '', tag: 'Your own PGP mail server', host: 'mail.yourdomain.com',
        blurb: 'SMTP send + receive, IMAP and POP3, RFC-6376 DKIM signing and direct-to-MX delivery — a real mail server for your domain, with automatic MX / SPF / DKIM / DMARC and live DNS health checks.',
        points: ['Send & receive from your own domain', 'DKIM-signed, direct-to-MX with STARTTLS', 'IMAP + POP3 for any client', 'Native PGP keys per account, via WKD', 'Official Android app — connect in one scan'],
        cta: { label: 'Get the app', href: 'https://github.com/johalputt/VayuMail-Mobile' } },
      { name: 'VayuTalk', icon: '💬', rgb: '139,92,246', badge: 'NEW', tag: 'Ephemeral E2E chat', host: 'talk.yourdomain.com',
        blurb: 'Real-time PGP-encrypted messaging across the web console and the mobile app over one shared relay — a message typed on the web reaches the phone. Nothing touches disk; every message vanishes on read.',
        points: ['PGP end-to-end, web ⇄ app interop', 'Read-once — self-destructs on read', 'Short TTL; nothing is persisted', 'Safety-number verification defeats MITM', 'Dedicated relay bypasses any CDN'],
        cta: { label: 'How VayuTalk works', href: 'https://github.com/johalputt/VayuPress/blob/main/docs/adr/ADR-0131-vayutalk-ephemeral-messaging.md' } },
      { name: 'VayuShield', icon: '🛡', rgb: '244,63,94', badge: '', tag: 'Bot shield & anti-DDoS — no Cloudflare', host: '/os/shield',
        blurb: 'A five-layer, self-learning bot & DDoS shield in the same binary. It keeps Save and refresh working during a volumetric flood, jails offenders in minutes and forgives automatically — search engines and AI assistants always welcome.',
        points: ['Admin-sovereignty lane survives a flood', 'Fixed-memory fair-shed vs spoofed botnets', 'Reputation brain jails & auto-forgives', 'Silent, self-calibrating proof-of-work', 'Optional kernel nftables + XDP offload'],
        cta: { label: 'Read the architecture', href: 'https://github.com/johalputt/VayuPress/blob/main/docs/adr/ADR-0111-vayushield-bot-protection-and-analytics.md' } },
      { name: 'VayuAnalytics', icon: '📊', rgb: '232,121,249', badge: '', tag: 'Analytics without surveillance', host: '/os/analytics',
        blurb: 'Pageviews, sessions, funnels, retention, revenue and a live visitor panel — computed entirely from your local SQLite. No cookies, no localStorage, no IP or User-Agent stored, no consent banner.',
        points: ['Cookieless — daily-rotating salted hash', 'No IP or User-Agent ever stored', 'Funnels, retention, revenue & UTM', 'Live visitor panel in real time', 'Offline country table — no GeoIP call'],
        cta: { label: 'See the dashboard', href: 'https://github.com/johalputt/VayuPress' } },
      { name: 'Website & Blog', icon: '🌐', rgb: '45,212,191', badge: '', tag: 'A real website + Ghost-class blog', host: 'yourdomain.com',
        blurb: 'Eleven elegant, modern-minimalist site templates plus a best-in-class block editor with lossless Markdown and HTML modes, whole-site themes, members, newsletters and SEO — every word in your own SQLite.',
        points: ['11 modern-minimalist templates', 'Ghost-class block editor · Markdown · HTML', 'Whole-site themes with live Theme Studio', 'Memberships, paywalls & newsletters', 'Server-rendered Mermaid, math & code'],
        cta: { label: 'View the templates', href: 'https://github.com/johalputt/VayuPress' } },
      { name: 'VayuPGP', icon: '🔑', rgb: '245,158,11', badge: '', tag: 'Privacy by architecture', host: 'WKD',
        blurb: 'Every account gets a modern PGP keypair automatically. Private keys are AES-256-GCM encrypted at rest and never logged; public keys are published via Web Key Directory. It is the engine under VayuMail and VayuTalk.',
        points: ['Auto keypair generated per account', 'Private keys AES-256-GCM at rest', 'Encrypt · decrypt · sign · verify · rotate', 'WKD discovery for any GPG client', 'Powers both VayuMail and VayuTalk'],
        cta: { label: 'Learn about VayuPGP', href: 'https://github.com/johalputt/VayuPress' } }
    ],
    features: [
      { icon: '🏪', orb: 'rgba(251,146,60,.4)', title: 'A business website, not just a blog', desc: 'Pick from eleven elegant, modern-minimalist templates and deploy a full site — hero, offerings with prices, gallery, hours and contact — editing every word from VayuOS with a live preview.', tags: ['11 templates', 'website + blog + mail', 'edit from VayuOS'] },
      { icon: '✍️', orb: 'rgba(45,212,191,.5)', title: "A writing studio you'll love", desc: 'A calm, Ghost-clean block editor with tables, toggles, task lists, math, callouts, code, self-hosted audio and video. Switch to whole-document Markdown or raw HTML and back — losslessly — with a slash menu and ⌘K palette.', tags: ['blocks · Markdown · HTML', 'image drag/drop/link', 'slash menu · ⌘K'] },
      { icon: '✉️', orb: 'rgba(56,189,248,.5)', title: 'VayuMail — your own mail server', desc: 'Send and receive from your own domain without renting a provider. Outbound mail is DKIM-signed and delivered straight to the recipient; IMAP and the official app read your mail. Connect a phone with a rotating, revocable app password.', tags: ['DKIM-signed', 'Android app', 'rotating setup QR'] },
      { icon: '🔑', orb: 'rgba(167,139,250,.5)', title: 'VayuPGP — privacy by architecture', desc: 'Every account gets a modern PGP keypair automatically. Private keys are encrypted at rest and never logged; encrypt, decrypt, sign, verify and rotate keys, and a Web Key Directory lets any GPG client discover your public key.', tags: ['auto keypairs', 'encrypted at rest', 'WKD discovery'] },
      { icon: '🛡️', orb: 'rgba(52,211,153,.5)', title: 'VayuOS — one calm control room', desc: 'Every operator tool lives in one fast, strict-CSP admin at /os: posts, the editor, media, members, SEO, theme studio, mail and security. Creating an account quietly provisions a mailbox and PGP keys.', tags: ['single admin', '/os', 'strict CSP'] },
      { icon: '🎨', orb: 'rgba(244,114,182,.55)', title: 'Themes that restyle the whole site', desc: 'Pick a theme and the entire public site changes — nav, hero, post cards, article pages, author box, comments and footer. Tune logo, colours, fonts and your share image with a live preview. Served from your own origin.', tags: ['whole-site themes', 'live preview', 'self-hosted CSS'] },
      { icon: '💳', orb: 'rgba(74,222,128,.4)', title: 'Memberships & paywalls', desc: 'Turn readers into members with passwordless magic-link sign-in — no reader passwords stored. Define priced tiers, publish a themed pricing page, and gate any article as public, members or paid. Optional Stripe webhook for upgrades.', tags: ['magic-link', 'tiers & paywalls', 'member portal'] },
      { icon: '📈', orb: 'rgba(232,121,249,.4)', title: 'Analytics without surveillance', desc: 'See pageviews, top pages, referrers and trends — without cookies, consent banners, or storing an IP address. Visitor identity is a daily-rotating salted hash that can\'t be traced to a person, all in your own SQLite.', tags: ['cookieless', 'no PII', 'no consent banner'] },
      { icon: '🛡', orb: 'rgba(244,63,94,.45)', title: 'VayuShield — sovereign bot defense', desc: 'A replacement for Cloudflare Bot Management and Google Analytics, built into the binary. It fingerprints every client from its TLS handshake (JA3/JA4), challenges the suspicious with silent proof-of-work, and shields availability under load.', tags: ['JA3/JA4 + PoW', 'rate-limit + load-shed', 'no Cloudflare/GA'] },
      { icon: '📦', orb: 'rgba(245,158,11,.4)', title: 'Update & back up in one click', desc: 'Install the latest signed release from inside VayuOS — checksum- and signature-verified, database backed up first, binary swapped atomically. Export your entire site as one AES-256-GCM file only you can open.', tags: ['signed updates', 'encrypted backups', 'reversible'] },
      { icon: '🔁', orb: 'rgba(129,140,248,.4)', title: 'Bring your whole archive', desc: 'Move in from Ghost, WordPress, Substack, Medium, Hugo, Jekyll, Notion or a plain folder of Markdown — slugs, tags, dates, images and drafts preserved. The importers are resumable enough to migrate a 200,000-post site.', tags: ['Ghost · WP · Substack', 'Medium · Hugo · Jekyll', 'resumable'] }
    ],
    screenshots: [
      { label: 'Homepage', src: 'screenshots/homepage.png', caption: 'Your public homepage — clean, fast, free of third-party scripts.' },
      { label: 'VayuOS dashboard', src: 'screenshots/admin-os-dashboard.png', caption: 'VayuOS — one fast, calm admin for everything.' },
      { label: 'Block editor', src: 'screenshots/admin-os-editor.png', caption: 'The block editor — distraction-free, slash menu, live preview.' },
      { label: 'Theme Studio', src: 'screenshots/admin-os-theme.png', caption: 'Theme Studio — restyle your whole site, all self-hosted.' },
      { label: 'Post manager', src: 'screenshots/admin-os-posts.png', caption: 'Post manager — every article in one view.' },
      { label: 'Member signup', src: 'screenshots/member-signup.png', caption: 'Reader sign-up — branded and passwordless.' },
      { label: 'Media library', src: 'screenshots/admin-os-media.png', caption: 'Media library — fast, validated uploads.' },
      { label: 'SEO', src: 'screenshots/admin-os-seo.png', caption: 'Native SEO — sitemap, robots, structured data.' },
      { label: 'Analytics', src: 'screenshots/admin-os-analytics.png', caption: 'Privacy-first analytics — no cookies or PII.' },
      { label: 'Security (2FA)', src: 'screenshots/admin-os-security.png', caption: 'Security — two-factor authentication enforced.' }
    ],
    compareCols: ['VayuPress', 'WordPress', 'Ghost', 'Substack'],
    compareRows: [
      { f: 'Single self-contained binary', v: ['yes', 'no', 'no', 'n/a'] },
      { f: 'Website + blog + mail on one domain', v: ['yes', 'partial', 'no', 'no'] },
      { f: 'Your data in your own SQLite file', v: ['yes', 'partial', 'partial', 'no'] },
      { f: 'Native mail server built in (DKIM)', v: ['yes', 'no', 'no', 'no'] },
      { f: 'IMAP/POP3 + official mobile app', v: ['yes', 'no', 'no', 'no'] },
      { f: 'End-to-end PGP encryption + WKD', v: ['yes', 'no', 'no', 'no'] },
      { f: 'Ephemeral E2E chat built in', v: ['yes', 'no', 'no', 'no'] },
      { f: 'Zero reader-side trackers / cookies', v: ['yes', 'no', 'partial', 'no'] },
      { f: 'Offline analytics — no external GeoIP', v: ['yes', 'plugin', 'partial', 'partial'] },
      { f: 'Memberships & paywalls, no SDK lock-in', v: ['yes', 'plugin', 'yes', 'hosted-only'] },
      { f: 'Apache-2.0, self-hostable, no SaaS lock-in', v: ['yes', 'yes', 'yes', 'no'] }
    ],
    principles: [
      { title: 'Single-tenant by default', body: 'One operator, one VPS, one SQLite database. No multi-tenant complexity, no shared infrastructure. Your data never leaves your machine.' },
      { title: 'Operations as first-class surfaces', body: 'Modes, budgets, faults, traces and ADRs are observable, governable entities — not log lines buried in a sidecar. Every decision is auditable.' },
      { title: 'No invisible dependencies', body: 'Zero third-party fonts on your readers. Zero analytics. Zero CDN trackers on the sites you publish. The only external calls are ones you configure.' },
      { title: 'Decisions have records', body: 'Every significant choice is written down as an architecture decision record — from durability to the draft/publish security model. The codebase ships with its reasoning.' }
    ],
    steps: [
      { label: 'Clone the repository', cmd: 'git clone github.com/johalputt/vayupress' },
      { label: 'Build the binary (CGO + SQLite)', cmd: 'CGO_ENABLED=1 go build ./cmd/vayupress' },
      { label: 'Run the test suite', cmd: 'CGO_ENABLED=1 go test ./...' },
      { label: 'Start the server', cmd: 'STATIC_DIR=./static ./vayupress --port 8080' }
    ],
    footer: [
      { head: 'Project', links: [
        { label: 'GitHub', href: 'https://github.com/johalputt/VayuPress' },
        { label: 'About the developer', href: 'about.html' },
        { label: 'Changelog', href: 'https://github.com/johalputt/VayuPress/blob/main/CHANGELOG.md' },
        { label: 'Releases', href: 'https://github.com/johalputt/VayuPress/releases' }] },
      { head: 'Docs', links: [
        { label: 'Installation', href: 'https://github.com/johalputt/VayuPress/blob/main/docs/INSTALLATION.md' },
        { label: 'Architecture', href: 'https://github.com/johalputt/VayuPress/blob/main/docs/ARCHITECTURE.md' },
        { label: 'Operations', href: 'https://github.com/johalputt/VayuPress/blob/main/docs/OPERATIONS.md' }] },
      { head: 'Decisions', links: [
        { label: 'ADR registry', href: 'https://github.com/johalputt/VayuPress/tree/main/docs/adr' },
        { label: 'Threat model', href: 'https://github.com/johalputt/VayuPress/blob/main/docs/THREAT-MODEL.md' },
        { label: 'Plugins', href: 'https://github.com/johalputt/VayuPress/tree/main/docs/plugins' }] }
    ]
  };

  /* ═══ render content (always, regardless of motion libs) ═══ */
  function render() {
    var row = $('#pillRow'); if (row) DATA.pillars.forEach(function (p) { row.appendChild(el('span', 'pill', esc(p))); });

    var track = $('#prodTrack');
    if (track) DATA.products.forEach(function (p, i) {
      var pts = p.points.map(function (x) { return '<li>' + esc(x) + '</li>'; }).join('');
      var card = el('article', 'prod-card',
        '<div class="pc-idx">0' + (i + 1) + ' / 07</div>' +
        '<div class="pc-head"><div class="pc-icon">' + p.icon + '</div><div class="pc-name">' + esc(p.name) + (p.badge ? '<span class="pc-nw">' + esc(p.badge) + '</span>' : '') + '</div></div>' +
        '<div class="pc-tag">' + esc(p.tag) + '</div><div class="pc-host">' + esc(p.host) + '</div>' +
        '<p class="pc-blurb">' + esc(p.blurb) + '</p><ul class="pc-points">' + pts + '</ul>' +
        '<a class="pc-cta" href="' + esc(p.cta.href) + '" target="_blank" rel="noopener" data-hot>' + esc(p.cta.label) + ' →</a>');
      card.style.setProperty('--pr', p.rgb);
      track.appendChild(card);
    });

    var fg = $('#featGrid');
    if (fg) DATA.features.forEach(function (f) {
      var tags = f.tags.map(function (t) { return '<span>' + esc(t) + '</span>'; }).join('');
      var c = el('article', 'feat', '<div class="fi">' + f.icon + '</div><h3>' + esc(f.title) + '</h3><p>' + esc(f.desc) + '</p><div class="ftags">' + tags + '</div>');
      c.style.setProperty('--orb', f.orb); fg.appendChild(c);
    });

    var m = $('#marquee');
    if (m) {
      var mt = el('div', 'marquee-track');
      DATA.screenshots.concat(DATA.screenshots).forEach(function (s, i) {
        var idx = i % DATA.screenshots.length;
        var fig = el('figure', 'shot', '<img loading="lazy" src="' + esc(s.src) + '" alt="' + esc(s.label) + ' screenshot"><figcaption class="cap">' + esc(s.label) + '</figcaption>');
        $('img', fig).addEventListener('error', function () { fig.classList.add('noimg'); this.style.visibility = 'hidden'; });
        fig.addEventListener('click', function () { openLb(idx); });
        mt.appendChild(fig);
      });
      m.appendChild(mt);
    }

    var cw = $('#compareWrap');
    if (cw) {
      var mark = function (v, own) {
        if (v === 'yes') return '<td class="v' + (own ? ' own' : '') + '"><span class="mk yes">✓</span></td>';
        if (v === 'no') return '<td class="v"><span class="mk no">✕</span></td>';
        if (v === 'partial') return '<td class="v"><span class="mk part">◐</span></td>';
        return '<td class="v"><span class="mk txt">' + esc(v) + '</span></td>';
      };
      var head = '<tr><th>Capability</th>' + DATA.compareCols.map(function (c) { return '<th>' + esc(c) + '</th>'; }).join('') + '</tr>';
      var body = DATA.compareRows.map(function (r) { return '<tr><td>' + esc(r.f) + '</td>' + r.v.map(function (v, i) { return mark(v, i === 0); }).join('') + '</tr>'; }).join('');
      cw.innerHTML = '<table class="cmp"><thead>' + head + '</thead><tbody>' + body + '</tbody></table>';
    }

    var pg = $('#princGrid');
    if (pg) DATA.principles.forEach(function (p, i) { pg.appendChild(el('div', 'princ', '<div class="pnum">0' + (i + 1) + '</div><h3>' + esc(p.title) + '</h3><p>' + esc(p.body) + '</p>')); });

    var sg = $('#steps');
    if (sg) DATA.steps.forEach(function (s, i) { sg.appendChild(el('div', 'step', '<div class="sn">Step 0' + (i + 1) + '</div><div class="sl">' + esc(s.label) + '</div><code>' + esc(s.cmd) + '</code>')); });

    var fc = $('#footCols');
    if (fc) DATA.footer.forEach(function (col) {
      var links = col.links.map(function (l) { var ext = /^https?:/.test(l.href); return '<a href="' + esc(l.href) + '"' + (ext ? ' target="_blank" rel="noopener"' : '') + '>' + esc(l.label) + '</a>'; }).join('');
      fc.appendChild(el('div', 'foot-col', '<h4>' + esc(col.head) + '</h4>' + links));
    });
    var yr = $('#yr'); if (yr) yr.textContent = new Date().getFullYear();
  }

  /* ═══ lightbox ═══ */
  var lb, lbImg, lbCap, lbCur = 0;
  function buildLb() {
    lb = el('div', 'lb', '<button class="lb-x" aria-label="Close">✕</button><button class="lb-nav lb-prev" aria-label="Previous">‹</button><button class="lb-nav lb-next" aria-label="Next">›</button><figure class="lb-fig"><img alt=""><figcaption class="lb-cap"></figcaption></figure>');
    document.body.appendChild(lb);
    lbImg = $('img', lb); lbCap = $('.lb-cap', lb);
    $('.lb-x', lb).addEventListener('click', closeLb);
    $('.lb-prev', lb).addEventListener('click', function (e) { e.stopPropagation(); moveLb(-1); });
    $('.lb-next', lb).addEventListener('click', function (e) { e.stopPropagation(); moveLb(1); });
    lb.addEventListener('click', function (e) { if (e.target === lb) closeLb(); });
    addEventListener('keydown', function (e) { if (!lb.classList.contains('open')) return; if (e.key === 'Escape') closeLb(); if (e.key === 'ArrowLeft') moveLb(-1); if (e.key === 'ArrowRight') moveLb(1); });
  }
  function showLb() { var s = DATA.screenshots[lbCur]; lbImg.src = s.src; lbImg.alt = s.label; lbCap.textContent = s.caption; }
  function openLb(i) { lbCur = i; showLb(); lb.classList.add('open'); }
  function closeLb() { lb.classList.remove('open'); }
  function moveLb(d) { lbCur = (lbCur + d + DATA.screenshots.length) % DATA.screenshots.length; showLb(); }

  /* ═══ WebGL singularity ═══ */
  function shader() {
    var cv = $('#gl');
    var gl = cv.getContext('webgl') || cv.getContext('experimental-webgl');
    if (!gl) { cv.style.display = 'none'; $('#glfallback').style.display = 'block'; return; }
    var vs = 'attribute vec2 p;void main(){gl_Position=vec4(p,0.,1.);}';
    var fs = ['precision highp float;', 'uniform vec2 uRes;uniform float uTime;uniform vec2 uMouse;uniform float uScroll;',
      'float hash(vec2 p){p=fract(p*vec2(123.34,345.45));p+=dot(p,p+34.345);return fract(p.x*p.y);}',
      'float noise(vec2 p){vec2 i=floor(p),f=fract(p);float a=hash(i),b=hash(i+vec2(1.,0.)),c=hash(i+vec2(0.,1.)),d=hash(i+vec2(1.,1.));vec2 u=f*f*(3.-2.*f);return mix(a,b,u.x)+(c-a)*u.y*(1.-u.x)+(d-b)*u.x*u.y;}',
      'float fbm(vec2 p){float v=0.,a=.5;mat2 m=mat2(1.6,1.2,-1.2,1.6);for(int i=0;i<6;i++){v+=a*noise(p);p=m*p;a*=.5;}return v;}',
      'void main(){vec2 uv=(gl_FragCoord.xy-.5*uRes)/uRes.y;vec2 mo=(uMouse-.5)*vec2(uRes.x/uRes.y,1.);float t=uTime*.045;',
      'vec2 p=uv-mo*0.12;float r=length(p);float ang=atan(p.y,p.x);float swirl=0.55/(r+0.32);',
      'vec2 q=vec2(cos(ang+swirl+t),sin(ang+swirl+t))*r;vec2 w=q+0.6*vec2(fbm(q*1.5+t),fbm(q*1.5-t+5.2));',
      'float n=fbm(w*2.0+mo*0.35);float coreY=-0.05+uScroll*0.55;float rc=length(p-vec2(0.,coreY));',
      'float core=exp(-rc*3.1)+0.5*exp(-rc*8.0);float ml=0.22*exp(-length(uv-mo)*4.5);',
      'float dens=n*0.85+core*1.5+ml;dens*=smoothstep(1.45,0.05,r);',
      'vec3 cyan=vec3(0.20,0.95,0.85),violet=vec3(0.49,0.42,1.0),rose=vec3(1.0,0.44,0.66),gold=vec3(1.0,0.82,0.55);',
      'float h=n+ang*0.05+t*2.0+r;vec3 col=mix(violet,cyan,0.5+0.5*sin(h));col=mix(col,rose,0.5+0.5*sin(h*1.3+1.5));col=mix(col,gold,clamp(core*0.8,0.,1.));',
      'vec3 o=col*dens;o+=vec3(0.02,0.025,0.05)*(1.0-r);o=o/(o+0.72);o=pow(o,vec3(0.85));gl_FragColor=vec4(o,1.0);}'].join('\n');
    function sh(ty, src) { var s = gl.createShader(ty); gl.shaderSource(s, src); gl.compileShader(s); return s; }
    var prog = gl.createProgram(); gl.attachShader(prog, sh(gl.VERTEX_SHADER, vs)); gl.attachShader(prog, sh(gl.FRAGMENT_SHADER, fs)); gl.linkProgram(prog);
    if (!gl.getProgramParameter(prog, gl.LINK_STATUS)) { cv.style.display = 'none'; $('#glfallback').style.display = 'block'; return; }
    gl.useProgram(prog);
    var buf = gl.createBuffer(); gl.bindBuffer(gl.ARRAY_BUFFER, buf); gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 3, -1, -1, 3]), gl.STATIC_DRAW);
    var loc = gl.getAttribLocation(prog, 'p'); gl.enableVertexAttribArray(loc); gl.vertexAttribPointer(loc, 2, gl.FLOAT, false, 0, 0);
    var uRes = gl.getUniformLocation(prog, 'uRes'), uTime = gl.getUniformLocation(prog, 'uTime'), uMouse = gl.getUniformLocation(prog, 'uMouse'), uScroll = gl.getUniformLocation(prog, 'uScroll');
    var dpr = Math.min(devicePixelRatio || 1, 1.6);
    function resize() { cv.width = cv.clientWidth * dpr; cv.height = cv.clientHeight * dpr; gl.viewport(0, 0, cv.width, cv.height); gl.uniform2f(uRes, cv.width, cv.height); }
    addEventListener('resize', resize, { passive: true }); resize();
    var mx = 0.5, my = 0.5, tmx = 0.5, tmy = 0.5, scr = 0;
    addEventListener('pointermove', function (e) { tmx = e.clientX / innerWidth; tmy = 1 - e.clientY / innerHeight; }, { passive: true });
    addEventListener('scroll', function () { var d = document.documentElement; scr = d.scrollTop / ((d.scrollHeight - innerHeight) || 1); }, { passive: true });
    var raf, vis = true;
    document.addEventListener('visibilitychange', function () { vis = !document.hidden; if (vis && !RM) { cancelAnimationFrame(raf); raf = requestAnimationFrame(loop); } });
    function loop(t) { if (!vis) return; mx += (tmx - mx) * 0.05; my += (tmy - my) * 0.05; gl.uniform1f(uTime, t * 0.001); gl.uniform2f(uMouse, mx, my); gl.uniform1f(uScroll, scr); gl.drawArrays(gl.TRIANGLES, 0, 3); raf = requestAnimationFrame(loop); }
    if (RM) { gl.uniform1f(uTime, 8.0); gl.uniform2f(uMouse, 0.5, 0.5); gl.uniform1f(uScroll, 0.0); gl.drawArrays(gl.TRIANGLES, 0, 3); }
    else raf = requestAnimationFrame(loop);
  }

  /* ═══ cursor ═══ */
  function cursor() {
    if (COARSE || RM) return;
    document.body.classList.add('cursorized');
    var dot = $('.cursor'), ring = $('.cursor-ring');
    var x = innerWidth / 2, y = innerHeight / 2, rx = x, ry = y;
    addEventListener('pointermove', function (e) { x = e.clientX; y = e.clientY; dot.style.transform = 'translate(' + x + 'px,' + y + 'px) translate(-50%,-50%)'; }, { passive: true });
    (function t() { rx += (x - rx) * 0.18; ry += (y - ry) * 0.18; ring.style.transform = 'translate(' + rx + 'px,' + ry + 'px) translate(-50%,-50%)'; requestAnimationFrame(t); })();
    document.addEventListener('pointerover', function (e) { if (e.target.closest('a,button,[data-hot],.shot,.prod-card')) ring.classList.add('hot'); });
    document.addEventListener('pointerout', function (e) { if (e.target.closest('a,button,[data-hot],.shot,.prod-card')) ring.classList.remove('hot'); });
  }

  /* ═══ rail + nav ═══ */
  function chrome() {
    var rail = $('#rail'), nav = $('#nav');
    function upd() { var d = document.documentElement, p = d.scrollTop / ((d.scrollHeight - innerHeight) || 1); rail.style.transform = 'scaleX(' + p + ')'; nav.classList.toggle('solid', d.scrollTop > 40); }
    addEventListener('scroll', upd, { passive: true }); upd();
  }

  /* ═══ rotating word ═══ */
  function rotate() {
    if (RM) return;
    var rot = $('#rot'); if (!rot) return;
    var words = ['a business website.', 'a Ghost-class blog.', 'a PGP mail server.', 'encrypted VayuTalk chat.', 'cookieless analytics.', 'your whole stack.'], i = 0;
    setInterval(function () {
      i = (i + 1) % words.length;
      rot.style.transition = 'none'; rot.style.opacity = '0'; rot.style.transform = 'translateY(60%)';
      requestAnimationFrame(function () { rot.textContent = words[i]; requestAnimationFrame(function () { rot.style.transition = 'opacity .5s,transform .5s cubic-bezier(.2,1,.3,1)'; rot.style.opacity = '1'; rot.style.transform = 'none'; }); });
    }, 2400);
  }

  /* ═══ terminal ═══ */
  var typed = false;
  function typeTerm() {
    if (typed) return; typed = true;
    var body = $('#termBody'); if (!body) return;
    var lines = [['m', '# 1 · get the sovereign binary'], ['', '<span class="c">curl</span> -fsSL https://vayupress.com/install.sh | sh'], ['n', ''],
      ['m', '# 2 · point it at your domain'], ['', '<span class="c">vayupress</span> init <span class="v">--domain</span> yourdomain.com'], ['n', ''],
      ['m', '# 3 · bring mail, chat & analytics online'], ['', '<span class="c">vayupress</span> up <span class="v">--mail --talk --analytics</span>'], ['n', ''],
      ['g', '✓ website · blog · PGP mail · chat · analytics — live']];
    function wrap(l) { return l[0] === 'm' ? '<span class="m">' + l[1] + '</span>' : l[0] === 'g' ? '<span class="g">' + l[1] + '</span>' : l[1]; }
    if (RM) { body.innerHTML = lines.map(function (l) { return l[0] === 'n' ? '' : wrap(l); }).join('\n'); return; }
    var i = 0;
    (function next() {
      if (i >= lines.length) return;
      var l = lines[i++]; var s = document.createElement('span');
      s.innerHTML = l[0] === 'n' ? '' : wrap(l); s.style.opacity = '0'; s.style.transition = 'opacity .3s';
      body.appendChild(s); body.appendChild(document.createTextNode('\n'));
      requestAnimationFrame(function () { s.style.opacity = '1'; });
      setTimeout(next, l[0] === 'n' ? 110 : 250);
    })();
  }

  /* ═══ copy ═══ */
  function copyBtn() {
    var btn = $('#copyBtn'); if (!btn) return;
    var script = 'curl -fsSL https://vayupress.com/install.sh | sh\nvayupress init --domain yourdomain.com\nvayupress up --mail --talk --analytics';
    btn.addEventListener('click', function () { if (!navigator.clipboard) return; navigator.clipboard.writeText(script).then(function () { btn.textContent = 'Copied ✓'; setTimeout(function () { btn.textContent = 'Copy'; }, 1800); }); });
  }

  /* ═══ live version + stars ═══ */
  function live() {
    function setVer(t) { if (typeof t === 'string' && /^v?\d/.test(t)) { var v = t.charAt(0) === 'v' ? t : 'v' + t; var e = $('#rel'); if (e) e.textContent = v; } }
    function setStars(n) { if (typeof n === 'number' && n >= 0) { var e = $('#stars'); if (e) e.textContent = n.toLocaleString(); } }
    fetch('assets/version.json', { cache: 'no-cache' }).then(function (r) { return r.ok ? r.json() : null; }).then(function (j) { if (j) setVer(j.tag_name || j.version); }).catch(function () {});
    fetch('https://api.github.com/repos/johalputt/VayuPress/releases/latest').then(function (r) { return r.ok ? r.json() : null; }).then(function (j) { if (j) setVer(j.tag_name); }).catch(function () {});
    fetch('assets/stars.json', { cache: 'no-cache' }).then(function (r) { return r.ok ? r.json() : null; }).then(function (j) { if (j) setStars(j.stargazers_count); }).catch(function () {});
    fetch('https://api.github.com/repos/johalputt/VayuPress').then(function (r) { return r.ok ? r.json() : null; }).then(function (j) { if (j) setStars(j.stargazers_count); }).catch(function () {});
  }

  /* ═══ static fallback for the convergence core (no GSAP) ═══ */
  function staticCore() { var c = $('#core'); if (c) { c.style.opacity = '1'; c.style.transform = 'translate(-50%,-50%) scale(1)'; } }

  /* ═══ GSAP + Lenis choreography ═══ */
  function motion() {
    var gsap = window.gsap, ST = window.ScrollTrigger;
    gsap.registerPlugin(ST);

    // Lenis smooth scroll
    if (HAS_LENIS) {
      var lenis = new Lenis({ lerp: 0.1, wheelMultiplier: 1, smoothWheel: true });
      lenis.on('scroll', ST.update);
      gsap.ticker.add(function (t) { lenis.raf(t * 1000); });
      gsap.ticker.lagSmoothing(0);
      window.__lenis = lenis;
    }

    // Text reveals via SplitType
    $$('[data-split]').forEach(function (h) {
      var targets;
      if (window.SplitType) { try { targets = new SplitType(h, { types: 'words' }).words; } catch (e) { targets = null; } }
      if (!targets || !targets.length) targets = [h];
      gsap.from(targets, {
        scrollTrigger: { trigger: h, start: 'top 85%', once: true },
        yPercent: 60, opacity: 0, duration: 0.9, ease: 'power3.out', stagger: 0.035
      });
    });

    // Hero entrance
    var heroTl = gsap.timeline({ delay: 0.1 });
    heroTl.from('#hero .reveal-fade', { y: 26, opacity: 0, duration: 0.9, ease: 'power3.out', stagger: 0.09 }, 0.2);

    // Generic reveals (prod-cards are revealed by the horizontal scroll itself)
    $$('.feat, .princ, .step').forEach(function (n) {
      gsap.from(n, { scrollTrigger: { trigger: n, start: 'top 90%', once: true }, y: 36, opacity: 0, duration: 0.8, ease: 'power3.out' });
    });
    $$('.sec-head, .prod-intro, .table-wrap, .term').forEach(function (n) {
      gsap.from(n, { scrollTrigger: { trigger: n, start: 'top 88%', once: true }, y: 30, opacity: 0, duration: 0.8, ease: 'power3.out' });
    });

    // Convergence — pinned scrub
    var stage = $('#stage');
    if (stage) {
      var vw = function (v) { return v / 100 * innerWidth; }, vh = function (v) { return v / 100 * innerHeight; };
      $$('[data-orbit]', stage).forEach(function (o) {
        var tx = parseFloat(o.style.getPropertyValue('--tx')), ty = parseFloat(o.style.getPropertyValue('--ty'));
        gsap.set(o, { xPercent: -50, yPercent: -50, x: vw(tx), y: vh(ty) });
      });
      gsap.set('#core', { xPercent: -50, yPercent: -50, scale: 0.001, opacity: 0 });
      var ctl = gsap.timeline({ scrollTrigger: { trigger: '#idea', start: 'top top', end: '+=110%', pin: '.idea-pin', scrub: 1, invalidateOnRefresh: true } });
      ctl.to('[data-orbit]', { x: 0, y: 0, scale: 0.2, opacity: 0, ease: 'power2.in', stagger: 0.05 }, 0)
        .to('#core', { opacity: 1, scale: 1, ease: 'power2.out' }, 0.25);
    }

    // Products — horizontal pinned scroll (desktop only)
    var hs = $('#hscroll'), track = $('#prodTrack');
    if (hs && track && DESKTOP) {
      hs.style.overflow = 'visible';
      gsap.to(track, {
        x: function () { return -(track.scrollWidth - innerWidth + 40); }, ease: 'none',
        scrollTrigger: { trigger: '#products', start: 'top top', end: function () { return '+=' + (track.scrollWidth - innerWidth + 40); }, pin: true, scrub: 1, invalidateOnRefresh: true, anticipatePin: 1 }
      });
    }

    // terminal + parallax shot rows
    ST.create({ trigger: '#term', start: 'top 80%', once: true, onEnter: typeTerm });

    setTimeout(function () { ST.refresh(); }, 300);
    addEventListener('load', function () { ST.refresh(); });
  }

  /* ═══ preloader → boot ═══ */
  function boot() {
    render(); buildLb(); shader(); cursor(); chrome(); rotate(); copyBtn(); live();
    if (HAS_GSAP) { try { motion(); } catch (e) { staticCore(); typeTerm(); } }
    else { staticCore(); typeTerm(); }
  }

  function preloader() {
    var pre = $('#preload'), num = $('#plNum'), word = $('#plWord');
    var words = ['assembling the binary', 'lighting the singularity', 'one process, no sprawl'];
    if (RM || !pre) { if (pre) pre.style.display = 'none'; boot(); return; }
    boot();                              // build everything (incl. Lenis) first
    if (window.__lenis) window.__lenis.stop(); // then hold scroll during the count
    var n = 0, wi = 0;
    var iv = setInterval(function () {
      n = Math.min(100, n + Math.round(4 + Math.random() * 9));
      num.textContent = n;
      if (n > 33 * (wi + 1) && wi < words.length - 1) { wi++; word.textContent = words[wi]; }
      if (n >= 100) {
        clearInterval(iv);
        setTimeout(function () {
          pre.classList.add('done');
          if (window.__lenis) window.__lenis.start();
          setTimeout(function () { pre.style.display = 'none'; if (window.ScrollTrigger) ScrollTrigger.refresh(); }, 950);
        }, 260);
      }
    }, 90);
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', preloader);
  else preloader();
})();
