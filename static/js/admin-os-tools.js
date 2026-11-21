/*
 * admin-os-tools.js — Tools & Plugins panel for VayuOS (VayuOS foundation).
 * Strict CSP: no eval, DOM updates via textContent / className only.
 *
 * Wires each module switch to POST /os/api/tools/toggle. On success the
 * card's status label is updated in place; on failure the switch reverts so the
 * UI never drifts from server state. Its words go through vpT, so the page
 * says them in its own language.
 */
(function () {
  'use strict';

  function csrf() {
    var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }

  // The state is a dot and a word (ui.State), rebuilt from nodes: markup is
  // never written from script here.
  function setStatus(card, enabled) {
    var el = card.querySelector('[data-tool-status]');
    if (!el) return;
    var mark = document.createElement('span');
    mark.className = 'sa-indicator';
    var dot = document.createElement('span');
    dot.className = 'sa-dot sa-dot--' + (enabled ? 'ok' : 'neutral');
    dot.setAttribute('aria-hidden', 'true');
    mark.appendChild(dot);
    mark.appendChild(document.createTextNode(enabled ? vpT('On') : vpT('Off')));
    el.textContent = '';
    el.appendChild(mark);
  }

  var toggles = document.querySelectorAll('[data-tool-toggle]');
  Array.prototype.forEach.call(toggles, function (input) {
    input.addEventListener('change', function () {
      var id = input.getAttribute('data-tool-toggle');
      // The module's name as the page shows it, not its id.
      var name = input.getAttribute('aria-label') || id;
      var card = input.closest('[data-tool-card]');
      var want = input.checked;
      input.disabled = true;

      fetch('/os/api/tools/toggle', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-CSRF-Token': csrf()
        },
        body: JSON.stringify({ id: id, enabled: want })
      })
        .then(function (r) { return r.ok ? r.json() : Promise.reject(r); })
        .then(function () {
          if (card) setStatus(card, want);
          if (window.vpToast) {
            window.vpToast(want ? vpT('{name} is on', { name: name }) : vpT('{name} is off', { name: name }), 'ok');
          }
        })
        .catch(function () {
          // Revert the optimistic switch so the UI matches server truth.
          input.checked = !want;
          if (window.vpToast) window.vpToast(vpT('{name} could not be switched', { name: name }), 'error');
        })
        .finally(function () { input.disabled = false; });
    });
  });
})();
