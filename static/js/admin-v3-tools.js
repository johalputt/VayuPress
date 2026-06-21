/*
 * admin-v3-tools.js — Tools & Plugins panel for Admin v3 (VayuOS foundation).
 * Strict CSP: no eval, DOM updates via textContent / className only.
 *
 * Wires each module switch to POST /os/api/tools/toggle. On success the
 * card's status label is updated in place; on failure the switch reverts so the
 * UI never drifts from server state.
 */
(function () {
  'use strict';

  function csrf() {
    var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }

  function setStatus(card, enabled) {
    var el = card.querySelector('[data-tool-status]');
    if (!el) return;
    if (enabled) {
      el.textContent = 'Active';
      el.className = 'tool-status tool-status--on';
    } else {
      el.textContent = 'Disabled';
      el.className = 'tool-status tool-status--off';
    }
  }

  var toggles = document.querySelectorAll('[data-tool-toggle]');
  Array.prototype.forEach.call(toggles, function (input) {
    input.addEventListener('change', function () {
      var id = input.getAttribute('data-tool-toggle');
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
            window.vpToast((want ? 'Enabled ' : 'Disabled ') + id, 'ok');
          }
        })
        .catch(function () {
          // Revert the optimistic switch so the UI matches server truth.
          input.checked = !want;
          if (window.vpToast) window.vpToast('Could not update ' + id, 'error');
        })
        .finally(function () { input.disabled = false; });
    });
  });
})();
