/*
 * admin-os-tor.js — VayuTor page island. Copy-to-clipboard for onion addresses
 * and a lightweight count-only poller (no per-visitor data ever crosses the
 * wire). Strict CSP: no eval, no inline styles; DOM via textContent only.
 */
(function () {
  'use strict';

  var root = document.querySelector('[data-tor]');
  if (!root) return;

  // ── CSRF: fill every VayuTor form's hidden field from the LIVE vp_csrf cookie
  // at submit time. The admin layout rotates the double-submit cookie on every
  // render, so any token baked into the page markup is already stale by the time
  // the form posts. Reading document.cookie here guarantees the field matches
  // whatever cookie the browser currently holds. ──
  document.querySelectorAll('[data-tor-form]').forEach(function (form) {
    form.addEventListener('submit', function () {
      var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
      var field = form.querySelector('input[name="csrf_token"]');
      if (m && field) field.value = m[1];
    });
  });

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

  // ── Live poller (count + vanity search progress) ──
  var countEl = document.querySelector('[data-tor-visits]');
  var vanityActive = !!document.querySelector('[data-vanity-active]');
  // Nothing to poll for if there's no counter AND no running vanity search.
  if (!countEl && !vanityActive) return;

  function applyVanity(v) {
    // A finished/started search (found, or active→gone) changes the card layout —
    // reload so the server re-renders it. Otherwise just refresh the live numbers.
    if (!v) {
      if (vanityActive) { window.location.reload(); } // search ended
      return;
    }
    if (v.found || !v.active) { window.location.reload(); return; }
    var triesEl = document.querySelector('[data-vanity-tries]');
    var elapsedEl = document.querySelector('[data-vanity-elapsed]');
    if (triesEl && typeof v.tries === 'number') triesEl.textContent = String(v.tries);
    if (elapsedEl && typeof v.elapsed === 'string') elapsedEl.textContent = v.elapsed;
  }

  function poll() {
    fetch('/os/tor/stats', { headers: { 'Accept': 'application/json' } })
      .then(function (r) { return r.ok ? r.json() : Promise.reject(r); })
      .then(function (d) {
        if (!d) return;
        if (countEl && typeof d.visits === 'number') countEl.textContent = String(d.visits);
        if (vanityActive) applyVanity(d.vanity);
        // If onions came up since page load, a soft reload surfaces the table.
        var onionsAppeared = d.active && d.connected && d.onions > 0 &&
            document.querySelectorAll('[data-onion]').length === 0;
        // If tor finished joining the network since the page rendered, reload so
        // the state flips from "Connecting to Tor (NN%)" to "Active".
        var renderedBoot = parseInt(root.getAttribute('data-boot-pct') || '0', 10);
        var bootstrapped = d.connected && typeof d.bootstrap === 'number' &&
            d.bootstrap >= 100 && renderedBoot < 100;
        if (onionsAppeared || bootstrapped) {
          window.location.reload();
        }
      })
      .catch(function () { /* transient; keep last value */ });
  }

  // Poll quickly while a vanity search is running (progress feels live); slowly
  // otherwise (count only).
  var interval = vanityActive ? 2000 : 15000;
  var timer = window.setInterval(poll, interval);
  document.addEventListener('visibilitychange', function () {
    if (document.hidden) { window.clearInterval(timer); }
    else { poll(); timer = window.setInterval(poll, interval); }
  });
})();
