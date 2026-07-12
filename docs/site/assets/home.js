'use strict';
/* ═══════════════════════════════════════════════════════════════════════
   VAYUPRESS · SINGULARITY — the sovereign site engine.
   Vanilla JS, no framework. A WebGL singularity shader, a custom cursor,
   scroll choreography, and all dynamic content rendered from local data.
   Everything degrades: no WebGL → CSS fallback; reduced-motion → static.
   ═══════════════════════════════════════════════════════════════════════ */
(function () {
  var RM = matchMedia('(prefers-reduced-motion: reduce)').matches;
  var COARSE = matchMedia('(hover: none)').matches;
  var $ = function (s, r) { return (r || document).querySelector(s); };
  var el = function (t, c, h) { var e = document.createElement(t); if (c) e.className = c; if (h != null) e.innerHTML = h; return e; };
  var esc = function (s) { return String(s).replace(/[&<>"]/g, function (m) { return ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[m]; }); };

  /* ═══ 1 · WebGL singularity ═══ */
  (function shader() {
    var cv = $('#gl');
    var gl = cv.getContext('webgl') || cv.getContext('experimental-webgl');
    if (!gl) { cv.style.display = 'none'; $('#glfallback').style.display = 'block'; return; }
    var vs = 'attribute vec2 p;void main(){gl_Position=vec4(p,0.,1.);}';
    var fs = [
      'precision highp float;',
      'uniform vec2 uRes;uniform float uTime;uniform vec2 uMouse;uniform float uScroll;',
      'float hash(vec2 p){p=fract(p*vec2(123.34,345.45));p+=dot(p,p+34.345);return fract(p.x*p.y);}',
      'float noise(vec2 p){vec2 i=floor(p),f=fract(p);float a=hash(i),b=hash(i+vec2(1.,0.)),c=hash(i+vec2(0.,1.)),d=hash(i+vec2(1.,1.));vec2 u=f*f*(3.-2.*f);return mix(a,b,u.x)+(c-a)*u.y*(1.-u.x)+(d-b)*u.x*u.y;}',
      'float fbm(vec2 p){float v=0.,a=.5;mat2 m=mat2(1.6,1.2,-1.2,1.6);for(int i=0;i<6;i++){v+=a*noise(p);p=m*p;a*=.5;}return v;}',
      'void main(){',
      ' vec2 uv=(gl_FragCoord.xy-.5*uRes)/uRes.y;',
      ' vec2 mo=(uMouse-.5)*vec2(uRes.x/uRes.y,1.);',
      ' float t=uTime*.045;',
      ' vec2 p=uv-mo*0.12;',
      ' float r=length(p);',
      ' float ang=atan(p.y,p.x);',
      ' float swirl=0.55/(r+0.32);',
      ' vec2 q=vec2(cos(ang+swirl+t),sin(ang+swirl+t))*r;',
      ' vec2 w=q+0.6*vec2(fbm(q*1.5+t),fbm(q*1.5-t+5.2));',
      ' float n=fbm(w*2.0+mo*0.35);',
      ' float coreY=-0.05+uScroll*0.55;',
      ' float rc=length(p-vec2(0.,coreY));',
      ' float core=exp(-rc*3.1)+0.5*exp(-rc*8.0);',
      ' float ml=0.22*exp(-length(uv-mo)*4.5);',
      ' float dens=n*0.85+core*1.5+ml;',
      ' dens*=smoothstep(1.45,0.05,r);',
      ' vec3 cyan=vec3(0.20,0.95,0.85),violet=vec3(0.49,0.42,1.0),rose=vec3(1.0,0.44,0.66),gold=vec3(1.0,0.82,0.55);',
      ' float h=n+ang*0.05+t*2.0+r;',
      ' vec3 col=mix(violet,cyan,0.5+0.5*sin(h));',
      ' col=mix(col,rose,0.5+0.5*sin(h*1.3+1.5));',
      ' col=mix(col,gold,clamp(core*0.8,0.,1.));',
      ' vec3 o=col*dens;',
      ' o+=vec3(0.02,0.025,0.05)*(1.0-r);',
      ' o=o/(o+0.72);o=pow(o,vec3(0.85));',
      ' gl_FragColor=vec4(o,1.0);',
      '}'].join('\n');
    function sh(ty, src) { var s = gl.createShader(ty); gl.shaderSource(s, src); gl.compileShader(s); return s; }
    var prog = gl.createProgram();
    gl.attachShader(prog, sh(gl.VERTEX_SHADER, vs));
    gl.attachShader(prog, sh(gl.FRAGMENT_SHADER, fs));
    gl.linkProgram(prog);
    if (!gl.getProgramParameter(prog, gl.LINK_STATUS)) { cv.style.display = 'none'; $('#glfallback').style.display = 'block'; return; }
    gl.useProgram(prog);
    var buf = gl.createBuffer(); gl.bindBuffer(gl.ARRAY_BUFFER, buf);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 3, -1, -1, 3]), gl.STATIC_DRAW);
    var loc = gl.getAttribLocation(prog, 'p'); gl.enableVertexAttribArray(loc); gl.vertexAttribPointer(loc, 2, gl.FLOAT, false, 0, 0);
    var uRes = gl.getUniformLocation(prog, 'uRes'), uTime = gl.getUniformLocation(prog, 'uTime'),
      uMouse = gl.getUniformLocation(prog, 'uMouse'), uScroll = gl.getUniformLocation(prog, 'uScroll');
    var dpr = Math.min(devicePixelRatio || 1, 1.6);
    function resize() { cv.width = cv.clientWidth * dpr; cv.height = cv.clientHeight * dpr; gl.viewport(0, 0, cv.width, cv.height); gl.uniform2f(uRes, cv.width, cv.height); }
    addEventListener('resize', resize, { passive: true }); resize();
    var mx = 0.5, my = 0.5, tmx = 0.5, tmy = 0.5, scroll = 0;
    addEventListener('pointermove', function (e) { tmx = e.clientX / innerWidth; tmy = 1 - e.clientY / innerHeight; }, { passive: true });
    addEventListener('scroll', function () { var d = document.documentElement; scroll = d.scrollTop / ((d.scrollHeight - innerHeight) || 1); }, { passive: true });
    var raf, vis = true;
    document.addEventListener('visibilitychange', function () { vis = !document.hidden; if (vis && !RM) { cancelAnimationFrame(raf); raf = requestAnimationFrame(loop); } });
    function loop(t) {
      if (!vis) return;
      mx += (tmx - mx) * 0.05; my += (tmy - my) * 0.05;
      gl.uniform1f(uTime, t * 0.001); gl.uniform2f(uMouse, mx, my); gl.uniform1f(uScroll, scroll);
      gl.drawArrays(gl.TRIANGLES, 0, 3);
      raf = requestAnimationFrame(loop);
    }
    if (RM) { gl.uniform1f(uTime, 8.0); gl.uniform2f(uMouse, 0.5, 0.5); gl.uniform1f(uScroll, 0.0); gl.drawArrays(gl.TRIANGLES, 0, 3); }
    else raf = requestAnimationFrame(loop);
  })();

  /* ═══ 2 · custom cursor ═══ */
  (function cursor() {
    if (COARSE || RM) return;
    document.body.classList.add('cursorized');
    var dot = $('.cursor'), ring = $('.cursor-ring');
    var x = innerWidth / 2, y = innerHeight / 2, rx = x, ry = y;
    addEventListener('pointermove', function (e) { x = e.clientX; y = e.clientY; dot.style.transform = 'translate(' + x + 'px,' + y + 'px) translate(-50%,-50%)'; }, { passive: true });
    (function tick() { rx += (x - rx) * 0.18; ry += (y - ry) * 0.18; ring.style.transform = 'translate(' + rx + 'px,' + ry + 'px) translate(-50%,-50%)'; requestAnimationFrame(tick); })();
    document.addEventListener('pointerover', function (e) { if (e.target.closest('a,button,[data-hot],.shot,.ptab')) ring.classList.add('hot'); });
    document.addEventListener('pointerout', function (e) { if (e.target.closest('a,button,[data-hot],.shot,.ptab')) ring.classList.remove('hot'); });
  })();

  /* ═══ 3 · rail + nav + reveals + convergence ═══ */
  (function scroll() {
    var rail = $('#rail'), nav = $('#nav');
    addEventListener('scroll', function () {
      var d = document.documentElement, p = d.scrollTop / ((d.scrollHeight - innerHeight) || 1);
      rail.style.transform = 'scaleX(' + p + ')';
      nav.classList.toggle('solid', d.scrollTop > 40);
    }, { passive: true });
    if (!('IntersectionObserver' in window)) { document.querySelectorAll('.rv').forEach(function (n) { n.classList.add('in'); }); var st = $('#stage'); if (st) st.classList.add('go'); typeTerm(); return; }
    var io = new IntersectionObserver(function (es) {
      es.forEach(function (e) {
        if (!e.isIntersecting) return;
        e.target.classList.add('in');
        if (e.target.id === 'stage') setTimeout(function () { e.target.classList.add('go'); }, 350);
        if (e.target.id === 'term') typeTerm();
        io.unobserve(e.target);
      });
    }, { threshold: 0.16 });
    document.querySelectorAll('.rv').forEach(function (n) { io.observe(n); });
  })();

  /* ═══ 4 · hero assemble + rotating subject ═══ */
  (function hero() {
    var box = $('.hero-in');
    if (!RM) box.classList.add('hero-anim');
    var rot = $('#rot');
    var words = ['a business website.', 'a Ghost-class blog.', 'a PGP mail server.', 'encrypted VayuTalk chat.', 'cookieless analytics.', 'your whole stack.'];
    if (RM) return;
    var i = 0;
    setInterval(function () {
      i = (i + 1) % words.length;
      rot.style.transition = 'none'; rot.style.opacity = '0'; rot.style.transform = 'translateY(60%)';
      requestAnimationFrame(function () {
        rot.textContent = words[i];
        requestAnimationFrame(function () { rot.style.transition = 'opacity .5s,transform .5s cubic-bezier(.2,1,.3,1)'; rot.style.opacity = '1'; rot.style.transform = 'none'; });
      });
    }, 2400);
  })();

  /* ═══ 5 · magnetic buttons ═══ */
  (function magnet() {
    if (COARSE || RM) return;
    document.querySelectorAll('.btn').forEach(function (b) {
      b.addEventListener('pointermove', function (e) { var r = b.getBoundingClientRect(); b.style.transform = 'translate(' + (e.clientX - r.left - r.width / 2) * 0.22 + 'px,' + (e.clientY - r.top - r.height / 2) * 0.34 + 'px)'; });
      b.addEventListener('pointerleave', function () { b.style.transform = ''; });
    });
  })();

  /* ═══ 6 · DATA ═══ */
  var DATA = {
    pillars: ['Website', 'Blog', 'PGP Mail', 'VayuTalk', 'Mobile App', 'Analytics', 'VayuOS'],
    products: [
      { key: 'vayuos', name: 'VayuOS', icon: '🛠', rgb: '52,211,153', badge: '', tag: 'One calm control room for the whole platform', host: '/os',
        blurb: 'Every surface — publishing, mail, chat, analytics and security — in one fast, strict-CSP admin. No sprawl, no SPA bloat, no second weaker path in. Creating an account quietly provisions a mailbox and PGP keys for you.',
        points: ['⌘K command palette + a live 14-day publishing chart', 'Block editor, media library & Theme Studio', 'Members, newsletter, SEO & mail in one place', 'TOTP two-factor, roles & a WORM audit log', 'One-click signed update & encrypted backup', 'HTMX + hand-written CSS — negligible RAM/CPU'],
        cta: { label: 'Explore VayuOS', href: 'https://github.com/johalputt/VayuPress' } },
      { key: 'vayumail', name: 'VayuMail', icon: '✉️', rgb: '56,189,248', badge: '', tag: 'Your own sovereign PGP mail server', host: 'mail.yourdomain.com',
        blurb: 'SMTP send + receive, IMAP and POP3, RFC-6376 DKIM signing and direct-to-MX delivery — a real mail server for your domain, with automatic MX / SPF / DKIM / DMARC records and live DNS health checks.',
        points: ['Send & receive from your own domain', 'DKIM-signed, direct-to-MX with STARTTLS', 'IMAP + POP3 for any client (Thunderbird, K-9…)', 'Automatic MX / SPF / DKIM / DMARC + DNS health', 'Native PGP: keys per account, published via WKD', 'Official Android app — connect in one scan'],
        cta: { label: 'Get the app', href: 'https://github.com/johalputt/VayuMail-Mobile' } },
      { key: 'vayutalk', name: 'VayuTalk', icon: '💬', rgb: '139,92,246', badge: 'NEW', tag: 'Ephemeral, end-to-end-encrypted chat', host: 'talk.yourdomain.com',
        blurb: 'Real-time PGP-encrypted messaging that works seamlessly across the web console and the mobile app over one shared relay — a message typed on the web reaches the phone and vice-versa. Nothing ever touches disk; every message vanishes the moment it is read.',
        points: ['PGP end-to-end encrypted, web ⇄ app interop', 'Read-once — a message self-destructs on read', 'Short TTL (5 min – 1 h); nothing is persisted', 'Safety-number verification defeats a MITM swap', 'Dedicated talk.yourdomain.com relay — bypasses any CDN', 'Auto-provisioned by the installer — one DNS record'],
        cta: { label: 'How VayuTalk works', href: 'https://github.com/johalputt/VayuPress/blob/main/docs/adr/ADR-0131-vayutalk-ephemeral-messaging.md' } },
      { key: 'vayushield', name: 'VayuShield', icon: '🛡', rgb: '244,63,94', badge: '', tag: 'Enterprise bot shield & anti-DDoS — no Cloudflare', host: '/os/shield',
        blurb: 'A five-layer, self-learning bot & DDoS shield built into the same binary. It keeps Save and refresh working even during a volumetric flood, jails offenders in minutes and forgives automatically — search engines and AI assistants are always welcome.',
        points: ['Admin-sovereignty lane survives a volumetric flood', 'Fixed-memory fair-shed catches spoofed botnets', 'Reputation brain jails offenders & auto-forgives', 'Silent, self-calibrating proof-of-work challenges', 'Search engines & AI crawlers always allowed', 'Optional kernel-level nftables + XDP offload'],
        cta: { label: 'Read the architecture', href: 'https://github.com/johalputt/VayuPress/blob/main/docs/adr/ADR-0111-vayushield-bot-protection-and-analytics.md' } },
      { key: 'vayuanalytics', name: 'VayuAnalytics', icon: '📊', rgb: '232,121,249', badge: '', tag: 'Product analytics without surveillance', host: '/os/analytics',
        blurb: 'Pageviews, sessions, top pages, funnels, retention, revenue and a live visitor panel — computed entirely from your local SQLite. No cookies, no localStorage, no IP or User-Agent ever stored, and no consent banner required.',
        points: ['Cookieless — a daily-rotating salted hash identity', 'No IP or User-Agent ever stored — nothing to leak', 'Funnels, retention, revenue & UTM campaigns', 'Live visitor panel, updated in real time', 'Offline country table — no external GeoIP call', 'GDPR by design — no consent banner needed'],
        cta: { label: 'See the dashboard', href: 'https://github.com/johalputt/VayuPress' } },
      { key: 'website', name: 'Website & Blog', icon: '🌐', rgb: '45,212,191', badge: '', tag: 'A real website and a Ghost-class blog', host: 'yourdomain.com',
        blurb: 'Eleven elegant, modern-minimalist site templates plus a best-in-class block editor with lossless Markdown and HTML modes, whole-site themes, members, newsletters and SEO — every word in your own SQLite, styled entirely from your own origin.',
        points: ['11 modern-minimalist business templates', 'Ghost-class block editor · Markdown · HTML', 'Whole-site themes with a live Theme Studio', 'Memberships, paywalls & newsletters', 'Server-rendered Mermaid, math & code', 'SEO, JSON-LD & sitemaps baked in'],
        cta: { label: 'View the templates', href: 'https://github.com/johalputt/VayuPress' } },
      { key: 'vayupgp', name: 'VayuPGP', icon: '🔑', rgb: '245,158,11', badge: '', tag: 'Privacy by architecture', host: 'WKD',
        blurb: 'Every account gets a modern PGP keypair automatically. Private keys are AES-256-GCM encrypted at rest and never logged; public keys are published via Web Key Directory so any GPG client can discover them. It is the engine under both VayuMail and VayuTalk.',
        points: ['Auto keypair generated per account', 'Private keys AES-256-GCM encrypted at rest', 'Encrypt · decrypt · sign · verify · rotate', 'WKD discovery for any GPG client', 'Powers both VayuMail and VayuTalk', 'End-to-end encryption with nothing to bolt on'],
        cta: { label: 'Learn about VayuPGP', href: 'https://github.com/johalputt/VayuPress' } }
    ],
    features: [
      { icon: '🏪', orb: 'rgba(251,146,60,.4)', title: 'A business website, not just a blog', desc: 'Pick from eleven elegant, modern-minimalist templates and deploy a full site — hero, offerings with prices, gallery, hours and contact — editing every word from VayuOS with a live preview. Your domain shows the website, the blog moves to blog.yourdomain.com and mail to mail.yourdomain.com.', tags: ['11 templates', 'website + blog + mail', 'edit from VayuOS'] },
      { icon: '✍️', orb: 'rgba(45,212,191,.5)', title: "A writing studio you'll love", desc: 'A calm, Ghost-clean block editor with tables, toggles, task lists, math, callouts, code, self-hosted audio and privacy-first video. Switch to whole-document Markdown or raw HTML and back — losslessly — with a slash menu, ⌘K palette, focus mode and split preview.', tags: ['blocks · Markdown · HTML', 'image drag/drop/link', 'slash menu · ⌘K'] },
      { icon: '✉️', orb: 'rgba(56,189,248,.5)', title: 'VayuMail — your own mail server', desc: 'Send and receive from your own domain without renting a mail provider. Outbound mail is DKIM-signed and delivered straight to the recipient\'s server; IMAP and the official app read your mail. Connect a phone in one scan with a rotating, revocable app password.', tags: ['DKIM-signed', 'Android app', 'rotating setup QR'] },
      { icon: '🔑', orb: 'rgba(167,139,250,.5)', title: 'VayuPGP — privacy by architecture', desc: 'Every account gets a modern PGP keypair automatically. Private keys are encrypted at rest and never logged; you can encrypt, decrypt, sign, verify and rotate keys, and a Web Key Directory lets any GPG client discover your public key.', tags: ['auto keypairs', 'encrypted at rest', 'WKD discovery'] },
      { icon: '🛡️', orb: 'rgba(52,211,153,.5)', title: 'VayuOS — one calm control room', desc: 'Every operator tool lives in one fast, strict-CSP admin at /os: posts, the editor, media, members, SEO, theme studio, mail and security. Creating an account quietly provisions a mailbox and PGP keys for you. One front door, no sprawl.', tags: ['single admin', '/os', 'strict CSP'] },
      { icon: '🎨', orb: 'rgba(244,114,182,.55)', title: 'Themes that restyle the whole site', desc: 'Pick a theme and the entire public site changes — navigation, hero, post cards, article pages, author box, comments and footer. Tune logo, colours, fonts and your social share image with a live preview. Served from your own origin — no CDNs.', tags: ['whole-site themes', 'live preview', 'self-hosted CSS'] },
      { icon: '💳', orb: 'rgba(74,222,128,.4)', title: 'Memberships & paywalls', desc: 'Turn readers into members with passwordless magic-link sign-in — no reader passwords ever stored. Define priced tiers, publish a themed pricing page, and gate any article as public, members or paid. An optional Stripe webhook handles paid upgrades.', tags: ['magic-link', 'tiers & paywalls', 'member portal'] },
      { icon: '📈', orb: 'rgba(232,121,249,.4)', title: 'Analytics without surveillance', desc: 'See pageviews, top pages, referrers and trends — without cookies, consent banners, or storing a single IP address. Visitor identity is a daily-rotating salted hash that can\'t be traced to a person, and everything lives in your own SQLite.', tags: ['cookieless', 'no PII', 'no consent banner'] },
      { icon: '🛡', orb: 'rgba(244,63,94,.45)', title: 'VayuShield — sovereign bot defense', desc: 'A replacement for Cloudflare Bot Management and Google Analytics, built into the binary. It fingerprints every client from its TLS handshake (JA3/JA4), challenges the suspicious with silent proof-of-work, and shields availability under load — welcoming good bots and AI assistants.', tags: ['JA3/JA4 + PoW', 'rate-limit + load-shed', 'no Cloudflare/GA'] },
      { icon: '📦', orb: 'rgba(245,158,11,.4)', title: 'Update & back up in one click', desc: 'Install the latest signed release from inside VayuOS — checksum- and signature-verified, database backed up first, binary swapped atomically. Export your entire site — database, media, mailboxes and PGP keys — as one AES-256-GCM file only you can open.', tags: ['signed updates', 'encrypted backups', 'reversible'] },
      { icon: '🔁', orb: 'rgba(129,140,248,.4)', title: 'Bring your whole archive', desc: 'Move in from Ghost, WordPress, Substack, Medium, Hugo, Jekyll, Notion or a plain folder of Markdown — slugs, tags, dates, images and drafts preserved. The importers are resumable and gentle enough to migrate a 200,000-post site onto a small VPS.', tags: ['Ghost · WP · Substack', 'Medium · Hugo · Jekyll', 'resumable'] }
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
      { title: 'No invisible dependencies', body: 'Zero third-party fonts on your readers. Zero analytics. Zero CDN trackers. The only external calls are ones you explicitly configure.' },
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

  /* ═══ 7 · renderers ═══ */
  // hero pill row
  (function pills() {
    var row = $('#pillRow'); if (!row) return;
    DATA.pillars.forEach(function (p) { row.appendChild(el('span', 'pill', esc(p))); });
  })();

  // products — tabs + panel
  (function products() {
    var tabs = $('#prodTabs'), panel = $('#prodPanel'); if (!tabs || !panel) return;
    var active = 0;
    function paint() {
      var p = DATA.products[active];
      tabs.style.setProperty('--pr', p.rgb);
      panel.style.setProperty('--pr', p.rgb);
      Array.prototype.forEach.call(tabs.children, function (b, i) { b.classList.toggle('active', i === active); b.setAttribute('aria-selected', i === active ? 'true' : 'false'); });
      var pts = p.points.map(function (x) { return '<li>' + esc(x) + '</li>'; }).join('');
      panel.innerHTML = '<div class="pp-in panel-anim">' +
        '<div class="pp-left"><div class="pn">' + esc(p.tag) + '</div>' +
        '<h3>' + esc(p.name) + '</h3><div class="pp-host">' + esc(p.host) + '</div>' +
        '<p class="pp-blurb">' + esc(p.blurb) + '</p>' +
        '<a class="pp-cta" href="' + esc(p.cta.href) + '" target="_blank" rel="noopener" data-hot>' + esc(p.cta.label) + ' →</a></div>' +
        '<ul class="pp-points">' + pts + '</ul></div>';
    }
    DATA.products.forEach(function (p, i) {
      var b = el('button', 'ptab', '<span class="pi">' + p.icon + '</span>' + esc(p.name) + (p.badge ? '<span class="nw">' + esc(p.badge) + '</span>' : ''));
      b.setAttribute('role', 'tab'); b.style.setProperty('--pr', p.rgb);
      b.addEventListener('click', function () { if (i === active) return; active = i; paint(); });
      tabs.appendChild(b);
    });
    paint();
  })();

  // features
  (function features() {
    var g = $('#featGrid'); if (!g) return;
    DATA.features.forEach(function (f) {
      var tags = f.tags.map(function (t) { return '<span>' + esc(t) + '</span>'; }).join('');
      var c = el('article', 'feat', '<div class="fi">' + f.icon + '</div><h3>' + esc(f.title) + '</h3><p>' + esc(f.desc) + '</p><div class="ftags">' + tags + '</div>');
      c.style.setProperty('--orb', f.orb);
      g.appendChild(c);
    });
  })();

  // gallery marquee + lightbox
  (function gallery() {
    var m = $('#marquee'); if (!m) return;
    var track = el('div', 'marquee-track');
    var loop = DATA.screenshots.concat(DATA.screenshots);
    loop.forEach(function (s, i) {
      var realIdx = i % DATA.screenshots.length;
      var fig = el('figure', 'shot', '<img loading="lazy" src="' + esc(s.src) + '" alt="' + esc(s.label) + ' screenshot"><figcaption class="cap">' + esc(s.label) + '</figcaption>');
      // Graceful placeholder if an image ever fails to load (or in a sandbox
      // where the screenshots are absent): show a branded tile, not a broken icon.
      $('img', fig).addEventListener('error', function () { fig.classList.add('noimg'); this.style.visibility = 'hidden'; });
      fig.addEventListener('click', function () { openLb(realIdx); });
      track.appendChild(fig);
    });
    m.appendChild(track);

    var lb = el('div', 'lb', '<button class="lb-x" aria-label="Close">✕</button><button class="lb-nav lb-prev" aria-label="Previous">‹</button><button class="lb-nav lb-next" aria-label="Next">›</button><figure class="lb-fig"><img alt=""><figcaption class="lb-cap"></figcaption></figure>');
    document.body.appendChild(lb);
    var img = $('img', lb), cap = $('.lb-cap', lb), cur = 0;
    function show() { var s = DATA.screenshots[cur]; img.src = s.src; img.alt = s.label + ' screenshot'; cap.textContent = s.caption; }
    function openLb(i) { cur = i; show(); lb.classList.add('open'); }
    function close() { lb.classList.remove('open'); }
    function move(d) { cur = (cur + d + DATA.screenshots.length) % DATA.screenshots.length; show(); }
    $('.lb-x', lb).addEventListener('click', close);
    $('.lb-prev', lb).addEventListener('click', function (e) { e.stopPropagation(); move(-1); });
    $('.lb-next', lb).addEventListener('click', function (e) { e.stopPropagation(); move(1); });
    lb.addEventListener('click', function (e) { if (e.target === lb) close(); });
    addEventListener('keydown', function (e) { if (!lb.classList.contains('open')) return; if (e.key === 'Escape') close(); if (e.key === 'ArrowLeft') move(-1); if (e.key === 'ArrowRight') move(1); });
  })();

  // compare table
  (function compare() {
    var wrap = $('#compareWrap'); if (!wrap) return;
    function mark(v, own) {
      if (v === 'yes') return '<td class="v' + (own ? ' own' : '') + '"><span class="mk yes" title="Yes">✓</span></td>';
      if (v === 'no') return '<td class="v"><span class="mk no" title="No">✕</span></td>';
      if (v === 'partial') return '<td class="v"><span class="mk part" title="Partial">◐</span></td>';
      return '<td class="v"><span class="mk txt">' + esc(v) + '</span></td>';
    }
    var head = '<tr><th>Capability</th>' + DATA.compareCols.map(function (c) { return '<th>' + esc(c) + '</th>'; }).join('') + '</tr>';
    var body = DATA.compareRows.map(function (r) {
      return '<tr><td>' + esc(r.f) + '</td>' + r.v.map(function (v, i) { return mark(v, i === 0); }).join('') + '</tr>';
    }).join('');
    wrap.innerHTML = '<table class="cmp"><thead>' + head + '</thead><tbody>' + body + '</tbody></table>';
  })();

  // principles
  (function principles() {
    var g = $('#princGrid'); if (!g) return;
    DATA.principles.forEach(function (p, i) {
      g.appendChild(el('div', 'princ', '<div class="pnum">0' + (i + 1) + '</div><h3>' + esc(p.title) + '</h3><p>' + esc(p.body) + '</p>'));
    });
  })();

  // steps
  (function steps() {
    var g = $('#steps'); if (!g) return;
    DATA.steps.forEach(function (s, i) {
      g.appendChild(el('div', 'step', '<div class="sn">Step 0' + (i + 1) + '</div><div class="sl">' + esc(s.label) + '</div><code>' + esc(s.cmd) + '</code>'));
    });
  })();

  // footer columns
  (function footer() {
    var g = $('#footCols'); if (!g) return;
    DATA.footer.forEach(function (col) {
      var links = col.links.map(function (l) { var ext = /^https?:/.test(l.href); return '<a href="' + esc(l.href) + '"' + (ext ? ' target="_blank" rel="noopener"' : '') + '>' + esc(l.label) + '</a>'; }).join('');
      g.appendChild(el('div', 'foot-col', '<h4>' + esc(col.head) + '</h4>' + links));
    });
    var yr = $('#yr'); if (yr) yr.textContent = new Date().getFullYear();
  })();

  /* ═══ 8 · terminal typing ═══ */
  var termTyped = false;
  function typeTerm() {
    if (termTyped) return; termTyped = true;
    var body = $('#termBody'); if (!body) return;
    var lines = [
      ['m', '# 1 · get the sovereign binary'],
      ['cmd', '<span class="c">curl</span> -fsSL https://vayupress.com/install.sh | sh'],
      ['', ''],
      ['m', '# 2 · point it at your domain'],
      ['cmd', '<span class="c">vayupress</span> init <span class="v">--domain</span> yourdomain.com'],
      ['', ''],
      ['m', '# 3 · bring mail, chat & analytics online'],
      ['cmd', '<span class="c">vayupress</span> up <span class="v">--mail --talk --analytics</span>'],
      ['', ''],
      ['g', '✓ website · blog · PGP mail · chat · analytics — live']
    ];
    if (RM) { body.innerHTML = lines.map(function (l) { return l[1] ? (l[0] === 'm' ? '<span class="m">' + l[1] + '</span>' : l[0] === 'g' ? '<span class="g">' + l[1] + '</span>' : l[1]) : ''; }).join('\n'); return; }
    var i = 0;
    (function next() {
      if (i >= lines.length) return;
      var l = lines[i++];
      var span = document.createElement('span');
      if (l[0] === 'm') span.innerHTML = '<span class="m">' + l[1] + '</span>';
      else if (l[0] === 'g') span.innerHTML = '<span class="g">' + l[1] + '</span>';
      else span.innerHTML = l[1];
      span.style.opacity = '0'; span.style.transition = 'opacity .3s';
      body.appendChild(span); body.appendChild(document.createTextNode('\n'));
      requestAnimationFrame(function () { span.style.opacity = '1'; });
      setTimeout(next, l[1] ? 260 : 120);
    })();
  }

  // copy button
  (function copy() {
    var btn = $('#copyBtn'); if (!btn) return;
    var script = 'curl -fsSL https://vayupress.com/install.sh | sh\nvayupress init --domain yourdomain.com\nvayupress up --mail --talk --analytics';
    btn.addEventListener('click', function () {
      if (!navigator.clipboard) return;
      navigator.clipboard.writeText(script).then(function () { btn.textContent = 'Copied ✓'; setTimeout(function () { btn.textContent = 'Copy'; }, 1800); });
    });
  })();

  /* ═══ 9 · live version + stars ═══ */
  (function live() {
    function setVer(t) { if (typeof t === 'string' && /^v?\d/.test(t)) { var v = t.charAt(0) === 'v' ? t : 'v' + t; var e = $('#rel'); if (e) e.textContent = v; } }
    function setStars(n) { if (typeof n === 'number' && n >= 0) { var e = $('#stars'); if (e) e.textContent = n.toLocaleString(); } }
    fetch('assets/version.json', { cache: 'no-cache' }).then(function (r) { return r.ok ? r.json() : null; }).then(function (j) { if (j) setVer(j.tag_name || j.version); }).catch(function () {});
    fetch('https://api.github.com/repos/johalputt/VayuPress/releases/latest', { cache: 'default' }).then(function (r) { return r.ok ? r.json() : null; }).then(function (j) { if (j) setVer(j.tag_name); }).catch(function () {});
    fetch('assets/stars.json', { cache: 'no-cache' }).then(function (r) { return r.ok ? r.json() : null; }).then(function (j) { if (j) setStars(j.stargazers_count); }).catch(function () {});
    fetch('https://api.github.com/repos/johalputt/VayuPress', { cache: 'default' }).then(function (r) { return r.ok ? r.json() : null; }).then(function (j) { if (j) setStars(j.stargazers_count); }).catch(function () {});
  })();
})();
