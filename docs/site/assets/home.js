'use strict';
/* ═══════════════════════════════════════════════════════════════════════
   VAYUPRESS — "3D Creator"-style landing engine (adapted from the Jack brief).
   Lenis smooth scroll + vanilla Magnet / FadeIn / AnimatedText / scroll-driven
   marquee / sticky-scaling project cards. Everything degrades: no Lenis →
   native scroll; reduced-motion → static, fully readable.
   ═══════════════════════════════════════════════════════════════════════ */
(function () {
  var RM = matchMedia('(prefers-reduced-motion: reduce)').matches;
  var COARSE = matchMedia('(hover: none)').matches;
  var $ = function (s, r) { return (r || document).querySelector(s); };
  var el = function (t, c, h) { var e = document.createElement(t); if (c) e.className = c; if (h != null) e.innerHTML = h; return e; };
  var esc = function (s) { return String(s).replace(/[&<>"]/g, function (m) { return ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[m]; }); };
  var clamp = function (v, a, b) { return Math.min(b, Math.max(a, v)); };

  var SHOTS = ['homepage', 'admin-os-dashboard', 'admin-os-editor', 'admin-os-theme', 'admin-os-posts', 'member-signup', 'admin-os-media', 'admin-os-seo', 'admin-os-analytics', 'admin-os-security'];
  var shot = function (n) { return 'screenshots/' + n + '.png'; };

  var SERVICES = [
    { name: '3D Website & Blog', desc: 'A real business website and a Ghost-class blog — eleven templates, a block editor with lossless Markdown and HTML, whole-site themes, members and SEO — every word in your own SQLite.' },
    { name: 'Private PGP Mail', desc: 'Your own mail server: SMTP send & receive, IMAP, POP3, DKIM signing and direct-to-MX delivery, with an official mobile app and automatic MX / SPF / DKIM / DMARC.' },
    { name: 'Encrypted Chat', desc: 'VayuTalk — ephemeral, end-to-end-encrypted messaging across web and app over one relay. Read-once, nothing persisted, nothing on disk to subpoena.' },
    { name: 'Analytics & Shield', desc: 'Cookieless analytics with no IP or PII stored, plus a self-learning bot shield that replaces Cloudflare Bot Management and Google Analytics — all inside the one binary.' },
    { name: 'One Control Room', desc: 'VayuOS — publishing, mail, chat, analytics and security in a single fast, strict-CSP admin. One front door, no sprawl, negligible RAM and CPU.' }
  ];

  var PROJECTS = [
    { num: '01', cat: 'Control Plane', name: 'VayuOS', imgs: ['admin-os-dashboard', 'admin-os-posts', 'admin-os-editor'] },
    { num: '02', cat: 'Sovereign Mail & Members', name: 'VayuMail', imgs: ['member-signup', 'admin-os-media', 'admin-os-analytics'] },
    { num: '03', cat: 'Website · SEO · Shield', name: 'Solaris Site', imgs: ['homepage', 'admin-os-seo', 'admin-os-theme'] }
  ];

  /* ═══ render ═══ */
  function render() {
    var r1 = $('#mrow1'), r2 = $('#mrow2');
    function fill(row, list) {
      for (var t = 0; t < 3; t++) list.forEach(function (n) {
        row.appendChild(el('div', 'marquee-tile', '<img loading="lazy" src="' + shot(n) + '" alt="VayuPress preview">'));
      });
    }
    if (r1) fill(r1, SHOTS.slice(0, 5));
    if (r2) fill(r2, SHOTS.slice(5, 10));

    var sl = $('#servicesList');
    if (sl) SERVICES.forEach(function (s, i) {
      var n = '0' + (i + 1);
      var item = el('div', 'service', '<div class="service-num">' + n + '</div><div class="service-body"><div class="service-name">' + esc(s.name) + '</div><div class="service-desc">' + esc(s.desc) + '</div></div>');
      item.setAttribute('data-fade', ''); item.setAttribute('data-y', '30'); item.setAttribute('data-delay', String(i * 0.1));
      sl.appendChild(item);
    });

    var cc = $('#projectCards');
    if (cc) PROJECTS.forEach(function (p) {
      var slot = el('div', 'card-slot');
      var card = el('div', 'card',
        '<div class="card-top"><div class="card-topleft"><div class="card-num">' + p.num + '</div>' +
        '<div class="card-meta"><div class="card-cat">' + esc(p.cat) + '</div><div class="card-name">' + esc(p.name) + '</div></div></div>' +
        '<a class="live-btn" href="https://github.com/johalputt/VayuPress" target="_blank" rel="noopener">Live Project</a></div>' +
        '<div class="card-grid"><div class="card-col1"><img loading="lazy" src="' + shot(p.imgs[0]) + '" alt="' + esc(p.name) + '"><img loading="lazy" src="' + shot(p.imgs[1]) + '" alt="' + esc(p.name) + '"></div>' +
        '<div class="card-col2"><img loading="lazy" src="' + shot(p.imgs[2]) + '" alt="' + esc(p.name) + '"></div></div>');
      slot.appendChild(card); cc.appendChild(slot);
    });

    var yr = $('#yr'); if (yr) yr.textContent = new Date().getFullYear();

    // graceful branded placeholder if any image fails to load
    Array.prototype.forEach.call(document.querySelectorAll('.marquee-tile img, .card img'), function (img) {
      img.addEventListener('error', function () { this.onerror = null; this.classList.add('ph'); this.src = 'data:image/gif;base64,R0lGODlhAQABAAAAACH5BAEKAAEALAAAAAABAAEAAAICTAEAOw=='; });
    });
  }

  /* ═══ FadeIn (whileInView, once) ═══ */
  function fades() {
    var els = Array.prototype.slice.call(document.querySelectorAll('[data-fade]'));
    function prep(e) {
      var x = parseFloat(e.getAttribute('data-x') || '0'), y = parseFloat(e.getAttribute('data-y') || '30');
      e.style.transform = 'translate(' + x + 'px,' + y + 'px)';
      e.style.transition = 'none';
    }
    function show(e) {
      var d = parseFloat(e.getAttribute('data-delay') || '0');
      e.style.transition = 'opacity 0.7s cubic-bezier(0.25,0.1,0.25,1) ' + d + 's, transform 0.7s cubic-bezier(0.25,0.1,0.25,1) ' + d + 's';
      e.style.opacity = '1'; e.style.transform = 'none';
    }
    if (RM || !('IntersectionObserver' in window)) { els.forEach(function (e) { e.style.opacity = '1'; }); return; }
    els.forEach(prep);
    var io = new IntersectionObserver(function (list) {
      list.forEach(function (en) { if (en.isIntersecting) { show(en.target); io.unobserve(en.target); } });
    }, { rootMargin: '50px', threshold: 0 });
    els.forEach(function (e) { io.observe(e); });
  }

  /* ═══ Magnet (mouse-following) ═══ */
  function magnet() {
    var wrap = $('#magnet'), inner = $('#magnetInner');
    if (!wrap || !inner || COARSE || RM) return;
    var PAD = 150, STR = 3, active = false;
    addEventListener('pointermove', function (e) {
      var r = wrap.getBoundingClientRect();
      var cx = r.left + r.width / 2, cy = r.top + r.height / 2;
      var dx = e.clientX - cx, dy = e.clientY - cy;
      var inside = e.clientX > r.left - PAD && e.clientX < r.right + PAD && e.clientY > r.top - PAD && e.clientY < r.bottom + PAD;
      if (inside) {
        if (!active) { inner.style.transition = 'transform 0.3s ease-out'; active = true; }
        inner.style.transform = 'translate3d(' + (dx / STR) + 'px,' + (dy / STR) + 'px,0)';
      } else if (active) {
        active = false; inner.style.transition = 'transform 0.6s ease-in-out'; inner.style.transform = 'translate3d(0,0,0)';
      }
    }, { passive: true });
  }

  /* ═══ AnimatedText (char-by-char scroll reveal) ═══ */
  var animText = null;
  function buildAnimText() {
    var p = $('#aboutText'); if (!p) return;
    var text = p.textContent; p.textContent = '';
    var chars = [];
    for (var i = 0; i < text.length; i++) {
      var span = el('span', 'ch'); span.textContent = text[i] === ' ' ? ' ' : text[i];
      span.style.opacity = RM ? '1' : '0.2'; p.appendChild(span); chars.push(span);
    }
    animText = { el: p, chars: chars };
  }
  function updateAnimText() {
    if (!animText || RM) return;
    var r = animText.el.getBoundingClientRect(), vh = innerHeight;
    var startY = vh * 0.8, endY = vh * 0.2;
    var prog = (startY - r.top) / ((startY - endY) + r.height);
    prog = clamp(prog, 0, 1);
    var active = prog * animText.chars.length;
    for (var i = 0; i < animText.chars.length; i++) {
      animText.chars[i].style.opacity = String(clamp(0.2 + (active - i) * 0.9, 0.2, 1));
    }
  }

  /* ═══ marquee (scroll-driven parallax) ═══ */
  var mq = null;
  function buildMarquee() {
    var sec = $('#marquee'), r1 = $('#mrow1'), r2 = $('#mrow2');
    if (!sec || !r1 || !r2) return;
    mq = { sec: sec, r1: r1, r2: r2, sw1: r1.scrollWidth / 3, sw2: r2.scrollWidth / 3 };
  }
  function updateMarquee() {
    if (!mq) return;
    var top = mq.sec.getBoundingClientRect().top + scrollY;
    var offset = (scrollY - top + innerHeight) * 0.3;
    mq.r1.style.transform = 'translateX(' + ((offset - 200) - mq.sw1) + 'px)';
    mq.r2.style.transform = 'translateX(' + (-(offset - 200)) + 'px)';
  }

  /* ═══ project cards (sticky-stacking scale) ═══ */
  var cards = [];
  function buildCards() {
    var slots = Array.prototype.slice.call(document.querySelectorAll('.card-slot'));
    var total = slots.length;
    var base = matchMedia('(min-width:768px)').matches ? 128 : 96;
    cards = slots.map(function (slot, i) {
      var card = $('.card', slot);
      card.style.top = (base + i * 28) + 'px';
      return { slot: slot, card: card, target: 1 - (total - 1 - i) * 0.03, i: i, total: total };
    });
  }
  function updateCards() {
    if (RM) return;
    var wrap = $('#projectCards'); if (!wrap) return;
    var r = wrap.getBoundingClientRect(), vh = innerHeight;
    var prog = clamp((0 - r.top) / (r.height - vh || 1), 0, 1);
    cards.forEach(function (c) {
      var startP = c.i / c.total;
      var t = clamp((prog - startP) / ((1 - startP) || 1), 0, 1);
      var s = 1 + (c.target - 1) * t;
      c.card.style.transform = 'scale(' + s + ')';
    });
  }

  /* ═══ scroll loop ═══ */
  function onScroll() { updateMarquee(); updateAnimText(); updateCards(); }
  function remeasure() { if (mq) { mq.sw1 = mq.r1.scrollWidth / 3; mq.sw2 = mq.r2.scrollWidth / 3; } }

  function boot() {
    render();
    buildMarquee(); buildAnimText(); buildCards();
    fades(); magnet();

    if (window.Lenis && !RM) {
      var lenis = new Lenis({ lerp: 0.1, smoothWheel: true });
      (function raf(t) { lenis.raf(t); requestAnimationFrame(raf); })(0);
      lenis.on('scroll', onScroll);
    }
    addEventListener('scroll', onScroll, { passive: true });
    addEventListener('resize', function () { remeasure(); buildCards(); onScroll(); }, { passive: true });
    onScroll();
    addEventListener('load', function () { remeasure(); buildCards(); onScroll(); });
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', boot);
  else boot();
})();
