/* VayuOS · Still Air shell behaviour.
   Loaded only by the Still Air shell. Everything the classic shell also does
   (drawer, command bar, bell, PWA install, world switch) stays in admin-os.js
   and reaches this shell through the same hooks; this file adds only what the
   new shell introduces: popover menus and the colour-scheme control. The design
   switch lives in admin-os.js, because the classic console offers it too. No inline handlers, nothing evaluated: CSP script-src 'self'. */
(function () {
  'use strict';

  function csrf() {
    var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }
  function saveSetting(key, value) {
    return fetch('/os/api/settings', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() },
      body: JSON.stringify({ key: key, value: value })
    }).then(function (r) { if (!r.ok) throw new Error('HTTP ' + r.status); });
  }
  function say(msg, kind) { if (window.vpToast) window.vpToast(msg, kind); }

  /* ── Popovers (<details class="sa-pop">) ──────────────────────────────
     A details element opens and closes itself; what it lacks is closing when
     the pointer or focus goes elsewhere, Escape, and one-at-a-time. */
  var pops = Array.prototype.slice.call(document.querySelectorAll('details.sa-pop'));
  function closeAll(except) {
    pops.forEach(function (d) { if (d !== except) d.open = false; });
  }
  function items(d) {
    return Array.prototype.slice.call(d.querySelectorAll('[role="menuitem"]:not([hidden]), .sa-seg__opt'));
  }
  pops.forEach(function (d) {
    d.addEventListener('toggle', function () {
      if (!d.open) return;
      closeAll(d);
      // Opened from the keyboard: move into the menu so arrows work at once.
      if (d.dataset.kbd === '1') {
        var first = items(d)[0];
        if (first) first.focus();
      }
      d.dataset.kbd = '';
    });
    var sum = d.querySelector('summary');
    if (sum) {
      sum.addEventListener('keydown', function (e) {
        if (e.key === 'Enter' || e.key === ' ' || e.key === 'ArrowDown') {
          d.dataset.kbd = '1';
          if (e.key === 'ArrowDown') { e.preventDefault(); d.open = true; }
        }
      });
    }
    d.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && d.open) {
        e.preventDefault();
        d.open = false;
        if (sum) sum.focus();
        return;
      }
      if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
      var list = items(d);
      if (!list.length || !d.open) return;
      var i = list.indexOf(document.activeElement);
      if (i < 0 && document.activeElement === sum) return;
      e.preventDefault();
      i = e.key === 'ArrowDown' ? (i + 1) % list.length : (i - 1 + list.length) % list.length;
      list[i].focus();
    });
  });
  document.addEventListener('click', function (e) {
    var inside = e.target.closest ? e.target.closest('details.sa-pop') : null;
    closeAll(inside);
  });
  document.addEventListener('focusin', function (e) {
    var inside = e.target.closest ? e.target.closest('details.sa-pop') : null;
    closeAll(inside);
  });

  /* ── Colour scheme ─────────────────────────────────────────────────────
     Applied at once, saved for every device, mirrored for the sign-in pages
     (os-theme.js reads the same key). A failed save is said out loud: the page
     already shows the new scheme, so silence would read as saved. */
  var seg = Array.prototype.slice.call(document.querySelectorAll('[data-sa-theme]'));
  seg.forEach(function (b) {
    b.addEventListener('click', function () {
      var next = b.getAttribute('data-sa-theme');
      document.body.dataset.theme = next;
      seg.forEach(function (o) { o.setAttribute('aria-pressed', o === b ? 'true' : 'false'); });
      try { localStorage.setItem('vp-os-theme', next); } catch (e) { /* private mode: page-only */ }
      saveSetting('admin.theme', next).catch(function () {
        say('Colour scheme changed for this page only — it could not be saved.', 'warn');
      });
    });
  });

})();
