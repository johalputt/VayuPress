/*
 * admin-os-security.js — TOTP 2FA enrolment for VayuOS (ADR-0068, Phase 5).
 * Strict CSP: no eval, no innerHTML with untrusted data; DOM via textContent.
 */
(function () {
  'use strict';

  var card = document.querySelector('[data-totp-card]');
  if (!card) return;

  function csrf() {
    var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }
  function toast(msg, kind) {
    if (window.vpToast) window.vpToast(msg, kind);
  }
  function post(url, body) {
    return fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() },
      body: body ? JSON.stringify(body) : '{}'
    });
  }

  var beginBtn = card.querySelector('[data-totp-begin]');
  var enrollBox = card.querySelector('[data-totp-enroll]');
  var keyEl = card.querySelector('[data-totp-key]');
  var uriEl = card.querySelector('[data-totp-uri]');
  var codeEl = card.querySelector('[data-totp-code]');
  var verifyBtn = card.querySelector('[data-totp-verify]');
  var disableBtn = card.querySelector('[data-totp-disable]');

  if (beginBtn) {
    beginBtn.addEventListener('click', function () {
      post('/os/api/totp/begin')
        .then(function (r) { return r.json(); })
        .then(function (data) {
          if (!data.secret) { toast(data.message || 'Could not start 2FA', 'error'); return; }
          if (keyEl) keyEl.textContent = data.secret;
          if (uriEl) uriEl.setAttribute('href', data.uri);
          if (enrollBox) enrollBox.hidden = false;
          beginBtn.disabled = true;
          if (codeEl) codeEl.focus();
        })
        .catch(function () { toast('Network error', 'error'); });
    });
  }

  if (verifyBtn) {
    verifyBtn.addEventListener('click', function () {
      var code = codeEl ? codeEl.value.trim() : '';
      if (code.length !== 6) { toast('Enter the 6-digit code', 'error'); return; }
      post('/os/api/totp/verify', { code: code })
        .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, j: j }; }); })
        .then(function (res) {
          if (!res.ok) { toast(res.j.message || 'Invalid code', 'error'); return; }
          toast('Two-factor authentication enabled', 'ok');
          setTimeout(function () { window.location.reload(); }, 800);
        })
        .catch(function () { toast('Network error', 'error'); });
    });
  }

  if (disableBtn) {
    disableBtn.addEventListener('click', function () {
      if (!window.confirm('Disable two-factor authentication for your account?')) return;
      post('/os/api/totp/disable')
        .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, j: j }; }); })
        .then(function (res) {
          if (!res.ok) { toast(res.j.message || 'Could not disable', 'error'); return; }
          toast('Two-factor authentication disabled', 'ok');
          setTimeout(function () { window.location.reload(); }, 800);
        })
        .catch(function () { toast('Network error', 'error'); });
    });
  }
})();
