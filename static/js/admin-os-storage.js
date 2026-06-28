/* admin-os-storage.js — VayuOS Storage & System page.
 *
 * Handles deleting managed files (single + bulk selection). Downloads are plain
 * <a download> links, so no JS is needed for them. CSP-safe: no inline handlers,
 * DOM updates use textContent, the CSRF token is read from the vp_csrf cookie.
 */
(function () {
  'use strict';

  function csrf() {
    var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
    return m ? m[1] : '';
  }

  var msg = document.getElementById('action-msg');
  function show(text, isErr) {
    if (!msg) return;
    msg.textContent = text;
    msg.classList.toggle('is-error', !!isErr);
    msg.classList.add('visible');
  }

  function deletePaths(paths, onDone) {
    fetch('/os/api/storage/delete', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() },
      body: JSON.stringify({ paths: paths })
    })
      .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, d: d }; }); })
      .then(function (res) {
        if (!res.ok) { show((res.d && (res.d.detail || res.d.title)) || 'Delete failed', true); return; }
        var n = res.d.deleted || 0;
        var freed = res.d.freed ? ' · freed ' + res.d.freed : '';
        if (res.d.failed && res.d.failed.length) {
          show('Deleted ' + n + freed + ' · failed: ' + res.d.failed.join(', '), true);
        } else {
          show('Deleted ' + n + ' file' + (n === 1 ? '' : 's') + freed, false);
        }
        if (onDone) onDone(res.d);
      })
      .catch(function (e) { show('Error: ' + e, true); });
  }

  // ── Per-row delete ──────────────────────────────────────────────────────
  document.querySelectorAll('[data-file-delete]').forEach(function (b) {
    b.addEventListener('click', function () {
      var path = b.getAttribute('data-path');
      var name = b.getAttribute('data-name') || 'this file';
      if (!window.confirm('Delete "' + name + '"? This permanently removes the file and cannot be undone.')) return;
      b.disabled = true;
      deletePaths([path], function () {
        var row = b.closest('[data-file-row]');
        if (row) row.remove();
        refreshBulk();
      });
    });
  });

  // ── Bulk selection ──────────────────────────────────────────────────────
  var bulkBar = document.querySelector('[data-file-bulkbar]');
  var bulkCount = document.querySelector('[data-file-bulk-count]');
  var selectAll = document.querySelector('[data-file-select-all]');

  function selectedPaths() {
    return Array.prototype.slice
      .call(document.querySelectorAll('[data-file-select]:checked'))
      .map(function (c) { return c.value; });
  }
  function refreshBulk() {
    var n = selectedPaths().length;
    if (bulkCount) bulkCount.textContent = String(n);
    if (bulkBar) bulkBar.hidden = n === 0;
  }
  document.querySelectorAll('[data-file-select]').forEach(function (c) {
    c.addEventListener('change', refreshBulk);
  });
  if (selectAll) {
    selectAll.addEventListener('change', function () {
      document.querySelectorAll('[data-file-select]').forEach(function (c) { c.checked = selectAll.checked; });
      refreshBulk();
    });
  }
  var bulkDelete = document.querySelector('[data-file-bulk-delete]');
  if (bulkDelete) {
    bulkDelete.addEventListener('click', function () {
      var paths = selectedPaths();
      if (!paths.length) return;
      if (!window.confirm('Delete ' + paths.length + ' selected file' + (paths.length > 1 ? 's' : '') + '? This cannot be undone.')) return;
      bulkDelete.disabled = true;
      deletePaths(paths, function () {
        paths.forEach(function (p) {
          var box = document.querySelector('[data-file-select][value="' + (window.CSS && CSS.escape ? CSS.escape(p) : p) + '"]');
          var row = box && box.closest('[data-file-row]');
          if (row) row.remove();
        });
        bulkDelete.disabled = false;
        if (selectAll) selectAll.checked = false;
        refreshBulk();
      });
    });
  }
})();
