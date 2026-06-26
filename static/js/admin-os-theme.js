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

  // Theme-level customization controls (color scheme, width, corners, …).
  var optInputs = {};
  root.querySelectorAll('[data-token-opt]').forEach(function (el) {
    optInputs[el.getAttribute('data-token-opt')] = el;
  });
  var options = {};

  // Accent pairs mirror schemePalettes in internal/theme/options.go (dark
  // variants, for the dark preview panel) so the live preview matches Apply.
  var SCHEMES = {
    indigo: ['#6366f1', '#22d3ee'], violet: ['#8b5cf6', '#ec4899'], cyan: ['#06b6d4', '#3b82f6'],
    emerald: ['#10b981', '#84cc16'], rose: ['#f43f5e', '#fb923c'], amber: ['#f59e0b', '#ef4444'],
    crimson: ['#ef4444', '#a78bfa'], teal: ['#14b8a6', '#38bdf8'], slate: ['#64748b', '#94a3b8'],
    mono: ['#e5e7eb', '#9ca3af']
  };

  var model = {};
  var allPresets = [];
  var activePresetName = '';
  var loadedCustomCSS = '';   // per-theme component CSS carried through Apply

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

  // paintOptionPreview live-applies the option choices to the preview panel
  // (scheme accent + corner radius are the visible ones at this size).
  function paintOptionPreview() {
    if (!previewEl) return;
    var s = options.scheme;
    if (s && SCHEMES[s]) {
      previewEl.style.setProperty('--vp-accent', SCHEMES[s][0]);
      previewEl.style.setProperty('--vp-accent2', SCHEMES[s][1]);
    } else {
      // revert to the token-defined accent
      if (model.AccentDark) previewEl.style.setProperty('--vp-accent', model.AccentDark);
      if (model.Accent2Dark) previewEl.style.setProperty('--vp-accent2', model.Accent2Dark);
    }
    var c = options.corners;
    if (c === 'sharp') previewEl.style.setProperty('--vp-radius-lg', '0');
    else if (c === 'round') previewEl.style.setProperty('--vp-radius-lg', '1.5rem');
    else if (c === 'soft') previewEl.style.setProperty('--vp-radius-lg', '0.875rem');
    else if (model.RadiusLg) previewEl.style.setProperty('--vp-radius-lg', model.RadiusLg);
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
    // Preserve the preset's component CSS (apex.css/gale.css/etc.) so a theme
    // loaded via "Customize" keeps its full design when the operator hits Apply.
    loadedCustomCSS = tok.custom_css || tok.CustomCSS || '';
    // Theme-level options — restore saved choices (default-select otherwise).
    options = {};
    var savedOpts = (tok && (tok.options || tok.Options)) || {};
    Object.keys(optInputs).forEach(function (key) {
      var el = optInputs[key];
      el.value = savedOpts[key] != null ? String(savedOpts[key]) : (el.options[0] ? el.options[0].value : '');
      options[key] = el.value;
    });
    if (activeNameEl) {
      activeNameEl.textContent = activePresetName ? 'Current theme: ' + activePresetName : 'Current theme';
    }
    highlightActiveCard(activePresetName);
    paintPreview();
    paintOptionPreview();
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
      paintOptionPreview();
    });
  });

  // Option selects → update state + live preview.
  Object.keys(optInputs).forEach(function (key) {
    optInputs[key].addEventListener('change', function () {
      options[key] = optInputs[key].value;
      paintOptionPreview();
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
    var payload = {};
    Object.keys(model).forEach(function (k) { payload[k] = model[k]; });
    if (loadedCustomCSS) payload.custom_css = loadedCustomCSS;
    payload.options = options;
    fetch('/os/api/theme/apply', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
      body: JSON.stringify({ tokens: payload })
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
        // Gallery cards are rendered server-side; just keep the data for clicks.
        // NOTE: do NOT re-fetch tokens here — a second, un-awaited load would
        // resolve after applyLoadParam() and clobber a "Customize" selection.
        allPresets = Array.isArray(list) ? list : [];
      });
  }

  if (applyBtn) applyBtn.addEventListener('click', apply);
  if (revertBtn) revertBtn.addEventListener('click', function () {
    fetchTokens().then(function () { setStatus('Reverted to saved theme', 'ok'); });
  });

  // ── Custom CSS + Head/SEO code editor ───────────────────────────────────────
  var cssArea = root.querySelector('[data-theme-css]');
  var codeSaveBtn = root.querySelector('[data-theme-code-save]');
  var codeStatusEl = root.querySelector('[data-theme-code-status]');

  function setCodeStatus(msg, kind) {
    if (!codeStatusEl) return;
    codeStatusEl.textContent = msg;
    codeStatusEl.className = 'text-sm' + (kind ? ' status--' + kind : ' muted');
  }

  function headVal(name) {
    var el = root.querySelector('[data-head="' + name + '"]');
    return el ? el.value : '';
  }

  if (codeSaveBtn) {
    codeSaveBtn.addEventListener('click', function () {
      setCodeStatus('Saving…');
      codeSaveBtn.disabled = true;
      fetch('/os/api/theme/code', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
        body: JSON.stringify({
          custom_css: cssArea ? cssArea.value : '',
          keywords: headVal('keywords'),
          theme_color: headVal('theme_color'),
          robots: headVal('robots'),
          verify_google: headVal('verify_google'),
          verify_bing: headVal('verify_bing')
        })
      }).then(function (r) {
        if (!r.ok) return r.json().then(function (e) { throw new Error(e.error || ('save failed (' + r.status + ')')); });
        return r.json();
      }).then(function () {
        setCodeStatus('Saved · live on public pages · ' + new Date().toLocaleTimeString(), 'ok');
        if (window.vpToast) window.vpToast('Custom CSS & meta saved', 'ok');
      }).catch(function (err) {
        setCodeStatus(String(err.message || err), 'danger');
      }).then(function () { codeSaveBtn.disabled = false; });
    });
  }

  // ── Theme import / export ───────────────────────────────────────────────────
  var importFile = root.querySelector('[data-theme-import-file]');
  var importBtn = root.querySelector('[data-theme-import]');
  var importStatusEl = root.querySelector('[data-theme-import-status]');

  function setImportStatus(msg, kind) {
    if (!importStatusEl) return;
    importStatusEl.textContent = msg;
    importStatusEl.className = 'text-sm' + (kind ? ' status--' + kind : ' muted');
  }

  if (importBtn) {
    importBtn.addEventListener('click', function () {
      var f = importFile && importFile.files && importFile.files[0];
      if (!f) { setImportStatus('Choose a .json theme file first', 'danger'); return; }
      var reader = new FileReader();
      reader.onload = function () {
        setImportStatus('Importing…');
        importBtn.disabled = true;
        fetch('/os/api/theme/import', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
          body: reader.result
        }).then(function (r) {
          if (!r.ok) return r.json().then(function (e) { throw new Error(e.error || ('import failed (' + r.status + ')')); });
          return r.json();
        }).then(function (d) {
          setImportStatus('Imported “' + (d.name || 'theme') + '” — reloading…', 'ok');
          setTimeout(function () { window.location.reload(); }, 900);
        }).catch(function (err) {
          setImportStatus(String(err.message || err), 'danger');
          importBtn.disabled = false;
        });
      };
      reader.onerror = function () { setImportStatus('Could not read the file', 'danger'); };
      reader.readAsText(f);
    });
  }

  // ── Colorize gallery cards via CSSOM (CSP-safe: no inline style attrs) ──────
  // Every colour-bearing element in the gallery carries a data-color hex string;
  // we apply it as a background-color through the CSSOM, which style-src does not
  // gate. Covers the card "page" background, accent bar, body text lines and the
  // accent pills — i.e. the whole Tumblr-style preview.
  function paintSwatches() {
    if (!galleryEl) return;
    galleryEl.querySelectorAll('[data-color]').forEach(function (el) {
      var c = el.getAttribute('data-color');
      if (c) el.style.backgroundColor = c;
    });
  }
  paintSwatches();

  // ── Init ────────────────────────────────────────────────────────────────────
  // If arriving from the Theme Store via "Customize" (/os/theme?load=<Name>),
  // preselect that theme into the editor once presets have loaded so the
  // operator lands ready to fine-tune it (not yet applied).
  function applyLoadParam() {
    var m = window.location.search.match(/[?&]load=([^&]+)/);
    if (!m) return false;
    var want = decodeURIComponent(m[1].replace(/\+/g, ' '));
    var preset = allPresets.find(function (p) { return p.Name === want; });
    if (preset) {
      loadTokens(preset);
      setStatus('Loaded "' + preset.Name + '" from the Store — not yet applied', 'warn');
      return true;
    }
    return false;
  }

  Promise.all([fetchTokens(), fetchPresets()])
    .then(function () {
      if (!applyLoadParam()) {
        highlightActiveCard(activePresetName);
        setStatus('Ready');
      }
    })
    .catch(function () { setStatus('Could not load theme', 'danger'); });
})();
