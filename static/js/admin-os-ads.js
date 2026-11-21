/* SPDX-License-Identifier: Apache-2.0 */
/* Advertising (/os/ads, and a hosted site's own /os/d/{id}/ads): adding,
   editing, switching and deleting ad slots, reviewing members' ads, and a
   hosted site's own settings and copy. The calls go to the page's own site
   (#ads-ctx data-base). Its words go through vpT, so the page says them in
   its own language. */
(function () {
  'use strict';
  var ctx = document.getElementById('ads-ctx');
  if (!ctx) return;
  var BASE = ctx.getAttribute('data-base');
  function csrf() { var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/); return m ? m[1] : ''; }
  var msg = document.getElementById('action-msg');
  function show(t, e) { if (!msg) return; msg.textContent = t; msg.classList.toggle('is-error', !!e); msg.classList.add('visible'); }
  function jfetch(method, url, payload) {
    var o = { method: method, headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() } };
    if (payload) o.body = JSON.stringify(payload);
    return fetch(url, o).then(function (r) {
      return r.json().catch(function () { return {}; }).then(function (d) { return { ok: r.ok, d: d || {} }; });
    });
  }
  // refusal is what the server said when it refused: a refusal written as an
  // error carries its words in error.message, and showed as "Error".
  function refusal(res) {
    var d = res.d || {};
    return d.detail || (d.error && d.error.message) || d.title || vpT('That did not go through.');
  }
  function failed(e) { return vpT('Request failed: {error}', { error: String(e) }); }
  function val(id) { var el = document.getElementById(id); return el ? el.value : ''; }
  function slot(p) {
    return { name: val(p + '-name'), placement: val(p + '-placement'), kind: val(p + '-kind'), image_url: val(p + '-image'), link_url: val(p + '-link'),
      alt_text: val(p + '-alt'), html: val(p + '-html'), show_on: val(p + '-show-on'), show_paths: val(p + '-show-paths') };
  }
  function reloadOr(b) {
    return function (res) { if (res.ok) { location.reload(); } else { if (b) b.disabled = false; show(refusal(res), true); } };
  }

  var createBtn = document.getElementById('ad-create-btn');
  if (createBtn) createBtn.addEventListener('click', function () {
    var payload = slot('ad'); payload.enabled = true;
    if (!payload.name.trim()) { show(vpT('Give the slot a name'), true); return; }
    createBtn.disabled = true; show(vpT('Creating…'), false);
    jfetch('POST', BASE, payload).then(reloadOr(createBtn)).catch(function (e) { createBtn.disabled = false; show(failed(e), true); });
  });

  document.querySelectorAll('[data-ad-save]').forEach(function (b) {
    b.addEventListener('click', function () {
      var payload = slot(b.getAttribute('data-prefix'));
      payload.enabled = b.getAttribute('data-enabled') === 'true';
      payload.sort = parseInt(b.getAttribute('data-sort'), 10) || 0;
      if (!payload.name.trim()) { show(vpT('Give the slot a name'), true); return; }
      b.disabled = true; show(vpT('Saving…'), false);
      jfetch('PUT', BASE + '/' + encodeURIComponent(b.getAttribute('data-ad-save')), payload).then(reloadOr(b)).catch(function (e) { b.disabled = false; show(failed(e), true); });
    });
  });

  document.querySelectorAll('[data-ad-action]').forEach(function (b) {
    b.addEventListener('click', function () {
      var act = b.getAttribute('data-ad-action'); var id = b.getAttribute('data-id');
      if (act === 'delete') {
        vpConfirm({ title: vpT('Delete this ad slot?'), confirm: vpT('Delete') }, function () {
          b.disabled = true; jfetch('DELETE', BASE + '/' + encodeURIComponent(id)).then(reloadOr(b));
        });
      } else if (act === 'toggle') {
        b.disabled = true; jfetch('POST', BASE + '/' + encodeURIComponent(id) + '/toggle', { enabled: b.getAttribute('data-to') === '1' }).then(reloadOr(b));
      }
    });
  });

  var copyBtn = document.querySelector('[data-ads-copy]');
  if (copyBtn) copyBtn.addEventListener('click', function () {
    vpConfirm({ title: vpT('Copy your ads to this site?'),
      message: vpT('Your own ad slots, AdSense publisher ID, disclosure and switches are copied onto this site. It is a copy: changing yours later leaves this site as it is.'),
      confirm: vpT('Copy') }, function () {
      copyBtn.disabled = true; jfetch('POST', BASE + '/copy').then(reloadOr(copyBtn));
    });
  });

  var setBtn = document.querySelector('[data-site-ads-save]');
  if (setBtn) setBtn.addEventListener('click', function () {
    var p = {};
    document.querySelectorAll('[data-site-ad-key]').forEach(function (el) {
      p[el.getAttribute('data-site-ad-key')] = el.type === 'checkbox' ? (el.checked ? 'on' : 'off') : el.value;
    });
    setBtn.disabled = true; show(vpT('Saving…'), false);
    jfetch('POST', BASE + '/settings', { settings: p }).then(reloadOr(setBtn)).catch(function (e) { setBtn.disabled = false; show(failed(e), true); });
  });

  document.querySelectorAll('[data-adreview-action]').forEach(function (b) {
    b.addEventListener('click', function () {
      var act = b.getAttribute('data-adreview-action'); var id = b.getAttribute('data-id');
      var go = function () { b.disabled = true; jfetch('POST', BASE + '/' + encodeURIComponent(id) + '/' + act).then(reloadOr(b)); };
      if (act === 'reject') { vpConfirm({ title: vpT('Reject this member ad?'), message: vpT('It will not be published.'), confirm: vpT('Reject') }, go); } else { go(); }
    });
  });
})();
