/* VayuPress VayuOS — Bootstrap
 * Sovereign · Self-hosted · Zero-CDN · Strict-CSP
 * No eval, no new Function, no innerHTML with untrusted data.
 * All DOM mutation via textContent / createElement / appendChild.
 */
'use strict';
(function () {

/* ── Helpers ─────────────────────────────────────────────────── */
const $ = (sel, root) => (root || document).querySelector(sel);
const $$ = (sel, root) => Array.from((root || document).querySelectorAll(sel));
const on = (el, ev, fn) => el && el.addEventListener(ev, fn);

/* ── Theme ───────────────────────────────────────────────────── */
(function initTheme() {
  // The theme attribute lives on the .vp-os element itself (<body>), so the
  // .vp-os[data-theme] token overrides win over the base .vp-os tokens. Go
  // renders data-theme + data-admin-theme on <body>; default to auto (follows
  // the OS). The toggle cycles light → dark → auto and persists to settings.
  const el = document.body;
  if (!el.dataset.theme) { el.dataset.theme = el.dataset.adminTheme || 'auto'; }
  // The sign-in pages cannot read the saved setting (nobody is signed in yet), so
  // they keep their own copy under this key (os-theme.js). Mirroring the console's
  // choice into it means signing out does not flip the theme back.
  const remember = function (t) { try { localStorage.setItem('vp-os-theme', t); } catch (e) {} };
  remember(el.dataset.theme);

  const btn = $('.topbar-theme-btn');
  if (!btn) return;
  btn.title = 'Theme: ' + el.dataset.theme;
  btn.addEventListener('click', function () {
    const themes = ['light', 'dark', 'auto'];
    const cur = themes.indexOf(el.dataset.theme);
    const next = themes[(cur + 1) % themes.length];
    el.dataset.theme = next;
    btn.title = 'Theme: ' + next;
    remember(next);
    // Saved server-side so every device follows it. A failed save is said out
    // loud: this page shows the new theme either way, so silence would read as
    // saved until the next page load quietly reverted it.
    const failed = function () { toast('Theme changed for this page only — it could not be saved.', 'warn'); };
    fetch('/os/api/settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': cookie('vp_csrf') },
      body: JSON.stringify({ key: 'admin.theme', value: next }),
    }).then(function (r) { if (!r.ok) failed(); }, failed);
  });
})();

/* ── Cookies ─────────────────────────────────────────────────── */
function cookie(name) {
  // Take everything after the first '=' so base64 values keep any '=' padding.
  var row = document.cookie.split('; ').find(function (r) { return r.startsWith(name + '='); });
  return row ? row.slice(name.length + 1) : '';
}

/* ── Shared JSON POST (Wave 3.11) ──────────────────────────────
   window.vpPost: one CSRF-carrying JSON POST helper for the console. The
   per-page inline scripts have their own (ops-block) variant that reloads on
   success; this one takes callbacks and never reloads, so island-style
   handlers can update in place. Every error path toasts — a silent catch is a
   lie by omission.

   Two fixes worth naming: a body that is NOT JSON (a proxy error page, a 502,
   an expired session redirect) is now reported as its HTTP status instead of
   being dressed up as "Network error"; and an expired CSRF token says so,
   because "reload the page" is the actual remedy. */
window.vpCsrf = function () { return cookie('vp_csrf'); };
window.vpPost = function (url, body, onok, onerr) {
  fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': window.vpCsrf() },
    body: JSON.stringify(body || {}),
  })
    .then(function (r) {
      return r.json().catch(function () { return {}; }).then(function (d) {
        return { ok: r.ok, d: d, status: r.status };
      });
    })
    .then(function (res) {
      if (res.ok) { if (onok) onok(res.d); return; }
      var msg = (res.d && (res.d.detail || res.d.title || res.d.error || res.d.message)) ||
        (res.status === 403 ? 'session token expired — reload the page and try again'
          : 'Request failed (' + res.status + ')');
      if (onerr) onerr(res.d, msg); else toast(msg, 'error');
    })
    .catch(function (e) {
      if (onerr) onerr(null, String(e)); else toast('Network error', 'error');
    });
};

/* ── Toast system ────────────────────────────────────────────── */
function toast(msg, kind) {
  // Only ok/error/info/warn have styles in vayuos.css. Call sites have passed
  // 'success' and 'danger' too — those rendered an unstyled toast with no colour,
  // which is a silent loss of the signal. Normalise the aliases here so no call
  // site (including future ones) can drop its colour.
  kind = ({ success: 'ok', danger: 'error', warning: 'warn' })[kind] || kind || 'info';
  var container = $('.toast-container');
  if (!container) {
    container = document.createElement('div');
    container.className = 'toast-container';
    document.body.appendChild(container);
  }
  var el = document.createElement('div');
  el.className = 'toast toast--' + kind;

  var icon = document.createElement('span');
  icon.appendChild(window.vpIcon(kind === 'ok' ? 'check-c' : kind === 'error' ? 'error' : kind === 'warn' ? 'warn' : 'info'));
  icon.setAttribute('aria-hidden', 'true');

  var text = document.createElement('span');
  text.textContent = msg;

  el.appendChild(icon);
  el.appendChild(text);
  container.appendChild(el);

  setTimeout(function () {
    el.classList.add('leaving');
    setTimeout(function () { el.remove(); }, 200);
  }, 3800);
  return el; // callers may attach a click handler (e.g. the new-mail notifier)
}
window.vpToast = toast;

/* ── Sidebar drawer (mobile) ─────────────────────────────────
   Single source of truth for the slide-in nav. Binds every toggle (the topbar
   hamburger AND the bottom-bar "Menu" button — anything matching .menu-toggle
   or [data-action="toggle-sidebar"]). The drawer closes on overlay tap, on Esc,
   when a nav link is followed, and when the viewport grows back to desktop.
   Keeping all toggles here avoids the previous double-handling (a second
   document-level handler that cancelled the open). */
(function initSidebar() {
  var sidebar = $('.sidebar');
  if (!sidebar) return;
  var overlay = $('.sidebar-overlay');
  var toggles = $$('.menu-toggle, [data-action="toggle-sidebar"]');
  var body = document.body;
  var desktop = window.matchMedia('(min-width: 769px)');
  // Below this the stylesheet collapses the rail unless the toggle expanded it.
  var roomy = window.matchMedia('(min-width: 1280px)');
  var KEY = 'vp_nav_collapsed';

  function setExpanded(v) {
    toggles.forEach(function (b) { b.setAttribute('aria-expanded', v ? 'true' : 'false'); });
  }
  function store(v) { try { localStorage.setItem(KEY, v ? '1' : '0'); } catch (e) {} }
  // The operator's choice, or null when they have not made one.
  function chosen() { try { var v = localStorage.getItem(KEY); return v === '1' ? true : v === '0' ? false : null; } catch (e) { return null; } }
  function collapsed() {
    if (body.classList.contains('nav-collapsed')) return true;
    return !body.classList.contains('nav-expanded') && !roomy.matches;
  }
  function applyChoice() {
    var c = chosen();
    body.classList.toggle('nav-collapsed', c === true);
    body.classList.toggle('nav-expanded', c === false);
    setExpanded(!collapsed());
  }

  // ── Mobile: slide-in drawer (locks scroll while open) ──────────────────────
  function openDrawer() {
    sidebar.classList.add('open');
    if (overlay) overlay.classList.add('open');
    body.style.overflow = 'hidden';
    setExpanded(true);
  }
  function closeDrawer() {
    sidebar.classList.remove('open');
    if (overlay) overlay.classList.remove('open');
    body.style.overflow = '';
    setExpanded(false);
  }

  // ── Desktop: collapse the sidebar (persisted; never locks scroll) ──────────
  function toggle() {
    if (desktop.matches) {
      store(!collapsed());
      applyChoice();
    } else {
      sidebar.classList.contains('open') ? closeDrawer() : openDrawer();
    }
  }

  // Restore the persisted desktop collapse state on load (desktop only).
  if (desktop.matches) applyChoice(); else setExpanded(false);

  toggles.forEach(function (b) {
    on(b, 'click', function (e) { e.preventDefault(); toggle(); });
  });
  if (overlay) on(overlay, 'click', closeDrawer);
  // On mobile, following a nav link closes the drawer; on desktop the sidebar
  // stays as-is (collapsed or not) so navigation doesn't fight the toggle.
  $$('.sidebar .nav-link').forEach(function (a) { on(a, 'click', function () { if (!desktop.matches) closeDrawer(); }); });
  on(document, 'keydown', function (e) { if (e.key === 'Escape' && !desktop.matches) closeDrawer(); });

  // Crossing the breakpoint: entering desktop closes any open mobile drawer and
  // applies the persisted collapse; entering mobile drops the desktop collapse
  // class (the drawer owns visibility there) and unlocks scroll.
  var onChange = function (e) {
    if (e.matches) {
      closeDrawer();
      applyChoice();
    } else {
      body.classList.remove('nav-collapsed', 'nav-expanded');
      body.style.overflow = '';
    }
  };
  if (desktop.addEventListener) desktop.addEventListener('change', onChange);
  else if (desktop.addListener) desktop.addListener(onChange);
  var onRoom = function () { if (desktop.matches) setExpanded(!collapsed()); };
  if (roomy.addEventListener) roomy.addEventListener('change', onRoom);
  else if (roomy.addListener) roomy.addListener(onRoom);
})();

/* ── Responsive data tables → cards ──────────────────────────
   Generic, zero-config: for every .table-wrap > table.table, copy each column
   header into its body cells as data-label and flag the wrapper .vp-stackable.
   CSS then folds the table into labelled cards on phones. Skips tables that opt
   out (data-no-stack), have fewer than two columns, or lead with a selection
   checkbox (management grids that read better as a horizontal scroll).

   Runs on first load AND after every HTMX swap, scoped to the swapped subtree —
   so tables delivered by HTMX (analytics, VayuShield sections, the mailbox
   list) get the same phone-friendly card layout as server-rendered ones,
   instead of a wide horizontal scroll. Idempotent: cells already labelled and
   wrappers already flagged are skipped, so repeated swaps never re-walk work. */
  function stackTablesIn(root) {
    var scope = (root && root.querySelectorAll) ? root : document;
    var tables = scope.querySelectorAll('.table-wrap > table.table');
    // A swap target may itself BE the table's wrapper; include that case.
    Array.prototype.forEach.call(tables, function (table) {
      var wrap = table.parentElement;
      if (wrap.classList.contains('vp-stackable')) return;
      if (wrap.hasAttribute('data-no-stack') || table.hasAttribute('data-no-stack')) return;

      var heads = $$('thead th', table);
      if (heads.length < 2) return;
      if (heads[0].querySelector('input')) return; // select-all column → keep scroll

      var labels = heads.map(function (th) { return th.textContent.trim(); });
      $$('tbody tr', table).forEach(function (tr) {
        var cells = tr.children;
        if (cells.length !== labels.length) return; // colspan / empty-state rows
        for (var i = 0; i < cells.length; i++) {
          if (!cells[i].hasAttribute('data-label')) cells[i].setAttribute('data-label', labels[i]);
        }
      });
      wrap.classList.add('vp-stackable');
    });
  }
  stackTablesIn(document);
  document.body.addEventListener('htmx:afterSwap', function (e) {
    stackTablesIn(e.target || document);
  });

/* ── In-place refresh after a change ──────────────────────────
   Re-renders the page's content without reloading the page: the same URL is
   fetched and #main-content's children replaced. A reload threw away the toast
   that reported the change, the scroll position and the focus, so an operator
   saw a flash and had to trust it had worked. Scripts in the new markup are
   dropped — the page's own already ran, and they listen by delegation, so the
   new controls work. Any failure falls back to a reload: the change happened on
   the server, and a page that does not show it is worse than a flash. */
window.vpRefresh = function () {
  var main = document.getElementById('main-content');
  if (!main || !window.DOMParser) { location.reload(); return Promise.resolve(); }
  var focusId = document.activeElement && document.activeElement.id;
  // Sections the operator had open stay open: the new markup carries the
  // server's defaults, and a goal added inside a collapsed-by-default section
  // would otherwise vanish from view the moment it was saved.
  var summaryText = function (d) { var s = d.querySelector('summary'); return s ? s.textContent.trim() : ''; };
  var wasOpen = $$('details[open]', main).map(summaryText);
  return fetch(location.href, { credentials: 'same-origin', headers: { 'Accept': 'text/html' } })
    .then(function (r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.text(); })
    .then(function (html) {
      var next = new DOMParser().parseFromString(html, 'text/html').getElementById('main-content');
      if (!next) throw new Error('no content'); // e.g. the session ended and this is the sign-in page
      $$('script', next).forEach(function (s) { s.remove(); });
      var frag = document.createDocumentFragment();
      Array.prototype.forEach.call(next.childNodes, function (n) { frag.appendChild(document.importNode(n, true)); });
      main.replaceChildren(frag);
      $$('details', main).forEach(function (d) { if (wasOpen.indexOf(summaryText(d)) !== -1) d.open = true; });
      if (window.htmx) window.htmx.process(main);
      var f = focusId && document.getElementById(focusId);
      if (f) f.focus({ preventScroll: true });
      document.dispatchEvent(new CustomEvent('vp:refreshed'));
    })
    .catch(function () { location.reload(); });
};

/* ── hx-confirm in the console's own dialog ────────────────────
   HTMX asks with the browser's native confirm() unless the htmx:confirm event
   is answered, so every hx-confirm (deleting mail, deleting a mailbox, shield
   actions) was still unstyled chrome that blocks the tab, while the scripts
   beside it had all moved to vpConfirm. Answered here once, for all of them. */
document.addEventListener('htmx:confirm', function (e) {
  var q = e.detail && e.detail.question;
  if (!q || typeof window.vpConfirm !== 'function') return; // no prompt asked, or no dialog to ask it with
  e.preventDefault();
  window.vpConfirm({ title: q }, function () { e.detail.issueRequest(true); });
});

/* ── Command bar (Cmd+K / Ctrl+K, render 03) ─────────────────────
   Three kinds of result: Go to (the rail's own gated index of pages, then
   posts), Actions (a page to open or one request to run) and Settings (every
   row, the list Search settings reads). The highlighted result is previewed
   beside the list; Tab narrows to one kind. Built with textContent and
   elements only: no markup from any response is ever inserted. */
(function initCommandBar() {
  var backdrop = $('#cmd-backdrop');
  var input = $('#cmd-input');
  var results = $('#cmd-results');
  var preview = $('[data-cmd-preview]');
  var kindChip = $('[data-cmd-kind]');
  if (!backdrop || !input || !results) return;

  var KINDS = ['All', 'Go to', 'Actions', 'Settings'];
  var kind = 0;
  var index = null;
  var items = [];      // [{el, data}]
  var activeIdx = -1;
  var pageCache = {};  // href -> summary, for the live page preview
  var previewTimer = null;

  function open() {
    backdrop.removeAttribute('hidden');
    input.value = '';
    kind = 0;
    if (kindChip) kindChip.textContent = KINDS[kind];
    input.focus();
    loadIndex();
    render();
  }
  function close() {
    backdrop.setAttribute('hidden', '');
    activeIdx = -1;
  }

  document.addEventListener('keydown', function (e) {
    if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
      e.preventDefault();
      backdrop.hasAttribute('hidden') ? open() : close();
      return;
    }
    if (backdrop.hasAttribute('hidden')) return;
    if (e.key === 'Escape') close();
    else if (e.key === 'ArrowDown') { e.preventDefault(); setActive(activeIdx + 1); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setActive(activeIdx - 1); }
    else if (e.key === 'Enter') { e.preventDefault(); if (items[activeIdx]) items[activeIdx].el.click(); }
    else if (e.key === 'Tab') { e.preventDefault(); cycleKind(e.shiftKey ? -1 : 1); }
  });
  // The chip is the same filter for a pointer or a finger, which have no Tab.
  if (kindChip) kindChip.addEventListener('click', function () { cycleKind(1); input.focus(); });
  function cycleKind(step) {
    kind = (kind + step + KINDS.length) % KINDS.length;
    if (kindChip) kindChip.textContent = KINDS[kind];
    render();
  }
  backdrop.addEventListener('click', function (e) { if (e.target === backdrop) close(); });
  $$('.topbar-cmd, .sa-search').forEach(function (b) { b.addEventListener('click', open); });
  input.addEventListener('input', render);

  function loadIndex() {
    if (index !== null) return;
    fetch('/os/api/cmd-index')
      .then(function (r) { if (!r.ok) throw new Error(r.status); return r.json(); })
      .then(function (data) { index = data; render(); })
      .catch(function () { index = { posts: [], actions: [], settings: [] }; render(); });
  }

  // The label with every place the query appears marked, not only the first.
  function label(text, q) {
    var el = document.createElement('span');
    el.className = 'cmd-item__label';
    var low = text.toLowerCase(), at = 0, i;
    while (q && (i = low.indexOf(q, at)) >= 0) {
      el.appendChild(document.createTextNode(text.slice(at, i)));
      var m = document.createElement('mark');
      m.textContent = text.slice(i, i + q.length);
      el.appendChild(m);
      at = i + q.length;
    }
    el.appendChild(document.createTextNode(text.slice(at)));
    return el;
  }

  function row(data, q) {
    var el = document.createElement(data.href ? 'a' : 'button');
    el.className = 'cmd-item';
    el.setAttribute('role', 'option');
    if (data.href) el.href = data.href; else el.type = 'button';
    var ic = document.createElement('span');
    ic.className = 'cmd-item__icon';
    ic.appendChild(window.vpIcon(data.icon));
    el.appendChild(ic);
    el.appendChild(label(data.text, q));
    if (data.where) {
      var w = document.createElement('span');
      w.className = 'cmd-item__hint';
      w.textContent = data.where;
      el.appendChild(w);
    }
    el.addEventListener('click', function () { close(); if (data.post) run(data); });
    el.addEventListener('mousemove', function () { var n = items.findIndex(function (it) { return it.el === el; }); if (n !== activeIdx) setActive(n); });
    return el;
  }

  function group(title, list, q) {
    if (!list.length) return;
    var g = document.createElement('div');
    g.className = 'cmd-group-label';
    g.textContent = title;
    results.appendChild(g);
    list.forEach(function (d) {
      var el = row(d, q);
      results.appendChild(el);
      items.push({ el: el, data: d });
    });
  }

  function render() {
    var q = input.value.toLowerCase().trim();
    results.textContent = '';
    items = [];
    activeIdx = -1;
    if (!index) {
      var loading = document.createElement('div');
      loading.className = 'cmd-group-label';
      loading.textContent = 'Loading…';
      results.appendChild(loading);
      showPreview(null);
      return;
    }
    var hit = function (s) { return !q || s.toLowerCase().indexOf(q) >= 0; };
    var want = function (k) { return kind === 0 || KINDS[kind] === k; };
    var cap = kind === 0 ? (q ? 6 : 4) : 30;

    if (want('Go to')) {
      var pages = $$('[data-sa-index] a').filter(function (a) { return hit(a.textContent); }).map(function (a) {
        return { kind: 'page', text: a.textContent, href: a.getAttribute('href'), icon: a.getAttribute('data-icon') };
      });
      var posts = (index.posts || []).filter(function (p) { return q && (hit(p.label) || p.slug.indexOf(q) >= 0); }).map(function (p) {
        return { kind: 'post', text: p.label, href: '/os/editor/' + p.slug, icon: 'content', where: p.status === 'draft' ? 'Draft' : 'Post', post: null, p: p };
      });
      group('Go to', pages.concat(posts).slice(0, cap), q);
    }
    if (want('Actions')) {
      group('Actions', (index.actions || []).filter(function (a) { return hit(a.label); }).slice(0, cap).map(function (a) {
        return { kind: 'action', text: a.label, href: a.href || null, post: a.post || null, icon: a.icon, hint: a.hint, done: a.done };
      }), q);
    }
    if (want('Settings') && (q || kind !== 0)) {
      group('Settings', (index.settings || []).filter(function (st) { return hit(st.label + ' ' + st.where); }).slice(0, cap).map(function (st) {
        return { kind: 'setting', text: st.label, href: st.href, icon: 'settings', where: st.where, hint: st.hint };
      }), q);
    }
    if (!items.length) {
      var empty = document.createElement('div');
      empty.className = 'cmd-group-label';
      empty.textContent = q ? 'Nothing matches “' + input.value.trim() + '”.' : 'Nothing to show.';
      results.appendChild(empty);
    }
    setActive(0);
  }

  function setActive(n) {
    if (!items.length) { showPreview(null); return; }
    n = (n + items.length) % items.length;
    if (items[activeIdx]) { items[activeIdx].el.classList.remove('cmd-item--active'); items[activeIdx].el.removeAttribute('aria-selected'); }
    activeIdx = n;
    items[n].el.classList.add('cmd-item--active');
    items[n].el.setAttribute('aria-selected', 'true');
    items[n].el.scrollIntoView({ block: 'nearest' });
    showPreview(items[n].data);
  }

  // One request, and what the server said about it. A backup answers 200
  // with ok:false when its test restore fails, so the body decides.
  function run(d) {
    if (window.vpToast) window.vpToast(d.text + '…', 'info');
    window.vpPost(d.post, {}, function (res) {
      if (res && res.ok === false) { window.vpToast((res.detail || d.text + ' did not complete.'), 'error'); return; }
      window.vpToast((res && (res.detail || res.message)) || d.done || 'Done.', 'ok');
    });
  }

  function line(cls, text) {
    var el = document.createElement('div');
    el.className = cls;
    el.textContent = text;
    return el;
  }

  function showPreview(d) {
    if (!preview) return;
    clearTimeout(previewTimer);
    preview.textContent = '';
    if (!d) return;
    if (d.kind === 'page') {
      var parts = d.text.split(' › ');
      preview.appendChild(line('cmd-preview__path', parts.slice(0, -1).join(' › ') || 'Go to'));
      preview.appendChild(line('cmd-preview__title', parts[parts.length - 1]));
      // The page itself, as it is now: its line under the title and its
      // figures. Fetched only while the pane is on screen, once per page.
      if (preview.offsetParent === null) return;
      var cached = pageCache[d.href];
      if (cached) { pageSummary(cached); return; }
      previewTimer = setTimeout(function () {
        fetch(d.href, { credentials: 'same-origin' }).then(function (r) { return r.ok ? r.text() : ''; }).then(function (html) {
          var doc = new DOMParser().parseFromString(html, 'text/html');
          var main = doc.querySelector('main') || doc.body;
          var sub = main.querySelector('.page-sub');
          var figs = Array.prototype.slice.call(main.querySelectorAll('.stat-card')).slice(0, 4).map(function (c) {
            var l = c.querySelector('.stat-card__label'), v = c.querySelector('.stat-card__value');
            return [l ? l.textContent.trim() : '', v ? v.textContent.trim().replace(/\s+/g, ' ') : ''];
          }).filter(function (f) { return f[0] && f[1]; });
          pageCache[d.href] = { sub: sub ? sub.textContent.trim() : '', figs: figs };
          if (items[activeIdx] && items[activeIdx].data === d) pageSummary(pageCache[d.href]);
        }).catch(function () {});
      }, 180);
      return;
    }
    if (d.kind === 'post') {
      preview.appendChild(line('cmd-preview__path', d.p.status === 'draft' ? 'Draft' : 'Published'));
      preview.appendChild(line('cmd-preview__title', d.text));
      if (d.p.updated && window.vpRelTime) preview.appendChild(line('cmd-preview__meta', 'Updated ' + window.vpRelTime(d.p.updated)));
      if (d.p.excerpt) preview.appendChild(line('cmd-preview__text', d.p.excerpt));
      return;
    }
    if (d.kind === 'action') {
      preview.appendChild(line('cmd-preview__path', d.post ? 'Runs now' : 'Opens'));
      preview.appendChild(line('cmd-preview__title', d.text));
      if (d.hint) preview.appendChild(line('cmd-preview__text', d.hint));
      if (d.post) preview.appendChild(line('cmd-preview__meta', 'What the server answers is shown when it finishes.'));
      return;
    }
    preview.appendChild(line('cmd-preview__path', 'Settings › ' + d.where));
    preview.appendChild(line('cmd-preview__title', d.text));
    if (d.hint) preview.appendChild(line('cmd-preview__text', d.hint));
  }

  function pageSummary(sum) {
    if (sum.sub) preview.appendChild(line('cmd-preview__text', sum.sub));
    if (!sum.figs.length) return;
    var list = document.createElement('dl');
    list.className = 'cmd-preview__facts';
    sum.figs.forEach(function (f) {
      var dt = document.createElement('dt'); dt.textContent = f[0];
      var dd = document.createElement('dd'); dd.textContent = f[1];
      list.appendChild(dt); list.appendChild(dd);
    });
    preview.appendChild(list);
  }
})();

/* ── Posts search (client-side) ────────────────────────────────
   Removed (Wave 3.11): the HTMX server-side search replaced this, and its
   input hooks no longer exist in the Go templates — a handler for a selector
   nothing renders is dead weight that pretends a feature exists. The
   selector-parity test pins this. */

/* ── Quick compose ───────────────────────────────────────────── */
(function initQuickCompose() {
  var input = $('#quick-compose-input');
  if (!input) return;
  input.addEventListener('keydown', function (e) {
    if (e.key !== 'Enter') return;
    var title = input.value.trim();
    if (!title) return;
    input.disabled = true;
    var csrf = cookie('vp_csrf');
    fetch('/os/api/posts/quick-create', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
      body: JSON.stringify({ title: title }),
    })
    .then(function (r) { return r.json(); })
    .then(function (data) {
      if (data.slug) {
        window.location.href = '/os/editor/' + data.slug;
      } else {
        toast(data.error || 'Could not create post', 'error');
        input.disabled = false;
      }
    })
    .catch(function () { toast('Network error', 'error'); input.disabled = false; });
  });
})();

/* ── Data-action dispatcher ──────────────────────────────────
   Generic click router for [data-action] buttons. Note: 'toggle-sidebar' is
   intentionally NOT handled here — initSidebar binds those elements directly so
   the drawer open/close (with overlay + scroll-lock) has a single owner. */
document.addEventListener('click', function (e) {
  var el = e.target.closest('[data-action]');
  if (!el) return;
  var action = el.dataset.action;
  var actions = {
    // (room for future generic actions)
  };
  if (actions[action]) { e.preventDefault(); actions[action](el); }
});

/* ── Relative time ───────────────────────────────────────────── */
function relativeTime(iso) {
  var d = new Date(iso);
  var diff = (Date.now() - d.getTime()) / 1000;
  if (diff < 60)  return 'just now';
  if (diff < 3600) return Math.floor(diff / 60) + 'm ago';
  if (diff < 86400) return Math.floor(diff / 3600) + 'h ago';
  if (diff < 604800) return Math.floor(diff / 86400) + 'd ago';
  return d.toLocaleDateString();
}
window.vpRelTime = relativeTime;

/* ── Activity feed ───────────────────────────────────────────── */
(function initActivityFeed() {
  var feed = $('#activity-feed');
  if (!feed) return;
  var feedTimer = null;

  function renderError() {
    feed.innerHTML = '';
    var err = document.createElement('div');
    err.className = 'table-empty';
    err.textContent = 'Activity feed is unavailable right now — it will retry shortly.';
    feed.appendChild(err);
  }

  function loadFeed() {
    fetch('/os/api/activity')
      .then(function (r) { if (!r.ok) throw new Error('feed ' + r.status); return r.json(); })
      .then(function (data) {
        feed.innerHTML = '';
        if (!data || !data.length) {
          var empty = document.createElement('div');
          empty.className = 'table-empty';
          empty.textContent = 'No recent activity.';
          feed.appendChild(empty);
          return;
        }
        data.forEach(function (item) {
          // Every row that knows where the work happens is a link to it —
          // a feed you cannot act on is a log, not a feed.
          var row = document.createElement(item.href ? 'a' : 'div');
          row.className = 'activity-item';
          if (item.href) row.href = item.href;

          var icon = document.createElement('div');
          icon.className = 'activity-icon activity-icon--' + (item.kind || 'system');
          icon.appendChild(window.vpIcon(item.icon || 'pulse'));
          icon.setAttribute('aria-hidden', 'true');

          var body = document.createElement('div');
          body.className = 'activity-body';

          var text = document.createElement('div');
          text.className = 'activity-text';
          text.textContent = item.text || '';

          var time = document.createElement('div');
          time.className = 'activity-time';
          time.textContent = item.time ? relativeTime(item.time) : '';

          body.appendChild(text);
          body.appendChild(time);
          row.appendChild(icon);
          row.appendChild(body);
          feed.appendChild(row);
        });
      })
      .catch(renderError);
  }

  loadFeed();
  // Relative timestamps go stale in place ("just now" for an hour); re-render
  // every minute so the words always match the clock.
  feedTimer = setInterval(loadFeed, 60000);
  document.addEventListener('visibilitychange', function () {
    if (document.visibilityState === 'visible' && feedTimer) {
      loadFeed();
    }
  });
})();

/* ── Media library (Phase 4) ─────────────────────────────────── */
(function initMedia() {
  /* Media library (render 07): a table or grid of every file, an inspector
     for the selected one, a context menu on the selection (right-click, the
     menu key or Shift+F10), and uploads with progress and time left. Every
     name, alt text and use is set with textContent; nothing from the server
     is parsed as markup. */
  var list = $('[data-media-list]');
  if (!list) return;
  var inspector = $('[data-media-inspector]');
  var menu = $('[data-media-menu]');
  var uploads = $('[data-media-uploads]');
  var search = $('[data-media-search]');
  var empty = $('[data-media-empty]');
  var input = $('[data-media-input]');
  var drop = $('[data-media-drop]');
  var items = [], shown = [], selected = [], filter = 'all', view = 'list';
  var mac = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
  try { view = localStorage.getItem('vp_media_view') === 'grid' ? 'grid' : 'list'; } catch (e) {}

  function el(tag, cls, text) { var e = document.createElement(tag); if (cls) e.className = cls; if (text != null) e.textContent = text; return e; }
  function fmtSize(b) { return b < 1024 ? b + ' B' : b < 1048576 ? (b / 1024).toFixed(b < 10240 ? 1 : 0) + ' KB' : (b / 1048576).toFixed(1) + ' MB'; }
  function fmtDay(unix) { return new Date(unix * 1000).toLocaleDateString([], { day: '2-digit', month: 'short' }); }
  function fmtWhen(unix) { return new Date(unix * 1000).toLocaleString([], { day: '2-digit', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit' }); }
  function usedIn(it) {
    if (!it.uses.length) return 'Not used';
    var n = { post: 0, page: 0, other: [] };
    it.uses.forEach(function (u) {
      if (u.label.indexOf('Post · ') === 0) n.post++;
      else if (u.label.indexOf('Page · ') === 0) n.page++;
      else if (n.other.indexOf(u.label.split(' · ')[0]) < 0) n.other.push(u.label.split(' · ')[0]);
    });
    var parts = [];
    if (n.post) parts.push(n.post + ' post' + (n.post === 1 ? '' : 's'));
    if (n.page) parts.push(n.page + ' page' + (n.page === 1 ? '' : 's'));
    return parts.concat(n.other).join(' · ');
  }
  function thumb(it, size) {
    var box = el('span', 'media-thumb media-thumb--' + size);
    if (it.isPdf) box.appendChild(window.vpIcon('doc'));
    else { var img = el('img'); img.src = it.url; img.alt = ''; img.loading = 'lazy'; img.decoding = 'async'; box.appendChild(img); }
    return box;
  }

  function load() {
    fetch('/os/api/media', { headers: { Accept: 'application/json' } })
      .then(function (r) { if (!r.ok) throw new Error(r.status); return r.json(); })
      .then(function (d) {
        items = d.items || [];
        selected = selected.filter(function (n) { return items.some(function (it) { return it.name === n; }); });
        draw();
      })
      .catch(function () { if (window.vpToast) window.vpToast('The media library could not be read.', 'error'); });
  }

  function draw() {
    var q = (search && search.value || '').toLowerCase().trim();
    shown = items.filter(function (it) {
      if (filter === 'image' && it.isPdf) return false;
      if (filter === 'pdf' && !it.isPdf) return false;
      return !q || it.title.toLowerCase().indexOf(q) >= 0 || (it.alt || '').toLowerCase().indexOf(q) >= 0;
    });
    var hadFocus = list.contains(document.activeElement);
    list.textContent = '';
    if (empty) empty.hidden = shown.length > 0;
    if (view === 'grid') {
      var grid = el('div', 'media-grid');
      shown.forEach(function (it) {
        var c = el('div', 'media-card');
        c.appendChild(thumb(it, 'lg'));
        c.appendChild(el('span', 'media-card__name', it.title));
        c.appendChild(el('span', 'media-card__meta', fmtSize(it.size)));
        grid.appendChild(wire(c, it));
      });
      list.appendChild(grid);
    } else {
      var wrap = el('div', 'table-wrap');
      var t = el('table', 'table media-table');
      var head = el('tr');
      ['Name', 'Kind', 'Size', 'Added', 'Used in'].forEach(function (h) { head.appendChild(el('th', null, h)); });
      var thead = el('thead'); thead.appendChild(head); t.appendChild(thead);
      var tb = el('tbody');
      shown.forEach(function (it) {
        var tr = el('tr', 'media-row');
        var name = el('span', 'media-row__name');
        name.appendChild(thumb(it, 'sm'));
        name.appendChild(el('span', 'media-row__title', it.title));
        var cell = el('td'); cell.appendChild(name); tr.appendChild(cell);
        tr.appendChild(el('td', 'muted', it.kind));
        tr.appendChild(el('td', 'media-row__num', fmtSize(it.size)));
        tr.appendChild(el('td', 'media-row__num', fmtDay(it.mod)));
        tr.appendChild(el('td', it.uses.length ? null : 'muted', usedIn(it)));
        tb.appendChild(wire(tr, it));
      });
      t.appendChild(tb); wrap.appendChild(t); list.appendChild(wrap);
    }
    // One tab stop into the list: the selected file, else the first. A redraw
    // after a rename or a delete keeps the keyboard where it was.
    var stop = $('[aria-selected="true"]', list) || $('[data-name]', list);
    if (stop) { stop.tabIndex = 0; if (hadFocus) stop.focus({ preventScroll: true }); }
    inspect();
  }

  // A row or card: selectable by click (Ctrl/Cmd adds to the selection),
  // by the arrow keys, and with a menu on right-click or the menu key.
  function wire(node, it) {
    node.tabIndex = -1;
    node.setAttribute('data-name', it.name);
    node.setAttribute('aria-selected', selected.indexOf(it.name) >= 0 ? 'true' : 'false');
    node.addEventListener('click', function (e) { choose(it.name, e.metaKey || e.ctrlKey); });
    node.addEventListener('dblclick', function () { window.open(it.url, '_blank', 'noopener'); });
    // The browser fires contextmenu for the menu key and Shift+F10 too, at a
    // point on the focused file, so this one listener serves all three; a
    // keydown handler for those keys opened the menu twice.
    node.addEventListener('contextmenu', function (e) {
      e.preventDefault();
      if (selected.indexOf(it.name) < 0) choose(it.name, false);
      openMenu(e.clientX, e.clientY);
    });
    return node;
  }
  function nodes() { return $$('[data-name]', list); }
  function choose(name, add) {
    if (add) {
      var at = selected.indexOf(name);
      if (at >= 0) selected.splice(at, 1); else selected.push(name);
    } else selected = [name];
    nodes().forEach(function (n) { n.setAttribute('aria-selected', selected.indexOf(n.getAttribute('data-name')) >= 0 ? 'true' : 'false'); });
    var cur = $('[data-name="' + name + '"]', list);
    if (cur) { nodes().forEach(function (n) { n.tabIndex = -1; }); cur.tabIndex = 0; cur.focus({ preventScroll: true }); }
    inspect();
  }
  function current() { return items.filter(function (it) { return selected.indexOf(it.name) >= 0; }); }

  list.addEventListener('keydown', function (e) {
    var all = nodes(); if (!all.length) return;
    var at = all.indexOf(document.activeElement);
    var one = current()[0];
    if (e.key === 'ArrowDown' || e.key === 'ArrowRight') { e.preventDefault(); choose(all[Math.min(all.length - 1, at + 1)].getAttribute('data-name'), false); }
    else if (e.key === 'ArrowUp' || e.key === 'ArrowLeft') { e.preventDefault(); choose(all[Math.max(0, at - 1)].getAttribute('data-name'), false); }
    else if (e.key === ' ' && one) { e.preventDefault(); window.open(one.url, '_blank', 'noopener'); }
    else if (e.key === 'F2' && one) { e.preventDefault(); rename(one); }
    else if ((e.key === 'Delete' || e.key === 'Backspace') && selected.length) { e.preventDefault(); trash(); }
    else if ((e.metaKey || e.ctrlKey) && e.key === 'c' && one) { e.preventDefault(); copy(one); }
  });

  // ── The inspector ─────────────────────────────────────────────
  function fact(dl, label, value) {
    dl.appendChild(el('dt', null, label));
    var dd = el('dd'); if (typeof value === 'string') dd.textContent = value; else dd.appendChild(value);
    dl.appendChild(dd);
    return dd;
  }
  function inspect() {
    if (!inspector) return;
    inspector.textContent = '';
    var sel = current();
    if (!sel.length) { inspector.appendChild(el('p', 'table-empty', 'Select a file to see its details.')); return; }
    if (sel.length > 1) {
      inspector.appendChild(el('div', 'media-inspector__title', sel.length + ' files selected'));
      inspector.appendChild(el('div', 'media-inspector__meta', fmtSize(sel.reduce(function (t, it) { return t + it.size; }, 0)) + ' together'));
      var del = el('button', 'btn btn--danger btn--sm', 'Move ' + sel.length + ' files to trash'); del.type = 'button';
      del.addEventListener('click', trash); inspector.appendChild(del);
      return;
    }
    var it = sel[0];
    var pv = el('div', 'media-inspector__preview');
    pv.appendChild(thumb(it, 'xl'));
    inspector.appendChild(pv);
    inspector.appendChild(el('div', 'media-inspector__title', it.title));
    var meta = el('div', 'media-inspector__meta', it.kind + ' · ' + fmtSize(it.size));
    inspector.appendChild(meta);
    var img = $('img', pv);
    if (img) img.addEventListener('load', function () { meta.textContent = img.naturalWidth + ' × ' + img.naturalHeight + ' · ' + it.kind + ' · ' + fmtSize(it.size); });
    var dl = el('dl', 'media-inspector__facts');
    if (!it.isPdf) {
      var alt = el('textarea', 'textarea media-inspector__alt'); alt.rows = 2; alt.value = it.alt || ''; alt.placeholder = 'Describe the picture';
      alt.setAttribute('aria-label', 'Alt text');
      alt.addEventListener('change', function () {
        window.vpPost('/os/api/media/alt', { name: it.name, alt: alt.value }, function () { it.alt = alt.value.trim(); window.vpToast('Alt text saved.', 'ok'); });
      });
      fact(dl, 'Alt text', alt);
    }
    fact(dl, 'Added', fmtWhen(it.mod));
    var uses = el('div', 'media-inspector__uses');
    if (!it.uses.length) uses.textContent = 'Not used anywhere';
    it.uses.forEach(function (u) { var a = el('a', null, u.label); a.href = u.href; uses.appendChild(a); });
    fact(dl, 'Used in', uses).setAttribute('data-media-uses', '');
    inspector.appendChild(dl);
    var row = el('div', 'media-inspector__actions');
    [['Copy link', function () { copy(it); }, 'btn btn--sm'], ['Rename', function () { rename(it); }, 'btn btn--sm'],
     ['Download', function () { download(it); }, 'btn btn--sm'], ['Move to trash', trash, 'btn btn--danger btn--sm']].forEach(function (b) {
      var btn = el('button', b[2], b[0]); btn.type = 'button'; btn.addEventListener('click', b[1]); row.appendChild(btn);
    });
    inspector.appendChild(row);
  }

  // ── Actions ───────────────────────────────────────────────────
  function copy(it) {
    var full = location.origin + it.url;
    (navigator.clipboard ? navigator.clipboard.writeText(full) : Promise.reject()).then(
      function () { window.vpToast('Link copied.', 'ok'); },
      function () { window.vpPrompt({ title: 'Copy the link', label: 'Link', value: full, confirm: 'Done' }); });
  }
  function download(it) {
    var a = el('a'); a.href = it.url; a.download = it.title; document.body.appendChild(a); a.click(); a.remove();
  }
  function rename(it) {
    window.vpPrompt({ title: 'Rename', label: 'Name', value: it.title, confirm: 'Rename',
      message: 'The name is how you find it here. Its link, and every post that uses it, stay as they are.' }, function (v) {
      if (v == null || !v.trim() || v.trim() === it.title) return;
      window.vpPost('/os/api/media/name', { name: it.name, title: v }, function (d) { it.title = d.title; draw(); window.vpToast('Renamed.', 'ok'); });
    });
  }
  function trash() {
    var sel = current(); if (!sel.length) return;
    var used = sel.filter(function (it) { return it.uses.length; });
    var one = sel.length === 1;
    window.vpConfirm({
      title: one ? 'Move “' + sel[0].title + '” to trash?' : 'Move ' + sel.length + ' files to trash?',
      message: used.length
        ? (one ? 'It is used in ' + usedIn(sel[0]) + '; those will show a broken image.' : used.length + ' of them are in use, and those places will show a broken image.') + ' This cannot be undone.'
        : 'Nothing uses ' + (one ? 'it' : 'them') + '. This cannot be undone.',
      confirm: 'Move to trash'
    }, function () {
      window.vpPost('/os/api/media/delete', { names: sel.map(function (it) { return it.name; }) }, function (d) {
        window.vpToast((d.deleted || 0) + ' file' + (d.deleted === 1 ? '' : 's') + ' moved to trash.', 'ok');
        selected = []; load();
      });
    });
  }

  // ── The context menu ──────────────────────────────────────────
  function openMenu(x, y) {
    if (!menu) return;
    var sel = current(), one = sel.length === 1 ? sel[0] : null;
    menu.textContent = '';
    function item(icon, label, key, fn, danger) {
      var b = el('button', 'sa-menu__item' + (danger ? ' sa-menu__item--danger' : '')); b.type = 'button'; b.setAttribute('role', 'menuitem');
      b.appendChild(window.vpIcon(icon)); b.appendChild(el('span', 'sa-menu__text', label));
      if (key) b.appendChild(el('kbd', 'sa-menu__key', key));
      b.addEventListener('click', function () { closeMenu(); fn(); });
      menu.appendChild(b);
    }
    function sep() { menu.appendChild(el('div', 'sa-menu__sep')); }
    if (one) {
      item('eye', 'Open preview', 'Space', function () { window.open(one.url, '_blank', 'noopener'); });
      item('link', 'Copy link', mac ? '⌘C' : 'Ctrl+C', function () { copy(one); });
      item('rename', 'Rename', 'F2', function () { rename(one); });
      sep();
      item('where', 'Show where it’s used', '', function () { var u = $('[data-media-uses]', inspector); if (u) { u.scrollIntoView({ block: 'nearest' }); var a = $('a', u); (a || u).focus && (a || u).focus(); } });
      item('download', 'Download', '', function () { download(one); });
      sep();
    }
    item('trash', one ? 'Move to trash' : 'Move ' + sel.length + ' files to trash', mac ? '⌫' : 'Del', trash, true);
    menu.hidden = false;
    var w = menu.offsetWidth, h = menu.offsetHeight;
    menu.style.left = Math.max(8, Math.min(x, innerWidth - w - 8)) + 'px';
    menu.style.top = Math.max(8, Math.min(y, innerHeight - h - 8)) + 'px';
    var first = $('button', menu); if (first) first.focus();
  }
  function closeMenu() { if (menu && !menu.hidden) { menu.hidden = true; var cur = $('[data-name][tabindex="0"]', list); if (cur) cur.focus({ preventScroll: true }); } }
  if (menu) {
    menu.addEventListener('keydown', function (e) {
      var bs = $$('button', menu), at = bs.indexOf(document.activeElement);
      if (e.key === 'Escape') { e.preventDefault(); closeMenu(); }
      else if (e.key === 'ArrowDown') { e.preventDefault(); bs[(at + 1) % bs.length].focus(); }
      else if (e.key === 'ArrowUp') { e.preventDefault(); bs[(at - 1 + bs.length) % bs.length].focus(); }
    });
    document.addEventListener('click', function (e) { if (!menu.contains(e.target)) closeMenu(); });
    document.addEventListener('scroll', closeMenu, true);
  }

  // ── Upload, with progress and time left ───────────────────────
  // One file at a time, so the time left is measured, not guessed: bytes sent
  // over time taken, applied to the bytes still to go.
  function uploadAll(files) {
    files = Array.prototype.slice.call(files || []);
    if (!files.length || !uploads) return;
    var total = files.reduce(function (t, f) { return t + f.size; }, 0), done = 0, started = Date.now(), failed = [];
    uploads.hidden = false;
    uploads.textContent = '';
    var head = el('div', 'media-uploads__head');
    var title = el('span', 'media-uploads__title', 'Uploading ' + files.length + ' file' + (files.length === 1 ? '' : 's'));
    var pct = el('span', 'media-uploads__pct', '0%');
    head.appendChild(title); head.appendChild(pct);
    var bar = el('progress', 'media-uploads__bar'); bar.max = 100; bar.value = 0;
    var now = el('div', 'media-uploads__now');
    uploads.appendChild(head); uploads.appendChild(bar); uploads.appendChild(now);
    function show(sent, file) {
      var p = total ? Math.min(100, Math.round((done + sent) / total * 100)) : 100;
      var secs = (Date.now() - started) / 1000, rate = (done + sent) / Math.max(secs, 0.25);
      var left = rate > 0 ? Math.ceil((total - done - sent) / rate) : 0;
      pct.textContent = p + '%' + (p < 100 && secs > 1 ? ' · ' + (left < 60 ? left + ' s' : Math.ceil(left / 60) + ' min') + ' left' : '');
      bar.value = p;
      if (file) now.textContent = file.name + (sent >= file.size ? ' — making web versions' : '');
    }
    (function next(i) {
      if (i >= files.length) {
        title.textContent = failed.length ? (files.length - failed.length) + ' of ' + files.length + ' uploaded' : files.length + ' file' + (files.length === 1 ? '' : 's') + ' uploaded';
        pct.textContent = ''; bar.value = 100;
        now.textContent = failed.length ? failed.join(' · ') : '';
        uploads.classList.toggle('is-failed', failed.length > 0);
        load();
        if (!failed.length) setTimeout(function () { uploads.hidden = true; }, 4000);
        return;
      }
      var f = files[i], fd = new FormData(), x = new XMLHttpRequest();
      fd.append('file', f);
      x.open('POST', '/os/api/media/upload');
      x.setRequestHeader('X-CSRF-Token', window.vpCsrf());
      x.upload.onprogress = function (e) { show(e.loaded, f); };
      x.onload = function () {
        if (x.status >= 300) { var m = ''; try { var j = JSON.parse(x.responseText); m = (j.error && j.error.message) || j.detail || ''; } catch (err) {} failed.push(f.name + ': ' + (m || 'refused (' + x.status + ')')); }
        done += f.size; show(0, null); next(i + 1);
      };
      x.onerror = function () { failed.push(f.name + ': the connection dropped'); done += f.size; next(i + 1); };
      show(0, f);
      x.send(fd);
    })(0);
  }

  // ── Wiring ────────────────────────────────────────────────────
  if (search) search.addEventListener('input', draw);
  $$('[data-media-filter]').forEach(function (b) {
    b.addEventListener('click', function () {
      $$('[data-media-filter]').forEach(function (x) { x.classList.toggle('is-active', x === b); });
      filter = b.getAttribute('data-media-filter'); draw();
    });
  });
  $$('[data-media-view]').forEach(function (b) {
    b.addEventListener('click', function () {
      view = b.getAttribute('data-media-view');
      try { localStorage.setItem('vp_media_view', view); } catch (e) {}
      $$('[data-media-view]').forEach(function (x) { x.setAttribute('aria-pressed', x === b ? 'true' : 'false'); });
      draw();
    });
    b.setAttribute('aria-pressed', b.getAttribute('data-media-view') === view ? 'true' : 'false');
  });
  var up = $('[data-media-upload]');
  if (up && input) up.addEventListener('click', function () { input.click(); });
  if (input) input.addEventListener('change', function () { uploadAll(input.files); input.value = ''; });
  // The whole page takes a drop, as the page says; the list lights up to
  // show where the files will land.
  if (drop) {
    document.addEventListener('dragover', function (e) { e.preventDefault(); drop.classList.add('is-drop'); });
    document.addEventListener('dragleave', function (e) { if (!e.relatedTarget) drop.classList.remove('is-drop'); });
    document.addEventListener('drop', function (e) { e.preventDefault(); drop.classList.remove('is-drop'); uploadAll(e.dataTransfer && e.dataTransfer.files); });
  }
  load();
})();

/* ── Notification centre ─────────────────────────────────────── */
/* The topbar bell opens an expandable panel of actionable notifications
 * (rendered server-side as plain links, so clicking a row navigates straight
 * to the page that clears it). This only toggles visibility — no fetch, no
 * innerHTML — keeping the strict CSP intact. */
(function initNotifications() {
  var wrap = $('[data-notif]');
  if (!wrap) return;
  var btn = $('[data-notif-toggle]', wrap);
  var panel = $('[data-notif-panel]', wrap);
  if (!btn || !panel) return;
  function open() {
    panel.hidden = false;
    wrap.classList.add('is-open');
    btn.setAttribute('aria-expanded', 'true');
  }
  function close() {
    panel.hidden = true;
    wrap.classList.remove('is-open');
    btn.setAttribute('aria-expanded', 'false');
  }
  on(btn, 'click', function (e) {
    e.preventDefault();
    e.stopPropagation();
    panel.hidden ? open() : close();
  });
  // Dismiss on outside click or Escape.
  on(document, 'click', function (e) { if (!wrap.contains(e.target)) close(); });
  on(document, 'keydown', function (e) { if (e.key === 'Escape') close(); });

  // Times in the viewer's clock, and the last day split into Today and
  // Yesterday by it: the server does not know the viewer's timezone, so it
  // sends UTC and a neutral "Last 24 hours".
  var recent = $('[data-notif-recent]', wrap);
  if (recent) {
    var today = new Date().toDateString();
    var label = $('.notif-group__label', recent);
    var firstDay = null;
    $$('time[datetime]', recent).forEach(function (t) {
      var d = new Date(t.getAttribute('datetime'));
      if (isNaN(d)) return;
      t.textContent = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
      var day = d.toDateString() === today ? 'Today' : 'Yesterday';
      if (firstDay === null) { firstDay = day; if (label) label.textContent = day; return; }
      if (day !== firstDay && !$('[data-notif-yesterday]', recent)) {
        var y = document.createElement('div');
        y.className = 'notif-group__label';
        y.setAttribute('data-notif-yesterday', '');
        y.textContent = 'Yesterday';
        var row = t.closest('.notif-item');
        row.parentNode.insertBefore(y, row);
      }
    });
  }

  // Mark all read clears the events; what needs the operator stays counted,
  // because a condition is not dealt with by being looked at.
  var seen = $('[data-notif-seen]', wrap);
  if (seen) on(seen, 'click', function (e) {
    e.preventDefault();
    e.stopPropagation();
    window.vpPost('/os/api/notifications/seen', {}, function () {
      $$('.notif-item.is-unread', wrap).forEach(function (r) { r.classList.remove('is-unread'); });
      seen.remove();
      var needs = parseInt(wrap.getAttribute('data-notif-needs'), 10) || 0;
      var badge = $('[data-notif-badge]', wrap);
      if (!badge) return;
      if (needs > 0) { badge.textContent = needs > 99 ? '99+' : String(needs); return; }
      badge.remove();
      btn.classList.remove('topbar-notif__btn--active');
    });
  });
})();

/* ── Login page shake on error ───────────────────────────────── */
(function initLogin() {
  var panel = $('.login-panel');
  if (!panel) return;
  // The error div is rendered server-side; its presence triggers a shake.
  if ($('.login-error', panel)) {
    panel.classList.add('shake');
    panel.addEventListener('animationend', function () { panel.classList.remove('shake'); }, { once: true });
  }
})();

/* ── Installable app (PWA) ───────────────────────────────────────
   Register the console's /os-scoped service worker (privacy-first: never caches
   authenticated pages) and turn the topbar install button into a one-tap "Install
   VayuOS". Handles Chrome/Edge/Android via beforeinstallprompt, and gives iOS —
   which never fires it — an "Add to Home Screen" hint. Hidden once installed. */
(function initPWA() {
  if ('serviceWorker' in navigator) {
    // When a new worker takes control (e.g. after a deploy), reload once so the
    // freshest VayuOS shows immediately — no stale build ever lingers on a device.
    var swReloaded = false;
    navigator.serviceWorker.addEventListener('controllerchange', function () {
      if (swReloaded) return;
      swReloaded = true;
      window.location.reload();
    });
    window.addEventListener('load', function () {
      navigator.serviceWorker.register('/os/sw.js', { scope: '/os/' }).then(function (reg) {
        if (reg && reg.update) { try { reg.update(); } catch (e) { /* no-op */ } }
      }).catch(function () {});
    });
  }
  var btn = document.querySelector('[data-pwa-install]');
  if (!btn) return;
  var standalone = (window.matchMedia && window.matchMedia('(display-mode: standalone)').matches) ||
    window.navigator.standalone === true;
  if (standalone) return; // already installed — nothing to offer

  var isIOS = /iphone|ipad|ipod/i.test(navigator.userAgent);
  var deferred = null;

  window.addEventListener('beforeinstallprompt', function (e) {
    e.preventDefault();
    deferred = e;
    btn.hidden = false;
  });
  window.addEventListener('appinstalled', function () {
    deferred = null;
    btn.hidden = true;
    if (window.vpToast) window.vpToast('VayuOS installed — find it on your home screen.', 'ok');
  });
  on(btn, 'click', function () {
    if (deferred) {
      deferred.prompt();
      deferred.userChoice.then(function () { deferred = null; btn.hidden = true; });
      return;
    }
    if (window.vpToast) {
      window.vpToast(isIOS
        ? 'To install: tap the Share button, then “Add to Home Screen”.'
        : 'To install: open your browser menu and choose “Install app” / “Add to Home screen”.',
        'info');
    }
  });
  // iOS never fires beforeinstallprompt — reveal the button so iPhone/iPad users
  // still get the install hint.
  if (isIOS) { btn.hidden = false; }
})();

/* ── New-mail notifications ──────────────────────────────────────────────────
   Polls /os/vayumail/unseen on every console page and raises a desktop
   notification the moment any mailbox's unseen count rises — so a new mail in
   ANY mailbox reaches you even when you're on another VayuOS page. Clicking the
   notification opens that mailbox directly. The baseline lives only here (no
   server state); the first poll just records counts and never notifies (so
   already-unread mail is not announced on load). Desktop notifications need a
   secure context (HTTPS); on the http .onion (Tor) the API is unavailable, so it
   degrades to a clickable toast + a title badge. Endpoint auth already scopes
   what you see (admin = all mailboxes; staff = only their own), so this just
   renders whatever it is allowed to return. */
(function initMailNotify() {
  if (!document.querySelector('.vp-os')) return;        // console pages only
  if (document.querySelector('.auth-page')) return;     // not the sign-in shell
  var ENDPOINT = '/os/vayumail/unseen';
  var POLL_MS = 60000;
  var baseline = null;                 // address -> unseen; null until first poll
  var notifyReady = false;
  var baseTitle = document.title;
  var titleCount = 0;

  function secureCtx() { return window.isSecureContext !== false; }
  function ensurePerm() {
    if (!('Notification' in window) || !secureCtx()) return;
    if (Notification.permission === 'granted') { notifyReady = true; return; }
    if (Notification.permission === 'default') {
      try {
        Notification.requestPermission().then(function (p) { notifyReady = (p === 'granted'); });
      } catch (e) { /* Safari <16 uses a callback form; ignore */ }
    }
  }
  function deepLink(key) { return '/os/vayumail/inbox?user=' + encodeURIComponent(key); }
  function goTo(box) { try { window.focus(); } catch (e) { /* no-op */ } window.location.href = deepLink(box.key); }

  function announce(box, delta) {
    var msg = (delta > 1 ? delta + ' new messages' : 'New message') + ' in ' + box.address;
    if (notifyReady && ('Notification' in window)) {
      var opts = { body: msg, tag: 'vmail:' + box.address, renotify: true, data: { url: deepLink(box.key) } };
      // Prefer the service worker's showNotification: it is the ONLY path that
      // works in an installed PWA and on Android/mobile browsers (where the
      // `new Notification()` constructor is unsupported), and its click is
      // handled by the worker (see sw.js `notificationclick`) so the mailbox
      // opens even if this tab is gone. Fall back to the page Notification
      // constructor on desktop browsers without an active worker.
      if (navigator.serviceWorker && navigator.serviceWorker.ready) {
        navigator.serviceWorker.ready
          .then(function (reg) { return reg.showNotification('VayuMail — new mail', opts); })
          .catch(function () { pageNotify(box, opts); });
        return;
      }
      if (pageNotify(box, opts)) return;
    }
    if (typeof window.vpToast === 'function') {
      // Clickable toast: an operator on an .onion (no secure context) or with
      // notifications denied still gets the alert and a one-click way in.
      var t = window.vpToast(msg + ' — open', 'info');
      if (t && t.addEventListener) { t.style.cursor = 'pointer'; on(t, 'click', function () { goTo(box); }); }
    }
  }
  function pageNotify(box, opts) {
    try {
      var n = new Notification('VayuMail — new mail', opts);
      n.onclick = function () { goTo(box); n.close(); };
      return true;
    } catch (e) { return false; }
  }
  function bumpTitle(n) {
    titleCount += n;
    document.title = titleCount > 0 ? '(' + titleCount + ') ' + baseTitle : baseTitle;
  }
  function clearTitle() { titleCount = 0; document.title = baseTitle; }
  window.addEventListener('focus', clearTitle);
  // Live-bump the topbar notification bell so new mail shows there in real time
  // (the bell is otherwise only recomputed on a full page render). It reflects the
  // combined count of all notification types, so we add the new-mail delta.
  function bumpBell(delta) {
    var wrap = document.querySelector('[data-notif]');
    if (!wrap) return;
    var btn = wrap.querySelector('[data-notif-toggle]');
    if (!btn) return;
    var badge = wrap.querySelector('.topbar-notif__badge');
    var cur = badge ? (parseInt((badge.textContent || '').replace(/\D/g, ''), 10) || 0) : 0;
    var label = (cur + delta) > 99 ? '99+' : String(cur + delta);
    if (!badge) { badge = document.createElement('span'); badge.className = 'topbar-notif__badge'; badge.setAttribute('data-notif-badge', ''); btn.appendChild(badge); }
    badge.textContent = label;
    btn.classList.add('topbar-notif__btn--active');
    // New mail is something that needs the operator, so Mark all read, which
    // clears only events, must not take it back off the badge.
    wrap.setAttribute('data-notif-needs', String((parseInt(wrap.getAttribute('data-notif-needs'), 10) || 0) + delta));
  }

  function poll() {
    fetch(ENDPOINT, { headers: { 'Accept': 'application/json' } })
      .then(function (res) { return res.ok ? res.json() : null; })
      .then(function (list) {
        if (!Array.isArray(list)) return;
        var seen = Object.create(null);
        list.forEach(function (b) { seen[b.address] = true; });
        if (baseline === null) {                 // first poll: record, never notify
          baseline = Object.create(null);
          list.forEach(function (b) { baseline[b.address] = b.unseen; });
          ensurePerm();
          return;
        }
        var totalNew = 0;
        list.forEach(function (b) {
          var prev = baseline[b.address] || 0;
          if (b.unseen > prev) { var delta = b.unseen - prev; totalNew += delta; announce(b, delta); }
          baseline[b.address] = b.unseen;
        });
        Object.keys(baseline).forEach(function (addr) { if (!seen[addr]) delete baseline[addr]; });
        if (totalNew > 0) { bumpTitle(totalNew); bumpBell(totalNew); }
      })
      .catch(function () { /* offline / transient — try again next tick */ });
  }
  // First poll a few seconds after load (so a fresh sign-in isn't spammed), then
  // on a steady interval. Background tabs are throttled by the browser, which is
  // fine — the notification is exactly what a backgrounded operator wants.
  setTimeout(poll, 4000);
  setInterval(poll, POLL_MS);
})();

/* ── Keyboard layer (Wave 3.7) ────────────────────────────────
   j/k move through the post rows, x toggles the highlighted row's bulk select,
   Enter opens the highlighted row, n starts a new post (or focuses quick
   compose on the dashboard). Only fires when a post list exists; never fires
   while typing, inside a form field, or with a modifier held. */
(function initKeyboardLayer() {
  var rows = function () { return $$('.post-row [data-post-row], [data-post-row]').filter(function (r) { return !r.hidden; }); };
  var idx = -1;
  function highlight(next) {
    var list = rows();
    if (!list.length) return;
    if (idx >= 0 && list[idx]) list[idx].classList.remove('post-row--kbd');
    idx = (next + list.length) % list.length;
    var row = list[idx];
    row.classList.add('post-row--kbd');
    var sum = row.querySelector('summary');
    if (sum) sum.scrollIntoView({ block: 'nearest' });
  }
  function isTyping(el) {
    if (!el) return false;
    var tag = (el.tagName || '').toLowerCase();
    return tag === 'input' || tag === 'textarea' || tag === 'select' || el.isContentEditable;
  }
  document.addEventListener('keydown', function (e) {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    if (isTyping(e.target)) return;
    var list = rows();
    var hasList = list.length > 0;
    var composer = $('#quick-compose-input');
    if (e.key === 'n') {
      // n = "new post": focus quick compose where it exists, otherwise open the
      // editor — but only from a post list. This layer sat outside its wrapper and
      // threw before reaching here for a long time; once it ran, an unscoped "n"
      // would have thrown the operator out of Mail or Talk into the post editor.
      if (composer) { e.preventDefault(); composer.focus(); }
      else if (hasList) { window.location.href = '/os/editor'; }
      return;
    }
    if (!hasList) return;
    switch (e.key) {
      case 'j': e.preventDefault(); highlight(idx + 1); break;
      case 'k': e.preventDefault(); highlight(idx - 1); break;
      case 'x':
        if (idx < 0 || !list[idx]) return;
        e.preventDefault();
        var chk = list[idx].querySelector('[data-post-select]');
        if (chk) { chk.checked = !chk.checked; chk.dispatchEvent(new Event('change', { bubbles: true })); }
        break;
      case 'Enter':
        if (idx < 0 || !list[idx]) return;
        e.preventDefault();
        var sum = list[idx].querySelector('summary');
        if (sum) sum.click();
        break;
    }
  });
})();

})(); // end IIFE — the keyboard layer above uses its $$ helper, so it must stay inside
