/*
 * admin-os-theme-store.js — VayuOS Theme Store.
 *
 * Vanilla JS, strict CSP: no eval, no innerHTML with untrusted data, no inline
 * <style> injection. Preview colours are applied through the CSSOM.
 *
 * Responsibilities:
 *   1. Paint each card's mini-preview from its data-color attributes (CSSOM).
 *   2. Deploy a theme: POST {preset:<name>} to /os/api/theme/apply, then move
 *      the "Active" badge/state to the deployed card — no full reload needed.
 *   3. Filter the catalogue by category chip and by free-text search, updating
 *      the visible count and the empty-state message.
 */
(function () {
  'use strict';

  var root = document.querySelector('[data-theme-store]');
  if (!root) return;

  var grid = root.querySelector('[data-store-grid]');
  var statusEl = document.querySelector('[data-store-status]');
  var countEl = root.querySelector('[data-store-count]');
  var emptyEl = root.querySelector('[data-store-empty]');
  var searchEl = root.querySelector('[data-store-search]');
  var chips = Array.prototype.slice.call(root.querySelectorAll('[data-store-filter]'));
  var cards = Array.prototype.slice.call(root.querySelectorAll('[data-store-item]'));

  var activeFilter = 'all';

  function setStatus(msg, kind) {
    if (!statusEl) return;
    statusEl.textContent = msg || '';
    statusEl.className = 'text-sm' + (kind ? ' status--' + kind : ' muted');
  }

  function csrfToken() {
    var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }

  // ── 1. Paint preview swatches via CSSOM (CSP-safe) ──────────────────────────
  root.querySelectorAll('[data-color]').forEach(function (el) {
    var c = el.getAttribute('data-color');
    if (c) el.style.backgroundColor = c;
  });

  // ── 2. Filtering (category chip + text search) ──────────────────────────────
  function applyFilters() {
    var q = (searchEl && searchEl.value ? searchEl.value : '').toLowerCase().trim();
    var visible = 0;
    cards.forEach(function (card) {
      var cat = card.getAttribute('data-category') || '';
      var hay = (card.getAttribute('data-haystack') || '').toLowerCase();
      var matchCat = activeFilter === 'all' || cat === activeFilter;
      var matchText = q === '' || hay.indexOf(q) !== -1;
      var show = matchCat && matchText;
      card.hidden = !show;
      if (show) visible++;
    });
    if (countEl) countEl.textContent = String(visible);
    if (emptyEl) emptyEl.hidden = visible !== 0;
  }

  chips.forEach(function (chip) {
    chip.addEventListener('click', function () {
      activeFilter = chip.getAttribute('data-store-filter') || 'all';
      chips.forEach(function (c) {
        var on = c === chip;
        c.classList.toggle('store-chip--active', on);
        c.setAttribute('aria-pressed', on ? 'true' : 'false');
      });
      applyFilters();
    });
  });

  if (searchEl) {
    searchEl.addEventListener('input', applyFilters);
  }

  // ── 3. Deploy a theme ───────────────────────────────────────────────────────
  function markActive(name) {
    root.setAttribute('data-active-theme', name);
    cards.forEach(function (card) {
      var isActive = card.getAttribute('data-name') === name;
      card.classList.toggle('store-card--active', isActive);

      var badge = card.querySelector('[data-store-badge]');
      if (badge) badge.hidden = !isActive;

      var btn = card.querySelector('[data-store-deploy]');
      if (btn) {
        if (isActive) {
          btn.textContent = 'Active';
          btn.setAttribute('data-store-active', 'true');
          btn.classList.remove('btn--primary');
          btn.disabled = true;
        } else {
          btn.textContent = 'Deploy';
          btn.removeAttribute('data-store-active');
          btn.classList.add('btn--primary');
          btn.disabled = false;
        }
      }
    });
  }

  function deploy(name, btn) {
    if (!name) return;
    var prevLabel = btn ? btn.textContent : '';
    if (btn) { btn.disabled = true; btn.textContent = 'Deploying…'; }
    setStatus('Deploying “' + name + '”…');
    fetch('/os/api/theme/apply', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
      body: JSON.stringify({ preset: name })
    }).then(function (r) {
      if (!r.ok) {
        return r.json().then(function (e) {
          throw new Error((e.error && e.error.message) || e.error || ('deploy failed (' + r.status + ')'));
        });
      }
      return r.json();
    }).then(function (d) {
      var deployed = (d && d.name) || name;
      markActive(deployed);
      setStatus('Deployed “' + deployed + '” · live on your site · ' + new Date().toLocaleTimeString(), 'ok');
      if (window.vpToast) window.vpToast('“' + deployed + '” deployed', 'ok');
    }).catch(function (err) {
      if (btn) { btn.disabled = false; btn.textContent = prevLabel || 'Deploy'; }
      setStatus(String(err.message || err), 'danger');
      if (window.vpToast) window.vpToast('Deploy failed', 'error');
    });
  }

  if (grid) {
    grid.addEventListener('click', function (e) {
      var btn = e.target.closest('[data-store-deploy]');
      if (!btn || btn.disabled) return;
      deploy(btn.getAttribute('data-store-deploy'), btn);
    });
  }

  // ── Init ────────────────────────────────────────────────────────────────────
  applyFilters();
})();
