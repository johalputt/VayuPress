/* admin-os-newsletter.js — behaviour for the VayuOS Newsletter console.
 *
 * CSP-safe: no inline handlers, no innerHTML with untrusted data. All server
 * calls carry the vp_csrf token. Search/segment filtering is client-side over
 * the server-rendered table; delete/broadcast re-render the page in place
 * (vpRefresh) so the stats, history and table reflect the new state. Listeners
 * are delegated and elements looked up when used, because that refresh replaces
 * them; the search text and segment survive it.
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

  function show(text, isErr) {
    var msg = document.getElementById('nl-compose-msg');
    if (!msg) { return; }
    msg.textContent = text;
    msg.classList.toggle('is-error', !!isErr);
    msg.classList.add('visible');
  }
  function errText(d) {
    return (d && (d.detail || d.title || d.error)) || 'The server refused this without saying why. Reload the page and try again.';
  }
  // After a change: confirm it in a toast (the page's own message line is about
  // to be re-rendered), then refresh in place.
  function done(text) {
    if (window.vpToast) { window.vpToast(text, 'ok'); }
    if (window.vpRefresh) { window.vpRefresh(); } else { location.reload(); }
  }

  // ── Subscriber search + segment filter (client-side) ─────────────────────
  var activeSeg = 'all';
  var query = '';

  function applyFilter() {
    var rows = document.querySelectorAll('[data-sub-row]');
    var shown = 0;
    rows.forEach(function (row) {
      var segOk = activeSeg === 'all' || row.getAttribute('data-seg') === activeSeg;
      var qOk = !query || (row.getAttribute('data-search') || '').indexOf(query) !== -1;
      var hit = segOk && qOk;
      row.hidden = !hit;
      if (hit) { shown++; }
    });
    var emptyEl = document.querySelector('[data-subs-empty]');
    if (emptyEl) { emptyEl.hidden = shown !== 0 || rows.length === 0; }
  }
  function markSegment() {
    document.querySelectorAll('[data-sub-filter]').forEach(function (x) {
      x.classList.toggle('is-active', x.getAttribute('data-sub-filter') === activeSeg);
    });
  }

  document.addEventListener('input', function (e) {
    if (e.target && e.target.matches && e.target.matches('[data-sub-search]')) {
      query = e.target.value.trim().toLowerCase();
      applyFilter();
    }
  });
  // A refreshed table arrives unfiltered: put back what the operator was looking at.
  document.addEventListener('vp:refreshed', function () {
    var search = document.querySelector('[data-sub-search]');
    if (search) { search.value = query; }
    markSegment();
    applyFilter();
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

  function deleteSubscriber(btn) {
    var email = btn.getAttribute('data-email');
    var id = btn.getAttribute('data-id');
    vpConfirm({
      title: 'Delete this subscriber',
      message: 'Permanently delete ' + email + '? This cannot be undone.',
      confirm: 'Delete',
    }, function () {
      btn.disabled = true;
      api('DELETE', '/os/api/newsletter/subscribers/' + encodeURIComponent(id))
        .then(function (res) {
          if (res.ok) { done('Deleted ' + email + '.'); }
          else { btn.disabled = false; show(errText(res.d), true); }
        })
        .catch(function (e) { btn.disabled = false; show('Error: ' + e, true); });
    });
  }

  function sendTest(testBtn) {
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
  }

  function sendBroadcast(sendBtn) {
    var p = payload();
    if (!validate(p)) { return; }
    vpConfirm({
      title: 'Send this broadcast',
      message: 'Send this broadcast to all confirmed subscribers?',
      confirm: 'Send',
    }, function () {
      sendBtn.disabled = true;
      show('Queuing broadcast…', false);
      api('POST', '/os/api/newsletter/broadcast', p).then(function (res) {
        if (res.ok) {
          done('Broadcast queued to ' + (res.d.queued || 0) + ' subscribers.');
        } else {
          sendBtn.disabled = false;
          show(errText(res.d), true);
        }
      }).catch(function (e) { sendBtn.disabled = false; show('Error: ' + e, true); });
    });
  }

  document.addEventListener('click', function (e) {
    var t = e.target;
    if (!t || !t.closest) { return; }
    var btn;
    if ((btn = t.closest('[data-sub-filter]'))) {
      activeSeg = btn.getAttribute('data-sub-filter');
      markSegment();
      applyFilter();
      return;
    }
    if ((btn = t.closest('[data-sub-delete]'))) { deleteSubscriber(btn); return; }
    if ((btn = t.closest('#nl-send-test'))) { sendTest(btn); return; }
    if ((btn = t.closest('#nl-send-broadcast'))) { sendBroadcast(btn); }
  });
})();
