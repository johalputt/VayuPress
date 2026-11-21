/* SPDX-License-Identifier: Apache-2.0 */
/* The Backups page (/os/vayukeep): every control on it, in both its forms
   (set up, or not yet). Each looks its element up and does nothing without
   it. Its words go through vpT, so the page says them in its own language. */
(function () {
  'use strict';
  function csrf() { var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/); return m ? decodeURIComponent(m[1]) : ''; }
  function toast(msg, kind) { if (window.vpToast) { window.vpToast(msg, kind); } }
  // The word a restore is confirmed with. The server compares it as typed, so
  // it is the same in every language, and the sentence around it says so.
  var confirmWord = 'RESTORE';

  // Every control reports the real outcome. Back up now and Test restore answer
  // at once because a paced run can outlast any request, but the page then shows
  // the run and its outcome (#vk-run): never an optimistic "started" that a later
  // failure fails to correct.
  function vkPost(url, payload, btn, working, outId, then) {
    // A button in a sheet reports next to itself, where the operator is looking.
    var sheet = btn && btn.closest('.sa-sheet');
    var out = (sheet && sheet.querySelector('[data-sheet-status]')) || document.getElementById(outId || 'vk-status');
    var label = btn ? btn.textContent : '';
    if (btn) { btn.disabled = true; btn.textContent = working; }
    if (out) { out.textContent = vpT('Working…'); }
    fetch(url, { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() }, body: JSON.stringify(payload || {}) })
      .then(function (r) { return r.json().catch(function () { return { ok: false, detail: vpT('Unexpected response ({status}).', { status: r.status }) }; }); })
      .then(function (d) {
        // A refusal written as an error (writeAPIError) carries its words in
        // error.message, not detail: it was shown as "Done" in the error tone.
        var said = d.detail || (d.error && d.error.message) || (d.ok ? vpT('Done.') : vpT('That did not go through.'));
        if (out) { out.textContent = said; }
        toast(said, d.ok ? 'success' : 'error');
        if (then) { then(d); }
        if (d.restart) {
          setTimeout(function () {
            fetch('/os/api/power/restart', { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() }, body: '{}' })
              .finally(function () { setTimeout(function () { location.reload(); }, 6000); });
          }, 800);
        } else if (d.reload) { setTimeout(function () { location.reload(); }, 1500); }
      })
      .catch(function (e) {
        var m = vpT('Request failed: {error}', { error: String(e) });
        if (out) { out.textContent = m; }
        toast(m, 'error');
      })
      .finally(function () { if (btn) { btn.disabled = false; btn.textContent = label; } });
  }

  var b = document.querySelector('[data-vk-backup]');
  // Back up now starts the backup and the page shows it running (#vk-run polls
  // itself). Only a backup that passed its test restore offers the older points
  // for removal, and the server refuses the removal unless a tested point exists.
  if (b) { b.addEventListener('click', function () { vkPost('/os/api/vayukeep/backup', {}, b, vpT('Starting…')); }); }
  // The offer arrives in a polled fragment, after this script ran: delegated.
  document.addEventListener('click', function (ev) {
    var clr = ev.target.closest('[data-vk-clear-older]'); if (!clr) { return; }
    var n = parseInt(clr.getAttribute('data-vk-older'), 10), g = clr.getAttribute('data-vk-generation');
    vpConfirm({
      title: vpT('Remove older restore points'),
      message: vpT('{n, plural, one {Delete the # restore point older than {point}?} other {Delete the # restore points older than {point}?}} {point} passed its test restore and is kept. This cannot be undone.', { n: n, point: g }),
      confirm: vpT('Remove')
    }, function () {
      vkPost('/os/api/vayukeep/clear-older', {}, clr, vpT('Removing…'), 'vk-status');
    });
  });
  // A large site's download takes longer to prepare than a proxy waits for its
  // first byte, so it is prepared as background work and offered in #vk-run.
  var ex = document.querySelector('[data-backup-export]');
  if (ex) { ex.addEventListener('click', function () { vkPost('/os/api/backup/export/start', {}, ex, vpT('Starting…')); }); }
  var d = document.querySelector('[data-vk-drill]');
  if (d) { d.addEventListener('click', function () { vkPost('/os/api/vayukeep/drill', {}, d, vpT('Restoring…')); }); }
  Array.prototype.forEach.call(document.querySelectorAll('[data-vk-verify]'), function (el) {
    el.addEventListener('click', function () {
      vkPost('/os/api/vayukeep/verify', { name: el.getAttribute('data-vk-verify') }, el, vpT('Checking…'), 'vk-verify-status');
    });
  });
  // Generate a passphrase in the browser so it never needs a round trip before
  // the operator has it. 20 characters from a 32-symbol unambiguous alphabet is
  // ~100 bits — and readable enough to copy onto paper, which is the point.
  var genBtn = document.querySelector('[data-vk-gen]');
  if (genBtn) {
    genBtn.addEventListener('click', function () {
      var alpha = 'abcdefghjkmnpqrstuvwxyz23456789'; // no i/l/o/0/1 — they get mistranscribed
      var buf = new Uint8Array(20); (window.crypto || window.msCrypto).getRandomValues(buf);
      var out = ''; for (var i = 0; i < buf.length; i++) { if (i && i % 5 === 0) out += '-'; out += alpha[buf[i] % alpha.length]; }
      var f = document.getElementById('vk-pass');
      if (f) { f.type = 'text'; f.value = out; f.focus(); f.select(); }
      var warn = document.getElementById('vk-pass-warn'); if (warn) { warn.hidden = false; }
    });
  }
  var copyBtn = document.querySelector('[data-vk-copy]');
  if (copyBtn) {
    copyBtn.addEventListener('click', function () {
      var f = document.getElementById('vk-pass');
      if (!f || !f.value) { toast(vpT('Nothing to copy yet: generate or type a passphrase first.'), 'error'); return; }
      f.type = 'text'; f.select();
      var done = function () {
        toast(vpT('Passphrase copied. Save it somewhere other than this server.'), 'success');
        var warn = document.getElementById('vk-pass-warn'); if (warn) { warn.hidden = false; }
      };
      if (navigator.clipboard && navigator.clipboard.writeText) { navigator.clipboard.writeText(f.value).then(done, function () { document.execCommand('copy'); done(); }); }
      else { document.execCommand('copy'); done(); }
    });
  }
  Array.prototype.forEach.call(document.querySelectorAll('[data-vk-delete]'), function (el) {
    el.addEventListener('click', function () {
      var name = el.getAttribute('data-vk-delete');
      vpConfirm({
        title: vpT('Delete restore point'),
        message: vpT('Delete {point} permanently? This copy is gone, not moved to a bin.', { point: name }),
        confirm: vpT('Delete')
      }, function () {
        vkPost('/os/api/vayukeep/delete', { name: name }, el, vpT('Deleting…'), 'vk-verify-status');
      });
    });
  });
  var retBtn = document.querySelector('[data-vk-retention]');
  if (retBtn) {
    retBtn.addEventListener('click', function () {
      var n = document.getElementById('vk-keep-n'), dd = document.getElementById('vk-keep-d'), ev = document.getElementById('vk-every');
      vkPost('/os/api/vayukeep/retention', { generations: parseInt(n ? n.value : '0', 10), days: parseInt(dd ? dd.value : '0', 10), every_minutes: parseInt(ev ? ev.value : '0', 10) }, retBtn, vpT('Saving…'), 'vk-retention-status');
    });
  }
  var pruneBtn = document.querySelector('[data-vk-prune]');
  if (pruneBtn) {
    pruneBtn.addEventListener('click', function () {
      vpConfirm({ title: vpT('Prune restore points'), message: vpT('Delete every restore point that is outside both limits?'), confirm: vpT('Prune') }, function () {
        vkPost('/os/api/vayukeep/prune', {}, pruneBtn, vpT('Cleaning…'), 'vk-retention-status');
      });
    });
  }
  var setupBtn = document.querySelector('[data-vk-setup]');
  if (setupBtn) {
    setupBtn.addEventListener('click', function () {
      var t = document.getElementById('vk-target'), p = document.getElementById('vk-pass');
      vkPost('/os/api/vayukeep/setup', { target: t ? t.value : '', passphrase: p ? p.value : '' }, setupBtn, vpT('Setting up…'), 'vk-setup-status');
    });
  }
  var offBtn = document.querySelector('[data-vk-disable]');
  if (offBtn) {
    offBtn.addEventListener('click', function () {
      vpConfirm({ title: vpT('Turn automatic backup off?'), message: vpT('Your existing restore points are kept, but no new ones will be made.'), confirm: vpT('Turn off') }, function () {
        vkPost('/os/api/vayukeep/disable', {}, offBtn, vpT('Turning off…'));
      });
    });
  }
  Array.prototype.forEach.call(document.querySelectorAll('[data-vk-restore]'), function (el) {
    el.addEventListener('click', function () {
      var name = el.getAttribute('data-vk-restore');
      // Typed confirmation, not a click. This replaces the live database.
      vpPrompt({
        title: vpT('Restore {point}?', { point: name }),
        message: vpT('This puts your site back to {point} and restarts. Your current database is copied aside first, so it is reversible.', { point: name }),
        label: vpT('Type {word} to confirm', { word: confirmWord }),
        placeholder: confirmWord,
        confirm: vpT('Restore')
      }, function (typed) {
        if (typed !== confirmWord) return;
        vkPost('/os/api/vayukeep/restore', { name: name, confirm: typed }, el, vpT('Restoring…'), 'vk-verify-status');
      });
    });
  });
})();
