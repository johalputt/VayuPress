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
  document.querySelectorAll('[data-biz-template]').forEach(function (card) {
    card.addEventListener('click', function () {
      state.template = card.getAttribute('data-biz-template');
      document.querySelectorAll('[data-biz-template]').forEach(function (c) {
        c.classList.toggle('biz-card--active', c === card);
      });
      setStatus('Design selected — Save & publish to apply', true);
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
})();
