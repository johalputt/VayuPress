/*
 * admin-os-update.js — Update & Backup panel for VayuOS.
 *
 * Strict CSP: no eval, no innerHTML with server data. All DOM text is set via
 * textContent; class changes only. Every write carries the vp_csrf token.
 *
 * Capabilities:
 *   - Check for updates (GET /os/api/update/check)
 *   - One-click update with auto-restart (POST /os/api/update/apply {restart})
 *   - Roll back to the previous binary (POST /os/api/update/rollback)
 *   - Restore from an uploaded snapshot with upload progress
 *     (POST /os/api/backup/import, multipart "snapshot")
 * After any action that restarts the service, the panel waits for the server to
 * cycle and then reloads itself.
 */
(function () {
  'use strict';

  function csrf() {
    var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }

  var card = document.querySelector('[data-update-card]');
  if (!card) return;

  var latestEl = document.querySelector('[data-latest-version]');
  var statusEl = document.querySelector('[data-update-status]');
  var notesEl = document.querySelector('[data-update-notes]');
  var msgEl = document.querySelector('[data-update-msg]');
  var checkBtn = document.querySelector('[data-update-check]');
  var applyBtn = document.querySelector('[data-update-apply]');
  var rollbackBtn = document.querySelector('[data-update-rollback]');

  function setMsg(el, text, isErr) {
    if (!el) return;
    el.textContent = text || '';
    el.classList.toggle('is-error', !!isErr);
  }

  // Poll a cheap endpoint until the service has clearly cycled (one failure
  // followed by a success), then reload so the operator sees the new state.
  function waitForRestartThenReload(el) {
    var sawDown = false;
    var tries = 0;
    var max = 90; // ~3 minutes at 2s
    var timer = setInterval(function () {
      tries++;
      fetch('/os/api/update/history', { cache: 'no-store' })
        .then(function (r) {
          if (r.ok) {
            if (sawDown) {
              clearInterval(timer);
              setMsg(el, 'Service is back online — reloading…', false);
              setTimeout(function () { location.reload(); }, 800);
            }
          } else {
            sawDown = true;
          }
        })
        .catch(function () { sawDown = true; })
        .finally(function () {
          if (tries >= max) {
            clearInterval(timer);
            setMsg(el, 'Still restarting — reload the page in a moment.', true);
          }
        });
    }, 2000);
  }

  // ── Check for updates ──────────────────────────────────────────────────────
  function doCheck() {
    if (checkBtn) checkBtn.disabled = true;
    setMsg(msgEl, 'Checking GitHub for the latest release…', false);
    fetch('/os/api/update/check', { headers: { 'X-Requested-With': 'XMLHttpRequest' } })
      .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, d: d }; }); })
      .then(function (res) {
        if (!res.ok) {
          setMsg(msgEl, res.d.detail || res.d.title || 'Check failed', true);
          return;
        }
        var d = res.d;
        if (latestEl) latestEl.textContent = d.latest || '—';
        if (statusEl) {
          statusEl.textContent = d.available ? 'Update available' : 'Up to date';
          statusEl.className = 'update-version__value' + (d.available ? ' is-available' : '');
        }
        if (notesEl) {
          if (d.notes) {
            notesEl.textContent = d.notes;
            notesEl.hidden = false;
          } else {
            notesEl.hidden = true;
          }
        }
        if (applyBtn) {
          applyBtn.disabled = !(d.canApply && d.available);
        }
        if (d.available && !d.canApply) {
          setMsg(msgEl, 'A new release is available. Arm one-click apply (see the note above) to install it from here.', false);
        } else if (d.available) {
          setMsg(msgEl, 'Version ' + d.latest + ' is ready to install.', false);
        } else {
          setMsg(msgEl, 'You are running the latest release.', false);
        }
      })
      .catch(function (e) { setMsg(msgEl, 'Error: ' + e, true); })
      .finally(function () { if (checkBtn) checkBtn.disabled = false; });
  }

  // ── Apply update (one-click, auto-restart) ──────────────────────────────────
  function doApply() {
    if (!window.confirm('Install the latest release now? Your database is backed up automatically and the service will restart to finish. This usually takes under a minute.')) {
      return;
    }
    if (applyBtn) applyBtn.disabled = true;
    if (checkBtn) checkBtn.disabled = true;
    setMsg(msgEl, 'Downloading and verifying the release… do not close this tab.', false);
    fetch('/os/api/update/apply', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() },
      body: JSON.stringify({ restart: true })
    })
      .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, d: d }; }); })
      .then(function (res) {
        if (!res.ok) {
          setMsg(msgEl, res.d.detail || res.d.title || 'Update failed', true);
          if (applyBtn) applyBtn.disabled = false;
          if (checkBtn) checkBtn.disabled = false;
          return;
        }
        setMsg(msgEl, 'Installed v' + (res.d.version || '') + '. Restarting to activate…', false);
        waitForRestartThenReload(msgEl);
      })
      .catch(function (e) {
        setMsg(msgEl, 'Error: ' + e, true);
        if (applyBtn) applyBtn.disabled = false;
        if (checkBtn) checkBtn.disabled = false;
      });
  }

  // ── Roll back to the previous binary ────────────────────────────────────────
  function doRollback() {
    if (!window.confirm('Roll back to the previous binary and restart? This undoes the most recent update.')) {
      return;
    }
    if (rollbackBtn) rollbackBtn.disabled = true;
    setMsg(msgEl, 'Rolling back and restarting…', false);
    fetch('/os/api/update/rollback', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() }
    })
      .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, d: d }; }); })
      .then(function (res) {
        if (!res.ok) {
          setMsg(msgEl, res.d.detail || res.d.title || 'Rollback failed', true);
          if (rollbackBtn) rollbackBtn.disabled = false;
          return;
        }
        waitForRestartThenReload(msgEl);
      })
      .catch(function (e) {
        setMsg(msgEl, 'Error: ' + e, true);
        if (rollbackBtn) rollbackBtn.disabled = false;
      });
  }

  // ── Restore from an uploaded snapshot (with upload progress) ────────────────
  var fileInput = document.querySelector('[data-backup-file]');
  var importBtn = document.querySelector('[data-backup-import]');
  var backupMsg = document.querySelector('[data-backup-msg]');
  var progWrap = document.querySelector('[data-restore-progress]');
  var progBar = document.querySelector('[data-restore-bar]');

  function setBar(pct) {
    if (!progBar) return;
    // Bucketed width class keeps us CSP-clean (no inline style).
    var buckets = [0, 10, 20, 25, 30, 40, 50, 60, 70, 75, 80, 90, 100];
    var chosen = 0;
    for (var i = 0; i < buckets.length; i++) {
      if (pct >= buckets[i]) chosen = buckets[i];
    }
    progBar.className = 'progress__bar progress__bar--ok w-' + chosen;
  }

  function doImport() {
    var f = fileInput && fileInput.files && fileInput.files[0];
    if (!f) { setMsg(backupMsg, 'Choose a backup file first.', true); return; }
    if (!window.confirm('Restore from "' + f.name + '"? This REPLACES all current content and settings. Your current database is backed up automatically, then the service restarts.')) {
      return;
    }
    if (importBtn) importBtn.disabled = true;
    if (progWrap) progWrap.hidden = false;
    setBar(0);
    setMsg(backupMsg, 'Uploading…', false);

    var fd = new FormData();
    fd.append('snapshot', f, f.name);

    var xhr = new XMLHttpRequest();
    xhr.open('POST', '/os/api/backup/import', true);
    xhr.setRequestHeader('X-CSRF-Token', csrf());
    xhr.upload.onprogress = function (e) {
      if (e.lengthComputable) {
        var pct = Math.round((e.loaded / e.total) * 100);
        setBar(pct);
        setMsg(backupMsg, 'Uploading… ' + pct + '%', false);
      }
    };
    xhr.onload = function () {
      var d = {};
      try { d = JSON.parse(xhr.responseText); } catch (e) { d = {}; }
      if (xhr.status >= 200 && xhr.status < 300) {
        setBar(100);
        setMsg(backupMsg, 'Backup validated. Restoring and restarting…', false);
        waitForRestartThenReload(backupMsg);
      } else {
        setMsg(backupMsg, d.detail || d.title || ('Restore failed (HTTP ' + xhr.status + ')'), true);
        if (importBtn) importBtn.disabled = false;
      }
    };
    xhr.onerror = function () {
      setMsg(backupMsg, 'Upload failed — network error.', true);
      if (importBtn) importBtn.disabled = false;
    };
    xhr.send(fd);
  }

  if (checkBtn) checkBtn.addEventListener('click', doCheck);
  if (applyBtn) applyBtn.addEventListener('click', doApply);
  if (rollbackBtn) rollbackBtn.addEventListener('click', doRollback);
  if (importBtn) importBtn.addEventListener('click', doImport);

  // Auto-check on load so the operator immediately sees whether an update exists.
  doCheck();
})();
