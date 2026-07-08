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
    var sendBtn = compose.querySelector('[data-c-send]');
    var EMAIL_RE = /^[^\s@,;]+@[^\s@,;]+\.[^\s@,;]+$/;
    function localPart(addr) { var m = (addr || '').match(/[^<@\s]+(?=@)/); return m ? m[0] : ''; }

    // Reveal Cc/Bcc and Reply-To on demand (kept out of the way by default).
    var toggle = function (btnSel, fieldSels) {
      var btn = compose.querySelector(btnSel);
      if (!btn) return;
      btn.addEventListener('click', function () {
        fieldSels.forEach(function (s) { var el = compose.querySelector(s); if (el) el.hidden = !el.hidden; });
      });
    };
    toggle('[data-c-toggle-cc]', ['[data-c-cc-field]', '[data-c-bcc-field]']);
    toggle('[data-c-toggle-reply]', ['[data-c-reply-field]']);

    // Recipient chips: type an address + Enter/comma → a chip; invalid ones are
    // flagged; the comma-joined value is mirrored into the hidden data-c-<field>
    // input so the send/draft payloads are unchanged.
    function setupChips(field) {
      var container = compose.querySelector('[data-c-chips="' + field + '"]');
      var hidden = compose.querySelector('[data-c-' + field + ']');
      if (!container || !hidden) return;
      var input = container.querySelector('[data-c-chip-input]');
      var list = [];
      function render() {
        container.querySelectorAll('.vm-chip').forEach(function (c) { c.remove(); });
        list.forEach(function (addr, i) {
          var chip = document.createElement('span');
          chip.className = 'vm-chip' + (EMAIL_RE.test(addr) ? '' : ' vm-chip--bad');
          if (!EMAIL_RE.test(addr)) chip.title = 'This does not look like a valid email address';
          var label = document.createElement('span'); label.textContent = addr;
          var x = document.createElement('button');
          x.type = 'button'; x.className = 'vm-chip-x'; x.setAttribute('aria-label', 'Remove ' + addr); x.textContent = '×';
          x.addEventListener('click', function () { list.splice(i, 1); render(); sync(); });
          chip.appendChild(label); chip.appendChild(x);
          container.insertBefore(chip, input);
        });
      }
      function sync() { hidden.value = list.join(', '); updatePGP(); }
      function addFromText(text) {
        (text || '').split(/[,;\n]/).forEach(function (t) { t = t.trim(); if (t && list.indexOf(t) === -1) list.push(t); });
      }
      input.addEventListener('keydown', function (e) {
        if (e.key === 'Enter' || e.key === ',' || e.key === ';') {
          e.preventDefault();
          if (input.value.trim()) { addFromText(input.value); input.value = ''; render(); sync(); }
        } else if (e.key === 'Backspace' && !input.value && list.length) {
          list.pop(); render(); sync();
        }
      });
      input.addEventListener('blur', function () {
        if (input.value.trim()) { addFromText(input.value); input.value = ''; render(); sync(); }
      });
      if (hidden.value.trim()) { addFromText(hidden.value); render(); sync(); } // prefill (reply/forward)
    }
    setupChips('to'); setupChips('cc'); setupChips('bcc');

    // Attachment tray: array-backed so files can be removed and drag-dropped
    // (an <input type=file> FileList is immutable). The send handler builds the
    // multipart body from this array.
    var filesEl = compose.querySelector('[data-c-files]');
    var dropzone = compose.querySelector('[data-c-dropzone]');
    var tray = compose.querySelector('[data-c-attach-list]');
    var browseBtn = compose.querySelector('[data-c-attach-btn]');
    var composeFiles = [];
    function humanSize(n) {
      if (n < 1024) return n + ' B';
      if (n < 1048576) return (n / 1024).toFixed(1) + ' KB';
      return (n / 1048576).toFixed(1) + ' MB';
    }
    function renderFiles() {
      if (!tray) return;
      tray.textContent = '';
      composeFiles.forEach(function (f, i) {
        var chip = document.createElement('span');
        chip.className = 'vm-attach-chip';
        var ico = document.createElement('span'); ico.className = 'vm-attach-ico'; ico.textContent = '📄';
        var name = document.createElement('span'); name.className = 'vm-attach-name'; name.textContent = f.name;
        var size = document.createElement('span'); size.className = 'vm-attach-size'; size.textContent = humanSize(f.size);
        var x = document.createElement('button');
        x.type = 'button'; x.className = 'vm-chip-x'; x.setAttribute('aria-label', 'Remove ' + f.name); x.textContent = '×';
        x.addEventListener('click', function () { composeFiles.splice(i, 1); renderFiles(); updatePGP(); });
        chip.appendChild(ico); chip.appendChild(name); chip.appendChild(size); chip.appendChild(x);
        tray.appendChild(chip);
      });
    }
    function addFiles(fileList) {
      for (var i = 0; i < fileList.length; i++) composeFiles.push(fileList[i]);
      renderFiles(); updatePGP();
    }
    if (browseBtn && filesEl) browseBtn.addEventListener('click', function () { filesEl.click(); });
    if (filesEl) filesEl.addEventListener('change', function () { addFiles(filesEl.files); filesEl.value = ''; });
    if (dropzone) {
      ['dragenter', 'dragover'].forEach(function (ev) {
        dropzone.addEventListener(ev, function (e) { e.preventDefault(); dropzone.classList.add('vm-dropzone--over'); });
      });
      ['dragleave', 'drop'].forEach(function (ev) {
        dropzone.addEventListener(ev, function (e) { e.preventDefault(); dropzone.classList.remove('vm-dropzone--over'); });
      });
      dropzone.addEventListener('drop', function (e) { if (e.dataTransfer && e.dataTransfer.files) addFiles(e.dataTransfer.files); });
    }

    // PGP eligibility hint — reflects the engine's rule (single recipient, no
    // Cc/Bcc, no attachments) client-side; the actual encryption still depends
    // on the recipient's key being on file.
    var pgpHint = compose.querySelector('[data-c-pgp]');
    function countAddrs(v) { return (v || '').split(',').map(function (s) { return s.trim(); }).filter(Boolean).length; }
    function updatePGP() {
      if (!pgpHint) return;
      var to = countAddrs(val(compose, '[data-c-to]')), cc = countAddrs(val(compose, '[data-c-cc]')), bcc = countAddrs(val(compose, '[data-c-bcc]'));
      if (to + cc + bcc === 0) { pgpHint.textContent = ''; pgpHint.className = 'vm-pgp-hint'; return; }
      if (to === 1 && cc === 0 && bcc === 0 && composeFiles.length === 0) {
        pgpHint.textContent = '🔒 PGP-eligible — encrypts if the recipient key is on file';
        pgpHint.className = 'vm-pgp-hint vm-pgp-hint--enc';
      } else {
        pgpHint.textContent = '🔓 DKIM-signed (PGP needs one recipient, no Cc/Bcc, no attachments)';
        pgpHint.className = 'vm-pgp-hint';
      }
    }
    updatePGP();

    var composeFields = function () {
      return {
        from: val(compose, '[data-c-from]'),
        to: val(compose, '[data-c-to]'),
        cc: val(compose, '[data-c-cc]'),
        bcc: val(compose, '[data-c-bcc]'),
        replyTo: val(compose, '[data-c-reply]'),
        subject: val(compose, '[data-c-subject]'),
        body: val(compose, '[data-c-body]'),
      };
    };

    // Draft: manual save + a debounced autosave that REPLACES the previous
    // autosaved draft (delete-then-save) so Drafts never fills with copies.
    var draftId = '';
    var lastSavedSig = '';
    var autosaveTimer = null;
    function saveDraft(silent) {
      var f = composeFields();
      if (!f.to && !f.subject && !f.body) return;
      var sig = f.to + '|' + f.subject + '|' + f.body;
      if (silent && sig === lastSavedSig) return;
      lastSavedSig = sig;
      if (!silent && cStatus) cStatus.textContent = 'Saving draft…';
      var prev = draftId, user = localPart(f.from);
      postJSON('/os/vayumail/draft', f).then(function (res) {
        if (res.ok) {
          draftId = (res.body && res.body.id) || '';
          if (prev && user && prev !== draftId) {
            postJSON('/os/vayumail/message/action', { user: user, folder: 'Drafts', id: prev, delete: true });
          }
          if (cStatus) cStatus.textContent = silent ? 'Draft saved' : 'Saved to Drafts ✓';
        } else if (!silent && cStatus) {
          cStatus.textContent = 'Draft failed: ' + errText(res);
        }
      });
    }
    function stopAutosave() { if (autosaveTimer) { clearInterval(autosaveTimer); autosaveTimer = null; } }
    autosaveTimer = setInterval(function () { saveDraft(true); }, 20000);

    var draftBtn = compose.querySelector('[data-c-draft]');
    if (draftBtn) {
      draftBtn.addEventListener('click', function () {
        stopAutosave();
        saveDraft(false);
        var f = composeFields();
        setTimeout(function () { window.location.href = '/os/vayumail/inbox?user=' + encodeURIComponent(localPart(f.from)) + '&folder=Drafts'; }, 700);
      });
    }

    compose.addEventListener('submit', function (e) {
      e.preventDefault();
      var f = composeFields();
      if (!f.to && !f.cc && !f.bcc) { if (cStatus) cStatus.textContent = 'Add at least one recipient.'; return; }
      if (cStatus) cStatus.textContent = 'Sending…';
      if (sendBtn) sendBtn.disabled = true;
      var done = function (res) {
        if (sendBtn) sendBtn.disabled = false;
        if (res.ok) {
          stopAutosave();
          // Sent — clear the autosaved draft copy, like every mail client.
          if (draftId) { postJSON('/os/vayumail/message/action', { user: localPart(f.from), folder: 'Drafts', id: draftId, delete: true }); }
          if (cStatus) cStatus.textContent = 'Queued for delivery ✓';
          acctToast('Message queued for delivery');
          setTimeout(function () { window.location.href = '/os/vayumail/sent'; }, 650);
        } else {
          if (cStatus) cStatus.textContent = 'Failed: ' + errText(res);
          acctToast('Send failed: ' + errText(res), true);
        }
      };
      if (composeFiles.length > 0) {
        var fd = new FormData();
        Object.keys(f).forEach(function (k) { fd.append(k, f[k] || ''); });
        composeFiles.forEach(function (file) { fd.append('attachments', file); });
        fetch('/os/vayumail/send', { method: 'POST', headers: { 'X-CSRF-Token': cookie('vp_csrf') }, body: fd })
          .then(function (r) { return r.json().catch(function () { return {}; }).then(function (b) { return { ok: r.ok, status: r.status, body: b }; }); })
          .then(done);
      } else {
        postJSON('/os/vayumail/send', f).then(done);
      }
    });
  }

  // Enterprise feedback: a non-blocking toast (from the admin shell) instead of
  // a blocking alert() dialog. Falls back to alert() only if the shell toast is
  // somehow unavailable.
  function acctToast(msg, isErr) {
    if (window.vpToast) { window.vpToast(msg, isErr ? 'error' : 'success'); }
    else { window.alert(msg); }
  }
  // Soft reload after a state change that touches several cells (status/2FA
  // badges + button labels), giving the toast a moment to show first.
  function acctReload() { setTimeout(function () { window.location.reload(); }, 550); }

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
      postJSON('/os/vayumail/accounts/create', {
        local: local, name: val(acctForm, '[data-a-name]'), pass: pass,
        role: val(acctForm, '[data-a-role]'),
        quota_mb: parseFloat(val(acctForm, '[data-a-quota]')) || 0,
      }).then(function (res) {
        if (res.ok) { acctToast('Mailbox ' + local + ' created'); acctReload(); }
        else if (aStatus) aStatus.textContent = 'Failed: ' + errText(res);
      });
    });
  }

  // ── Change account role (reverts the select on failure) ──────────────────────
  document.querySelectorAll('[data-acct-role]').forEach(function (sel) {
    var prev = sel.value;
    sel.addEventListener('change', function () {
      var email = sel.getAttribute('data-acct-role');
      postJSON('/os/vayumail/accounts/update', { email: email, role: sel.value }).then(function (res) {
        if (res.ok) { acctToast('Role updated for ' + email); prev = sel.value; }
        else { sel.value = prev; acctToast('Role update failed: ' + errText(res), true); }
      });
    });
  });

  // ── Mailbox storage bar: apply width via CSSOM (CSP blocks inline styles) ────
  document.querySelectorAll('[data-quota-pct]').forEach(function (el) {
    var pct = parseInt(el.getAttribute('data-quota-pct'), 10);
    if (isNaN(pct)) pct = 0;
    el.style.width = Math.max(0, Math.min(100, pct)) + '%';
  });

  // ── Set mailbox storage quota (MB; 0 = unlimited) ────────────────────────────
  document.querySelectorAll('[data-acct-quota-save]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var email = btn.getAttribute('data-acct-quota-save');
      var input = document.querySelector('[data-acct-quota="' + (window.CSS && CSS.escape ? CSS.escape(email) : email) + '"]');
      var mb = input ? (parseFloat(input.value) || 0) : 0;
      if (mb < 0) mb = 0;
      btn.disabled = true;
      postJSON('/os/vayumail/accounts/update', { email: email, quota_mb: mb }).then(function (res) {
        btn.disabled = false;
        if (res.ok) { btn.textContent = 'Saved ✓'; setTimeout(function () { btn.textContent = 'Save'; }, 1500); }
        else acctToast('Quota update failed: ' + errText(res), true);
      });
    });
  });

  // ── Delete mail account (removes the row in place) ───────────────────────────
  document.querySelectorAll('[data-acct-delete]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var email = btn.getAttribute('data-acct-delete');
      if (!window.confirm('Delete mail account ' + email + '? This cannot be undone.')) return;
      btn.disabled = true;
      postJSON('/os/vayumail/accounts/delete', { email: email }).then(function (res) {
        if (res.ok) {
          acctToast('Deleted ' + email);
          var row = btn.closest('tr, [data-acct-row]');
          if (row && row.parentNode) { row.parentNode.removeChild(row); } else { acctReload(); }
        } else { btn.disabled = false; acctToast('Delete failed: ' + errText(res), true); }
      });
    });
  });

  // ── Set account password ─────────────────────────────────────────────────────
  document.querySelectorAll('[data-acct-pass]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var email = btn.getAttribute('data-acct-pass');
      var pass = window.prompt('New password for ' + email + ' (min 8 characters):');
      if (pass === null) return;
      if (pass.length < 8) { acctToast('Password must be at least 8 characters.', true); return; }
      postJSON('/os/vayumail/accounts/update', { email: email, pass: pass }).then(function (res) {
        if (res.ok) acctToast('Password updated for ' + email);
        else acctToast('Update failed: ' + errText(res), true);
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
      postJSON('/os/vayumail/accounts/update', { email: email, active: active }).then(function (res) {
        if (res.ok) { acctToast((active ? 'Enabled ' : 'Disabled ') + email); acctReload(); }
        else acctToast('Update failed: ' + errText(res), true);
      });
    });
  });

  // ── Enable two-factor (TOTP) on a mail account ───────────────────────────────
  // Two-step: begin (generate + store secret) → verify (validate a code → on).
  document.querySelectorAll('[data-acct-2fa-enable]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var email = btn.getAttribute('data-acct-2fa-enable');
      postJSON('/os/vayumail/accounts/totp', { email: email, action: 'begin' }).then(function (res) {
        if (!res.ok || !res.body || !res.body.secret) {
          acctToast('Could not start 2FA setup: ' + errText(res), true);
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
        postJSON('/os/vayumail/accounts/totp', { email: email, action: 'verify', code: (code || '').trim() }).then(function (vr) {
          if (vr.ok) { acctToast('Two-factor authentication is now ON for ' + email); acctReload(); }
          else acctToast('Verification failed: ' + errText(vr), true);
        });
      });
    });
  });

  // ── Disable two-factor on a mail account ─────────────────────────────────────
  document.querySelectorAll('[data-acct-2fa-disable]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var email = btn.getAttribute('data-acct-2fa-disable');
      if (!window.confirm('Turn OFF two-factor authentication for ' + email + '?')) return;
      postJSON('/os/vayumail/accounts/totp', { email: email, action: 'disable' }).then(function (res) {
        if (res.ok) { acctToast('Two-factor disabled for ' + email); acctReload(); }
        else acctToast('Update failed: ' + errText(res), true);
      });
    });
  });

  // ── Message actions (Junk / Trash / Restore / Delete) ────────────────────────
  var actions = document.querySelector('[data-mail-actions]');
  if (actions) {
    var user = actions.getAttribute('data-user');
    var folder = actions.getAttribute('data-folder');
    var id = actions.getAttribute('data-id');
    var backURL = actions.getAttribute('data-back') || '/os/vayumail/inbox';
    var nextURL = actions.getAttribute('data-next') || '';
    // After a message leaves the folder (move/junk/trash/delete) continue to the
    // next message if there is one, otherwise fall back to the folder list.
    var advance = function () { window.location.href = nextURL || backURL; };

    // Move / Junk / Trash / Restore buttons.
    actions.querySelectorAll('[data-mail-move]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        var target = btn.getAttribute('data-mail-move');
        btn.disabled = true;
        postJSON('/os/vayumail/message/action', { user: user, id: id, folder: folder, to: target }).then(function (res) {
          if (res.ok) { acctToast('Moved to ' + target); advance(); }
          else { btn.disabled = false; acctToast('Move failed: ' + errText(res), true); }
        });
      });
    });

    // Mark unread → drop the Seen flag and return to the folder (Gmail-style).
    actions.querySelectorAll('[data-mail-mark]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        var mark = btn.getAttribute('data-mail-mark');
        postJSON('/os/vayumail/message/action', { user: user, id: id, folder: folder, mark: mark }).then(function (res) {
          if (res.ok) { acctToast(mark === 'unread' ? 'Marked unread' : 'Marked read'); window.location.href = backURL; }
          else acctToast('Mark failed: ' + errText(res), true);
        });
      });
    });

    // Pin / unpin — flips in place (stay on the message).
    actions.querySelectorAll('[data-mail-pin]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        var pin = btn.getAttribute('data-mail-pin') === '1';
        postJSON('/os/vayumail/message/action', { user: user, id: id, folder: folder, pin: pin }).then(function (res) {
          if (res.ok) {
            acctToast(pin ? 'Pinned' : 'Unpinned');
            btn.setAttribute('data-mail-pin', pin ? '0' : '1');
            btn.textContent = pin ? '📌 Unpin' : '📌 Pin';
          } else acctToast('Pin failed: ' + errText(res), true);
        });
      });
    });

    // Move-to-folder picker.
    var moveSel = actions.querySelector('[data-mail-move-select]');
    if (moveSel) {
      moveSel.addEventListener('change', function () {
        var target = moveSel.value;
        if (!target) return;
        postJSON('/os/vayumail/message/action', { user: user, id: id, folder: folder, to: target }).then(function (res) {
          if (res.ok) { acctToast('Moved to ' + target); advance(); }
          else { moveSel.value = ''; acctToast('Move failed: ' + errText(res), true); }
        });
      });
    }

    // Delete permanently.
    var del = actions.querySelector('[data-mail-delete]');
    if (del) {
      del.addEventListener('click', function () {
        if (!window.confirm('Permanently delete this message?')) return;
        del.disabled = true;
        postJSON('/os/vayumail/message/action', { user: user, id: id, folder: folder, delete: true }).then(function (res) {
          if (res.ok) { acctToast('Deleted'); advance(); }
          else { del.disabled = false; acctToast('Delete failed: ' + errText(res), true); }
        });
      });
    }

    // Print the message.
    var printBtn = actions.querySelector('[data-mail-print]');
    if (printBtn) { printBtn.addEventListener('click', function () { window.print(); }); }
  }
  // ── Mailbox list: selection (event-delegated, survives HTMX swaps) ───────────
  // Row and bulk actions are pure HTMX: they POST to /os/vayumail/inbox/action
  // and swap #vm-inbox-body in place. This module only drives the selection
  // affordance — the "N selected" count, the select-all box, and showing or
  // hiding the bulk bar. It listens on document so it keeps working after the
  // inbox fragment is swapped (the once-loaded script never re-runs).
  (function () {
    function boxes() { return Array.prototype.slice.call(document.querySelectorAll('[data-vm-check]')); }
    function sync() {
      var list = boxes();
      var n = list.filter(function (c) { return c.checked; }).length;
      var count = document.querySelector('[data-vm-bulkcount]');
      var bar = document.querySelector('[data-vm-bulkbar]');
      var all = document.querySelector('[data-vm-check-all]');
      if (count) count.textContent = n + ' selected';
      if (bar) { if (n > 0) bar.removeAttribute('hidden'); else bar.setAttribute('hidden', ''); }
      if (all) all.checked = list.length > 0 && n === list.length;
    }
    document.addEventListener('change', function (e) {
      var t = e.target;
      if (!t || !t.matches) return;
      if (t.matches('[data-vm-check-all]')) {
        var on = t.checked;
        boxes().forEach(function (c) { c.checked = on; });
        sync();
      } else if (t.matches('[data-vm-check]')) {
        sync();
      }
    });
    // Each inbox swap replaces the rows (selection resets) — re-sync the bar.
    document.body.addEventListener('htmx:afterSwap', function (e) {
      if (e.target && e.target.id === 'vm-inbox-body') sync();
    });
    sync();
  })();

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
