/*
 * admin-v3-intel.js — SEO dashboard actions for Admin v3 (ADR-0068, Phase 6).
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
    fetch('/admin/v3/api/seo/regenerate', {
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
