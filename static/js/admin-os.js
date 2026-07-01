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

  const btn = $('.topbar-theme-btn');
  if (!btn) return;
  btn.title = 'Theme: ' + el.dataset.theme;
  btn.addEventListener('click', function () {
    const themes = ['light', 'dark', 'auto'];
    const cur = themes.indexOf(el.dataset.theme);
    const next = themes[(cur + 1) % themes.length];
    el.dataset.theme = next;
    btn.title = 'Theme: ' + next;
    // Persist via API (fire-and-forget)
    const csrf = cookie('vp_csrf');
    fetch('/os/api/settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
      body: JSON.stringify({ key: 'admin.theme', value: next }),
    }).catch(function () {});
  });
})();

/* ── Cookies ─────────────────────────────────────────────────── */
function cookie(name) {
  // Take everything after the first '=' so base64 values keep any '=' padding.
  var row = document.cookie.split('; ').find(function (r) { return r.startsWith(name + '='); });
  return row ? row.slice(name.length + 1) : '';
}

/* ── Toast system ────────────────────────────────────────────── */
function toast(msg, kind) {
  kind = kind || 'info';
  var container = $('.toast-container');
  if (!container) {
    container = document.createElement('div');
    container.className = 'toast-container';
    document.body.appendChild(container);
  }
  var el = document.createElement('div');
  el.className = 'toast toast--' + kind;

  var icon = document.createElement('span');
  icon.textContent = kind === 'ok' ? '✓' : kind === 'error' ? '✕' : kind === 'warn' ? '⚠' : 'ℹ';
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

  function setExpanded(v) {
    toggles.forEach(function (b) { b.setAttribute('aria-expanded', v ? 'true' : 'false'); });
  }
  function open() {
    sidebar.classList.add('open');
    if (overlay) overlay.classList.add('open');
    document.body.style.overflow = 'hidden';
    setExpanded(true);
  }
  function close() {
    sidebar.classList.remove('open');
    if (overlay) overlay.classList.remove('open');
    document.body.style.overflow = '';
    setExpanded(false);
  }
  function toggle() { sidebar.classList.contains('open') ? close() : open(); }

  toggles.forEach(function (b) {
    on(b, 'click', function (e) { e.preventDefault(); toggle(); });
  });
  if (overlay) on(overlay, 'click', close);
  $$('.sidebar .nav-link').forEach(function (a) { on(a, 'click', close); });
  on(document, 'keydown', function (e) { if (e.key === 'Escape') close(); });

  // Collapse the drawer (and unlock scroll) when returning to the desktop layout.
  var mq = window.matchMedia('(min-width: 769px)');
  var onChange = function (e) { if (e.matches) close(); };
  if (mq.addEventListener) mq.addEventListener('change', onChange);
  else if (mq.addListener) mq.addListener(onChange);
})();

/* ── Bottom bar: active state + role-aware quick links ───────
   The drawer is already role-scoped server-side; mirror that on the bottom bar
   by hiding any quick link whose destination isn't present in the sidebar for
   this session. Then highlight the item matching the current route. The "Menu"
   button (no data-nav) is always kept. */
(function initBottomNav() {
  var nav = $('.bottom-nav');
  if (!nav) return;
  var items = $$('.bottom-nav-item[data-nav]', nav);
  if (!items.length) return;

  var sideHrefs = $$('.sidebar .nav-link').map(function (a) { return a.getAttribute('href'); });
  // Only filter when we actually have a sidebar to compare against.
  if (sideHrefs.length) {
    items.forEach(function (it) {
      var href = it.getAttribute('data-nav');
      if (href && sideHrefs.indexOf(href) === -1) it.hidden = true;
    });
  }

  var path = location.pathname;
  var best = null, bestLen = -1;
  items.forEach(function (it) {
    if (it.hidden) return;
    var href = it.getAttribute('data-nav');
    if (!href) return;
    var match = path === href || (href !== '/os' && path.indexOf(href) === 0);
    if (match && href.length > bestLen) { best = it; bestLen = href.length; }
  });
  if (best) best.setAttribute('aria-current', 'page');
})();

/* ── Responsive data tables → cards ──────────────────────────
   Generic, zero-config: for every .table-wrap > table.table, copy each column
   header into its body cells as data-label and flag the wrapper .vp-stackable.
   CSS then folds the table into labelled cards on phones. Skips tables that opt
   out (data-no-stack), have fewer than two columns, or lead with a selection
   checkbox (management grids that read better as a horizontal scroll). */
(function initResponsiveTables() {
  $$('.table-wrap > table.table').forEach(function (table) {
    var wrap = table.parentElement;
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
})();

/* ── Command palette (Cmd+K / Ctrl+K) ───────────────────────── */
(function initCommandPalette() {
  var backdrop = $('#cmd-backdrop');
  var input = $('#cmd-input');
  var results = $('#cmd-results');
  if (!backdrop || !input || !results) return;

  var index = null; // Loaded lazily
  var activeIdx = -1;
  var items = [];

  function open() {
    backdrop.removeAttribute('hidden');
    input.value = '';
    input.focus();
    loadIndex();
    render('');
  }
  function close() {
    backdrop.setAttribute('hidden', '');
    activeIdx = -1;
  }

  document.addEventListener('keydown', function (e) {
    if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
      e.preventDefault();
      backdrop.hasAttribute('hidden') ? open() : close();
    }
    if (!backdrop.hasAttribute('hidden')) {
      if (e.key === 'Escape') close();
      if (e.key === 'ArrowDown') { e.preventDefault(); moveActive(1); }
      if (e.key === 'ArrowUp')   { e.preventDefault(); moveActive(-1); }
      if (e.key === 'Enter')     { e.preventDefault(); activateCurrent(); }
    }
  });
  backdrop.addEventListener('click', function (e) {
    if (e.target === backdrop) close();
  });

  var cmdBtn = $('.topbar-cmd');
  if (cmdBtn) cmdBtn.addEventListener('click', open);

  input.addEventListener('input', function () { render(input.value); });

  function loadIndex() {
    if (index !== null) return;
    var cached = null;
    try { cached = JSON.parse(sessionStorage.getItem('vp3_cmd_index_' + Date.now().toString().slice(0, -4))); } catch (e) {}
    if (cached) { index = cached; return; }
    fetch('/os/api/cmd-index')
      .then(function (r) { return r.json(); })
      .then(function (data) {
        index = data;
        try { sessionStorage.setItem('vp3_cmd_index_' + Date.now().toString().slice(0, -4), JSON.stringify(data)); } catch (e) {}
        render(input.value);
      })
      .catch(function () { index = { posts: [], actions: [], settings: [] }; });
  }

  function render(q) {
    q = q.toLowerCase().trim();
    results.innerHTML = '';
    items = [];
    activeIdx = -1;

    if (!index) {
      var loading = document.createElement('div');
      loading.className = 'cmd-group-label';
      loading.textContent = 'Loading…';
      results.appendChild(loading);
      return;
    }

    var sections = [
      { label: 'Posts', key: 'posts', icon: '✍', href: function(i){ return '/os/editor/' + i.slug; } },
      { label: 'Quick Actions', key: 'actions', icon: '⚡', fn: function(i){ return i.fn; } },
      { label: 'Settings', key: 'settings', icon: '⚙', href: function(i){ return i.href; } },
    ];

    sections.forEach(function (sec) {
      var list = (index[sec.key] || []).filter(function (item) {
        return !q || item.label.toLowerCase().includes(q) || (item.slug && item.slug.includes(q));
      }).slice(0, 6);
      if (!list.length) return;

      var label = document.createElement('div');
      label.className = 'cmd-group-label';
      label.textContent = sec.label;
      results.appendChild(label);

      list.forEach(function (item) {
        var el = sec.href
          ? document.createElement('a')
          : document.createElement('button');
        el.className = 'cmd-item';
        if (sec.href) el.href = sec.href(item);

        var icon = document.createElement('div');
        icon.className = 'cmd-item__icon';
        icon.textContent = item.icon || sec.icon;

        var lbl = document.createElement('div');
        lbl.className = 'cmd-item__label';
        lbl.textContent = item.label || item.title || '';

        el.appendChild(icon);
        el.appendChild(lbl);

        if (item.hint) {
          var hint = document.createElement('div');
          hint.className = 'cmd-item__hint';
          hint.textContent = item.hint;
          el.appendChild(hint);
        }

        if (!sec.href && item.fn) {
          el.addEventListener('click', function () { close(); window[item.fn] && window[item.fn](); });
        } else {
          el.addEventListener('click', close);
        }

        results.appendChild(el);
        items.push(el);
      });
    });

    if (!items.length && q) {
      var empty = document.createElement('div');
      empty.className = 'cmd-group-label';
      empty.textContent = 'No results for "' + q + '"';
      results.appendChild(empty);
    }
  }

  function moveActive(dir) {
    if (!items.length) return;
    var cur = $('.cmd-item--active', results);
    if (cur) cur.classList.remove('cmd-item--active');
    activeIdx = (activeIdx + dir + items.length) % items.length;
    items[activeIdx].classList.add('cmd-item--active');
    items[activeIdx].scrollIntoView({ block: 'nearest' });
  }

  function activateCurrent() {
    if (activeIdx >= 0 && items[activeIdx]) items[activeIdx].click();
  }
})();

/* ── Posts search (client-side) ──────────────────────────────── */
(function initPostsSearch() {
  var input = $('[data-posts-search]');
  if (!input) return;
  var empty = $('[data-search-empty]');
  input.addEventListener('input', function () {
    var q = input.value.toLowerCase().trim();
    var rows = $$('[data-post-row]');
    var visible = 0;
    rows.forEach(function (row) {
      var match = !q || (row.dataset.search || '').includes(q);
      row.hidden = !match;
      if (match) visible++;
    });
    if (empty) empty.hidden = visible > 0 || !q;
  });
})();

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
  fetch('/os/api/activity')
    .then(function (r) { return r.json(); })
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
        var row = document.createElement('div');
        row.className = 'activity-item';

        var icon = document.createElement('div');
        icon.className = 'activity-icon activity-icon--' + (item.kind || 'system');
        icon.textContent = item.icon || '·';
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
    .catch(function () {});
})();

/* ── Settings toggle rows ────────────────────────────────────── */
$$('[data-setting-key]').forEach(function (el) {
  el.addEventListener('change', function () {
    var key = el.dataset.settingKey;
    var val = el.type === 'checkbox' ? (el.checked ? 'true' : 'false') : el.value;
    var csrf = cookie('vp_csrf');
    fetch('/os/api/settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
      body: JSON.stringify({ key: key, value: val }),
    })
    .then(function (r) { return r.ok ? null : r.json(); })
    .then(function (err) {
      if (err) toast('Error saving setting', 'error');
      else toast('Saved', 'ok');
    })
    .catch(function () { toast('Network error', 'error'); });
  });
});

/* ── Media library (Phase 4) ─────────────────────────────────── */
(function initMedia() {
  var grid = $('[data-media-grid]');
  if (!grid) return;
  var dropzone = $('[data-media-dropzone]');
  var input = $('[data-media-input]');

  function relTime(unix) {
    var s = Math.floor(Date.now() / 1000) - unix;
    if (s < 60) return 'just now';
    if (s < 3600) return Math.floor(s / 60) + 'm ago';
    if (s < 86400) return Math.floor(s / 3600) + 'h ago';
    return Math.floor(s / 86400) + 'd ago';
  }

  function fmtSize(b) {
    if (b < 1024) return b + ' B';
    if (b < 1048576) return (b / 1024).toFixed(0) + ' KB';
    return (b / 1048576).toFixed(1) + ' MB';
  }

  function card(item) {
    var el = document.createElement('figure');
    el.className = 'media-card';

    var thumb = document.createElement('div');
    thumb.className = 'media-card__thumb';

    // Selection checkbox for bulk delete.
    var sel = document.createElement('input');
    sel.type = 'checkbox';
    sel.className = 'media-card__select';
    sel.setAttribute('data-media-select', '');
    sel.value = item.name;
    sel.setAttribute('aria-label', 'Select ' + item.name);
    sel.addEventListener('change', updateSelCount);
    thumb.appendChild(sel);

    if (item.isPdf) {
      var badge = document.createElement('span');
      badge.className = 'media-card__pdf';
      badge.textContent = 'PDF';
      thumb.appendChild(badge);
    } else {
      var img = document.createElement('img');
      img.loading = 'lazy';
      img.src = item.url;
      img.alt = item.alt || item.name;
      thumb.appendChild(img);
    }
    el.appendChild(thumb);

    var meta = document.createElement('figcaption');
    meta.className = 'media-card__meta';
    var size = document.createElement('span');
    size.textContent = fmtSize(item.size) + ' · ' + relTime(item.mod);
    meta.appendChild(size);

    // Alt-text editor (images only) — saves on blur.
    if (!item.isPdf) {
      var altI = document.createElement('input');
      altI.type = 'text';
      altI.className = 'media-card__alt';
      altI.placeholder = 'Alt text…';
      altI.value = item.alt || '';
      altI.maxLength = 300;
      altI.setAttribute('aria-label', 'Alt text for ' + item.name);
      altI.addEventListener('blur', function () {
        if (altI.value === (item.alt || '')) return;
        item.alt = altI.value;
        fetch('/os/api/media/alt', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': cookie('vp_csrf') },
          body: JSON.stringify({ name: item.name, alt: altI.value }),
        }).then(function (r) { toast(r.ok ? 'Alt text saved' : 'Could not save alt', r.ok ? 'ok' : 'error'); })
          .catch(function () { toast('Network error', 'error'); });
      });
      meta.appendChild(altI);
    }

    var copy = document.createElement('button');
    copy.type = 'button';
    copy.className = 'media-card__copy';
    copy.textContent = 'Copy URL';
    copy.addEventListener('click', function () {
      var full = window.location.origin + item.url;
      if (navigator.clipboard) {
        navigator.clipboard.writeText(full).then(function () { toast('URL copied', 'ok'); });
      } else {
        toast(full, 'ok');
      }
    });
    meta.appendChild(copy);
    el.appendChild(meta);
    return el;
  }

  var allItems = [];
  var search = $('[data-media-search]');
  var emptyMsg = $('[data-media-empty]');
  var typeFilter = 'all';

  function applyFilter() {
    while (grid.firstChild) grid.removeChild(grid.firstChild);
    if (!allItems.length) {
      var empty = document.createElement('div');
      empty.className = 'empty-state';
      empty.textContent = 'No media yet. Upload your first image or PDF.';
      grid.appendChild(empty);
      if (emptyMsg) emptyMsg.hidden = true;
      return;
    }
    var q = (search && search.value || '').trim().toLowerCase();
    var shown = allItems.filter(function (it) {
      if (typeFilter === 'image' && it.isPdf) return false;
      if (typeFilter === 'pdf' && !it.isPdf) return false;
      if (q && (it.name || '').toLowerCase().indexOf(q) === -1) return false;
      return true;
    });
    shown.forEach(function (it) { grid.appendChild(card(it)); });
    if (emptyMsg) emptyMsg.hidden = shown.length > 0;
    updateSelCount();
  }

  function load() {
    fetch('/os/api/media', { headers: { 'Accept': 'application/json' } })
      .then(function (r) { return r.json(); })
      .then(function (data) {
        allItems = (data && data.items) || [];
        applyFilter();
      })
      .catch(function () { toast('Could not load media', 'error'); });
  }

  if (search) search.addEventListener('input', applyFilter);
  document.querySelectorAll('[data-media-filter]').forEach(function (b) {
    b.addEventListener('click', function () {
      document.querySelectorAll('[data-media-filter]').forEach(function (x) { x.classList.remove('is-active'); });
      b.classList.add('is-active');
      typeFilter = b.getAttribute('data-media-filter');
      applyFilter();
    });
  });

  // ── Bulk delete ───────────────────────────────────────────────────────────
  var delBtn = $('[data-media-delete-selected]');
  var selCount = $('[data-media-sel-count]');
  function selectedNames() {
    return Array.prototype.slice.call(grid.querySelectorAll('[data-media-select]:checked')).map(function (c) { return c.value; });
  }
  function updateSelCount() {
    var n = selectedNames().length;
    if (selCount) selCount.textContent = String(n);
    if (delBtn) delBtn.disabled = n === 0;
  }
  if (delBtn) delBtn.addEventListener('click', function () {
    var names = selectedNames();
    if (!names.length) return;
    if (!window.confirm('Delete ' + names.length + ' file' + (names.length > 1 ? 's' : '') + '? This cannot be undone.')) return;
    delBtn.disabled = true;
    fetch('/os/api/media/delete', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': cookie('vp_csrf') },
      body: JSON.stringify({ names: names }),
    }).then(function (r) { return r.json().then(function (j) { return { ok: r.ok, j: j }; }); })
      .then(function (res) {
        if (res.ok) { toast('Deleted ' + (res.j.deleted || 0), 'ok'); load(); }
        else { delBtn.disabled = false; toast('Delete failed', 'error'); }
      }).catch(function () { delBtn.disabled = false; toast('Network error', 'error'); });
  });

  function upload(file) {
    if (!file) return;
    var fd = new FormData();
    fd.append('file', file);
    toast('Uploading…', 'ok');
    fetch('/os/api/media/upload', {
      method: 'POST',
      headers: { 'X-CSRF-Token': cookie('vp_csrf') },
      body: fd
    })
      .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, j: j }; }); })
      .then(function (res) {
        if (!res.ok) { toast(typeof res.j.error === 'string' ? res.j.error : (res.j.message || res.j.detail || 'Upload failed'), 'error'); return; }
        toast('Uploaded', 'ok');
        load();
      })
      .catch(function () { toast('Network error', 'error'); });
  }

  if (dropzone) {
    dropzone.addEventListener('click', function () { if (input) input.click(); });
    dropzone.addEventListener('keydown', function (e) {
      if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); if (input) input.click(); }
    });
    dropzone.addEventListener('dragover', function (e) { e.preventDefault(); dropzone.classList.add('media-dropzone--over'); });
    dropzone.addEventListener('dragleave', function () { dropzone.classList.remove('media-dropzone--over'); });
    dropzone.addEventListener('drop', function (e) {
      e.preventDefault();
      dropzone.classList.remove('media-dropzone--over');
      if (e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files.length) upload(e.dataTransfer.files[0]);
    });
  }
  if (input) input.addEventListener('change', function () { if (input.files.length) upload(input.files[0]); });

  load();
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

})(); // end IIFE
