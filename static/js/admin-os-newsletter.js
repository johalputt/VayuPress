/* admin-os-newsletter.js — behaviour for the VayuOS Newsletter console.
 *
 * CSP-safe: no inline handlers, no innerHTML with untrusted data. All server
 * calls carry the vp_csrf token. Search/segment filtering is client-side over
 * the server-rendered table; delete/broadcast reload the page so the stats,
 * history and table reflect the new state.
 */
(function () {
  'use strict';

  function csrf() {
    var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
    return m ? m[1] : '';
  }

  function api(method, url, body) {
    var opts = { method: method, headers: { 'X-CSRF-Token': csrf() } };
    if (body !== undefined) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body);
    }
    return fetch(url, opts).then(function (r) {
      return r.json().then(function (d) { return { ok: r.ok, d: d }; })
        .catch(function () { return { ok: r.ok, d: {} }; });
    });
  }

  var msg = document.getElementById('nl-compose-msg');
  function show(text, isErr) {
    if (!msg) { return; }
    msg.textContent = text;
    msg.classList.toggle('is-error', !!isErr);
    msg.classList.add('visible');
  }
  function errText(d) {
    return (d && (d.detail || d.title || d.error)) || 'Something went wrong';
  }

  // ── Subscriber search + segment filter (client-side) ─────────────────────
  var search = document.querySelector('[data-sub-search]');
  var rows = Array.prototype.slice.call(document.querySelectorAll('[data-sub-row]'));
  var emptyEl = document.querySelector('[data-subs-empty]');
  var activeSeg = 'all';

  function applyFilter() {
    var q = (search && search.value ? search.value : '').trim().toLowerCase();
    var shown = 0;
    rows.forEach(function (row) {
      var segOk = activeSeg === 'all' || row.getAttribute('data-seg') === activeSeg;
      var qOk = !q || (row.getAttribute('data-search') || '').indexOf(q) !== -1;
      var hit = segOk && qOk;
      row.hidden = !hit;
      if (hit) { shown++; }
    });
    if (emptyEl) { emptyEl.hidden = shown !== 0 || rows.length === 0; }
  }

  if (search) { search.addEventListener('input', applyFilter); }
  document.querySelectorAll('[data-sub-filter]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      document.querySelectorAll('[data-sub-filter]').forEach(function (x) { x.classList.remove('is-active'); });
      btn.classList.add('is-active');
      activeSeg = btn.getAttribute('data-sub-filter');
      applyFilter();
    });
  });

  // ── Delete a subscriber ──────────────────────────────────────────────────
  document.querySelectorAll('[data-sub-delete]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var email = btn.getAttribute('data-email');
      if (!window.confirm('Permanently delete ' + email + '? This cannot be undone.')) { return; }
      btn.disabled = true;
      api('DELETE', '/os/api/newsletter/subscribers/' + encodeURIComponent(btn.getAttribute('data-id')))
        .then(function (res) {
          if (res.ok) { location.reload(); }
          else { btn.disabled = false; show(errText(res.d), true); }
        })
        .catch(function (e) { btn.disabled = false; show('Error: ' + e, true); });
    });
  });

  // ── Compose: gather + validate ───────────────────────────────────────────
  function payload() {
    var subj = document.getElementById('nl-subject');
    var text = document.getElementById('nl-text');
    var htm = document.getElementById('nl-html');
    return {
      subject: subj ? subj.value.trim() : '',
      text: text ? text.value.trim() : '',
      html: htm ? htm.value.trim() : ''
    };
  }
  function validate(p) {
    if (!p.subject) { show('A subject is required.', true); return false; }
    if (!p.text) { show('Plain-text content is required.', true); return false; }
    return true;
  }

  // ── Send test ────────────────────────────────────────────────────────────
  var testBtn = document.getElementById('nl-send-test');
  if (testBtn) {
    testBtn.addEventListener('click', function () {
      var p = payload();
      if (!validate(p)) { return; }
      var toEl = document.getElementById('nl-test-to');
      var to = toEl ? toEl.value.trim() : '';
      if (!to) { show('Enter a test recipient address first.', true); return; }
      p.to = to;
      testBtn.disabled = true;
      show('Sending test…', false);
      api('POST', '/os/api/newsletter/test', p).then(function (res) {
        testBtn.disabled = false;
        show(res.ok ? ('Test sent to ' + to) : errText(res.d), !res.ok);
      }).catch(function (e) { testBtn.disabled = false; show('Error: ' + e, true); });
    });
  }

  // ── Send broadcast ───────────────────────────────────────────────────────
  var sendBtn = document.getElementById('nl-send-broadcast');
  if (sendBtn) {
    sendBtn.addEventListener('click', function () {
      var p = payload();
      if (!validate(p)) { return; }
      if (!window.confirm('Send this broadcast to all confirmed subscribers?')) { return; }
      sendBtn.disabled = true;
      show('Queuing broadcast…', false);
      api('POST', '/os/api/newsletter/broadcast', p).then(function (res) {
        if (res.ok) {
          show('Broadcast queued to ' + (res.d.queued || 0) + ' subscribers. Refreshing…', false);
          setTimeout(function () { location.reload(); }, 1200);
        } else {
          sendBtn.disabled = false;
          show(errText(res.d), true);
        }
      }).catch(function (e) { sendBtn.disabled = false; show('Error: ' + e, true); });
    });
  }
})();
