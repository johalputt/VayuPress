// Halcyon — the theme's one script. Everything on the page reads without it:
// the outline is links, search is a form, topics are links. With it come the
// reading line, the outline that follows the reader, minutes left, reading
// settings (Aa), the appearance switch, the selection toolbar, share, and the
// phone sheets. Every overlay opens from its control and moves focus into
// itself, closes on Escape, on its control or on a click outside, and gives
// focus back; only the full-screen sheets lock the page behind them.
(function () {
  'use strict';
  var d = document, r = d.documentElement, w = window;
  var K = 'vayu-theme', R = 'vp-read';

  function $(s, el) { return (el || d).querySelector(s); }
  function $$(s, el) { return Array.prototype.slice.call((el || d).querySelectorAll(s)); }
  function store(k, v) { try { if (v == null) localStorage.removeItem(k); else localStorage.setItem(k, v); } catch (e) {} }
  function read() { try { return JSON.parse(localStorage.getItem(R) || '{}') || {}; } catch (e) { return {}; } }
  function say(msg) {
    var l = $('#h-live');
    if (!l) { l = d.createElement('p'); l.id = 'h-live'; l.className = 'h-vh'; l.setAttribute('aria-live', 'polite'); d.body.appendChild(l); }
    l.textContent = ''; setTimeout(function () { l.textContent = msg; }, 30);
  }

  // ── Appearance ─────────────────────────────────────────────────────────
  // The reader's choice is stored under the shared theme key; "auto" removes
  // it, and the site's own default (on <html>) applies again.
  function setScheme(s) {
    if (s === 'auto') {
      store(K, null);
      var site = r.getAttribute('data-appearance');
      if (site === 'light' || site === 'dark') r.setAttribute('data-theme', site); else r.removeAttribute('data-theme');
    } else { store(K, s); r.setAttribute('data-theme', s); }
    paintSchemes();
  }
  function current() { try { return localStorage.getItem(K) || 'auto'; } catch (e) { return 'auto'; } }
  function paintSchemes() {
    var c = current();
    $$('[data-h-scheme]').forEach(function (b) { b.setAttribute('aria-pressed', String(b.getAttribute('data-h-scheme') === c)); });
    $$('[data-h-cycle]').forEach(function (b) {
      b.setAttribute('aria-label', c === 'auto' ? 'Appearance: follows the device' : 'Appearance: ' + c);
      var src = $('[data-h-scheme="' + c + '"] svg');
      if (src) { var old = $('svg', b); if (old) b.replaceChild(src.cloneNode(true), old); }
    });
  }
  d.addEventListener('click', function (e) {
    var b = e.target.closest && e.target.closest('[data-h-scheme],[data-h-cycle]');
    if (!b) return;
    if (b.hasAttribute('data-h-cycle')) {
      var order = ['auto', 'light', 'dark'];
      setScheme(order[(order.indexOf(current()) + 1) % 3]);
    } else setScheme(b.getAttribute('data-h-scheme'));
  });

  // ── Reading settings (Aa) ──────────────────────────────────────────────
  function paintReader() {
    var p = read();
    var face = p.face || r.getAttribute('data-site-face');
    var size = p.size || +r.getAttribute('data-site-size');
    var width = p.width || 'standard';
    $$('[data-h-face]').forEach(function (b) { b.setAttribute('aria-pressed', String(b.getAttribute('data-h-face') === face)); });
    $$('[data-h-width]').forEach(function (b) { b.setAttribute('aria-pressed', String(b.getAttribute('data-h-width') === width)); });
    var s = $('[data-h-size]'), o = $('[data-h-size-out]');
    if (s) s.value = size;
    if (o) o.textContent = size + ' px';
  }
  function setRead(k, v) {
    var p = read();
    p[k] = v;
    store(R, JSON.stringify(p));
    if (k === 'width') { if (v === 'standard') r.removeAttribute('data-width'); else r.setAttribute('data-width', v); }
    else r.setAttribute('data-' + k, String(v));
    paintReader();
  }
  d.addEventListener('click', function (e) {
    var b = e.target.closest && e.target.closest('[data-h-face],[data-h-width],[data-h-reset]');
    if (!b) return;
    if (b.hasAttribute('data-h-face')) setRead('face', b.getAttribute('data-h-face'));
    else if (b.hasAttribute('data-h-width')) setRead('width', b.getAttribute('data-h-width'));
    else {
      store(R, null);
      r.setAttribute('data-face', r.getAttribute('data-site-face'));
      r.setAttribute('data-size', r.getAttribute('data-site-size'));
      r.removeAttribute('data-width');
      setScheme('auto');
      paintReader();
      say('Using the site’s settings');
    }
  });
  var size = $('[data-h-size]');
  if (size) size.addEventListener('input', function () { setRead('size', +size.value); });

  // ── Overlays ───────────────────────────────────────────────────────────
  var open = null, opener = null, scrim = $('[data-h-scrim]');
  var FOCUS = 'a[href],button:not([disabled]),input,select,textarea,[tabindex]:not([tabindex="-1"])';
  function close(back) {
    if (!open) return;
    open.hidden = true;
    if (scrim) scrim.hidden = true;
    r.classList.remove('h-lock');
    $$('[aria-controls="' + open.id + '"]').forEach(function (b) { b.setAttribute('aria-expanded', 'false'); });
    var o = opener;
    open = opener = null;
    if (back !== false && o) o.focus();
  }
  function show(el, btn) {
    close(false);
    open = el; opener = btn;
    el.hidden = false;
    btn.setAttribute('aria-expanded', 'true');
    var sheet = el.classList.contains('h-bsheet');
    if (sheet) { if (scrim) scrim.hidden = false; r.classList.add('h-lock'); }
    else if (w.matchMedia('(min-width: 601px)').matches) {
      // A popover sits under its control, kept on screen.
      var b = btn.getBoundingClientRect();
      el.style.top = (b.bottom + w.scrollY + 8) + 'px';
      el.style.left = Math.max(8, Math.min(b.right + w.scrollX - el.offsetWidth, w.innerWidth - el.offsetWidth - 8)) + 'px';
    }
    if (el.id === 'h-reader') paintReader();
    var f = $(FOCUS, el);
    if (f) f.focus();
  }
  d.addEventListener('click', function (e) {
    var b = e.target.closest && e.target.closest('[data-h-open]');
    if (b) {
      var el = d.getElementById(b.getAttribute('data-h-open'));
      if (el) { if (open === el) close(); else show(el, b); }
      return;
    }
    if (open && !open.contains(e.target)) close(false);
    else if (open && e.target.closest('.h-it[href^="#"]')) close(false);
  });
  d.addEventListener('keydown', function (e) {
    if (!open) return;
    if (e.key === 'Escape') { e.preventDefault(); close(); return; }
    // A modal sheet keeps Tab inside itself.
    if (e.key === 'Tab' && open.getAttribute('aria-modal') === 'true') {
      var f = $$(FOCUS, open);
      if (!f.length) return;
      var a = f[0], z = f[f.length - 1];
      if (e.shiftKey && d.activeElement === a) { e.preventDefault(); z.focus(); }
      else if (!e.shiftKey && d.activeElement === z) { e.preventDefault(); a.focus(); }
    }
  });

  // ── Share and links ────────────────────────────────────────────────────
  // The clipboard API exists only on a secure page; a site served over plain
  // HTTP (a .onion) copies through a selected, off-screen field instead.
  function copy(text, done) {
    if (navigator.clipboard && w.isSecureContext) {
      navigator.clipboard.writeText(text).then(function () { say(done); }, function () {});
      return;
    }
    var t = d.createElement('textarea');
    t.value = text; t.setAttribute('readonly', ''); t.className = 'h-vh';
    d.body.appendChild(t); t.select();
    try { if (d.execCommand('copy')) say(done); } catch (e) {}
    d.body.removeChild(t);
  }
  d.addEventListener('click', function (e) {
    var b = e.target.closest && e.target.closest('[data-h-share],[data-h-copylink]');
    if (!b) return;
    var url = location.href.split('#')[0];
    if (b.hasAttribute('data-h-share') && navigator.share) navigator.share({ title: d.title, url: url }).catch(function () {});
    else copy(url, 'Link copied');
  });

  // ── Reading: the header line, the outline, minutes left ────────────────
  var bar = $('[data-h-bar]'), line = $('[data-h-line]'), body = $('.h-article .content');
  var lefts = $$('[data-h-left]');
  var minutes = 0;
  if (body) minutes = Math.max(1, Math.round(body.textContent.split(/\s+/).length / 200));
  // An author's own anchor can hold a % that does not decode; that heading is
  // left out of the tracking rather than stopping the whole script.
  var heads = $$('.h-outline a').map(function (a) {
    var id = a.getAttribute('href').slice(1);
    try { id = decodeURIComponent(id); } catch (e) {}
    return d.getElementById(id);
  }).filter(Boolean);
  var items = $$('.h-outline li');
  var ticking = false;
  function frame() {
    ticking = false;
    var y = w.scrollY;
    if (bar) bar.classList.toggle('is-scrolled', y > 4);
    if (!body) return;
    var top = body.getBoundingClientRect().top + y, h = body.offsetHeight - w.innerHeight * 0.6;
    var p = Math.min(1, Math.max(0, (y - top + w.innerHeight * 0.3) / Math.max(h, 1)));
    if (line) line.style.transform = 'scaleX(' + p + ')';
    var m = Math.ceil(minutes * (1 - p));
    var t = p >= 1 ? 'The end' : m <= 1 ? 'A minute left' : m + ' minutes left';
    lefts.forEach(function (l) { if (l.textContent !== t) l.textContent = t; });
    var on = -1;
    for (var i = 0; i < heads.length; i++) if (heads[i].getBoundingClientRect().top < 120) on = i;
    items.forEach(function (li, i) { li.classList.toggle('is-on', i === on); });
  }
  function tick() { if (!ticking) { ticking = true; w.requestAnimationFrame(frame); } }
  w.addEventListener('scroll', tick, { passive: true });
  w.addEventListener('resize', tick, { passive: true });
  frame();

  // ── The selection toolbar ──────────────────────────────────────────────
  if (body && r.hasAttribute('data-selection')) {
    var sel = null;
    var btn = function (label, act) {
      var b = d.createElement('button');
      b.type = 'button'; b.textContent = label;
      b.addEventListener('mousedown', function (e) { e.preventDefault(); });
      b.addEventListener('click', act);
      return b;
    };
    var hide = function () { if (sel) sel.hidden = true; };
    var text = function () { return String(w.getSelection()).trim(); };
    var place = function () {
      var s = w.getSelection();
      if (!s.rangeCount || s.isCollapsed || !body.contains(s.anchorNode) || text().length < 3) { hide(); return; }
      if (!sel) {
        sel = d.createElement('div');
        sel.className = 'h-pop h-selbar';
        sel.setAttribute('role', 'toolbar');
        sel.setAttribute('aria-label', 'Selected text');
        sel.appendChild(btn('Share quote', function () {
          var q = '“' + text() + '” — ' + location.href.split('#')[0];
          if (navigator.share) navigator.share({ text: q }).catch(function () {}); else copy(q, 'Quote copied');
          hide();
        }));
        sel.appendChild(btn('Link to here', function () {
          copy(location.href.split('#')[0] + '#:~:text=' + encodeURIComponent(text().slice(0, 120)), 'Link copied');
          hide();
        }));
        sel.appendChild(btn('Copy', function () { copy(text(), 'Copied'); hide(); }));
        d.body.appendChild(sel);
      }
      sel.hidden = false;
      var b = s.getRangeAt(0).getBoundingClientRect();
      sel.style.top = Math.max(8, b.top + w.scrollY - sel.offsetHeight - 10) + 'px';
      sel.style.left = Math.max(8, Math.min(b.left + w.scrollX + b.width / 2 - sel.offsetWidth / 2, w.innerWidth - sel.offsetWidth - 8)) + 'px';
    };
    d.addEventListener('mouseup', function () { setTimeout(place, 0); });
    d.addEventListener('keyup', function (e) { if (e.shiftKey) place(); else if (e.key === 'Escape') hide(); });
    d.addEventListener('selectionchange', function () { if (!text()) hide(); });
  }

  paintSchemes();
})();
