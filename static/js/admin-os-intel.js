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
    // No kind is progress, not success: a tick before anything happened is a claim.
    if (window.vpToast) window.vpToast(msg, kind === 'danger' ? 'error' : (kind === 'ok' ? 'ok' : 'info'));
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
  var NO_GOALS = 'No goals yet. Add one with New goal: a page such as /thank-you, or a custom event such as signup.';
  function goalsBody() { return document.querySelector('[data-goals] tbody'); }
  // The section names where its goals live: the install's, or one site's.
  function goalBase() {
    var g = document.querySelector('[data-goals]');
    return (g && g.getAttribute('data-goal-base')) || '/os/api/analytics/goals';
  }
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
    tr.appendChild(cell(name, 'post-row__name'));
    // The same words the server writes for a goal's kind (goalKindLabel).
    tr.appendChild(cell(kind === 'event' ? 'A custom event' : 'Viewing a page'));
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
    del.className = 'btn btn--ghost btn--sm';
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
    fetch(goalBase(), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() },
      body: JSON.stringify({ name: name, kind: kind, target: target })
    })
      .then(function (r) { return r.ok ? r.json() : Promise.reject(r); })
      .then(function (d) {
        addGoalRow(d.id, name, kind, target);
        form.reset();
        var sheet = form.closest('dialog');
        if (sheet && sheet.open) sheet.close();
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
      fetch(goalBase() + '/' + encodeURIComponent(id), {
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
 * Live analytics — polls the realtime endpoint every 10s and updates the
 * Right now panel: the readers' count, the pages they are on, and where they
 * came from. CSP-safe:
 * DOM updated via textContent only, no eval, no inline styles. No-op without
 * the live card.
 */
(function () {
  'use strict';

  // The card is looked up on every render, not kept: an in-place refresh after
  // adding a goal replaces it, and a kept reference would freeze the live view.
  if (!document.querySelector('[data-live]')) return;

  function clear(el) { while (el && el.firstChild) el.removeChild(el.firstChild); }

  // The empty sentence, in the server's own form (osBarList).
  function emptyState(el, msg) {
    var p = document.createElement('p');
    p.className = 'table-empty';
    p.textContent = msg;
    el.appendChild(p);
  }

  // widthClass snaps a 0..100 percentage to the nearest 5% bucket, mirroring the
  // server's barWidthClass so live bars reuse the same w-N utility classes and
  // need no inline width style (strict CSP: style-src 'self').
  function widthClass(pct) {
    if (pct < 0) pct = 0;
    if (pct > 100) pct = 100;
    return 'w-' + (Math.floor((pct + 2) / 5) * 5);
  }

  // makeBar builds one .vp-bar row as the server's osBarList does: a label,
  // the count with its share, and a bar in the one ink every list uses.
  // CSP-safe: DOM API only, no innerHTML.
  // flag, when given, is a country's flag from the console's own flags
  // folder, drawn before the label as the server's countryFlagImg does.
  function makeBar(text, value, max, total, flag) {
    var row = document.createElement('div');
    row.className = 'vp-bar';
    var label = document.createElement('span');
    label.className = 'vp-bar__label';
    label.title = text;
    if (flag && /^\/os\/static\/flags\/[a-z]{2}\.svg$/.test(flag)) {
      var img = document.createElement('img');
      img.className = 'vp-flag-img';
      img.src = flag;
      img.alt = '';
      img.width = 20;
      img.height = 15;
      label.appendChild(img);
      label.appendChild(document.createTextNode(' '));
    }
    label.appendChild(document.createTextNode(text));
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
    fill.className = 'vp-bar__fill ' + widthClass(max > 0 ? Math.floor(value * 100 / max) : 0);
    track.appendChild(fill);
    row.appendChild(label);
    row.appendChild(val);
    row.appendChild(track);
    return row;
  }

  // Fill a bar list from {label,count}/{path,count}: bars relative to the
  // busiest row, each with its share of the total.
  function fill(el, items, labelKey, emptyMsg) {
    if (!el) return;
    clear(el);
    items = items || [];
    if (!items.length) { emptyState(el, emptyMsg); return; }
    var max = 1, total = 0;
    items.forEach(function (it) { var v = it.count || 0; if (v > max) max = v; total += v; });
    items.forEach(function (it) {
      el.appendChild(makeBar(String(it[labelKey] || it.label || '(unknown)'), it.count || 0, max, total, it.flag));
    });
  }

  function render(data) {
    var card = document.querySelector('[data-live]');
    if (!card) return;
    data = data || {};
    var active = data.active_visitors || 0;
    var countEl = card.querySelector('[data-live-count]');
    if (countEl) countEl.textContent = String(active);
    // The noun follows the count the poller just wrote: "1 visitors" was the
    // sentence the design lint caught once the panel was laid open.
    var nounEl = card.querySelector('[data-live-noun]');
    if (nounEl) nounEl.textContent = active === 1 ? 'visitor' : 'visitors';
    fill(card.querySelector('[data-live-pages]'), data.active_pages, 'path', 'No one is reading now.');
    fill(card.querySelector('[data-live-referrers]'), data.active_referrers, 'label', 'No one came from another site in the last 5 minutes.');
    fill(card.querySelector('[data-live-channels]'), data.active_channels, 'label', 'No one is reading now.');
    fill(card.querySelector('[data-live-countries]'), data.active_countries, 'name', 'No one is reading now.');
    fill(card.querySelector('[data-live-cities]'), data.active_cities, 'name', 'No city recorded in the last 5 minutes.');
    fill(card.querySelector('[data-live-devices]'), data.active_devices, 'label', 'No one is reading now.');
    fill(card.querySelector('[data-live-browsers]'), data.active_browsers, 'label', 'No one is reading now.');
    var updatedEl = card.querySelector('[data-live-updated]');
    if (updatedEl) {
      var t = new Date();
      updatedEl.textContent = ', at ' + t.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    }
  }

  function poll() {
    // The card names its source: the install's figures, or one site's.
    var card = document.querySelector('[data-live]');
    var src = (card && card.getAttribute('data-live-src')) || '/os/api/analytics/realtime';
    fetch(src, { headers: { 'Accept': 'application/json' } })
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

/*
 * The traffic chart's readout. Each day's band carries its counts (and a
 * <title>, the browser's tooltip when this script is not run); here they are
 * set in the chart's own readout beside the hovered day, at reading size,
 * with a dot on each line. Positioned through the CSSOM, which the console's
 * style-src allows; the markup carries no style attribute. The dots' height
 * is the drawing's: its plot runs from 4 to 196 of 200 units.
 */
(function () {
  'use strict';

  function row(cls, label, value) {
    var r = document.createElement('div');
    r.className = 'vp-trend-tip__row';
    var key = document.createElement('span');
    key.className = cls;
    var b = document.createElement('b');
    b.textContent = Number(value).toLocaleString('en-US');
    r.appendChild(key);
    r.appendChild(document.createTextNode(label));
    r.appendChild(b);
    return r;
  }

  document.querySelectorAll('.vp-trend-plot').forEach(function (plot) {
    var tip = plot.querySelector('.vp-trend-tip');
    var svg = plot.querySelector('.vp-trend-svg');
    var dots = plot.querySelectorAll('.vp-trend-dot');
    if (!tip || !svg || dots.length !== 2) return;
    var max = Number(svg.getAttribute('data-max')) || 1;
    plot.querySelectorAll('.vp-pt').forEach(function (pt) {
      var title = pt.querySelector('title');
      if (title) pt.removeChild(title);
      pt.addEventListener('pointerenter', function () {
        var pv = pt.getAttribute('data-pv'), vis = pt.getAttribute('data-vis');
        while (tip.firstChild) tip.removeChild(tip.firstChild);
        var date = document.createElement('div');
        date.className = 'vp-trend-tip__date';
        date.textContent = pt.getAttribute('data-day');
        tip.appendChild(date);
        tip.appendChild(row('vp-legend__line', 'Page views', pv));
        tip.appendChild(row('vp-legend__line vp-legend__line--vis', 'Visitors', vis));
        tip.hidden = false;

        var box = plot.getBoundingClientRect();
        var area = svg.getBoundingClientRect();
        var day = pt.getBoundingClientRect();
        var x = day.left + day.width / 2 - box.left;
        [pv, vis].forEach(function (v, i) {
          var y = area.top - box.top + area.height * (196 - (Number(v) / max) * 192) / 200;
          dots[i].style.left = x + 'px';
          dots[i].style.top = y + 'px';
          dots[i].hidden = false;
        });
        var left = x + 14;
        if (left + tip.offsetWidth > box.width) left = x - 14 - tip.offsetWidth;
        tip.style.left = Math.max(0, left) + 'px';
      });
    });
    // A touch leaves as it lifts: the readout stays until another day is
    // tapped, or the page is touched elsewhere.
    plot.addEventListener('pointerleave', function (e) {
      if (e.pointerType === 'touch') return;
      tip.hidden = true;
      dots[0].hidden = dots[1].hidden = true;
    });
    document.addEventListener('pointerdown', function (e) {
      if (e.pointerType === 'touch' && !plot.contains(e.target)) {
        tip.hidden = true;
        dots[0].hidden = dots[1].hidden = true;
      }
    });
  });
})();
