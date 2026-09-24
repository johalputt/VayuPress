/*
 * admin-os-security.js — TOTP 2FA enrolment for VayuOS (ADR-0068, Phase 5).
 * Strict CSP: no eval, no innerHTML with untrusted data; DOM via textContent.
 */
(function () {
  'use strict';

  if (!document.querySelector('[data-totp-card]')) return;

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

  // Delegated, and every element looked up from the card when used: enabling or
  // disabling re-renders the page in place (vpRefresh), which replaces the card.
  function el(sel) { var c = document.querySelector('[data-totp-card]'); return c ? c.querySelector(sel) : null; }
  function done(msg) {
    toast(msg, 'ok');
    if (window.vpRefresh) window.vpRefresh(); else window.location.reload();
  }

  function begin(beginBtn) {
    post('/os/api/totp/begin')
      .then(function (r) { return r.json(); })
      .then(function (data) {
        if (!data.secret) { toast(data.message || 'Could not start 2FA', 'error'); return; }
        var keyEl = el('[data-totp-key]'), uriEl = el('[data-totp-uri]'), qrEl = el('[data-totp-qr]');
        var enrollBox = el('[data-totp-enroll]'), codeEl = el('[data-totp-code]');
        if (keyEl) keyEl.textContent = data.secret;
        if (uriEl) uriEl.setAttribute('href', data.uri);
        if (qrEl) {
          if (data.qr) { qrEl.src = data.qr; qrEl.style.display = ''; }
          else { qrEl.style.display = 'none'; }
        }
        if (enrollBox) enrollBox.hidden = false;
        beginBtn.disabled = true;
        if (codeEl) codeEl.focus();
      })
      .catch(function () { toast('Network error', 'error'); });
  }

  function verify() {
    var codeEl = el('[data-totp-code]');
    var code = codeEl ? codeEl.value.trim() : '';
    if (code.length !== 6) { toast('Enter the 6-digit code', 'error'); return; }
    post('/os/api/totp/verify', { code: code })
      .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, j: j }; }); })
      .then(function (res) {
        if (!res.ok) { toast(res.j.message || 'Invalid code', 'error'); return; }
        done('Two-factor authentication enabled');
      })
      .catch(function () { toast('Network error', 'error'); });
  }

  function disable() {
    vpConfirm({
      title: 'Disable two-factor authentication',
      message: 'Disable two-factor authentication for your account? From then on, the password alone signs you in.',
      confirm: 'Disable',
    }, function () {
      post('/os/api/totp/disable')
        .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, j: j }; }); })
        .then(function (res) {
          if (!res.ok) { toast(res.j.message || 'Could not disable', 'error'); return; }
          done('Two-factor authentication disabled');
        })
        .catch(function () { toast('Network error', 'error'); });
    });
  }

  document.addEventListener('click', function (e) {
    var t = e.target && e.target.closest ? e.target : null;
    if (!t || !t.closest('[data-totp-card]')) return;
    var btn;
    if ((btn = t.closest('[data-totp-begin]'))) { begin(btn); return; }
    if (t.closest('[data-totp-verify]')) { verify(); return; }
    if (t.closest('[data-totp-disable]')) { disable(); }
  });
})();
