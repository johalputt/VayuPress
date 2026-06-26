/*
 * admin-os-theme.js — VayuPress VayuOS Theme Studio (Ghost-style customizer).
 *
 * Vanilla JS, strict CSP: no eval, no innerHTML with untrusted data, no inline
 * <style> injection.
 *
 * The live preview is a real, full-page same-origin iframe that renders the
 * actual public markup. On EVERY change (preset, colour, typography, layout,
 * option, or custom CSS) we POST the exact token payload Apply would use to
 * /os/api/theme/preview-draft; the server compiles it with the real pipeline and
 * returns a stylesheet id. We then hot-swap the iframe's stylesheet via a
 * same-origin postMessage — so the preview reflects the WHOLE design instantly,
 * not just colours, with no full reload/flicker.
 *
 * Flow:
 *   1. Load active tokens + presets -> fill controls.
 *   2. Any control change -> debounced preview refresh (draft -> swap).
 *   3. Device toggle resizes the preview viewport (desktop/tablet/mobile).
 *   4. Apply persists; Revert reloads saved tokens; Import replaces everything.
 */
(function () {
  'use strict';

  var root = document.querySelector('[data-theme-studio]');
  if (!root) return;

  var galleryEl = root.querySelector('[data-theme-presets]');
  var statusEl = document.querySelector('[data-theme-status]');
  var applyBtn = document.querySelector('[data-theme-apply]');
  var revertBtn = document.querySelector('[data-theme-revert]');
  var activeNameEl = document.querySelector('[data-active-preset-name]');

  // Preview iframe + chrome.
  var frame = document.querySelector('[data-theme-frame]');
  var frameLoading = document.querySelector('[data-theme-frame-loading]');
  var viewport = document.querySelector('[data-theme-viewport]');
  var previewStatusEl = document.querySelector('[data-theme-preview-status]');
  var newTabLink = document.querySelector('[data-theme-newtab]');
  var deviceBtns = Array.prototype.slice.call(document.querySelectorAll('[data-theme-device]'));

  // Token + option controls.
  var inputs = {};
  root.querySelectorAll('[data-token]').forEach(function (el) { inputs[el.getAttribute('data-token')] = el; });
  var optInputs = {};
  root.querySelectorAll('[data-token-opt]').forEach(function (el) { optInputs[el.getAttribute('data-token-opt')] = el; });
  var cssArea = root.querySelector('[data-theme-css]');

  var model = {};
  var options = {};
  var allPresets = [];
  var activePresetName = '';
  var loadedCustomCSS = '';

  // Preview pipeline state.
  var previewReady = false;     // iframe loaded + handshake received
  var framePointed = false;     // iframe.src set to a draft page at least once
  var useReload = false;        // fallback mode: reload the iframe instead of hot-swap
  var pending = null;           // latest {pageURL, cssHref} awaiting handshake
  var debounceTimer = null;
  var ackTimer = null;
  var readyTimer = null;
  var reqSeq = 0;               // guards against out-of-order draft responses

  function setStatus(msg, kind) {
    if (!statusEl) return;
    statusEl.textContent = msg;
    statusEl.className = 'text-sm' + (kind ? ' status--' + kind : ' muted');
  }
  function setPreviewStatus(msg) { if (previewStatusEl) previewStatusEl.textContent = msg; }
  function showLoading(on) { if (frameLoading) frameLoading.hidden = !on; }

  function csrfToken() {
    var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }

  // ── Load tokens into controls ──────────────────────────────────────────────
  function loadTokens(tok) {
    model = {};
    Object.keys(inputs).forEach(function (field) {
      var v = tok[field] != null ? String(tok[field]) : '';
      model[field] = v;
      var el = inputs[field];
      if (el.type === 'color') { if (/^#[0-9a-fA-F]{6}$/.test(v)) el.value = v; }
      else el.value = v;
    });
    activePresetName = tok.Name || '';
    loadedCustomCSS = tok.custom_css || tok.CustomCSS || '';
    options = {};
    var saved = (tok && (tok.options || tok.Options)) || {};
    Object.keys(optInputs).forEach(function (key) {
      var el = optInputs[key];
      el.value = saved[key] != null ? String(saved[key]) : (el.options[0] ? el.options[0].value : '');
      options[key] = el.value;
    });
    if (activeNameEl) activeNameEl.textContent = activePresetName ? 'Current theme: ' + activePresetName : 'Current theme';
    updateOptionVisibility();
    highlightActiveCard(activePresetName);
  }

  function highlightActiveCard(name) {
    if (!galleryEl) return;
    galleryEl.querySelectorAll('.theme-card').forEach(function (card) {
      card.classList.toggle('theme-card--active', card.getAttribute('data-preset') === name);
    });
  }

  // Show only the per-theme extra options that apply to the active theme.
  function updateOptionVisibility() {
    Object.keys(optInputs).forEach(function (key) {
      var el = optInputs[key];
      var row = el.closest('[data-opt-theme]');
      if (!row) return;
      var themes = (row.getAttribute('data-opt-theme') || '').split(',');
      var show = themes.indexOf(activePresetName) !== -1;
      row.hidden = !show;
      if (!show && el.options.length && el.value !== el.options[0].value) {
        el.value = el.options[0].value;
        options[key] = el.value;
      }
    });
  }

  // ── Preview payload (mirrors Apply) ────────────────────────────────────────
  function buildTokens() {
    var t = {};
    Object.keys(model).forEach(function (k) { t[k] = model[k]; });
    // Combine the preset's component CSS with the operator's custom CSS so the
    // preview reflects BOTH (Apply persists them via separate paths).
    var css = loadedCustomCSS || '';
    if (cssArea && cssArea.value) css = (css ? css + '\n' : '') + cssArea.value;
    if (css) t.custom_css = css;
    t.options = options;
    return t;
  }

  // ── Live preview: draft -> hot-swap stylesheet ──────────────────────────────
  function schedulePreview() {
    if (debounceTimer) clearTimeout(debounceTimer);
    debounceTimer = setTimeout(refreshPreview, 220);
  }

  function refreshPreview() {
    var seq = ++reqSeq;
    showLoading(true);
    setPreviewStatus('Updating…');
    fetch('/os/api/theme/preview-draft', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
      body: JSON.stringify({ tokens: buildTokens() })
    }).then(function (r) {
      if (!r.ok) return r.json().then(function (e) { throw new Error((e.error) || ('preview failed (' + r.status + ')')); });
      return r.json();
    }).then(function (d) {
      if (seq !== reqSeq) return; // a newer request superseded this one
      applyPreview(d.id, d.css_href);
    }).catch(function (err) {
      if (seq !== reqSeq) return;
      showLoading(false);
      setPreviewStatus('Preview error');
      setStatus(String(err.message || err), 'danger');
    });
  }

  function applyPreview(id, cssHref) {
    var pageURL = '/os/theme/preview?draft=' + encodeURIComponent(id);
    if (newTabLink) newTabLink.setAttribute('href', pageURL);

    // Fallback mode (or first load): reload the iframe with the draft page —
    // always works, even if cross-frame messaging is blocked.
    if (useReload || !framePointed) {
      framePointed = true;
      if (frame) frame.src = pageURL;
      if (!useReload) startReadyTimer(); // first load: detect a dead handshake
      return;
    }
    // Hot-swap path: ask the iframe to swap its stylesheet (no flicker, keeps
    // scroll). If the iframe doesn't acknowledge quickly, fall back to a reload.
    if (previewReady && frame && frame.contentWindow) {
      frame.contentWindow.postMessage({ type: 'vayu-preview-css', href: cssHref }, location.origin);
      startAckTimer(pageURL);
    } else {
      pending = { pageURL: pageURL, cssHref: cssHref };
    }
  }

  function startReadyTimer() {
    clearTimeout(readyTimer);
    readyTimer = setTimeout(function () {
      if (previewReady) return;
      // Handshake never arrived — the in-frame script is unavailable. Switch to
      // reload mode permanently so changes still take effect.
      useReload = true;
      if (pending) { var p = pending; pending = null; if (frame) frame.src = p.pageURL; }
    }, 2500);
  }

  function startAckTimer(pageURL) {
    clearTimeout(ackTimer);
    ackTimer = setTimeout(function () {
      // No ack — assume messaging is unreliable and reload instead, now and on.
      useReload = true;
      if (frame) frame.src = pageURL;
      showLoading(false);
      setPreviewStatus('Live preview');
    }, 900);
  }

  // Handshake + ack wiring.
  window.addEventListener('message', function (e) {
    if (e.origin !== location.origin) return;
    var d = e.data || {};
    if (d.type === 'vayu-preview-ready') {
      previewReady = true;
      clearTimeout(readyTimer);
      showLoading(false);
      setPreviewStatus('Live preview');
      if (pending && frame && frame.contentWindow) {
        var p = pending; pending = null;
        frame.contentWindow.postMessage({ type: 'vayu-preview-css', href: p.cssHref }, location.origin);
        startAckTimer(p.pageURL);
      }
    } else if (d.type === 'vayu-preview-ack') {
      clearTimeout(ackTimer);
      showLoading(false);
      setPreviewStatus('Live preview');
    }
  });
  if (frame) {
    frame.addEventListener('load', function () { showLoading(false); });
  }

  // ── Device toggle ────────────────────────────────────────────────────────────
  deviceBtns.forEach(function (btn) {
    btn.addEventListener('click', function () {
      var dev = btn.getAttribute('data-theme-device') || 'desktop';
      if (viewport) viewport.setAttribute('data-device', dev);
      deviceBtns.forEach(function (b) {
        var on = b === btn;
        b.classList.toggle('cz-device--active', on);
        b.setAttribute('aria-pressed', on ? 'true' : 'false');
      });
    });
  });

  // ── Control change wiring ─────────────────────────────────────────────────────
  Object.keys(inputs).forEach(function (field) {
    var el = inputs[field];
    var evt = el.type === 'color' ? 'input' : 'change';
    el.addEventListener(evt, function () { model[field] = el.value; schedulePreview(); });
  });
  Object.keys(optInputs).forEach(function (key) {
    optInputs[key].addEventListener('change', function () { options[key] = optInputs[key].value; schedulePreview(); });
  });
  if (cssArea) cssArea.addEventListener('input', schedulePreview);

  // Font pairing quick-set: applies a sans + mono stack to the FontSans/FontMono
  // tokens at once, updates their text inputs, and refreshes the preview.
  var fontPair = root.querySelector('[data-font-pair]');
  if (fontPair) {
    fontPair.addEventListener('change', function () {
      var opt = fontPair.options[fontPair.selectedIndex];
      if (!opt) return;
      var sans = opt.getAttribute('data-sans') || '';
      var mono = opt.getAttribute('data-mono') || '';
      if (!sans) return; // "Keep current"
      model.FontSans = sans;
      if (inputs.FontSans) inputs.FontSans.value = sans;
      if (mono) { model.FontMono = mono; if (inputs.FontMono) inputs.FontMono.value = mono; }
      schedulePreview();
    });
  }

  // ── Gallery card clicks ────────────────────────────────────────────────────
  if (galleryEl) {
    galleryEl.addEventListener('click', function (e) {
      var card = e.target.closest('.theme-card');
      if (!card) return;
      var name = card.getAttribute('data-preset');
      var preset = allPresets.find(function (p) { return p.Name === name; });
      if (preset) {
        loadTokens(preset);
        setStatus('Preset "' + name + '" loaded — not yet applied', 'warn');
        schedulePreview();
      }
    });
  }

  // ── Apply / Revert ──────────────────────────────────────────────────────────
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
    }).catch(function (err) { setStatus(String(err.message || err), 'danger'); });
  }

  function fetchTokens() {
    return fetch('/os/api/theme/tokens', { headers: { Accept: 'application/json' } })
      .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
      .then(function (tok) { loadTokens(tok); });
  }
  function fetchPresets() {
    return fetch('/os/api/theme/presets', { headers: { Accept: 'application/json' } })
      .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
      .then(function (list) { allPresets = Array.isArray(list) ? list : []; });
  }

  if (applyBtn) applyBtn.addEventListener('click', apply);
  if (revertBtn) revertBtn.addEventListener('click', function () {
    fetchTokens().then(function () { setStatus('Reverted to saved theme', 'ok'); schedulePreview(); });
  });

  // ── Custom CSS + Head/SEO save ────────────────────────────────────────────────
  var codeSaveBtn = root.querySelector('[data-theme-code-save]');
  var codeStatusEl = root.querySelector('[data-theme-code-status]');
  function setCodeStatus(msg, kind) {
    if (!codeStatusEl) return;
    codeStatusEl.textContent = msg;
    codeStatusEl.className = 'text-sm' + (kind ? ' status--' + kind : ' muted');
  }
  function headVal(name) { var el = root.querySelector('[data-head="' + name + '"]'); return el ? el.value : ''; }
  if (codeSaveBtn) {
    codeSaveBtn.addEventListener('click', function () {
      setCodeStatus('Saving…');
      codeSaveBtn.disabled = true;
      fetch('/os/api/theme/code', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
        body: JSON.stringify({
          custom_css: cssArea ? cssArea.value : '',
          keywords: headVal('keywords'), theme_color: headVal('theme_color'),
          robots: headVal('robots'), verify_google: headVal('verify_google'), verify_bing: headVal('verify_bing')
        })
      }).then(function (r) {
        if (!r.ok) return r.json().then(function (e) { throw new Error(e.error || ('save failed (' + r.status + ')')); });
        return r.json();
      }).then(function () {
        setCodeStatus('Saved · live on public pages · ' + new Date().toLocaleTimeString(), 'ok');
        if (window.vpToast) window.vpToast('Custom CSS & meta saved', 'ok');
      }).catch(function (err) { setCodeStatus(String(err.message || err), 'danger'); })
        .then(function () { codeSaveBtn.disabled = false; });
    });
  }

  // ── Import ──────────────────────────────────────────────────────────────────
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
        }).catch(function (err) { setImportStatus(String(err.message || err), 'danger'); importBtn.disabled = false; });
      };
      reader.onerror = function () { setImportStatus('Could not read the file', 'danger'); };
      reader.readAsText(f);
    });
  }

  // ── Gallery swatches via CSSOM (CSP-safe) ──────────────────────────────────
  function paintSwatches() {
    if (!galleryEl) return;
    galleryEl.querySelectorAll('[data-color]').forEach(function (el) {
      var c = el.getAttribute('data-color');
      if (c) el.style.backgroundColor = c;
    });
  }
  paintSwatches();

  // ── Init ────────────────────────────────────────────────────────────────────
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
      if (!applyLoadParam()) { highlightActiveCard(activePresetName); setStatus('Ready'); }
      schedulePreview(); // first preview load
    })
    .catch(function () { setStatus('Could not load theme', 'danger'); });
})();
