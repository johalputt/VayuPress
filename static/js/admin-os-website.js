/* admin-os-website.js — VayuOS Website studio.
 * Hydrates the editor from #vp-biz-data, tracks template/mode selection, and
 * saves everything through POST /os/api/website/save. CSP-safe external file. */
(function () {
  'use strict';
  var dataEl = document.getElementById('vp-biz-data');
  if (!dataEl) return;
  var state = { mode: 'blog', template: '', content: {} };
  try { state = JSON.parse(dataEl.textContent) || state; } catch (e) { return; }
  state.content = state.content || {};

  var statusEl = document.querySelector('[data-biz-status]');
  function setStatus(msg, ok) {
    if (!statusEl) return;
    statusEl.textContent = msg;
    statusEl.style.color = ok ? '' : 'var(--danger)';
  }
  function csrf() {
    var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }

  // ── Hydrate fields ──────────────────────────────────────────────────────────
  var fields = document.querySelectorAll('[data-biz-f]');
  function servicesToText(list) {
    return (list || []).map(function (s) {
      return [s.title || '', s.desc || '', s.price || ''].join(' | ').replace(/\s*\|\s*$/,'').replace(/\s*\|\s*$/,'');
    }).join('\n');
  }
  function textToServices(text) {
    return String(text || '').split('\n').map(function (line) {
      var p = line.split('|').map(function (x) { return x.trim(); });
      return { title: p[0] || '', desc: p[1] || '', price: p[2] || '' };
    }).filter(function (s) { return s.title !== ''; });
  }
  fields.forEach(function (el) {
    var k = el.getAttribute('data-biz-f');
    var c = state.content;
    if (k === 'showBlog') { el.checked = !!c.showBlog; return; }
    if (k === 'services') { el.value = servicesToText(c.services); return; }
    if (k === 'gallery') { el.value = (c.gallery || []).join('\n'); return; }
    el.value = c[k] != null ? String(c[k]) : '';
  });

  // ── Mode + template selection ──────────────────────────────────────────────
  document.querySelectorAll('input[name="biz-mode"]').forEach(function (radio) {
    if (radio.value === state.mode || (state.mode === '' && radio.value === 'blog')) radio.checked = true;
    radio.addEventListener('change', function () { if (radio.checked) state.mode = radio.value; });
  });
  // Keep the "Preview" button pointed at the currently-selected design, so an
  // operator can preview a design before saving it (via /site?preview=<key>).
  // The page's own preview frame shows it too, so a design is seen before it
  // is saved; the frame's address is built on a fixed path, never read from
  // the page, so nothing on it can point the frame elsewhere.
  var previewLink = document.querySelector('[data-biz-preview]');
  var frame = document.querySelector('[data-biz-frame]');
  var designName = document.querySelector('[data-biz-design-name]');
  function showDesign() {
    if (!state.template) return;
    var key = encodeURIComponent(state.template);
    if (previewLink) previewLink.setAttribute('href', '/site?preview=' + key);
    if (frame) frame.setAttribute('src', '/os/api/site-doc/preview?page=&preview=' + key + '&t=' + Date.now());
  }
  document.querySelectorAll('[data-biz-template]').forEach(function (card) {
    card.addEventListener('click', function () {
      state.template = card.getAttribute('data-biz-template');
      document.querySelectorAll('[data-biz-template]').forEach(function (c) {
        c.classList.toggle('biz-card--active', c === card);
      });
      if (designName) designName.textContent = card.getAttribute('data-biz-template-name') || state.template;
      showDesign();
      var sheet = card.closest('dialog');
      if (sheet && sheet.open) sheet.close();
      setStatus('Design chosen. Save & publish to make it live', true);
    });
  });

  // ── Save ───────────────────────────────────────────────────────────────────
  function collect() {
    var c = {};
    fields.forEach(function (el) {
      var k = el.getAttribute('data-biz-f');
      if (k === 'showBlog') { c.showBlog = !!el.checked; return; }
      if (k === 'services') { c.services = textToServices(el.value); return; }
      if (k === 'gallery') {
        c.gallery = el.value.split('\n').map(function (s) { return s.trim(); }).filter(Boolean);
        return;
      }
      c[k] = el.value;
    });
    return c;
  }
  var saveBtn = document.querySelector('[data-biz-save]');
  if (saveBtn) saveBtn.addEventListener('click', function () {
    setStatus('Publishing…', true);
    fetch('/os/api/website/save', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() },
      body: JSON.stringify({ mode: state.mode, template: state.template, content: collect() })
    }).then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
      .then(function () {
        setStatus('Published', true);
        showDesign();
        if (window.vpToast) window.vpToast('Website published', 'ok');
      })
      .catch(function (code) { setStatus('Save failed (' + code + ')', false); });
  });

  // ── Custom build: deploy .zip / roll back ───────────────────────────────────
  var deployStatus = document.querySelector('[data-biz-deploy-status]');
  function setDeploy(msg, ok) {
    if (!deployStatus) return;
    deployStatus.textContent = msg;
    deployStatus.style.color = ok ? '' : 'var(--danger)';
  }
  function errMsg(j, fallback) {
    return (j && j.error && j.error.message) ? j.error.message : fallback;
  }
  var deployBtn = document.querySelector('[data-biz-deploy]');
  var zipInput = document.querySelector('[data-biz-zip]');
  if (deployBtn && zipInput) deployBtn.addEventListener('click', function () {
    var f = zipInput.files && zipInput.files[0];
    if (!f) { setDeploy('Choose a .zip file first', false); return; }
    deployBtn.disabled = true;
    window.vpBundleUpload('/os/api/website/custom-bundle', f, csrf(), function (done, total, phase) {
      setDeploy(window.vpBundleProgressText(done, total, phase), true);
    }).then(function (j) {
      deployBtn.disabled = false;
      var nf = j.files || 0;
      var msg = 'Deployed ' + nf + ' file' + (nf === 1 ? '' : 's') + ' \u2713';
      if (j.skipped) msg += ' (' + j.skipped + ' system file' + (j.skipped === 1 ? '' : 's') + ' ignored)';
      setDeploy(msg + '. Choose \u201CThe site you uploaded\u201D in Hosting, then Save & publish', true);
      if (window.vpToast) window.vpToast('Custom build deployed', 'ok');
    }, function (e) {
      deployBtn.disabled = false;
      setDeploy('Not deployed \u2014 the live site is unchanged. ' + e.message, false);
    });
  });
  var rollbackBtn = document.querySelector('[data-biz-rollback]');
  if (rollbackBtn) rollbackBtn.addEventListener('click', function () {
    setDeploy('Rolling back…', true);
    fetch('/os/api/website/custom-rollback', {
      method: 'POST', headers: { 'X-CSRF-Token': csrf() }
    }).then(function (r) { return r.json().then(function (j) { return { ok: r.ok, j: j }; }); })
      .then(function (res) {
        if (!res.ok) { setDeploy(errMsg(res.j, 'Rollback failed'), false); return; }
        setDeploy('Rolled back \u2713 — reload to refresh details', true);
        if (window.vpToast) window.vpToast('Rolled back', 'ok');
      })
      .catch(function () { setDeploy('Rollback failed (network)', false); });
  });
})();
