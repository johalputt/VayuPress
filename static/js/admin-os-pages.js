/* admin-os-pages.js — VayuOS Pages surface.
 *
 *   1. New page: the sheet's title and template → POST quick-create → open
 *      the editor on the new page slug.
 *   2. In each page's inspector, "In the site's menu" and its footer group:
 *      the public nav (nav.items) and footer (footer.config) are rewritten
 *      through the shared /os/api/settings endpoint — no bespoke server route,
 *      fully reusing the validated path.
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

  // ── New page ───────────────────────────────────────────────────────────────
  var createForm = document.querySelector('[data-page-create]');
  var input = document.getElementById('page-compose-input');
  var templateSel = document.getElementById('page-compose-template');
  var createStatus = document.getElementById('page-compose-status');
  if (createForm && input) {
    createForm.addEventListener('submit', function (e) {
      e.preventDefault();
      var title = input.value.trim();
      if (!title) return;
      input.disabled = true;
      setText(createStatus, 'Creating…');
      fetch('/os/api/pages/quick-create', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() },
        body: JSON.stringify({ title: title, template: templateSel ? templateSel.value : 'blank' }),
      })
        .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, d: d }; }); })
        .then(function (res) {
          if (res.ok && res.d.slug) {
            window.location.href = '/os/editor/' + res.d.slug;
          } else {
            setText(createStatus, (res.d && res.d.error && res.d.error.message) || 'Could not create page');
            input.disabled = false;
          }
        })
        .catch(function (e) { setText(createStatus, 'Network error: ' + e); input.disabled = false; });
    });
  }

  // ── Delete page ───────────────────────────────────────────────────────────────
  document.querySelectorAll('[data-page-delete]').forEach(function (b) {
    b.addEventListener('click', function () {
      var t = b.getAttribute('data-title') || 'this page';
      vpConfirm({
        title: 'Delete this page',
        message: 'Delete "' + t + '"? This permanently removes the page and cannot be undone.',
        confirm: 'Delete',
      }, function () {
        b.disabled = true;
        fetch('/os/api/posts/' + encodeURIComponent(b.getAttribute('data-slug')), { method: 'DELETE', headers: { 'X-CSRF-Token': csrf() } })
          .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, d: d }; }); })
          .then(function (res) {
            if (res.ok) {
              var slug = b.getAttribute('data-slug');
              document.querySelectorAll('[data-list-panel="' + slug + '"], [data-list-panel-id="' + slug + '"]').forEach(function (el) { el.remove(); });
            }
            else { b.disabled = false; setText(navStatus, (res.d && (res.d.detail || res.d.title)) || 'Could not delete'); }
          })
          .catch(function (e) { b.disabled = false; setText(navStatus, 'Network error: ' + e); });
      });
    });
  });

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

  // ── Footer placement ─────────────────────────────────────────────────────────
  // A page can sit nowhere, in the bottom-bar "legal" links, or in any footer
  // column (grouped links). The <select> value encodes the target: "" (none),
  // "legal", or "col:<Title>". We read the whole footer object, move the page's
  // link to the chosen target, prune any column we emptied, and POST it back.
  var footerCfg = {};
  if (seedEl) {
    try { footerCfg = JSON.parse(seedEl.getAttribute('data-footer') || '{}') || {}; }
    catch (_) { footerCfg = {}; }
  }
  if (typeof footerCfg !== 'object' || footerCfg === null) footerCfg = {};
  if (!Array.isArray(footerCfg.legal)) footerCfg.legal = [];
  if (!Array.isArray(footerCfg.columns)) footerCfg.columns = [];

  // Remove a page's link from every footer location (legal + all columns), then
  // drop any column left with no links.
  function footerRemove(href) {
    footerCfg.legal = footerCfg.legal.filter(function (it) { return !(it && it.href === href); });
    footerCfg.columns.forEach(function (col) {
      if (col && Array.isArray(col.links)) {
        col.links = col.links.filter(function (it) { return !(it && it.href === href); });
      }
    });
    footerCfg.columns = footerCfg.columns.filter(function (col) {
      return col && Array.isArray(col.links) && col.links.length > 0;
    });
  }

  function footerAddToColumn(title, link) {
    var col = null;
    for (var i = 0; i < footerCfg.columns.length; i++) {
      if (footerCfg.columns[i] && footerCfg.columns[i].title === title) { col = footerCfg.columns[i]; break; }
    }
    if (!col) { col = { title: title, links: [] }; footerCfg.columns.push(col); }
    if (!Array.isArray(col.links)) col.links = [];
    col.links.push(link);
  }

  function saveFooter() {
    return fetch('/os/api/settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() },
      body: JSON.stringify({ key: 'footer.config', value: JSON.stringify(footerCfg) }),
    });
  }

  var footerSelects = Array.prototype.slice.call(document.querySelectorAll('[data-page-footer]'));
  footerSelects.forEach(function (s) {
    s.addEventListener('change', function () {
      var href = s.getAttribute('data-href');
      var label = s.getAttribute('data-label') || href;
      var target = s.value;
      footerRemove(href); // single placement — clear any prior location first
      if (target === 'legal') {
        footerCfg.legal.push({ label: label, href: href });
      } else if (target.indexOf('col:') === 0) {
        footerAddToColumn(target.slice(4), { label: label, href: href });
      }
      s.disabled = true;
      setText(navStatus, 'Saving footer…');
      saveFooter()
        .then(function (r) {
          s.disabled = false;
          setText(navStatus, r.ok ? 'Footer updated' : 'Could not update footer');
        })
        .catch(function (e) { s.disabled = false; setText(navStatus, 'Network error: ' + e); });
    });
  });
})();
