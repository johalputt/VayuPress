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

  // ── Collapsible control groups — single-open accordion ─────────────────────
  // Clicking a header opens that section fully and closes the rest, so the panel
  // stays compact and every section name is always visible. Delegated for
  // robustness; CSP-safe (wired here, no inline handlers).
  var panel = root.querySelector('.customizer__panel') || root;
  panel.addEventListener('click', function (e) {
    var head = e.target.closest('.cz-group__head');
    if (!head || !panel.contains(head)) return;
    var group = head.closest('.cz-group');
    if (!group) return;
    var willOpen = !group.classList.contains('cz-group--open');
    panel.querySelectorAll('.cz-group').forEach(function (g) {
      g.classList.remove('cz-group--open');
      var h = g.querySelector('.cz-group__head');
      if (h) h.setAttribute('aria-expanded', 'false');
    });
    if (willOpen) {
      group.classList.add('cz-group--open');
      head.setAttribute('aria-expanded', 'true');
      if (group.scrollIntoView) group.scrollIntoView({ block: 'nearest' });
    }
  });

  // ── Quick-jump chip bar ───────────────────────────────────────────────────────
  // The customizer has many sections; a sticky row of section chips lets the
  // operator open and scroll to any one without hunting. Built from the section
  // headers so it always matches them. CSP-safe: DOM-built, no inline handlers.
  (function buildJump() {
    var groups = Array.prototype.slice.call(panel.querySelectorAll('.cz-group'));
    if (groups.length < 4) return;
    var bar = document.createElement('div');
    bar.className = 'cz-jump';
    groups.forEach(function (g) {
      var head = g.querySelector('.cz-group__head');
      if (!head) return;
      var label = head.firstChild ? head.firstChild.textContent.trim() : (head.textContent || '').trim();
      if (!label) return;
      var chip = document.createElement('button');
      chip.type = 'button';
      chip.className = 'cz-jump__chip';
      chip.textContent = label;
      chip.addEventListener('click', function () {
        panel.querySelectorAll('.cz-group').forEach(function (x) {
          x.classList.remove('cz-group--open');
          var hh = x.querySelector('.cz-group__head');
          if (hh) hh.setAttribute('aria-expanded', 'false');
        });
        g.classList.add('cz-group--open');
        head.setAttribute('aria-expanded', 'true');
        if (g.scrollIntoView) g.scrollIntoView({ block: 'start' });
      });
      bar.appendChild(chip);
    });
    panel.insertBefore(bar, panel.firstChild);
  })();

  // ── Unsaved-changes (dirty) tracking ──────────────────────────────────────────
  // A live preview is not the same as an applied theme: the operator must click
  // Apply to persist. Reflect that on the Apply button + warn before leaving so
  // tweaks are never silently lost. loadTokens()/revert call clearDirty().
  var dirty = false;
  function markDirty() {
    if (dirty) return;
    dirty = true;
    if (applyBtn) { applyBtn.classList.add('is-dirty'); applyBtn.textContent = 'Apply theme •'; }
  }
  function clearDirty() {
    dirty = false;
    if (applyBtn) { applyBtn.classList.remove('is-dirty'); applyBtn.textContent = 'Apply theme'; }
  }
  window.addEventListener('beforeunload', function (e) {
    if (dirty) { e.preventDefault(); e.returnValue = ''; }
  });

  // ── Control change wiring ─────────────────────────────────────────────────────
  Object.keys(inputs).forEach(function (field) {
    var el = inputs[field];
    var evt = el.type === 'color' ? 'input' : 'change';
    el.addEventListener(evt, function () { model[field] = el.value; markDirty(); schedulePreview(); });
  });
  Object.keys(optInputs).forEach(function (key) {
    optInputs[key].addEventListener('change', function () { options[key] = optInputs[key].value; markDirty(); schedulePreview(); });
  });
  if (cssArea) cssArea.addEventListener('input', function () { markDirty(); schedulePreview(); });

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
      markDirty();
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
        markDirty();
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
      clearDirty();
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
    fetchTokens().then(function () { clearDirty(); setStatus('Reverted to saved theme', 'ok'); schedulePreview(); });
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

  // ── Brand: logo / favicon upload (reuses the branding endpoint) ─────────────
  var favFile = document.getElementById('brand-favicon-file');
  var favUp = document.getElementById('brand-favicon-upload');
  var favRm = document.getElementById('brand-favicon-remove');
  var favStatus = document.getElementById('brand-favicon-status');
  var favImg = document.getElementById('brand-favicon-img');
  var favState = document.getElementById('brand-favicon-state');
  function favSet(t, kind) { if (favStatus) { favStatus.textContent = t; favStatus.className = 'text-xs' + (kind ? ' status--' + kind : ' muted'); } }
  function favBust() { if (favImg) favImg.src = '/favicon.ico?t=' + Date.now(); }
  if (favUp) favUp.addEventListener('click', function () {
    var f = favFile && favFile.files && favFile.files[0];
    if (!f) { favSet('Choose a PNG or ICO first', 'danger'); return; }
    favUp.disabled = true; favSet('Uploading…');
    var fd = new FormData(); fd.append('favicon', f);
    fetch('/os/api/branding/favicon', { method: 'POST', headers: { 'X-CSRF-Token': csrfToken() }, body: fd })
      .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, d: d }; }); })
      .then(function (res) {
        favUp.disabled = false;
        if (res.ok) { favSet('Logo updated', 'ok'); favBust(); if (favState) favState.textContent = 'Custom logo active — live on your site.'; }
        else { favSet((res.d && res.d.error && res.d.error.message) || 'Upload failed', 'danger'); }
      }).catch(function (e) { favUp.disabled = false; favSet('Error: ' + e, 'danger'); });
  });
  if (favRm) favRm.addEventListener('click', function () {
    favRm.disabled = true; favSet('Removing…');
    var fd = new FormData(); fd.append('remove', '1');
    fetch('/os/api/branding/favicon', { method: 'POST', headers: { 'X-CSRF-Token': csrfToken() }, body: fd })
      .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, d: d }; }); })
      .then(function (res) {
        favRm.disabled = false;
        if (res.ok) { favSet('Default restored', 'ok'); favBust(); if (favState) favState.textContent = 'Using the default mark.'; }
        else { favSet((res.d && res.d.error && res.d.error.message) || 'Remove failed', 'danger'); }
      }).catch(function (e) { favRm.disabled = false; favSet('Error: ' + e, 'danger'); });
  });

  // ── Hero background image upload + live preview refresh ─────────────────────
  var heroFile = document.getElementById('hero-img-file');
  var heroUp = document.getElementById('hero-img-upload');
  var heroRm = document.getElementById('hero-img-remove');
  var heroStatus = document.getElementById('hero-img-status');
  var heroImg = document.getElementById('hero-img');
  var heroState = document.getElementById('hero-img-state');
  function heroSet(t, kind) { if (heroStatus) { heroStatus.textContent = t; heroStatus.className = 'text-xs' + (kind ? ' status--' + kind : ' muted'); } }
  function heroBust() { if (heroImg) { heroImg.style.display = ''; heroImg.src = '/theme-assets/hero?t=' + Date.now(); } }
  // Hide the thumbnail if no hero image exists (404) — CSP-safe (no inline onerror).
  if (heroImg) heroImg.addEventListener('error', function () { heroImg.style.display = 'none'; });
  if (heroUp) heroUp.addEventListener('click', function () {
    var f = heroFile && heroFile.files && heroFile.files[0];
    if (!f) { heroSet('Choose a PNG, JPEG or WebP first', 'danger'); return; }
    heroUp.disabled = true; heroSet('Uploading…');
    var fd = new FormData(); fd.append('image', f);
    fetch('/os/api/branding/hero', { method: 'POST', headers: { 'X-CSRF-Token': csrfToken() }, body: fd })
      .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, d: d }; }); })
      .then(function (res) {
        heroUp.disabled = false;
        if (res.ok) {
          heroSet('Hero image updated', 'ok'); heroBust();
          if (heroState) heroState.textContent = 'Custom hero image active.';
          // Switch the Hero background option to Image and refresh the preview.
          if (optInputs.herobg) { optInputs.herobg.value = 'image'; options.herobg = 'image'; }
          schedulePreview();
        } else { heroSet((res.d && res.d.error) || 'Upload failed', 'danger'); }
      }).catch(function (e) { heroUp.disabled = false; heroSet('Error: ' + e, 'danger'); });
  });
  if (heroRm) heroRm.addEventListener('click', function () {
    heroRm.disabled = true; heroSet('Removing…');
    var fd = new FormData(); fd.append('remove', '1');
    fetch('/os/api/branding/hero', { method: 'POST', headers: { 'X-CSRF-Token': csrfToken() }, body: fd })
      .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, d: d }; }); })
      .then(function (res) {
        heroRm.disabled = false;
        if (res.ok) { heroSet('Hero image removed', 'ok'); if (heroImg) heroImg.style.display = 'none'; if (heroState) heroState.textContent = 'No hero image set.'; schedulePreview(); }
        else { heroSet((res.d && res.d.error) || 'Remove failed', 'danger'); }
      }).catch(function (e) { heroRm.disabled = false; heroSet('Error: ' + e, 'danger'); });
  });

  // ── Social/share (OG) image upload ─────────────────────────────────────────
  var ogFile = document.getElementById('og-img-file');
  var ogUp = document.getElementById('og-img-upload');
  var ogRm = document.getElementById('og-img-remove');
  var ogStatus = document.getElementById('og-img-status');
  var ogImg = document.getElementById('og-img');
  var ogState = document.getElementById('og-img-state');
  function ogSet(t, kind) { if (ogStatus) { ogStatus.textContent = t; ogStatus.className = 'text-xs' + (kind ? ' status--' + kind : ' muted'); } }
  function ogBust() { if (ogImg) { ogImg.style.display = ''; ogImg.src = '/theme-assets/og?t=' + Date.now(); } }
  if (ogImg) ogImg.addEventListener('error', function () { ogImg.style.display = 'none'; });
  if (ogUp) ogUp.addEventListener('click', function () {
    var f = ogFile && ogFile.files && ogFile.files[0];
    if (!f) { ogSet('Choose a PNG, JPEG or WebP first', 'danger'); return; }
    ogUp.disabled = true; ogSet('Uploading…');
    var fd = new FormData(); fd.append('image', f);
    fetch('/os/api/branding/og', { method: 'POST', headers: { 'X-CSRF-Token': csrfToken() }, body: fd })
      .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, d: d }; }); })
      .then(function (res) {
        ogUp.disabled = false;
        if (res.ok) { ogSet('Share image updated', 'ok'); ogBust(); if (ogState) ogState.textContent = 'Custom share image active.'; }
        else { ogSet((res.d && res.d.error) || 'Upload failed', 'danger'); }
      }).catch(function (e) { ogUp.disabled = false; ogSet('Error: ' + e, 'danger'); });
  });
  if (ogRm) ogRm.addEventListener('click', function () {
    ogRm.disabled = true; ogSet('Removing…');
    var fd = new FormData(); fd.append('remove', '1');
    fetch('/os/api/branding/og', { method: 'POST', headers: { 'X-CSRF-Token': csrfToken() }, body: fd })
      .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, d: d }; }); })
      .then(function (res) {
        ogRm.disabled = false;
        if (res.ok) { ogSet('Share image removed', 'ok'); if (ogImg) ogImg.style.display = 'none'; if (ogState) ogState.textContent = 'No share image set.'; }
        else { ogSet((res.d && res.d.error) || 'Remove failed', 'danger'); }
      }).catch(function (e) { ogRm.disabled = false; ogSet('Error: ' + e, 'danger'); });
  });

  // ── Membership buttons toggle (saved straight to the live site) ─────────────
  var memToggle = document.getElementById('site-membership');
  var memStatus = document.getElementById('site-membership-status');
  if (memToggle) memToggle.addEventListener('change', function () {
    if (memStatus) { memStatus.textContent = 'Saving…'; memStatus.className = 'text-xs muted'; }
    fetch('/os/api/settings', {
      method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
      body: JSON.stringify({ key: 'site.membership_buttons', value: memToggle.checked ? 'true' : 'false' })
    }).then(function (r) {
      if (!r.ok) return r.json().then(function (e) { throw new Error((e.error && e.error.message) || e.error || ('save failed (' + r.status + ')')); });
      return r.json();
    }).then(function () {
      if (memStatus) { memStatus.textContent = 'Saved'; memStatus.className = 'text-xs status--ok'; }
      if (window.vpToast) window.vpToast('Saved', 'ok');
    }).catch(function (err) { if (memStatus) { memStatus.textContent = String(err.message || err); memStatus.className = 'text-xs status--danger'; } });
  });

  // ── Homepage hero toggle (saved straight to the live site) ──────────────────
  var heroToggle = document.getElementById('home-hero');
  var heroToggleStatus = document.getElementById('home-hero-status');
  if (heroToggle) heroToggle.addEventListener('change', function () {
    if (heroToggleStatus) { heroToggleStatus.textContent = 'Saving…'; heroToggleStatus.className = 'text-xs muted'; }
    fetch('/os/api/settings', {
      method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
      body: JSON.stringify({ key: 'home.hero', value: heroToggle.checked ? 'true' : 'false' })
    }).then(function (r) {
      if (!r.ok) return r.json().then(function (e) { throw new Error((e.error && e.error.message) || e.error || ('save failed (' + r.status + ')')); });
      return r.json();
    }).then(function () {
      if (heroToggleStatus) { heroToggleStatus.textContent = 'Saved'; heroToggleStatus.className = 'text-xs status--ok'; }
      if (window.vpToast) window.vpToast('Saved', 'ok');
    }).catch(function (err) { if (heroToggleStatus) { heroToggleStatus.textContent = String(err.message || err); heroToggleStatus.className = 'text-xs status--danger'; } });
  });

  // ── Author bio (saved to the live site on change) ───────────────────────────
  var bioInput = document.getElementById('author-bio');
  var bioStatus = document.getElementById('author-bio-status');
  if (bioInput) bioInput.addEventListener('change', function () {
    if (bioStatus) { bioStatus.textContent = 'Saving…'; bioStatus.className = 'theme-field__hint'; }
    fetch('/os/api/settings', {
      method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
      body: JSON.stringify({ key: 'site.author_bio', value: bioInput.value })
    }).then(function (r) {
      if (!r.ok) return r.json().then(function (e) { throw new Error((e.error && e.error.message) || e.error || ('save failed (' + r.status + ')')); });
      return r.json();
    }).then(function () {
      if (bioStatus) { bioStatus.textContent = 'Saved · live on your posts'; bioStatus.className = 'theme-field__hint status--ok'; }
      if (window.vpToast) window.vpToast('Author bio saved', 'ok');
    }).catch(function (err) { if (bioStatus) { bioStatus.textContent = String(err.message || err); bioStatus.className = 'theme-field__hint status--danger'; } });
  });

  // ── Navigation editor (saves nav.items straight to the live site) ───────────
  var navRows = document.getElementById('cz-nav-rows');
  var navAdd = document.getElementById('cz-nav-add');
  var navSave = document.getElementById('cz-nav-save');
  var navStatus = document.getElementById('cz-nav-status');
  var navSeedEl = document.getElementById('cz-nav-seed');
  function navSet(t, kind) { if (navStatus) { navStatus.textContent = t; navStatus.className = 'text-sm' + (kind ? ' status--' + kind : ' muted'); } }
  function navRow(label, href) {
    var row = document.createElement('div'); row.setAttribute('data-nav-row', ''); row.className = 'cz-nav-row';
    var li = document.createElement('input'); li.className = 'input'; li.type = 'text'; li.placeholder = 'Label'; li.value = label || ''; li.setAttribute('data-nav-label', '');
    var hi = document.createElement('input'); hi.className = 'input'; hi.type = 'text'; hi.placeholder = '/path or https://…'; hi.value = href || ''; hi.setAttribute('data-nav-href', '');
    var rm = document.createElement('button'); rm.type = 'button'; rm.className = 'btn btn--sm'; rm.textContent = '✕';
    rm.addEventListener('click', function () { row.remove(); });
    row.appendChild(li); row.appendChild(hi); row.appendChild(rm);
    return row;
  }
  if (navRows) {
    var navSeed = [];
    try { navSeed = JSON.parse(navSeedEl && navSeedEl.value ? navSeedEl.value : '[]'); } catch (e) { navSeed = []; }
    if (!Array.isArray(navSeed) || !navSeed.length) navSeed = [{ label: 'Home', href: '/' }, { label: 'Archive', href: '/feed.xml' }];
    navSeed.forEach(function (it) { navRows.appendChild(navRow(it.label || it.Label, it.href || it.Href)); });
  }
  if (navAdd) navAdd.addEventListener('click', function () { if (navRows) navRows.appendChild(navRow('', '')); });
  if (navSave) navSave.addEventListener('click', function () {
    var items = [];
    if (navRows) navRows.querySelectorAll('[data-nav-row]').forEach(function (row) {
      var l = row.querySelector('[data-nav-label]').value.trim();
      var h = row.querySelector('[data-nav-href]').value.trim();
      if (l && h) items.push({ label: l, href: h });
    });
    navSet('Saving…'); navSave.disabled = true;
    fetch('/os/api/settings', {
      method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
      body: JSON.stringify({ key: 'nav.items', value: JSON.stringify(items) })
    }).then(function (r) {
      if (!r.ok) return r.json().then(function (e) { throw new Error((e.error && e.error.message) || e.error || ('save failed (' + r.status + ')')); });
      return r.json();
    }).then(function () {
      navSet('Saved · live on your site · ' + new Date().toLocaleTimeString(), 'ok');
      if (window.vpToast) window.vpToast('Navigation saved', 'ok');
    }).catch(function (err) { navSet(String(err.message || err), 'danger'); })
      .then(function () { navSave.disabled = false; });
  });

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
