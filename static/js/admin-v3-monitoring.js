/*
 * admin-v3-monitoring.js — live refresh for the Admin v3 Monitoring surface.
 * Strict CSP: no eval, DOM updates via textContent / className only.
 *
 * Polls the existing read-only operator APIs every 5 s and updates the system
 * mode pill and the governance-budget states in place. Read-only GETs need no
 * CSRF token. The page already renders a correct server-side snapshot, so this
 * only keeps an open dashboard fresh; any fetch error is swallowed silently and
 * retried on the next tick.
 */
(function () {
  'use strict';

  var POLL_MS = 5000;

  function pill(el, label, cls) {
    if (!el) return;
    el.textContent = label;
    el.className = 'tool-status ' + cls;
  }

  function modeClass(m) {
    if (m === 'normal') return 'tool-status--on';
    if (m === 'degraded' || m === 'recovery' || m === 'maintenance') return 'tool-status--idle';
    return 'tool-status--off';
  }

  function budgetClass(state) {
    if (state === 'healthy') return 'tool-status--on';
    if (state === 'at-risk') return 'tool-status--idle';
    return 'tool-status--off';
  }

  function stamp() {
    var u = document.querySelector('[data-mon-updated]');
    if (u) u.textContent = 'updated ' + new Date().toLocaleTimeString();
  }

  function refreshMode() {
    return fetch('/admin/v3/api/mode', { headers: { Accept: 'application/json' } })
      .then(function (r) { return r.ok ? r.json() : Promise.reject(r); })
      .then(function (d) {
        if (d && typeof d.mode === 'string') {
          pill(document.querySelector('[data-mon-mode]'), d.mode, modeClass(d.mode));
        }
      });
  }

  function refreshBudgets() {
    return fetch('/admin/v3/api/budgets', { headers: { Accept: 'application/json' } })
      .then(function (r) { return r.ok ? r.json() : Promise.reject(r); })
      .then(function (d) {
        var list = (d && d.budgets) || [];
        list.forEach(function (b) {
          var row = document.querySelector('[data-mon-budget="' + (window.CSS && CSS.escape ? CSS.escape(b.name) : b.name) + '"]');
          if (row) {
            pill(row.querySelector('[data-mon-budget-state]'), b.state, budgetClass(b.state));
          }
        });
      });
  }

  function tick() {
    Promise.all([refreshMode(), refreshBudgets()]).then(stamp).catch(function () {});
  }

  tick();
  setInterval(tick, POLL_MS);
})();
