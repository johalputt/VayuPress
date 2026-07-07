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
    statusEl.style.color = ok ? '' : 'var(--color-danger, #ef4444)';
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
  var previewLink = document.querySelector('[data-biz-preview]');
  function updatePreviewLink() {
    if (previewLink && state.template) {
      previewLink.setAttribute('href', '/site?preview=' + encodeURIComponent(state.template));
    }
  }
  updatePreviewLink();
  document.querySelectorAll('[data-biz-template]').forEach(function (card) {
    card.addEventListener('click', function () {
      state.template = card.getAttribute('data-biz-template');
      document.querySelectorAll('[data-biz-template]').forEach(function (c) {
        c.classList.toggle('biz-card--active', c === card);
      });
      updatePreviewLink();
      setStatus('Design selected — Preview it, or Save & publish to apply', true);
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
        setStatus('Published ✓ — view it at /site' + (state.mode === 'business' ? ' (and your domain root)' : ''), true);
        if (window.vpToast) window.vpToast('Website published', 'ok');
      })
      .catch(function (code) { setStatus('Save failed (' + code + ')', false); });
  });

  // ── Custom build: deploy .zip / roll back ───────────────────────────────────
  var deployStatus = document.querySelector('[data-biz-deploy-status]');
  function setDeploy(msg, ok) {
    if (!deployStatus) return;
    deployStatus.textContent = msg;
    deployStatus.style.color = ok ? '' : 'var(--color-danger, #ef4444)';
  }
  function errMsg(j, fallback) {
    return (j && j.error && j.error.message) ? j.error.message : fallback;
  }
  var deployBtn = document.querySelector('[data-biz-deploy]');
  var zipInput = document.querySelector('[data-biz-zip]');
  if (deployBtn && zipInput) deployBtn.addEventListener('click', function () {
    var f = zipInput.files && zipInput.files[0];
    if (!f) { setDeploy('Choose a .zip file first', false); return; }
    var fd = new FormData();
    fd.append('bundle', f);
    setDeploy('Uploading & validating…', true);
    fetch('/os/api/website/custom-upload', {
      method: 'POST', headers: { 'X-CSRF-Token': csrf() }, body: fd
    }).then(function (r) { return r.json().then(function (j) { return { ok: r.ok, j: j }; }); })
      .then(function (res) {
        if (!res.ok) { setDeploy(errMsg(res.j, 'Deploy failed'), false); return; }
        setDeploy('Deployed ' + (res.j.files || '') + ' files \u2713 — select \u201CCustom uploaded website\u201D above, then Save & publish', true);
        if (window.vpToast) window.vpToast('Custom build deployed', 'ok');
      })
      .catch(function () { setDeploy('Deploy failed (network)', false); });
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
