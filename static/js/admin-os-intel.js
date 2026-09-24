/*
 * admin-os-intel.js — SEO dashboard actions for VayuOS (ADR-0068, Phase 6).
 * Strict CSP: no eval, DOM updates via textContent only.
 */
(function () {
  'use strict';

  // Delegated, and every element looked up when used: vpRefresh replaces the
  // page's content after a change, so bound listeners and kept references die.
  function csrf() {
    var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }
  function show(msg, kind) {
    var status = document.querySelector('[data-seo-status]');
    if (status) {
      status.hidden = false;
      status.textContent = msg;
      status.className = 'seo-status mt-3' + (kind ? ' editor-status--' + kind : '');
    }
    if (window.vpToast) window.vpToast(msg, kind === 'danger' ? 'error' : 'ok');
  }

  document.addEventListener('click', function (e) {
    var btn = e.target && e.target.closest ? e.target.closest('[data-seo-regenerate]') : null;
    if (!btn) return;
    btn.disabled = true;
    show('Regenerating sitemap, feed, and robots…');
    fetch('/os/api/seo/regenerate', {
      method: 'POST',
      headers: { 'X-CSRF-Token': csrf() }
    })
      .then(function (r) { return r.ok ? r : Promise.reject(r); })
      .then(function () {
        show('SEO artefacts regenerated.', 'ok');
        if (window.vpRefresh) window.vpRefresh(); else window.location.reload();
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

  // The Analytics report is served from a background cache (admin_dashcache.go),
  // so re-fetching the page after a change would show the goal list as it was.
  // The server rebuilds that cache on every goal change; this page updates its
  // own table from the confirmed reply, which is exactly what the rebuilt report
  // will say (a new goal has no completions yet).
  var NO_GOALS = 'No goals yet. Add one above (e.g. a "/thank-you" path view or a "signup" custom event).';
  function goalsBody() { return document.querySelector('[data-goals] tbody'); }
  function cell(text, cls) {
    var td = document.createElement('td');
    if (cls) td.className = cls;
    td.textContent = text;
    return td;
  }
  function addGoalRow(id, name, kind, target) {
    var body = goalsBody();
    if (!body) return;
    if (!body.querySelector('[data-goal-delete]')) { while (body.firstChild) body.removeChild(body.firstChild); }
    var tr = document.createElement('tr');
    tr.appendChild(cell(name, 'row-title'));
    var kindTd = document.createElement('td');
    var badge = document.createElement('span');
    badge.className = 'badge';
    badge.textContent = kind;
    kindTd.appendChild(badge);
    tr.appendChild(kindTd);
    tr.appendChild(cell(target, 'muted'));
    var comp = cell('0 ');
    var vis = document.createElement('span');
    vis.className = 'muted text-xs';
    vis.textContent = '(0 visitors)';
    comp.appendChild(vis);
    tr.appendChild(comp);
    tr.appendChild(cell('0.0%'));
    var delTd = document.createElement('td');
    var del = document.createElement('button');
    del.className = 'btn btn--danger btn--sm';
    del.setAttribute('data-goal-delete', id);
    del.textContent = 'Delete';
    delTd.appendChild(del);
    tr.appendChild(delTd);
    body.appendChild(tr);
  }
  function removeGoalRow(btn) {
    var row = btn.closest('tr');
    var body = goalsBody();
    if (row) row.parentNode.removeChild(row);
    if (body && !body.querySelector('[data-goal-delete]')) {
      var tr = document.createElement('tr');
      var td = cell(NO_GOALS, 'muted');
      td.colSpan = 6;
      tr.appendChild(td);
      body.appendChild(tr);
    }
  }

  document.addEventListener('submit', function (e) {
    var form = e.target;
    if (!form || !form.matches || !form.matches('[data-goal-form]')) return;
    e.preventDefault();
    var name = ((form.querySelector('[data-goal-name]') || {}).value || '').trim();
    var kind = (form.querySelector('[data-goal-kind]') || {}).value || 'path';
    var target = ((form.querySelector('[data-goal-target]') || {}).value || '').trim();
    if (!name || !target) { if (window.vpToast) window.vpToast('Name and target are required.', 'error'); return; }
    fetch('/os/api/analytics/goals', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() },
      body: JSON.stringify({ name: name, kind: kind, target: target })
    })
      .then(function (r) { return r.ok ? r.json() : Promise.reject(r); })
      .then(function (d) {
        addGoalRow(d.id, name, kind, target);
        form.reset();
        if (window.vpToast) window.vpToast('Goal “' + name + '” added.', 'ok');
      })
      .catch(function () { if (window.vpToast) window.vpToast('Could not add goal. Check the name and target.', 'error'); });
  });

  document.addEventListener('click', function (e) {
    var b = e.target && e.target.closest ? e.target.closest('[data-goal-delete]') : null;
    if (!b) return;
    var id = b.getAttribute('data-goal-delete');
    vpConfirm({
      title: 'Delete this goal',
      message: 'Delete this goal? Its conversions stop being counted.',
      confirm: 'Delete',
    }, function () {
      fetch('/os/api/analytics/goals/' + encodeURIComponent(id), {
        method: 'DELETE',
        headers: { 'X-CSRF-Token': csrf() }
      })
        .then(function (r) { return r.ok ? r : Promise.reject(r); })
        .then(function () {
          removeGoalRow(b);
          if (window.vpToast) window.vpToast('Goal deleted.', 'ok');
        })
        .catch(function () { if (window.vpToast) window.vpToast('Delete failed.', 'error'); });
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

  // The card is looked up on every render, not kept: an in-place refresh after
  // adding a goal replaces it, and a kept reference would freeze the live view.
  if (!document.querySelector('[data-live]')) return;

  function clear(el) { while (el && el.firstChild) el.removeChild(el.firstChild); }

  function emptyState(el, msg) {
    var d = document.createElement('div');
    d.className = 'empty-state';
    d.textContent = msg;
    el.appendChild(d);
  }

  // widthClass snaps a 0..100 percentage to the nearest 5% bucket, mirroring the
  // server's barWidthClass so live bars reuse the same w-N utility classes and
  // need no inline width style (strict CSP: style-src 'self').
  function widthClass(pct) {
    if (pct < 0) pct = 0;
    if (pct > 100) pct = 100;
    return 'w-' + (Math.floor((pct + 2) / 5) * 5);
  }

  // makeBar builds one .vp-bar row — structurally identical to the server's
  // osBarList output: a label, the count with its share %, and a proportional
  // coloured track. labelNode is a string or a DOM node (flag + country name);
  // title is the hover/aria text. CSP-safe: DOM API only, no innerHTML.
  function makeBar(labelNode, title, value, max, total, colorIndex) {
    var row = document.createElement('div');
    row.className = 'vp-bar vp-bar--c' + colorIndex + ' vp-bar--enter';

    var label = document.createElement('span');
    label.className = 'vp-bar__label';
    if (title) label.title = title;
    if (typeof labelNode === 'string') label.textContent = labelNode;
    else label.appendChild(labelNode);

    var val = document.createElement('span');
    val.className = 'vp-bar__val';
    val.appendChild(document.createTextNode(String(value)));
    if (total > 0) {
      var pctSpan = document.createElement('span');
      pctSpan.className = 'vp-bar__pct';
      pctSpan.textContent = Math.round(value * 100 / total) + '%';
      val.appendChild(pctSpan);
    }

    var track = document.createElement('span');
    track.className = 'vp-bar__track';
    var fill = document.createElement('span');
    var pct = max > 0 ? Math.floor(value * 100 / max) : 0;
    fill.className = 'vp-bar__fill ' + widthClass(pct);
    track.appendChild(fill);

    row.appendChild(label);
    row.appendChild(val);
    row.appendChild(track);
    return row;
  }

  // maxTotal returns {max, total} of the count field across items (max floored
  // at 1 so a single visitor still fills its bar proportionally).
  function maxTotal(items) {
    var max = 1, total = 0;
    items.forEach(function (it) { var v = it.count || 0; if (v > max) max = v; total += v; });
    return { max: max, total: total };
  }

  // Fill a bar list from {label,count}/{path,count}. Proportional coloured bars
  // (relative to the busiest row) plus each row's share of the total.
  function fill(el, items, labelKey, emptyMsg) {
    if (!el) return;
    clear(el);
    items = items || [];
    if (!items.length) { emptyState(el, emptyMsg); return; }
    var mt = maxTotal(items);
    items.forEach(function (it, i) {
      var label = String(it[labelKey] || it.label || '(unknown)');
      el.appendChild(makeBar(label, label, it.count || 0, mt.max, mt.total, (i % 8) + 1));
    });
  }

  // Fill the live-countries bars with a flag <img> (or a neutral globe for
  // unknown locations) + name + count. Resilient to old/new payload shapes.
  function fillCountries(el, items) {
    if (!el) return;
    clear(el);
    items = items || [];
    if (!items.length) { emptyState(el, 'No active visitors right now.'); return; }
    var mt = maxTotal(items);
    items.forEach(function (c, i) {
      var name = c.name || c.label || c.code || 'Unknown';
      var frag = document.createDocumentFragment();
      if (c.flag) {
        var img = document.createElement('img');
        img.className = 'vp-flag-img';
        img.src = c.flag;
        img.alt = '';
        img.width = 20; img.height = 15;
        img.loading = 'lazy';
        frag.appendChild(img);
      } else {
        var globe = document.createElement('span');
        globe.className = 'vp-flag-img vp-flag-unknown';
        globe.setAttribute('aria-hidden', 'true');
        frag.appendChild(globe);
      }
      frag.appendChild(document.createTextNode(' ' + name));
      el.appendChild(makeBar(frag, name, c.count || 0, mt.max, mt.total, (i % 8) + 1));
    });
  }

  function render(data) {
    var card = document.querySelector('[data-live]');
    if (!card) return;
    var countEl = card.querySelector('[data-live-count]');
    var pagesEl = card.querySelector('[data-live-pages]');
    var countriesEl = card.querySelector('[data-live-countries]');
    var referrersEl = card.querySelector('[data-live-referrers]');
    var updatedEl = card.querySelector('[data-live-updated]');
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
 * Period selector loading cue (Analytics page). The selector is plain GET
 * navigation; on click we mark the bar busy so the operator gets immediate
 * feedback while the next time-window renders. No-op without the bar. CSP-safe:
 * toggles a class only, no inline styles.
 */
(function () {
  'use strict';

  document.addEventListener('click', function (e) {
    var link = e.target && e.target.closest ? e.target.closest('[data-period] a') : null;
    if (!link) return;
    var bar = link.closest('[data-period]');
    bar.classList.add('is-loading');
    bar.setAttribute('aria-busy', 'true');
  });
})();
