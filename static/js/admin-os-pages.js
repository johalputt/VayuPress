/* admin-os-pages.js — VayuOS Pages surface.
 *
 *   1. Quick-create a page: type a title, press Enter → POST quick-create →
 *      open the editor on the new page slug.
 *   2. "In menu" toggle per page: add/remove the page's {label,href} in the
 *      public nav (settings key nav.items) via the shared /os/api/settings
 *      endpoint — no bespoke server route, fully reusing the validated path.
 *
 * CSP-clean: external same-origin script, no inline handlers, no eval.
 */
(function () {
  'use strict';

  function csrf() {
    var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }
  function setText(el, t) { if (el) el.textContent = t; }

  // ── Quick-create ───────────────────────────────────────────────────────────
  var input = document.getElementById('page-compose-input');
  var createStatus = document.getElementById('page-compose-status');
  if (input) {
    input.addEventListener('keydown', function (e) {
      if (e.key !== 'Enter') return;
      var title = input.value.trim();
      if (!title) return;
      input.disabled = true;
      setText(createStatus, 'Creating…');
      fetch('/os/api/pages/quick-create', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() },
        body: JSON.stringify({ title: title }),
      })
        .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, d: d }; }); })
        .then(function (res) {
          if (res.ok && res.d.slug) {
            window.location.href = '/os/editor/' + res.d.slug;
          } else {
            setText(createStatus, (res.d && (res.d.detail || res.d.title)) || 'Could not create page');
            input.disabled = false;
          }
        })
        .catch(function (e) { setText(createStatus, 'Network error: ' + e); input.disabled = false; });
    });
  }

  // ── Navigation toggles ───────────────────────────────────────────────────────
  var navStatus = document.getElementById('page-nav-status');
  var seedEl = document.getElementById('page-nav-seed');
  var navItems = [];
  if (seedEl) {
    try { navItems = JSON.parse(seedEl.getAttribute('data-nav') || '[]') || []; }
    catch (_) { navItems = []; }
  }
  if (!Array.isArray(navItems)) navItems = [];

  // Reflect current nav membership in the toggles on load.
  var toggles = Array.prototype.slice.call(document.querySelectorAll('[data-page-nav]'));
  toggles.forEach(function (t) {
    var href = t.getAttribute('data-href');
    t.checked = navItems.some(function (it) { return it && it.href === href; });
  });

  function saveNav() {
    return fetch('/os/api/settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() },
      body: JSON.stringify({ key: 'nav.items', value: JSON.stringify(navItems) }),
    });
  }

  toggles.forEach(function (t) {
    t.addEventListener('change', function () {
      var href = t.getAttribute('data-href');
      var label = t.getAttribute('data-label') || href;
      if (t.checked) {
        if (!navItems.some(function (it) { return it && it.href === href; })) {
          navItems.push({ label: label, href: href });
        }
      } else {
        navItems = navItems.filter(function (it) { return !(it && it.href === href); });
      }
      t.disabled = true;
      setText(navStatus, 'Saving menu…');
      saveNav()
        .then(function (r) {
          t.disabled = false;
          setText(navStatus, r.ok ? 'Menu updated' : 'Could not update menu');
        })
        .catch(function (e) { t.disabled = false; setText(navStatus, 'Network error: ' + e); });
    });
  });

  // ── Footer toggles ───────────────────────────────────────────────────────────
  // Footer pages live in the footer config's "legal" bottom-bar links (where
  // About / Privacy / Terms belong). We read the whole footer object, edit just
  // the legal array, and POST it back through the shared settings endpoint.
  var footerCfg = {};
  if (seedEl) {
    try { footerCfg = JSON.parse(seedEl.getAttribute('data-footer') || '{}') || {}; }
    catch (_) { footerCfg = {}; }
  }
  if (typeof footerCfg !== 'object' || footerCfg === null) footerCfg = {};
  if (!Array.isArray(footerCfg.legal)) footerCfg.legal = [];

  var footerToggles = Array.prototype.slice.call(document.querySelectorAll('[data-page-footer]'));
  footerToggles.forEach(function (t) {
    var href = t.getAttribute('data-href');
    t.checked = footerCfg.legal.some(function (it) { return it && it.href === href; });
  });

  function saveFooter() {
    return fetch('/os/api/settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() },
      body: JSON.stringify({ key: 'footer.config', value: JSON.stringify(footerCfg) }),
    });
  }

  footerToggles.forEach(function (t) {
    t.addEventListener('change', function () {
      var href = t.getAttribute('data-href');
      var label = t.getAttribute('data-label') || href;
      if (t.checked) {
        if (!footerCfg.legal.some(function (it) { return it && it.href === href; })) {
          footerCfg.legal.push({ label: label, href: href });
        }
      } else {
        footerCfg.legal = footerCfg.legal.filter(function (it) { return !(it && it.href === href); });
      }
      t.disabled = true;
      setText(navStatus, 'Saving footer…');
      saveFooter()
        .then(function (r) {
          t.disabled = false;
          setText(navStatus, r.ok ? 'Footer updated' : 'Could not update footer');
        })
        .catch(function (e) { t.disabled = false; setText(navStatus, 'Network error: ' + e); });
    });
  });
})();
