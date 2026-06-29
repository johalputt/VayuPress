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

  // readResponse reads a fetch Response defensively. The server (or the reverse
  // proxy in front of it) can return a NON-JSON body — an nginx 502/504 HTML
  // page while the service is restarting mid-update, or a login page if the
  // session lapsed. Calling r.json() on that throws "Unexpected token '<'", so we
  // read the text first and only parse when it is actually JSON. Returns
  // { ok, status, isJSON, d }.
  function readResponse(r) {
    return r.text().then(function (t) {
      var d = {};
      var isJSON = false;
      if (t) {
        try { d = JSON.parse(t); isJSON = true; } catch (e) { isJSON = false; }
      } else {
        isJSON = true; // empty body is acceptable (treated as {})
      }
      return { ok: r.ok, status: r.status, isJSON: isJSON, d: d };
    });
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
    fetch('/os/api/update/check', { headers: { 'X-Requested-With': 'XMLHttpRequest' }, cache: 'no-store' })
      .then(readResponse)
      .then(function (res) {
        if (!res.isJSON) {
          // HTML/empty body → the update service is unreachable (often a brief
          // window while it restarts behind the proxy). Don't surface raw HTML.
          setMsg(msgEl, 'The update service is unavailable right now — it may be restarting. Try Check again in a moment.', true);
          return;
        }
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
        if (!d.available) {
          setMsg(msgEl, 'You are running the latest release.', false);
        } else if (!d.canApply) {
          setMsg(msgEl, 'Version ' + d.latest + ' is available, but updates are paused while the system mode is ' + (d.mode || 'restricted') + '.', false);
        } else if (d.signed) {
          setMsg(msgEl, 'Version ' + d.latest + ' is ready to install (checksum + signature verified).', false);
        } else {
          setMsg(msgEl, 'Version ' + d.latest + ' is ready to install (checksum verified).', false);
        }
      })
      .catch(function () { setMsg(msgEl, 'Could not reach the update service — check your connection and try again.', true); })
      .finally(function () { if (checkBtn) checkBtn.disabled = false; });
  }

  // ── Apply update (one-click, auto-restart) ──────────────────────────────────
  function doApply() {
    var backupEl = document.querySelector('[data-update-backup]');
    var doBackup = !backupEl || backupEl.checked;
    var prompt = doBackup
      ? 'Install the latest release now? Your database will be backed up first, then the service restarts to finish. This usually takes under a minute (longer for very large databases).'
      : 'Install the latest release now WITHOUT a database backup? A binary update does not change your database, and the previous binary is kept for rollback. The service will restart to finish.';
    if (!window.confirm(prompt)) {
      return;
    }
    if (applyBtn) applyBtn.disabled = true;
    if (checkBtn) checkBtn.disabled = true;
    setMsg(msgEl, doBackup ? 'Backing up, downloading and verifying the release… do not close this tab.' : 'Downloading and verifying the release… do not close this tab.', false);
    fetch('/os/api/update/apply', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() },
      body: JSON.stringify({ restart: true, backup: doBackup })
    })
      .then(readResponse)
      .then(function (res) {
        // A genuine, pre-restart failure (e.g. checksum/signature, paused mode)
        // comes back as a JSON error — show it and re-enable the buttons.
        if (res.isJSON && !res.ok) {
          setMsg(msgEl, res.d.detail || res.d.title || 'Update failed', true);
          if (applyBtn) applyBtn.disabled = false;
          if (checkBtn) checkBtn.disabled = false;
          return;
        }
        // Otherwise the update was accepted. Whether we got the JSON success body
        // or a non-JSON body (the service already cycled behind the proxy, or a
        // gateway timeout while it installs+restarts), the right thing is to wait
        // for the service to come back — NOT to report an error.
        if (res.isJSON && res.ok && res.d.version) {
          setMsg(msgEl, 'Installed v' + res.d.version + '. Restarting to activate…', false);
        } else {
          setMsg(msgEl, 'Update applied — the service is restarting to activate it…', false);
        }
        waitForRestartThenReload(msgEl);
      })
      .catch(function () {
        // A dropped connection right after POSTing is the expected restart, not a
        // failure — wait for the service to return rather than alarming the user.
        setMsg(msgEl, 'The service is restarting to finish the update…', false);
        waitForRestartThenReload(msgEl);
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
      .then(readResponse)
      .then(function (res) {
        if (res.isJSON && !res.ok) {
          setMsg(msgEl, res.d.detail || res.d.title || 'Rollback failed', true);
          if (rollbackBtn) rollbackBtn.disabled = false;
          return;
        }
        // Accepted (JSON success or a non-JSON body from the cycling service) →
        // wait for the restart rather than reporting a false error.
        setMsg(msgEl, 'Rolled back — the service is restarting…', false);
        waitForRestartThenReload(msgEl);
      })
      .catch(function () {
        setMsg(msgEl, 'The service is restarting to finish the rollback…', false);
        waitForRestartThenReload(msgEl);
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
