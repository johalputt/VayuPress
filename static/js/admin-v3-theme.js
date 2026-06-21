/*
 * admin-v3-theme.js — VayuPress Admin v3 Theme Studio.
 *
 * Vanilla JS, strict CSP: no eval, no innerHTML with untrusted data, no inline
 * <style> injection. The live preview is driven entirely through the CSSOM
 * (element.style.setProperty for --vp-* variables) — scripted style writes are
 * not gated by style-src, so no compiled-CSS string is ever parsed client side.
 *
 * Flow:
 *   1. Load active tokens (/os/api/theme/tokens) → fill inputs + preview.
 *   2. Load presets (/os/api/theme/presets) → gallery of swatch cards.
 *   3. Editing any token live-updates the preview locally.
 *   4. "Apply theme" POSTs the full token set to /os/api/theme/apply,
 *      which validates + compiles + persists server-side (the only source of
 *      truth). "Revert" reloads the persisted tokens.
 */
(function () {
  'use strict';

  var root = document.querySelector('[data-theme-studio]');
  if (!root) return;

  var presetsEl = root.querySelector('[data-theme-presets]');
  var previewEl = root.querySelector('[data-theme-preview]');
  var statusEl = document.querySelector('[data-theme-status]');
  var applyBtn = document.querySelector('[data-theme-apply]');
  var revertBtn = document.querySelector('[data-theme-revert]');

  // All token inputs keyed by canonical field name.
  var inputs = {};
  root.querySelectorAll('[data-token]').forEach(function (el) {
    inputs[el.getAttribute('data-token')] = el;
  });

  // current token model (field name → value string).
  var model = {};

  function setStatus(msg, kind) {
    if (!statusEl) return;
    statusEl.textContent = msg;
    statusEl.className = 'text-sm' + (kind ? ' status--' + kind : ' muted');
  }

  function csrfToken() {
    var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }

  // Apply the model to the live preview by setting --vp-* custom properties on
  // the preview container (dark-mode tokens only — those carry data-token-var).
  function paintPreview() {
    root.querySelectorAll('[data-token-var]').forEach(function (el) {
      var field = el.getAttribute('data-token');
      var vari = el.getAttribute('data-token-var');
      var val = model[field];
      if (val) previewEl.style.setProperty('--vp-' + vari, val);
    });
    // Typography/layout, when valid CSS-ish, also drive the preview.
    if (model.FontSans) previewEl.style.setProperty('--vp-font-sans', model.FontSans);
    if (model.FontMono) previewEl.style.setProperty('--vp-font-mono', model.FontMono);
    if (model.FontSizeBase) previewEl.style.setProperty('--vp-font-size-base', model.FontSizeBase);
    if (model.LineHeight) previewEl.style.setProperty('--vp-line-height', model.LineHeight);
    if (model.RadiusLg) previewEl.style.setProperty('--vp-radius-lg', model.RadiusLg);
  }

  // Fill the form inputs and model from a token object (Go field names).
  function loadTokens(tok) {
    model = {};
    Object.keys(inputs).forEach(function (field) {
      var val = tok[field] != null ? String(tok[field]) : '';
      model[field] = val;
      var el = inputs[field];
      if (el.type === 'color') {
        // <input type=color> needs a 7-char hex; ignore shorthand/empty safely.
        if (/^#[0-9a-fA-F]{6}$/.test(val)) el.value = val;
      } else {
        el.value = val;
      }
    });
    paintPreview();
  }

  // Wire input → model → preview.
  Object.keys(inputs).forEach(function (field) {
    var el = inputs[field];
    var evt = el.type === 'color' ? 'input' : 'change';
    el.addEventListener(evt, function () {
      model[field] = el.value;
      paintPreview();
    });
  });

  // ── Presets gallery ─────────────────────────────────────────────────────────
  function swatch(color) {
    var s = document.createElement('span');
    s.className = 'theme-preset__swatch';
    if (/^#[0-9a-fA-F]{3,8}$/.test(color || '')) {
      s.style.setProperty('background-color', color);
    }
    return s;
  }

  function renderPresets(list) {
    while (presetsEl.firstChild) presetsEl.removeChild(presetsEl.firstChild);
    list.forEach(function (p) {
      var card = document.createElement('button');
      card.type = 'button';
      card.className = 'theme-preset';

      var swatches = document.createElement('div');
      swatches.className = 'theme-preset__swatches';
      [p.BgDark, p.SurfaceDark, p.AccentDark, p.Accent2Dark, p.HiDark].forEach(function (c) {
        swatches.appendChild(swatch(c));
      });

      var name = document.createElement('div');
      name.className = 'theme-preset__name';
      name.textContent = p.Name || 'Preset';

      card.appendChild(swatches);
      card.appendChild(name);
      card.addEventListener('click', function () {
        loadTokens(p);
        setStatus('Preset “' + (p.Name || '') + '” loaded — not yet applied', 'warn');
      });
      presetsEl.appendChild(card);
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
      .then(function (list) { renderPresets(Array.isArray(list) ? list : []); });
  }

  if (applyBtn) applyBtn.addEventListener('click', apply);
  if (revertBtn) revertBtn.addEventListener('click', function () {
    fetchTokens().then(function () { setStatus('Reverted to saved theme', 'ok'); });
  });

  // ── Init ────────────────────────────────────────────────────────────────────
  Promise.all([fetchTokens(), fetchPresets()])
    .then(function () { setStatus('Ready'); })
    .catch(function () { setStatus('Could not load theme', 'danger'); });
})();
