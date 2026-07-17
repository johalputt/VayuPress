/*
 * admin-os-tor.js — VayuTor page island. Copy-to-clipboard for onion addresses
 * and a lightweight count-only poller (no per-visitor data ever crosses the
 * wire). Strict CSP: no eval, no inline styles; DOM via textContent only.
 */
(function () {
  'use strict';

  var root = document.querySelector('[data-tor]');
  if (!root) return;

  // ── Copy .onion address to clipboard ──
  document.querySelectorAll('[data-copy]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var val = btn.getAttribute('data-copy') || '';
      var done = function () {
        var prev = btn.textContent;
        btn.textContent = 'Copied';
        btn.classList.add('is-copied');
        setTimeout(function () { btn.textContent = prev; btn.classList.remove('is-copied'); }, 1400);
      };
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(val).then(done, function () { fallbackCopy(val, done); });
      } else {
        fallbackCopy(val, done);
      }
    });
  });

  function fallbackCopy(text, done) {
    var ta = document.createElement('textarea');
    ta.value = text;
    ta.setAttribute('readonly', '');
    ta.className = 'vt-copy-buf';
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand('copy'); done(); } catch (e) { /* no-op */ }
    document.body.removeChild(ta);
  }

  // ── Live count poller (count only) ──
  var countEl = document.querySelector('[data-tor-visits]');
  if (!countEl) return;

  function poll() {
    fetch('/os/tor/stats', { headers: { 'Accept': 'application/json' } })
      .then(function (r) { return r.ok ? r.json() : Promise.reject(r); })
      .then(function (d) {
        if (d && typeof d.visits === 'number') countEl.textContent = String(d.visits);
        // If onions came up since page load, a soft reload surfaces the table.
        if (d && d.active && d.connected && d.onions > 0 &&
            document.querySelectorAll('[data-onion]').length === 0) {
          window.location.reload();
        }
      })
      .catch(function () { /* transient; keep last value */ });
  }

  var timer = window.setInterval(poll, 15000);
  document.addEventListener('visibilitychange', function () {
    if (document.hidden) { window.clearInterval(timer); }
    else { poll(); timer = window.setInterval(poll, 15000); }
  });
})();
