/* VayuPress Admin v3 — Bootstrap
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
  const root = document.documentElement;
  // Theme is written as data-admin-theme on <body> by Go (from settings).
  // We mirror it to <html data-theme> for CSS selectors.
  const pref = document.body.dataset.adminTheme || 'dark';
  root.dataset.theme = pref;

  const btn = $('.topbar-theme-btn');
  if (!btn) return;
  btn.addEventListener('click', function () {
    const themes = ['dark', 'light', 'auto'];
    const cur = themes.indexOf(root.dataset.theme);
    const next = themes[(cur + 1) % themes.length];
    root.dataset.theme = next;
    btn.title = 'Theme: ' + next;
    // Persist via API (fire-and-forget)
    const csrf = cookie('vp_csrf');
    fetch('/admin/v3/api/settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
      body: JSON.stringify({ key: 'admin.theme', value: next }),
    }).catch(function () {});
  });
})();

/* ── Cookies ─────────────────────────────────────────────────── */
function cookie(name) {
  return (document.cookie.split('; ').find(function (r) { return r.startsWith(name + '='); }) || '').split('=')[1] || '';
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

/* ── Sidebar toggle (mobile) ─────────────────────────────────── */
(function initSidebar() {
  var btn = $('.menu-toggle');
  var sidebar = $('.sidebar');
  var overlay = $('.sidebar-overlay');
  if (!btn || !sidebar) return;

  function open() {
    sidebar.classList.add('open');
    if (overlay) overlay.classList.add('open');
    document.body.style.overflow = 'hidden';
  }
  function close() {
    sidebar.classList.remove('open');
    if (overlay) overlay.classList.remove('open');
    document.body.style.overflow = '';
  }

  btn.addEventListener('click', function () {
    sidebar.classList.contains('open') ? close() : open();
  });
  if (overlay) overlay.addEventListener('click', close);
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') close();
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
    fetch('/admin/v3/api/cmd-index')
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
      { label: 'Posts', key: 'posts', icon: '✍', href: function(i){ return '/admin/v3/editor/' + i.slug; } },
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
    fetch('/admin/v3/api/posts/quick-create', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
      body: JSON.stringify({ title: title }),
    })
    .then(function (r) { return r.json(); })
    .then(function (data) {
      if (data.slug) {
        window.location.href = '/admin/v3/editor/' + data.slug;
      } else {
        toast(data.error || 'Could not create post', 'error');
        input.disabled = false;
      }
    })
    .catch(function () { toast('Network error', 'error'); input.disabled = false; });
  });
})();

/* ── Data-action dispatcher ──────────────────────────────────── */
document.addEventListener('click', function (e) {
  var el = e.target.closest('[data-action]');
  if (!el) return;
  var action = el.dataset.action;
  var actions = {
    'toggle-sidebar': function () {
      var sidebar = $('.sidebar');
      if (sidebar) sidebar.classList.toggle('open');
    },
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
  fetch('/admin/v3/api/activity')
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
    fetch('/admin/v3/api/settings', {
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
