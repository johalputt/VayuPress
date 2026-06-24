/* admin-os-mail.js — VayuMail panel interactions (compose, accounts, message
 * actions). CSRF: reads the vp_csrf cookie and sends it as X-CSRF-Token, matching
 * the double-submit middleware. No inline handlers (CSP-safe). */
(function () {
  'use strict';

  function cookie(name) {
    var m = document.cookie.match(new RegExp('(?:^|; )' + name + '=([^;]*)'));
    return m ? decodeURIComponent(m[1]) : '';
  }

  function postJSON(url, data) {
    return fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': cookie('vp_csrf') },
      body: JSON.stringify(data || {}),
    }).then(function (r) {
      return r.json().catch(function () { return {}; }).then(function (body) {
        return { ok: r.ok, status: r.status, body: body };
      });
    });
  }

  function val(root, sel) {
    var el = root.querySelector(sel);
    return el ? (el.value || '').trim() : '';
  }

  // ── Compose ────────────────────────────────────────────────────────────────
  var compose = document.querySelector('form[data-mail-compose]');
  if (compose) {
    var cStatus = compose.querySelector('[data-c-status]');
    compose.addEventListener('submit', function (e) {
      e.preventDefault();
      var to = val(compose, '[data-c-to]');
      if (!to) { if (cStatus) cStatus.textContent = 'Add at least one recipient.'; return; }
      if (cStatus) cStatus.textContent = 'Sending…';
      postJSON('/os/vayuos/mail/send', {
        from: val(compose, '[data-c-from]'),
        to: to,
        subject: val(compose, '[data-c-subject]'),
        body: val(compose, '[data-c-body]'),
      }).then(function (res) {
        if (res.ok) {
          if (cStatus) cStatus.textContent = 'Queued for delivery ✓';
          setTimeout(function () { window.location.href = '/os/vayuos/mail/sent'; }, 700);
        } else {
          if (cStatus) cStatus.textContent = 'Failed: ' + ((res.body && res.body.message) || res.status);
        }
      });
    });
  }

  // ── Create mail account ──────────────────────────────────────────────────────
  var acctForm = document.querySelector('form[data-acct-create]');
  if (acctForm) {
    var aStatus = acctForm.querySelector('[data-a-status]');
    acctForm.addEventListener('submit', function (e) {
      e.preventDefault();
      var local = val(acctForm, '[data-a-local]');
      var pass = val(acctForm, '[data-a-pass]');
      if (!local || pass.length < 8) { if (aStatus) aStatus.textContent = 'Address and an 8+ character password are required.'; return; }
      if (aStatus) aStatus.textContent = 'Creating…';
      postJSON('/os/vayuos/mail/accounts/create', {
        local: local, name: val(acctForm, '[data-a-name]'), pass: pass,
      }).then(function (res) {
        if (res.ok) { window.location.reload(); }
        else if (aStatus) aStatus.textContent = 'Failed: ' + ((res.body && res.body.message) || res.status);
      });
    });
  }

  // ── Delete mail account ──────────────────────────────────────────────────────
  document.querySelectorAll('[data-acct-delete]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var email = btn.getAttribute('data-acct-delete');
      if (!window.confirm('Delete mail account ' + email + '? This cannot be undone.')) return;
      postJSON('/os/vayuos/mail/accounts/delete', { email: email }).then(function (res) {
        if (res.ok) window.location.reload();
        else window.alert('Delete failed: ' + ((res.body && res.body.message) || res.status));
      });
    });
  });

  // ── Message actions (Junk / Trash / Restore / Delete) ────────────────────────
  var actions = document.querySelector('[data-mail-actions]');
  if (actions) {
    var user = actions.getAttribute('data-user');
    var folder = actions.getAttribute('data-folder');
    var id = actions.getAttribute('data-id');
    var backFolder = function (f) { return '/os/vayuos/mail/inbox?user=' + encodeURIComponent(user) + '&folder=' + encodeURIComponent(f); };

    actions.querySelectorAll('[data-mail-move]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        var target = btn.getAttribute('data-mail-move');
        postJSON('/os/vayuos/mail/message/action', { user: user, id: id, folder: folder, to: target }).then(function (res) {
          if (res.ok) window.location.href = backFolder(folder);
          else window.alert('Move failed: ' + ((res.body && res.body.message) || res.status));
        });
      });
    });

    var del = actions.querySelector('[data-mail-delete]');
    if (del) {
      del.addEventListener('click', function () {
        if (!window.confirm('Permanently delete this message?')) return;
        postJSON('/os/vayuos/mail/message/action', { user: user, id: id, folder: folder, delete: true }).then(function (res) {
          if (res.ok) window.location.href = backFolder(folder);
          else window.alert('Delete failed: ' + ((res.body && res.body.message) || res.status));
        });
      });
    }
  }
})();
