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

  // errText turns a failed response into a readable message, with a clear hint
  // for the expired-CSRF (403) case so the operator knows to just reload.
  function errText(res) {
    if (res.status === 403) return 'session token expired — reload the page and try again';
    return (res.body && res.body.message) || res.status;
  }

  function val(root, sel) {
    var el = root.querySelector(sel);
    return el ? (el.value || '').trim() : '';
  }

  // ── Compose ────────────────────────────────────────────────────────────────
  var compose = document.querySelector('form[data-mail-compose]');
  if (compose) {
    var cStatus = compose.querySelector('[data-c-status]');
    var composeFields = function () {
      return {
        from: val(compose, '[data-c-from]'),
        to: val(compose, '[data-c-to]'),
        subject: val(compose, '[data-c-subject]'),
        body: val(compose, '[data-c-body]'),
      };
    };
    compose.addEventListener('submit', function (e) {
      e.preventDefault();
      var f = composeFields();
      if (!f.to) { if (cStatus) cStatus.textContent = 'Add at least one recipient.'; return; }
      if (cStatus) cStatus.textContent = 'Sending…';
      postJSON('/os/vayuos/mail/send', f).then(function (res) {
        if (res.ok) {
          if (cStatus) cStatus.textContent = 'Queued for delivery ✓';
          setTimeout(function () { window.location.href = '/os/vayuos/mail/sent'; }, 700);
        } else {
          if (cStatus) cStatus.textContent = 'Failed: ' + errText(res);
        }
      });
    });
    var draftBtn = compose.querySelector('[data-c-draft]');
    if (draftBtn) {
      draftBtn.addEventListener('click', function () {
        var f = composeFields();
        if (cStatus) cStatus.textContent = 'Saving draft…';
        postJSON('/os/vayuos/mail/draft', f).then(function (res) {
          if (res.ok) {
            if (cStatus) cStatus.textContent = 'Saved to Drafts ✓';
            setTimeout(function () { window.location.href = '/os/vayuos/mail/inbox?user=' + encodeURIComponent((f.from.match(/[^<@\s]+(?=@)/) || [''])[0]) + '&folder=Drafts'; }, 700);
          } else if (cStatus) {
            cStatus.textContent = 'Draft failed: ' + errText(res);
          }
        });
      });
    }
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
        role: val(acctForm, '[data-a-role]'),
      }).then(function (res) {
        if (res.ok) { window.location.reload(); }
        else if (aStatus) aStatus.textContent = 'Failed: ' + ((res.body && res.body.message) || res.status);
      });
    });
  }

  // ── Change account role ──────────────────────────────────────────────────────
  document.querySelectorAll('[data-acct-role]').forEach(function (sel) {
    sel.addEventListener('change', function () {
      var email = sel.getAttribute('data-acct-role');
      postJSON('/os/vayuos/mail/accounts/update', { email: email, role: sel.value }).then(function (res) {
        if (!res.ok) window.alert('Role update failed: ' + ((res.body && res.body.message) || res.status));
      });
    });
  });

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

  // ── Set account password ─────────────────────────────────────────────────────
  document.querySelectorAll('[data-acct-pass]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var email = btn.getAttribute('data-acct-pass');
      var pass = window.prompt('New password for ' + email + ' (min 8 characters):');
      if (pass === null) return;
      if (pass.length < 8) { window.alert('Password must be at least 8 characters.'); return; }
      postJSON('/os/vayuos/mail/accounts/update', { email: email, pass: pass }).then(function (res) {
        if (res.ok) window.alert('Password updated for ' + email);
        else window.alert('Update failed: ' + ((res.body && res.body.message) || res.status));
      });
    });
  });

  // ── Enable / disable account ─────────────────────────────────────────────────
  document.querySelectorAll('[data-acct-toggle]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var email = btn.getAttribute('data-acct-toggle');
      var active = btn.getAttribute('data-active') === 'true';
      var verb = active ? 'Enable' : 'Disable';
      if (!window.confirm(verb + ' mail account ' + email + '?')) return;
      postJSON('/os/vayuos/mail/accounts/update', { email: email, active: active }).then(function (res) {
        if (res.ok) window.location.reload();
        else window.alert('Update failed: ' + ((res.body && res.body.message) || res.status));
      });
    });
  });

  // ── Enable two-factor (TOTP) on a mail account ───────────────────────────────
  // Two-step: begin (generate + store secret) → verify (validate a code → on).
  document.querySelectorAll('[data-acct-2fa-enable]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var email = btn.getAttribute('data-acct-2fa-enable');
      postJSON('/os/vayuos/mail/accounts/totp', { email: email, action: 'begin' }).then(function (res) {
        if (!res.ok || !res.body || !res.body.secret) {
          window.alert('Could not start 2FA setup: ' + errText(res));
          return;
        }
        // Show the secret + otpauth URI so it can be added to an authenticator
        // app (or pasted into one that accepts otpauth:// links).
        window.prompt(
          'Add this account to an authenticator app, then enter the 6-digit code below.\n\n' +
          'Secret key:\n' + res.body.secret + '\n\notpauth URI (copyable):',
          res.body.uri || ''
        );
        var code = window.prompt('Enter the current 6-digit code from your authenticator for ' + email + ':');
        if (code === null) return;
        postJSON('/os/vayuos/mail/accounts/totp', { email: email, action: 'verify', code: (code || '').trim() }).then(function (vr) {
          if (vr.ok) { window.alert('Two-factor authentication is now ON for ' + email); window.location.reload(); }
          else window.alert('Verification failed: ' + errText(vr));
        });
      });
    });
  });

  // ── Disable two-factor on a mail account ─────────────────────────────────────
  document.querySelectorAll('[data-acct-2fa-disable]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var email = btn.getAttribute('data-acct-2fa-disable');
      if (!window.confirm('Turn OFF two-factor authentication for ' + email + '?')) return;
      postJSON('/os/vayuos/mail/accounts/totp', { email: email, action: 'disable' }).then(function (res) {
        if (res.ok) window.location.reload();
        else window.alert('Update failed: ' + errText(res));
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

    actions.querySelectorAll('[data-mail-mark]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        var mark = btn.getAttribute('data-mail-mark');
        postJSON('/os/vayuos/mail/message/action', { user: user, id: id, folder: folder, mark: mark }).then(function (res) {
          if (res.ok) window.location.href = backFolder(folder);
          else window.alert('Mark failed: ' + ((res.body && res.body.message) || res.status));
        });
      });
    });

    actions.querySelectorAll('[data-mail-pin]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        var pin = btn.getAttribute('data-mail-pin') === '1';
        postJSON('/os/vayuos/mail/message/action', { user: user, id: id, folder: folder, pin: pin }).then(function (res) {
          if (res.ok) window.location.reload();
          else window.alert('Pin failed: ' + ((res.body && res.body.message) || res.status));
        });
      });
    });

    var moveSel = actions.querySelector('[data-mail-move-select]');
    if (moveSel) {
      moveSel.addEventListener('change', function () {
        var target = moveSel.value;
        if (!target) return;
        postJSON('/os/vayuos/mail/message/action', { user: user, id: id, folder: folder, to: target }).then(function (res) {
          if (res.ok) window.location.href = backFolder(folder);
          else { moveSel.value = ''; window.alert('Move failed: ' + ((res.body && res.body.message) || res.status)); }
        });
      });
    }

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
  // ── Mailbox list: per-row read/unread toggle ─────────────────────────────────
  document.querySelectorAll('[data-mail-mark-row]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      postJSON('/os/vayuos/mail/message/action', {
        user: btn.getAttribute('data-user'),
        folder: btn.getAttribute('data-folder'),
        id: btn.getAttribute('data-id'),
        mark: btn.getAttribute('data-mail-mark-row'),
      }).then(function (res) {
        if (res.ok) window.location.reload();
        else window.alert('Mark failed: ' + ((res.body && res.body.message) || res.status));
      });
    });
  });

  // ── Mailbox list: per-row pin toggle ─────────────────────────────────────────
  document.querySelectorAll('[data-mail-pin-row]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      postJSON('/os/vayuos/mail/message/action', {
        user: btn.getAttribute('data-user'),
        folder: btn.getAttribute('data-folder'),
        id: btn.getAttribute('data-id'),
        pin: btn.getAttribute('data-mail-pin-row') === '1',
      }).then(function (res) {
        if (res.ok) window.location.reload();
        else window.alert('Pin failed: ' + ((res.body && res.body.message) || res.status));
      });
    });
  });

  // ── Mailbox list: bulk selection + bulk actions ──────────────────────────────
  var bulk = document.querySelector('[data-mail-bulk]');
  var listTable = document.querySelector('[data-mail-list]');
  if (bulk && listTable) {
    var bUser = bulk.getAttribute('data-user');
    var bFolder = bulk.getAttribute('data-folder');
    var countEl = bulk.querySelector('[data-bulk-count]');
    var checks = function () { return Array.prototype.slice.call(listTable.querySelectorAll('[data-mail-check]')); };
    var selectedIds = function () { return checks().filter(function (c) { return c.checked; }).map(function (c) { return c.value; }); };
    var refresh = function () {
      var n = selectedIds().length;
      if (countEl) countEl.textContent = n + ' selected';
      if (n > 0) bulk.removeAttribute('hidden'); else bulk.setAttribute('hidden', '');
      var all = listTable.querySelector('[data-mail-check-all]');
      if (all) all.checked = n > 0 && n === checks().length;
    };
    var allBox = listTable.querySelector('[data-mail-check-all]');
    if (allBox) {
      allBox.addEventListener('change', function () {
        checks().forEach(function (c) { c.checked = allBox.checked; });
        refresh();
      });
    }
    checks().forEach(function (c) { c.addEventListener('change', refresh); });

    var runBulk = function (payload, confirmMsg) {
      var ids = selectedIds();
      if (!ids.length) return;
      if (confirmMsg && !window.confirm(confirmMsg.replace('{n}', ids.length))) return;
      payload.user = bUser; payload.folder = bFolder; payload.ids = ids;
      postJSON('/os/vayuos/mail/message/action', payload).then(function (res) {
        if (res.ok) window.location.reload();
        else window.alert('Action failed: ' + ((res.body && res.body.message) || res.status));
      });
    };
    bulk.querySelectorAll('[data-bulk-action]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        var a = btn.getAttribute('data-bulk-action');
        if (a === 'read' || a === 'unread') runBulk({ mark: a });
        else if (a === 'pin') runBulk({ pin: true });
        else if (a === 'delete') runBulk({ delete: true }, 'Permanently delete {n} message(s)?');
      });
    });
    var bulkMove = bulk.querySelector('[data-bulk-move]');
    if (bulkMove) {
      bulkMove.addEventListener('change', function () {
        if (!bulkMove.value) return;
        runBulk({ to: bulkMove.value });
      });
    }
    refresh();
  }

  // ── Message raw-source toggle ────────────────────────────────────────────────
  var rawBtn = document.querySelector('[data-mail-raw-toggle]');
  var rawPre = document.querySelector('[data-mail-raw]');
  if (rawBtn && rawPre) {
    rawBtn.addEventListener('click', function () {
      if (rawPre.hasAttribute('hidden')) {
        rawPre.removeAttribute('hidden');
        rawBtn.textContent = 'Hide raw source';
      } else {
        rawPre.setAttribute('hidden', '');
        rawBtn.textContent = 'View raw source';
      }
    });
  }
})();
