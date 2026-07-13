/* admin-os-mail.js — VayuMail panel interactions (compose, accounts, message
 * actions). CSRF: reads the vp_csrf cookie and sends it as X-CSRF-Token, matching
 * the double-submit middleware. No inline handlers (CSP-safe). */
(function () {
  'use strict';

  // Post-action navigation targets are built inline from encodeURIComponent-
  // escaped components with a literal path prefix (see the actions block below),
  // so there is no full-URL-from-DOM navigation helper — the previous
  // localPath/safeNav guards are no longer needed.

  // Split reading pane: highlight the row whose message is open in the pane.
  // Delegated on document so it keeps working after the list is swapped by HTMX.
  document.addEventListener('click', function (e) {
    var link = e.target && e.target.closest ? e.target.closest('[data-vm-open]') : null;
    if (!link) return;
    document.querySelectorAll('tr.vm-active').forEach(function (r) { r.classList.remove('vm-active'); });
    var row = link.closest('[data-vm-row]');
    if (row) row.classList.add('vm-active');
  });

  // When a message lands in the split reading pane, bring it into view. On
  // narrow screens the pane renders above the list (CSS order:-1), so without
  // this the viewport can stay parked mid-list and tapping a mail looks like
  // nothing happened. Delegated on document so it survives HTMX swaps.
  document.addEventListener('htmx:afterSwap', function (e) {
    var pane = document.getElementById('vm-readpane');
    if (!pane || !e.target || e.target !== pane) return;
    var reader = pane.querySelector('.vm-reader');
    if (!reader) { pane.classList.remove('vm-readpane--full'); return; }
    if (typeof reader.scrollIntoView === 'function') {
      reader.scrollIntoView({ behavior: 'smooth', block: 'start' });
    }
  });

  // Reading-pane comfort controls: ⛶ toggles a full-screen overlay for the
  // open message (ESC collapses), 🖨 prints just the reader (see the
  // @media print rules). Delegated so they survive HTMX swaps.
  document.addEventListener('click', function (e) {
    if (e.target && e.target.closest && e.target.closest('[data-vm-print]')) {
      window.print();
      return;
    }
    var ex = e.target && e.target.closest ? e.target.closest('[data-vm-expand]') : null;
    if (ex) {
      var pane = document.getElementById('vm-readpane');
      if (pane) pane.classList.toggle('vm-readpane--full');
    }
  });
  document.addEventListener('keydown', function (e) {
    if (e.key !== 'Escape') return;
    var pane = document.getElementById('vm-readpane');
    if (pane) pane.classList.remove('vm-readpane--full');
  });

  // Conversation threading: the count badge on a thread's newest message
  // toggles its older (hidden) rows. Delegated, so it survives HTMX swaps.
  document.addEventListener('click', function (e) {
    var btn = e.target && e.target.closest ? e.target.closest('[data-vm-thread-toggle]') : null;
    if (!btn) return;
    e.preventDefault();
    var key = btn.getAttribute('data-vm-thread-toggle');
    var open = btn.classList.toggle('vm-thread-count--open');
    // Match by comparing the attribute value directly instead of building a
    // selector from a DOM-derived string: no selector injection is possible and
    // nothing DOM-sourced is interpolated into a query.
    document.querySelectorAll('[data-vm-thread]').forEach(function (r) {
      if (r.getAttribute('data-vm-thread') !== key) return;
      if (open) r.removeAttribute('hidden'); else r.setAttribute('hidden', '');
    });
  });

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

    // ── Signature ────────────────────────────────────────────────────────────
    // Each From <option> carries its account's signature in data-sig. The editor
    // saves it (self-service for your own mailbox; admins for the selected one),
    // and the "Append signature" toggle controls whether it is added on send.
    var fromSel = compose.querySelector('[data-c-from]');
    var sigToggle = compose.querySelector('[data-c-sig-toggle]');
    var sigText = compose.querySelector('[data-c-sig-text]');
    var sigPreview = compose.querySelector('[data-c-sig-preview]');
    var sigSave = compose.querySelector('[data-c-sig-save]');
    var sigStatus = compose.querySelector('[data-c-sig-status]');
    function currentSig() {
      if (!fromSel || fromSel.selectedIndex < 0) return '';
      var opt = fromSel.options[fromSel.selectedIndex];
      return opt ? (opt.getAttribute('data-sig') || '') : '';
    }
    function paintSig() {
      var s = currentSig();
      if (sigText && document.activeElement !== sigText) sigText.value = s;
      if (sigPreview) sigPreview.textContent = s || '(no signature set for this address)';
    }
    function sigAppend() { return !(sigToggle && !sigToggle.checked); }
    if (fromSel) fromSel.addEventListener('change', paintSig);
    if (sigSave) {
      sigSave.addEventListener('click', function () {
        if (!fromSel) return;
        var email = fromSel.value, text = sigText ? sigText.value : '';
        sigSave.disabled = true;
        if (sigStatus) sigStatus.textContent = 'Saving…';
        postJSON('/os/vayumail/accounts/update', { email: email, signature: text }).then(function (res) {
          sigSave.disabled = false;
          if (res.ok) {
            var opt = fromSel.options[fromSel.selectedIndex];
            if (opt) opt.setAttribute('data-sig', text);
            paintSig();
            if (sigStatus) sigStatus.textContent = 'Saved ✓';
            acctToast('Signature saved');
          } else {
            if (sigStatus) sigStatus.textContent = 'Failed: ' + errText(res);
            acctToast('Signature save failed: ' + errText(res), true);
          }
        });
      });
    }
    paintSig();

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

    // ── Undo-send ──────────────────────────────────────────────────────────
    // Clicking Send holds the message behind an Undo control for a few seconds
    // rather than dispatching immediately. If the operator leaves during the
    // hold, a beforeunload handler fires the send (keepalive) so nothing is
    // lost. A "sending" guard prevents a double send.
    var HOLD_MS = 8000;
    var holdTimer = null, countdownTimer = null, holdSnapshot = null, sending = false;
    var undoBar = compose.querySelector('[data-c-undobar]');
    var undoText = compose.querySelector('[data-c-undo-text]');
    var undoBtn = compose.querySelector('[data-c-undo]');

    function clearHoldTimers() {
      if (holdTimer) { clearTimeout(holdTimer); holdTimer = null; }
      if (countdownTimer) { clearInterval(countdownTimer); countdownTimer = null; }
    }
    function sendDone(res) {
      if (sendBtn) sendBtn.disabled = false;
      if (res.ok) {
        stopAutosave();
        if (holdSnapshot && holdSnapshot.draftId) {
          postJSON('/os/vayumail/message/action', { user: localPart(holdSnapshot.from), folder: 'Drafts', id: holdSnapshot.draftId, delete: true });
        }
        if (cStatus) cStatus.textContent = 'Sent ✓';
        acctToast('Message sent');
        setTimeout(function () { window.location.href = '/os/vayumail/sent'; }, 650);
      } else {
        sending = false;
        holdSnapshot = null;
        if (cStatus) cStatus.textContent = 'Failed: ' + errText(res);
        acctToast('Send failed: ' + errText(res), true);
      }
    }
    function performSend(keepalive) {
      if (sending || !holdSnapshot) return;
      sending = true;
      clearHoldTimers();
      if (undoBar) undoBar.setAttribute('hidden', '');
      var snap = holdSnapshot;
      var opts = { method: 'POST', keepalive: !!keepalive, headers: { 'X-CSRF-Token': cookie('vp_csrf') } };
      if (snap.files.length > 0) {
        var fd = new FormData();
        Object.keys(snap.fields).forEach(function (k) { fd.append(k, snap.fields[k] || ''); });
        snap.files.forEach(function (file) { fd.append('attachments', file); });
        fd.append('appendSig', snap.appendSig ? '1' : '0');
        opts.body = fd;
      } else {
        var payload = {};
        Object.keys(snap.fields).forEach(function (k) { payload[k] = snap.fields[k]; });
        payload.appendSig = snap.appendSig;
        opts.headers['Content-Type'] = 'application/json';
        opts.body = JSON.stringify(payload);
      }
      fetch('/os/vayumail/send', opts)
        .then(function (r) { return r.json().catch(function () { return {}; }).then(function (b) { return { ok: r.ok, status: r.status, body: b }; }); })
        .then(sendDone)
        .catch(function () { sending = false; });
    }
    function cancelHold() {
      clearHoldTimers();
      holdSnapshot = null;
      if (undoBar) undoBar.setAttribute('hidden', '');
      if (sendBtn) sendBtn.disabled = false;
      if (cStatus) cStatus.textContent = 'Cancelled — back to your draft.';
      if (!autosaveTimer) autosaveTimer = setInterval(function () { saveDraft(true); }, 20000);
    }
    if (undoBtn) undoBtn.addEventListener('click', cancelHold);

    compose.addEventListener('submit', function (e) {
      e.preventDefault();
      if (sending || holdSnapshot) return; // a send is already in flight/held
      var f = composeFields();
      if (!f.to && !f.cc && !f.bcc) { if (cStatus) cStatus.textContent = 'Add at least one recipient.'; return; }
      // Snapshot at click time so edits during the hold don't leak into the send.
      holdSnapshot = { fields: f, files: composeFiles.slice(), appendSig: sigAppend(), from: f.from, draftId: draftId };
      stopAutosave();
      if (sendBtn) sendBtn.disabled = true;
      if (cStatus) cStatus.textContent = '';
      var remaining = Math.ceil(HOLD_MS / 1000);
      if (undoText) undoText.textContent = 'Sending in ' + remaining + 's…';
      if (undoBar) undoBar.removeAttribute('hidden');
      clearHoldTimers();
      countdownTimer = setInterval(function () {
        remaining -= 1;
        if (undoText) undoText.textContent = remaining > 0 ? ('Sending in ' + remaining + 's…') : 'Sending…';
      }, 1000);
      holdTimer = setTimeout(function () { performSend(false); }, HOLD_MS);
    });

    // If the operator navigates away while a send is held, fire it best-effort.
    window.addEventListener('beforeunload', function () {
      if (holdSnapshot && !sending) { performSend(true); }
    });
  }

  // Enterprise feedback: a non-blocking toast (from the admin shell) instead of
  // a blocking alert() dialog. Falls back to alert() only if the shell toast is
  // somehow unavailable.
  function acctToast(msg, isErr) {
    if (window.vpToast) { window.vpToast(msg, isErr ? 'error' : 'success'); }
    else { window.alert(msg); }
  }
  // A state change (create / 2FA / password) refreshes the collapsible account
  // list in place via HTMX — no full-page reload. Falls back to a hard reload
  // only if htmx somehow isn't present.
  function acctReload() {
    if (window.htmx && document.getElementById('vm-accounts-list')) {
      window.htmx.ajax('GET', '/os/vayumail/accounts/fragment', { target: '#vm-accounts-list', swap: 'innerHTML' });
    } else {
      setTimeout(function () { window.location.reload(); }, 550);
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
      postJSON('/os/vayumail/accounts/create', {
        local: local, name: val(acctForm, '[data-a-name]'), pass: pass,
        role: val(acctForm, '[data-a-role]'),
        domain: val(acctForm, '[data-a-domain]'),
        quota_mb: parseFloat(val(acctForm, '[data-a-quota]')) || 0,
      }).then(function (res) {
        if (res.ok) {
          acctToast('Mailbox ' + local + ' created');
          if (aStatus) aStatus.textContent = '';
          acctForm.querySelectorAll('[data-a-local],[data-a-name],[data-a-pass]').forEach(function (el) { el.value = ''; });
          acctReload();
        } else if (aStatus) aStatus.textContent = 'Failed: ' + errText(res);
      });
    });
  }

  // Inline mailbox actions (enable/disable, role, quota, retention, delete) are
  // now HTMX: each posts /os/vayumail/accounts/action and the server swaps the
  // #vm-accounts-list fragment in place — no per-element JS, no page reload.
  //
  // The prompt-driven controls (set password, enable/disable 2FA) live INSIDE that
  // swappable list, so binding them per-element would break after the first swap.
  // A single delegated listener on document survives every swap.
  document.addEventListener('click', function (e) {
    if (!e.target || !e.target.closest) return;

    var pw = e.target.closest('[data-acct-pass]');
    if (pw) {
      var pemail = pw.getAttribute('data-acct-pass');
      var pass = window.prompt('New password for ' + pemail + ' (min 8 characters):');
      if (pass === null) return;
      if (pass.length < 8) { acctToast('Password must be at least 8 characters.', true); return; }
      postJSON('/os/vayumail/accounts/update', { email: pemail, pass: pass }).then(function (res) {
        acctToast(res.ok ? 'Password updated for ' + pemail : 'Update failed: ' + errText(res), !res.ok);
      });
      return;
    }

    var en = e.target.closest('[data-acct-2fa-enable]');
    if (en) {
      var eemail = en.getAttribute('data-acct-2fa-enable');
      postJSON('/os/vayumail/accounts/totp', { email: eemail, action: 'begin' }).then(function (res) {
        if (!res.ok || !res.body || !res.body.secret) { acctToast('Could not start 2FA setup: ' + errText(res), true); return; }
        window.prompt(
          'Add this account to an authenticator app, then enter the 6-digit code below.\n\n' +
          'Secret key:\n' + res.body.secret + '\n\notpauth URI (copyable):',
          res.body.uri || ''
        );
        var code = window.prompt('Enter the current 6-digit code from your authenticator for ' + eemail + ':');
        if (code === null) return;
        postJSON('/os/vayumail/accounts/totp', { email: eemail, action: 'verify', code: (code || '').trim() }).then(function (vr) {
          if (vr.ok) { acctToast('Two-factor authentication is now ON for ' + eemail); acctReload(); }
          else acctToast('Verification failed: ' + errText(vr), true);
        });
      });
      return;
    }

    var dis = e.target.closest('[data-acct-2fa-disable]');
    if (dis) {
      var demail = dis.getAttribute('data-acct-2fa-disable');
      if (!window.confirm('Turn OFF two-factor authentication for ' + demail + '?')) return;
      postJSON('/os/vayumail/accounts/totp', { email: demail, action: 'disable' }).then(function (res) {
        if (res.ok) { acctToast('Two-factor disabled for ' + demail); acctReload(); }
        else acctToast('Update failed: ' + errText(res), true);
      });
      return;
    }
  });

  // ── Message actions (Junk / Trash / Restore / Delete) ────────────────────────
  var actions = document.querySelector('[data-mail-actions]');
  if (actions) {
    var user = actions.getAttribute('data-user');
    var folder = actions.getAttribute('data-folder');
    var id = actions.getAttribute('data-id');
    // Build navigation targets from individual components with a LITERAL path
    // prefix and encodeURIComponent-escaped values — never a full URL read from
    // a DOM attribute. encodeURIComponent is a recognised sanitiser, so no
    // DOM-derived text can reach location unescaped, and the resulting URLs are
    // identical to the ones the server used to hand over.
    var backURL = '/os/vayumail/inbox?user=' + encodeURIComponent(user) + '&folder=' + encodeURIComponent(folder);
    var nextId = actions.getAttribute('data-next-id') || '';
    var nextURL = nextId
      ? '/os/vayumail/message?user=' + encodeURIComponent(user) + '&folder=' + encodeURIComponent(folder) + '&id=' + encodeURIComponent(nextId)
      : '';
    // After a message leaves the folder (move/junk/trash/delete) continue to the
    // next message if there is one, otherwise fall back to the folder list.
    var advance = function () { window.location.assign(nextURL || backURL); };

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
          if (res.ok) { acctToast(mark === 'unread' ? 'Marked unread' : 'Marked read'); window.location.assign(backURL); }
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

  // ── Keyboard shortcuts + help overlay ────────────────────────────────────────
  // Gmail/Superhuman-style single-key navigation. Shortcuts are ignored while
  // typing in a field. "?" opens a help overlay (native popover when available,
  // a positioned fallback otherwise). All CSP-safe — no inline handlers/styles.
  (function () {
    function typing() {
      var el = document.activeElement;
      return el && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.tagName === 'SELECT' || el.isContentEditable);
    }
    var help = null;
    function buildHelp() {
      if (help) return help;
      help = document.createElement('div');
      help.className = 'vm-help';
      help.setAttribute('role', 'dialog');
      help.setAttribute('aria-label', 'Keyboard shortcuts');
      try { help.setAttribute('popover', 'auto'); } catch (e) { /* older browser */ }
      var pairs = [
        ['Compose', 'c'], ['Search / focus search', '/'], ['Open message', 'Enter · o'],
        ['Move down · up', 'j · k'], ['Select', 'x'], ['Toggle read', 'u'], ['Pin', 's'],
        ['Archive', 'e'], ['Junk', '!'], ['Delete', '#'],
        ['Reply · Forward (reader)', 'r · f'], ['Next · prev (reader)', 'j · k'], ['This help', '?']
      ];
      var h = '<div class="vm-help-title">Keyboard shortcuts</div><div class="vm-help-grid">';
      pairs.forEach(function (p) { h += '<span></span><span class="vm-kbd"></span>'; });
      help.innerHTML = h;
      var spans = help.querySelectorAll('.vm-help-grid > span');
      pairs.forEach(function (p, i) { spans[i * 2].textContent = p[0]; spans[i * 2 + 1].textContent = p[1]; });
      var foot = document.createElement('div');
      foot.className = 'vm-help-foot muted text-xs';
      foot.textContent = 'Press Esc or ? to close';
      help.appendChild(foot);
      (document.querySelector('.vp-os') || document.body).appendChild(help);
      return help;
    }
    function helpOpen() { return help && ((help.matches && help.matches(':popover-open')) || help.classList.contains('vm-help--open')); }
    function toggleHelp() {
      var el = buildHelp();
      if (helpOpen()) { if (el.hidePopover) { try { el.hidePopover(); return; } catch (e) {} } el.classList.remove('vm-help--open'); return; }
      if (typeof el.showPopover === 'function') { try { el.showPopover(); return; } catch (e) {} }
      el.classList.add('vm-help--open');
    }

    var focusIdx = -1;
    function rows() { return Array.prototype.slice.call(document.querySelectorAll('#vm-inbox-body tbody tr.vm-row-item')); }
    function paint() {
      var rs = rows();
      rs.forEach(function (r, i) { if (i === focusIdx) r.classList.add('vm-focus'); else r.classList.remove('vm-focus'); });
      if (focusIdx >= 0 && rs[focusIdx]) rs[focusIdx].scrollIntoView({ block: 'nearest' });
    }
    function move(delta) {
      var rs = rows(); if (!rs.length) return;
      focusIdx = focusIdx < 0 ? 0 : focusIdx + delta;
      if (focusIdx < 0) focusIdx = 0;
      if (focusIdx > rs.length - 1) focusIdx = rs.length - 1;
      paint();
    }
    function focusedRow() { var rs = rows(); return focusIdx >= 0 ? rs[focusIdx] : null; }
    function clickIn(root, sel) { if (!root) return; var el = root.querySelector(sel); if (el) el.click(); }
    function selectFocused() {
      var row = focusedRow(); if (!row) return;
      var cb = row.querySelector('[data-vm-check]');
      if (cb && !cb.checked) { cb.checked = true; cb.dispatchEvent(new Event('change', { bubbles: true })); }
    }
    function bulkDelete() {
      var bar = document.querySelector('[data-vm-bulkbar]'); if (!bar) return;
      var btns = bar.querySelectorAll('button[hx-vals]');
      for (var i = 0; i < btns.length; i++) {
        if ((btns[i].getAttribute('hx-vals') || '').indexOf('"delete"') >= 0) { btns[i].click(); return; }
      }
    }
    function bulkMove(folder) {
      var sel = document.querySelector('[data-vm-bulkbar] select[name="to"]');
      if (sel) { sel.value = folder; sel.dispatchEvent(new Event('change', { bubbles: true })); }
    }
    function inboxAction(kind) {
      var row = focusedRow(); if (!row) return;
      if (kind === 'read') { clickIn(row, '.row-actions button'); return; }
      if (kind === 'pin') { clickIn(row, 'button[title="Pin"]'); return; }
      selectFocused();
      if (kind === 'delete') { bulkDelete(); return; }
      bulkMove(kind === 'archive' ? 'Archive' : 'Junk');
    }

    document.addEventListener('keydown', function (e) {
      if (e.altKey || e.ctrlKey || e.metaKey) return;
      if (e.key === 'Escape' && helpOpen()) { toggleHelp(); return; }
      if (typing()) return;
      if (e.key === '?') { e.preventDefault(); toggleHelp(); return; }
      if (e.key === 'c') { window.location.href = '/os/vayumail/compose'; return; }
      if (e.key === '/') {
        var s = document.querySelector('input[type="search"][name="q"]');
        if (s) { e.preventDefault(); s.focus(); } else { window.location.href = '/os/vayumail/search'; }
        return;
      }
      var reader = document.querySelector('[data-mail-actions]');
      if (reader) {
        switch (e.key) {
          case 'r': clickIn(reader, 'a[href*="reply=1"]'); return;
          case 'f': clickIn(reader, 'a[href*="forward=1"]'); return;
          case 'u': clickIn(reader, '[data-mail-mark="unread"]'); return;
          case '#': clickIn(reader, '[data-mail-delete]'); return;
          case 'e': { var ms = reader.querySelector('[data-mail-move-select]'); if (ms) { ms.value = 'Archive'; ms.dispatchEvent(new Event('change', { bubbles: true })); } return; }
          case 'j': clickIn(document, '.vm-reader-nav a[title="Next message"]'); return;
          case 'k': clickIn(document, '.vm-reader-nav a[title="Previous message"]'); return;
        }
        return;
      }
      if (document.getElementById('vm-inbox-body')) {
        switch (e.key) {
          case 'j': e.preventDefault(); move(1); return;
          case 'k': e.preventDefault(); move(-1); return;
          case 'o': case 'Enter': clickIn(focusedRow(), '.vm-subj a'); return;
          case 'x': e.preventDefault(); selectFocused(); return;
          case 'u': inboxAction('read'); return;
          case 's': inboxAction('pin'); return;
          case 'e': inboxAction('archive'); return;
          case '!': inboxAction('junk'); return;
          case '#': inboxAction('delete'); return;
        }
      }
    });
    document.body.addEventListener('htmx:afterSwap', function (e) {
      if (e.target && e.target.id === 'vm-inbox-body') focusIdx = -1;
    });
  })();
})();
