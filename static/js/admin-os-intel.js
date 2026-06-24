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
