/*
 * admin-os-theme.js — VayuPress VayuOS Theme Studio.
 *
 * Vanilla JS, strict CSP: no eval, no innerHTML with untrusted data, no inline
 * <style> injection. The live preview is driven entirely through the CSSOM.
 *
 * Flow:
 *   1. Load active tokens (/os/api/theme/tokens) -> fill inputs + preview.
 *   2. Load presets (/os/api/theme/presets) -> populate theme gallery cards.
 *   3. Click a gallery card -> load tokens into editor + auto-preview.
 *   4. Editing any token live-updates the preview locally.
 *   5. "Apply theme" POSTs full token set -> server validates, compiles, persists.
 *   6. "Revert" reloads the persisted tokens.
 */
(function () {
  'use strict';

  var root = document.querySelector('[data-theme-studio]');
  if (!root) return;

  var galleryEl = root.querySelector('[data-theme-presets]');
  var previewEl = root.querySelector('[data-theme-preview]');
  var statusEl = document.querySelector('[data-theme-status]');
  var applyBtn = document.querySelector('[data-theme-apply]');
  var revertBtn = document.querySelector('[data-theme-revert]');
  var activeNameEl = document.querySelector('[data-active-preset-name]');

  var inputs = {};
  root.querySelectorAll('[data-token]').forEach(function (el) {
    inputs[el.getAttribute('data-token')] = el;
  });

  var model = {};
  var allPresets = [];
  var activePresetName = '';

  function setStatus(msg, kind) {
    if (!statusEl) return;
    statusEl.textContent = msg;
    statusEl.className = 'text-sm' + (kind ? ' status--' + kind : ' muted');
  }

  function csrfToken() {
    var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }

  function paintPreview() {
    if (!previewEl) return;
    root.querySelectorAll('[data-token-var]').forEach(function (el) {
      var field = el.getAttribute('data-token');
      var vari = el.getAttribute('data-token-var');
      var val = model[field];
      if (val) previewEl.style.setProperty('--vp-' + vari, val);
    });
    if (model.FontSans) previewEl.style.setProperty('--vp-font-sans', model.FontSans);
    if (model.FontMono) previewEl.style.setProperty('--vp-font-mono', model.FontMono);
    if (model.FontSizeBase) previewEl.style.setProperty('--vp-font-size-base', model.FontSizeBase);
    if (model.LineHeight) previewEl.style.setProperty('--vp-line-height', model.LineHeight);
    if (model.RadiusLg) previewEl.style.setProperty('--vp-radius-lg', model.RadiusLg);
  }

  function loadTokens(tok) {
    model = {};
    Object.keys(inputs).forEach(function (field) {
      var val = tok[field] != null ? String(tok[field]) : '';
      model[field] = val;
      var el = inputs[field];
      if (el.type === 'color') {
        if (/^#[0-9a-fA-F]{6}$/.test(val)) el.value = val;
      } else {
        el.value = val;
      }
    });
    activePresetName = tok.Name || '';
    if (activeNameEl) {
      activeNameEl.textContent = activePresetName ? 'Current theme: ' + activePresetName : 'Current theme';
    }
    highlightActiveCard(activePresetName);
    paintPreview();
  }

  // Highlight the active theme card in the gallery
  function highlightActiveCard(name) {
    if (!galleryEl) return;
    galleryEl.querySelectorAll('.theme-card').forEach(function (card) {
      var presetName = card.getAttribute('data-preset');
      if (presetName === name) {
        card.classList.add('theme-card--active');
      } else {
        card.classList.remove('theme-card--active');
      }
    });
  }

  Object.keys(inputs).forEach(function (field) {
    var el = inputs[field];
    var evt = el.type === 'color' ? 'input' : 'change';
    el.addEventListener(evt, function () {
      model[field] = el.value;
      paintPreview();
    });
  });

  // ── Gallery card clicks ────────────────────────────────────────────────────
  if (galleryEl) {
    galleryEl.addEventListener('click', function (e) {
      var card = e.target.closest('.theme-card');
      if (!card) return;
      var presetName = card.getAttribute('data-preset');
      var preset = allPresets.find(function (p) { return p.Name === presetName; });
      if (preset) {
        loadTokens(preset);
        setStatus('Preset "' + presetName + '" loaded - not yet applied', 'warn');
      }
    });
  }

  // ── Persistence ─────────────────────────────────────────────────────────────
  function apply() {
    setStatus('Applying…');
    fetch('/os/api/theme/apply', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
      body: JSON.stringify({ tokens: model })
    }).then(function (r) {
      if (!r.ok) return r.json().then(function (e) { throw new Error((e.error && e.error.message) || ('apply failed (' + r.status + ')')); });
      return r.json();
    }).then(function (d) {
      setStatus('Applied · ' + (d.name || '') + ' · ' + new Date().toLocaleTimeString(), 'ok');
      highlightActiveCard(d.name || '');
      if (activeNameEl) activeNameEl.textContent = 'Current theme: ' + (d.name || 'Custom');
      if (window.vpToast) window.vpToast('Theme applied', 'ok');
    }).catch(function (err) {
      setStatus(String(err.message || err), 'danger');
    });
  }

  function fetchTokens() {
    return fetch('/os/api/theme/tokens', { headers: { Accept: 'application/json' } })
      .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
      .then(function (tok) { loadTokens(tok); });
  }

  function fetchPresets() {
    return fetch('/os/api/theme/presets', { headers: { Accept: 'application/json' } })
      .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
      .then(function (list) {
        allPresets = Array.isArray(list) ? list : [];
        // Gallery cards already rendered server-side — just sync active state
        fetchTokens().then(function () {
          highlightActiveCard(activePresetName);
        });
      });
  }

  if (applyBtn) applyBtn.addEventListener('click', apply);
  if (revertBtn) revertBtn.addEventListener('click', function () {
    fetchTokens().then(function () { setStatus('Reverted to saved theme', 'ok'); });
  });

  // ── Colorize preset swatches via CSSOM (CSP-safe: no inline style attrs) ────
  function paintSwatches() {
    if (!galleryEl) return;
    galleryEl.querySelectorAll('.theme-card__sw[data-color]').forEach(function (el) {
      var c = el.getAttribute('data-color');
      if (c) el.style.backgroundColor = c;
    });
  }
  paintSwatches();

  // ── Init ────────────────────────────────────────────────────────────────────
  Promise.all([fetchTokens(), fetchPresets()])
    .then(function () { setStatus('Ready'); })
    .catch(function () { setStatus('Could not load theme', 'danger'); });
})();
