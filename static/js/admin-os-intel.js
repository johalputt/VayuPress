/*
 * admin-os-intel.js — SEO dashboard actions for VayuOS (ADR-0068, Phase 6).
 * Strict CSP: no eval, DOM updates via textContent only.
 */
(function () {
  'use strict';

  var btn = document.querySelector('[data-seo-regenerate]');
  var status = document.querySelector('[data-seo-status]');
  if (!btn) return;

  function csrf() {
    var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }
  function show(msg, kind) {
    if (status) {
      status.hidden = false;
      status.textContent = msg;
      status.className = 'seo-status mt-3' + (kind ? ' editor-status--' + kind : '');
    }
    if (window.vpToast) window.vpToast(msg, kind === 'danger' ? 'error' : 'ok');
  }

  btn.addEventListener('click', function () {
    btn.disabled = true;
    show('Regenerating sitemap, feed, and robots…');
    fetch('/os/api/seo/regenerate', {
      method: 'POST',
      headers: { 'X-CSRF-Token': csrf() }
    })
      .then(function (r) { return r.ok ? r : Promise.reject(r); })
      .then(function () {
        show('SEO artefacts regenerated.', 'ok');
        setTimeout(function () { window.location.reload(); }, 1000);
      })
      .catch(function () { show('Regeneration failed.', 'danger'); btn.disabled = false; });
  });
})();

/*
 * Conversion goals — create and delete (Analytics page). Guarded so this is a
 * no-op on pages without the goals card. CSRF via the vp_csrf double-submit
 * cookie, matching the rest of VayuOS.
 */
(function () {
  'use strict';

  function csrf() {
    var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }

  var form = document.querySelector('[data-goal-form]');
  if (form) {
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      var name = (form.querySelector('[data-goal-name]') || {}).value || '';
      var kind = (form.querySelector('[data-goal-kind]') || {}).value || 'path';
      var target = (form.querySelector('[data-goal-target]') || {}).value || '';
      if (!name.trim() || !target.trim()) { window.alert('Name and target are required.'); return; }
      fetch('/os/api/analytics/goals', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() },
        body: JSON.stringify({ name: name, kind: kind, target: target })
      })
        .then(function (r) { return r.ok ? r : Promise.reject(r); })
        .then(function () { window.location.reload(); })
        .catch(function () { window.alert('Could not add goal. Check the name and target.'); });
    });
  }

  document.querySelectorAll('[data-goal-delete]').forEach(function (b) {
    b.addEventListener('click', function () {
      var id = b.getAttribute('data-goal-delete');
      if (!window.confirm('Delete this goal?')) return;
      fetch('/os/api/analytics/goals/' + encodeURIComponent(id), {
        method: 'DELETE',
        headers: { 'X-CSRF-Token': csrf() }
      })
        .then(function (r) { return r.ok ? r : Promise.reject(r); })
        .then(function () { window.location.reload(); })
        .catch(function () { window.alert('Delete failed.'); });
    });
  });
})();

/*
 * Live analytics — polls the realtime endpoint every 10s and updates the Live
 * tab: active-visitor count, countries, active pages, and referrers. CSP-safe:
 * DOM updated via textContent only, no eval, no inline styles. No-op without
 * the live card.
 */
(function () {
  'use strict';

  var card = document.querySelector('[data-live]');
  if (!card) return;
  var countEl = card.querySelector('[data-live-count]');
  var pagesEl = card.querySelector('[data-live-pages]');
  var countriesEl = card.querySelector('[data-live-countries]');
  var referrersEl = card.querySelector('[data-live-referrers]');
  var updatedEl = card.querySelector('[data-live-updated]');

  function clear(el) { while (el && el.firstChild) el.removeChild(el.firstChild); }

  function emptyRow(el, cols, msg) {
    var tr = document.createElement('tr');
    var td = document.createElement('td');
    if (cols > 1) td.setAttribute('colspan', String(cols));
    td.className = 'muted';
    td.textContent = msg;
    tr.appendChild(td);
    el.appendChild(tr);
  }

  // Fill a two-column [label, count] table body from {label,count}/{path,count}.
  function fill(el, items, labelKey, emptyMsg) {
    if (!el) return;
    clear(el);
    items = items || [];
    if (!items.length) { emptyRow(el, 2, emptyMsg); return; }
    items.forEach(function (it) {
      var tr = document.createElement('tr');
      var label = document.createElement('td');
      label.className = 'row-title';
      label.textContent = it[labelKey] || it.label || '(unknown)';
      var n = document.createElement('td');
      n.textContent = String(it.count || 0);
      tr.appendChild(label);
      tr.appendChild(n);
      el.appendChild(tr);
    });
  }

  // Fill the live-countries table with a flag <img> + name + count.
  function fillCountries(el, items) {
    if (!el) return;
    clear(el);
    items = items || [];
    if (!items.length) { emptyRow(el, 2, 'No location data (needs a geo-header proxy).'); return; }
    items.forEach(function (c) {
      var tr = document.createElement('tr');
      var label = document.createElement('td');
      label.className = 'row-title';
      if (c.flag) {
        var img = document.createElement('img');
        img.className = 'vp-flag-img';
        img.src = c.flag;
        img.alt = '';
        img.width = 20; img.height = 15;
        img.loading = 'lazy';
        label.appendChild(img);
        label.appendChild(document.createTextNode(' '));
      }
      label.appendChild(document.createTextNode(c.name || c.code || '(unknown)'));
      var n = document.createElement('td');
      n.textContent = String(c.count || 0);
      tr.appendChild(label);
      tr.appendChild(n);
      el.appendChild(tr);
    });
  }

  function render(data) {
    data = data || {};
    if (countEl) countEl.textContent = String(data.active_visitors || 0);
    fill(pagesEl, data.active_pages, 'path', 'No active visitors right now.');
    fillCountries(countriesEl, data.active_countries);
    fill(referrersEl, data.active_referrers, 'label', 'No referrers in the last 5 minutes.');
    if (updatedEl) {
      var t = new Date();
      updatedEl.textContent = '· updated ' + t.toLocaleTimeString();
    }
  }

  function poll() {
    fetch('/os/api/analytics/realtime', { headers: { 'Accept': 'application/json' } })
      .then(function (r) { return r.ok ? r.json() : Promise.reject(r); })
      .then(render)
      .catch(function () { /* transient; keep last view */ });
  }

  poll();
  var timer = window.setInterval(poll, 10000);
  // Pause polling when the tab is hidden to save resources.
  document.addEventListener('visibilitychange', function () {
    if (document.hidden) {
      window.clearInterval(timer);
    } else {
      poll();
      timer = window.setInterval(poll, 10000);
    }
  });
})();

/*
 * Analytics tabs — client-side section switching (no reload). The selected tab
 * is remembered in the URL hash so a refresh / shared link reopens it. CSP-safe:
 * toggles classes / the [hidden] attribute only. No-op without the tab bar.
 */
(function () {
  'use strict';

  var bar = document.querySelector('[data-analytics-tabs]');
  if (!bar) return;
  var tabs = Array.prototype.slice.call(bar.querySelectorAll('[data-atab]'));
  var panels = Array.prototype.slice.call(document.querySelectorAll('[data-atab-panel]'));
  if (!tabs.length) return;

  function activate(id, push) {
    var matched = false;
    tabs.forEach(function (t) {
      var on = t.getAttribute('data-atab') === id;
      t.classList.toggle('tab--active', on);
      t.setAttribute('aria-selected', on ? 'true' : 'false');
      if (on) matched = true;
    });
    if (!matched) return;
    panels.forEach(function (p) {
      p.hidden = p.getAttribute('data-atab-panel') !== id;
    });
    if (push && window.history && window.history.replaceState) {
      window.history.replaceState(null, '', '#' + id);
    }
  }

  tabs.forEach(function (t) {
    t.addEventListener('click', function () { activate(t.getAttribute('data-atab'), true); });
  });

  var initial = (window.location.hash || '').replace(/^#/, '');
  if (initial) activate(initial, false);
  window.addEventListener('hashchange', function () {
    activate((window.location.hash || '').replace(/^#/, ''), false);
  });
})();



/*
 * Period selector loading cue (Analytics page). The selector is plain GET
 * navigation; on click we mark the bar busy so the operator gets immediate
 * feedback while the next time-window renders. No-op without the bar. CSP-safe:
 * toggles a class only, no inline styles.
 */
(function () {
  'use strict';

  var bar = document.querySelector('[data-period]');
  if (!bar) return;
  bar.addEventListener('click', function (e) {
    var link = e.target.closest('a');
    if (!link || !bar.contains(link)) return;
    bar.classList.add('is-loading');
    bar.setAttribute('aria-busy', 'true');
  });
})();
