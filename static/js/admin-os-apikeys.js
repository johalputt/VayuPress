/* SPDX-License-Identifier: Apache-2.0 */
/* The API keys page (/os/apikeys): issuing, rotating and retiring keys, and
   the credentials VayuPress keeps for other services. Its words go through
   vpT, so the page says them in its own language. */
(function () {
  'use strict';
  function csrf() { var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/); return m ? decodeURIComponent(m[1]) : ''; }

  var akStatus = document.getElementById('ak-status');
  // A message about something done in a sheet is said in that sheet: the page's
  // own status line is behind the sheet while it is open.
  function akSet(t, isErr, from) {
    var d = from && from.closest('dialog');
    var s = (d && d.querySelector('[data-sheet-status]')) || akStatus;
    if (s) { s.textContent = t; s.style.color = isErr ? 'var(--danger)' : 'var(--ok)'; }
  }
  function jpost(url, payload) {
    return fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() }, body: JSON.stringify(payload || {}) })
      .then(function (r) { return r.json().catch(function () { return {}; }).then(function (d) { return { ok: r.ok, d: d || {} }; }); });
  }
  // refusal is what the server said when it refused: a refusal written as an
  // error carries its words in error.message, and was shown as "Error".
  function refusal(res) {
    var d = res.d || {};
    return d.detail || (d.error && d.error.message) || d.title || vpT('That did not go through.');
  }
  function failed(e) { return vpT('Request failed: {error}', { error: String(e) }); }

  // ── Token reveal banner (shown once after create/rotate) ──
  var banner = document.getElementById('ak-token-banner');
  var tokenVal = document.getElementById('ak-token-value');
  var copyBtn = document.getElementById('ak-token-copy');
  var doneBtn = document.getElementById('ak-token-done');
  // The key is shown once, so the sheet it was made in closes first: left open,
  // it would cover the only copy of the key.
  function showToken(tok) {
    if (!banner) return;
    var d = banner.ownerDocument.querySelector('dialog.sa-sheet[open]');
    if (d) d.close();
    tokenVal.value = tok; banner.hidden = false; banner.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }
  if (copyBtn) copyBtn.addEventListener('click', function () {
    tokenVal.select();
    try { document.execCommand('copy'); } catch (e) { /* the clipboard API below is the other way */ }
    if (navigator.clipboard) navigator.clipboard.writeText(tokenVal.value);
    akSet(vpT('Copied to clipboard'), false);
  });
  if (doneBtn) doneBtn.addEventListener('click', function () { location.reload(); });

  // ── Permission grid wiring (row "all", grand superuser) ──
  var grid = document.getElementById('ak-perm-grid');
  var superBox = document.getElementById('ak-perm-super');
  function permBoxes() { return grid ? grid.querySelectorAll('.ak-perm') : []; }
  function rowBox(section) { return grid ? grid.querySelector('.ak-perm-all[data-section="' + section + '"]') : null; }
  function rowCells(section) { return grid ? grid.querySelectorAll('.ak-perm[data-section="' + section + '"]') : []; }
  function syncRow(section) {
    var r = rowBox(section); if (!r) return;
    var cells = rowCells(section), all = cells.length > 0;
    cells.forEach(function (c) { if (!c.checked) all = false; });
    r.checked = all;
  }
  if (grid) grid.addEventListener('change', function (ev) {
    var t = ev.target;
    if (t.classList.contains('ak-perm-all')) { var sec = t.getAttribute('data-section'); rowCells(sec).forEach(function (c) { c.checked = t.checked; }); }
    else if (t.classList.contains('ak-perm')) { syncRow(t.getAttribute('data-section')); }
  });
  if (superBox) superBox.addEventListener('change', function () {
    // Full access dims the grid — grants are then implicit.
    if (grid) grid.classList.toggle('ak-grid--disabled', superBox.checked);
  });
  function collectCapabilities() {
    if (superBox && superBox.checked) return ['*:*'];
    var caps = [];
    permBoxes().forEach(function (c) { if (c.checked) caps.push(c.getAttribute('data-section') + ':' + c.getAttribute('data-action')); });
    return caps;
  }

  // ── Create / rotate / activate / deactivate / revoke / delete own keys ──
  var createBtn = document.getElementById('ak-create-btn');
  if (createBtn) createBtn.addEventListener('click', function () {
    var label = (document.getElementById('ak-new-label') || {}).value || '';
    var expiry = (document.getElementById('ak-new-expiry') || {}).value || '';
    var rateRaw = (document.getElementById('ak-new-rate') || {}).value || '';
    var rate = parseInt(rateRaw, 10); if (isNaN(rate) || rate < 0) rate = 0;
    if ((document.getElementById('ak-new-rate-unl') || {}).checked) rate = -1;
    var caps = collectCapabilities();
    if (caps.length === 0) { akSet(vpT('Grant at least one permission, or tick Full access.'), true, createBtn); return; }
    createBtn.disabled = true; akSet(vpT('Creating…'), false, createBtn);
    jpost('/os/api/apikeys/create', { label: label, capabilities: caps, expires_at: expiry, rate_per_min: rate }).then(function (res) {
      createBtn.disabled = false;
      if (res.ok) { showToken(res.d.token); akSet(vpT('Key created'), false); } else { akSet(refusal(res), true, createBtn); }
    }).catch(function (e) { createBtn.disabled = false; akSet(failed(e), true, createBtn); });
  });

  document.addEventListener('click', function (ev) {
    var cp = ev.target.closest('[data-copy]');
    if (cp) {
      var el = document.querySelector(cp.getAttribute('data-copy'));
      if (el) {
        var v = el.value !== undefined ? el.value : el.textContent;
        if (navigator.clipboard) navigator.clipboard.writeText(v).then(function () { akSet(vpT('Copied'), false); }, function () { akSet(vpT('Copy failed; select it instead'), true); });
      }
      return;
    }
    var b = ev.target.closest('[data-action]'); if (!b) return;
    var act = b.getAttribute('data-action'); var id = b.getAttribute('data-id');
    var reloadOr = function (res) { if (res.ok) { location.reload(); } else { b.disabled = false; akSet(refusal(res), true, b); } };
    if (act === 'ak-rate' || act === 'ak-rate-default') {
      var box = b.closest('[data-ak-rate]'); var n = parseInt(box.querySelector('input[type=number]').value, 10);
      var r = box.querySelector('input[type=checkbox]').checked ? -1 : (isNaN(n) ? 0 : n);
      var body = { rate_per_min: r }; if (act === 'ak-rate') body.id = id;
      b.disabled = true;
      jpost(act === 'ak-rate' ? '/os/api/apikeys/rate' : '/os/api/apikeys/rate-default', body).then(function (res) {
        b.disabled = false; if (res.ok) { akSet(vpT('Saved'), false, b); } else { akSet(refusal(res), true, b); }
      });
    } else if (act === 'ak-rotate') {
      vpConfirm({ title: vpT('Rotate this key?'), message: vpT('The current value stops working immediately.'), confirm: vpT('Rotate') }, function () {
        b.disabled = true;
        jpost('/os/api/apikeys/rotate', { id: id }).then(function (res) {
          b.disabled = false; if (res.ok) { showToken(res.d.token); akSet(vpT('Key rotated'), false); } else { akSet(refusal(res), true, b); }
        });
      });
    } else if (act === 'ak-activate') {
      b.disabled = true; jpost('/os/api/apikeys/activate', { id: id }).then(reloadOr);
    } else if (act === 'ak-deactivate') {
      vpConfirm({ title: vpT('Deactivate this key?'), message: vpT('It stops authenticating until you activate it again.'), confirm: vpT('Deactivate') }, function () {
        b.disabled = true; jpost('/os/api/apikeys/deactivate', { id: id }).then(reloadOr);
      });
    } else if (act === 'ak-revoke') {
      vpConfirm({ title: vpT('Revoke this key?'), message: vpT('It can no longer authenticate.'), confirm: vpT('Revoke') }, function () {
        b.disabled = true; jpost('/os/api/apikeys/revoke', { id: id }).then(reloadOr);
      });
    } else if (act === 'ak-delete') {
      vpConfirm({ title: vpT('Delete this key permanently?'), confirm: vpT('Delete') }, function () {
        b.disabled = true; jpost('/os/api/apikeys/delete', { id: id }).then(reloadOr);
      });
    } else if (act === 'cred-save') {
      saveCred(b);
    } else if (act === 'cred-reveal') {
      revealCred(b, id);
    } else if (act === 'cred-delete') {
      vpConfirm({ title: vpT('Delete this credential?'), message: vpT('The stored secret is erased.'), confirm: vpT('Delete') }, function () {
        b.disabled = true;
        jpost('/os/api/credentials/delete', { id: id }).then(function (res) {
          if (res.ok) { location.reload(); } else { b.disabled = false; cardStatus(cardOf(b), refusal(res), true); }
        });
      });
    }
  });

  function cardOf(el) { return el.closest('[data-cred-card]'); }
  function cardStatus(card, t, isErr) {
    var s = card.querySelector('[data-cred-status]');
    if (s) { s.textContent = t; s.style.color = isErr ? 'var(--danger)' : 'var(--ok)'; }
  }

  function saveCred(btn) {
    var card = cardOf(btn); if (!card) return;
    var ep = card.querySelector('[data-cred-endpoint]');
    var sec = card.querySelector('[data-cred-secret]');
    var en = card.querySelector('[data-cred-enabled]');
    var payload = { provider: btn.getAttribute('data-provider'), label: btn.getAttribute('data-label') || '', endpoint: ep ? ep.value : '', secret: sec ? sec.value : '', enabled: en ? en.checked : true };
    btn.disabled = true; cardStatus(card, vpT('Saving…'), false);
    jpost('/os/api/credentials/save', payload).then(function (res) {
      btn.disabled = false;
      if (res.ok) { cardStatus(card, vpT('Saved'), false); if (sec) sec.value = ''; setTimeout(function () { location.reload(); }, 600); }
      else { cardStatus(card, refusal(res), true); }
    }).catch(function (e) { btn.disabled = false; cardStatus(card, failed(e), true); });
  }

  function revealCred(btn, id) {
    var card = cardOf(btn); if (!card) return;
    var sec = card.querySelector('[data-cred-secret]'); if (!sec) return;
    btn.disabled = true;
    jpost('/os/api/credentials/reveal', { id: id }).then(function (res) {
      btn.disabled = false;
      if (res.ok) { sec.type = 'text'; sec.value = res.d.secret; btn.textContent = vpT('Hide'); btn.setAttribute('data-action', 'noop'); }
      else { cardStatus(card, refusal(res), true); }
    }).catch(function (e) { btn.disabled = false; cardStatus(card, failed(e), true); });
  }

  // ── Add a custom credential ──
  var ccAdd = document.getElementById('cc-add-btn');
  if (ccAdd) ccAdd.addEventListener('click', function () {
    var label = (document.getElementById('cc-label') || {}).value || '';
    var ep = (document.getElementById('cc-endpoint') || {}).value || '';
    var sec = (document.getElementById('cc-secret') || {}).value || '';
    if (!label.trim()) { akSet(vpT('Give the credential a name'), true, ccAdd); return; }
    ccAdd.disabled = true; akSet(vpT('Saving…'), false, ccAdd);
    jpost('/os/api/credentials/save', { provider: 'custom', label: label, endpoint: ep, secret: sec, enabled: true }).then(function (res) {
      ccAdd.disabled = false; if (res.ok) { location.reload(); } else { akSet(refusal(res), true, ccAdd); }
    }).catch(function (e) { ccAdd.disabled = false; akSet(failed(e), true, ccAdd); });
  });
})();
