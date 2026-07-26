/*
 * admin-os-mail-recovery.js — enrolment controls for VayuMail account recovery.
 *
 * Strict CSP: no eval, nothing from the server is ever assigned as markup. Every
 * value goes in through textContent, because this panel renders mail addresses
 * and server error strings.
 *
 * The recovery codes are shown exactly once, in the response to the generate
 * call — the server keeps only Argon2id hashes and cannot show them again. So
 * the one job that matters here is making sure they are not lost between the
 * response arriving and the operator writing them down.
 */
(function () {
  'use strict';

  var panel = document.querySelector('[data-recovery-panel]');
  if (!panel) { return; }

  var mailbox = panel.querySelector('[data-rec-mailbox]');
  var statusEl = panel.querySelector('[data-rec-status]');
  var codesEl = panel.querySelector('[data-rec-codes]');
  var contactEl = panel.querySelector('[data-rec-contact]');
  var msgEl = panel.querySelector('[data-rec-msg]');

  function csrf() {
    var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }

  function setMsg(text, isErr) {
    if (!msgEl) { return; }
    msgEl.textContent = text || '';
    msgEl.classList.toggle('is-error', !!isErr);
  }

  function post(url, payload) {
    return fetch(url, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() },
      body: JSON.stringify(payload)
    }).then(function (r) {
      return r.text().then(function (t) {
        var d = {};
        try { d = t ? JSON.parse(t) : {}; } catch (e) { d = {}; }
        return { ok: r.ok, d: d };
      });
    });
  }

  function errText(res, fallback) {
    var d = (res && res.d) || {};
    if (d.error && d.error.message) { return d.error.message; }
    if (d.message) { return d.message; }
    return fallback;
  }

  function current() { return (mailbox && mailbox.value) || ''; }

  // renderStatus states plainly whether this mailbox could actually be recovered.
  // "Ready" is reserved for a CONFIRMED factor: a pending address is not one.
  function renderStatus(st) {
    if (!statusEl) { return; }
    statusEl.textContent = '';
    if (!st) { return; }

    var line = document.createElement('div');
    var parts = [];
    if (st.codes_remaining > 0) {
      parts.push(st.codes_remaining + ' unused recovery code' + (st.codes_remaining === 1 ? '' : 's'));
    }
    if (st.contact) { parts.push('verified address ' + st.contact); }
    if (st.contact_pending) { parts.push('address ' + st.contact_pending + ' awaiting verification'); }

    if (st.ready) {
      line.textContent = '✓ Can be recovered — ' + parts.join('; ') + '.';
    } else if (parts.length) {
      line.textContent = '✕ Cannot be recovered yet — ' + parts.join('; ') +
        '. An address only counts once it is verified.';
    } else {
      line.textContent = '✕ Cannot be recovered — nothing is enrolled. If this holder forgets their ' +
        'password, only a server operator with shell access can help.';
    }
    statusEl.appendChild(line);

    if (contactEl) { contactEl.value = st.contact || st.contact_pending || ''; }
    // NOTE: this must NOT clear the codes. renderStatus also runs immediately
    // after a successful generate (to refresh the remaining count), so clearing
    // here wiped the one and only display of the codes a moment after showing
    // them — the panel looked like the button did nothing. Clearing belongs to
    // the mailbox-change handler, which is the case it was written for.
  }

  // clearCodes drops any codes on screen. Bound to mailbox selection: a set left
  // over from the previous mailbox belongs to a different account and would be
  // written down against the wrong one.
  function clearCodes() {
    if (codesEl) { codesEl.textContent = ''; codesEl.hidden = true; }
  }

  function loadStatus() {
    var m = current();
    if (!m) { return; }
    fetch('/os/api/vayuos/mail/recovery/status?email=' + encodeURIComponent(m),
      { credentials: 'same-origin', cache: 'no-store' })
      .then(function (r) { return r.json(); })
      .then(renderStatus)
      .catch(function () { setMsg('Could not load recovery status.', true); });
  }

  // showCodes renders the one and only time these exist in readable form.
  function showCodes(codes) {
    if (!codesEl) { return; }
    codesEl.textContent = '';

    var warn = document.createElement('p');
    warn.className = 'rec-codes__warn';
    warn.textContent = 'Save these now — they cannot be shown again. Each code works once. ' +
      'Any previous codes for this mailbox have stopped working.';
    codesEl.appendChild(warn);

    var grid = document.createElement('div');
    grid.className = 'rec-codes__grid';
    codes.forEach(function (c) {
      var cell = document.createElement('code');
      cell.className = 'rec-codes__code';
      cell.textContent = c;
      grid.appendChild(cell);
    });
    codesEl.appendChild(grid);

    var copy = document.createElement('button');
    copy.type = 'button';
    copy.className = 'btn btn--sm btn--ghost';
    copy.textContent = 'Copy all';
    copy.addEventListener('click', function () {
      var text = 'VayuMail recovery codes for ' + current() + '\n\n' + codes.join('\n') +
        '\n\nEach code can be used once. Keep them somewhere you can reach without this mailbox.\n';
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(function () {
          copy.textContent = 'Copied';
        }).catch(function () { copy.textContent = 'Copy failed — select the codes manually'; });
      } else {
        copy.textContent = 'Select the codes above and copy them manually';
      }
    });
    codesEl.appendChild(copy);
    codesEl.hidden = false;
  }

  if (mailbox) {
    mailbox.addEventListener('change', function () { setMsg(''); clearCodes(); loadStatus(); });
  }

  var genBtn = panel.querySelector('[data-rec-gen]');
  if (genBtn) {
    genBtn.addEventListener('click', function () {
      var m = current();
      if (!m) { return; }
      // Regeneration destroys the previous set, so it is worth one confirmation:
      // an operator who does this by accident has just invalidated the sheet the
      // holder is carrying.
      if (!window.confirm('Generate new codes for ' + m +
        '?\n\nAny codes already given to this holder will stop working.')) { return; }
      genBtn.disabled = true;
      setMsg('Generating…', false);
      post('/os/api/vayuos/mail/recovery/codes', { email: m }).then(function (res) {
        if (!res.ok) { setMsg(errText(res, 'Could not generate codes.'), true); return; }
        showCodes(res.d.codes || []);
        setMsg('', false);
        loadStatus();
      }).catch(function () {
        setMsg('Could not reach the server.', true);
      }).finally(function () { genBtn.disabled = false; });
    });
  }

  function contactAction(action, needValue) {
    var m = current();
    if (!m) { return; }
    var payload = { email: m, action: action };
    if (needValue) { payload.contact = (contactEl && contactEl.value) || ''; }
    setMsg('Saving…', false);
    post('/os/api/vayuos/mail/recovery/contact', payload).then(function (res) {
      if (!res.ok) { setMsg(errText(res, 'That did not work.'), true); return; }
      renderStatus(res.d);
      setMsg(action === 'verify' ? 'Address verified — it is now a working recovery factor.'
        : action === 'clear' ? 'Recovery address removed.'
          : 'Saved. It does not count as recovery until you mark it verified.', false);
    }).catch(function () { setMsg('Could not reach the server.', true); });
  }

  var setBtn = panel.querySelector('[data-rec-set]');
  if (setBtn) { setBtn.addEventListener('click', function () { contactAction('set', true); }); }
  var verifyBtn = panel.querySelector('[data-rec-verify]');
  if (verifyBtn) { verifyBtn.addEventListener('click', function () { contactAction('verify', false); }); }
  var clearBtn = panel.querySelector('[data-rec-clear]');
  if (clearBtn) {
    clearBtn.addEventListener('click', function () {
      if (!window.confirm('Remove the recovery address for ' + current() + '?')) { return; }
      contactAction('clear', false);
    });
  }

  loadStatus();
})();
