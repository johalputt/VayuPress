/* admin-os-newsletter.js — behaviour for the VayuOS Newsletter console.
 *
 * CSP-safe: no inline handlers, no innerHTML with untrusted data. All server
 * calls carry the vp_csrf token. Search/segment filtering is client-side over
 * the server-rendered table; delete/broadcast re-render the page in place
 * (vpRefresh) so the stats, history and table reflect the new state. Listeners
 * are delegated and elements looked up when used, because that refresh replaces
 * them; the search text and segment survive it. Its words go through vpT,
 * so the page says them in its own language.
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
  // A refusal written as an error carries its words in error.message, and
  // showed as "[object Object]".
  function errText(d) {
    d = d || {};
    return d.detail || (d.error && d.error.message) || d.title || vpT('The server refused this without saying why. Reload the page and try again.');
  }
  function failed(e) { return vpT('Request failed: {error}', { error: String(e) }); }
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
    if (!p.subject) { show(vpT('A subject is required.'), true); return false; }
    if (!p.text) { show(vpT('Plain-text content is required.'), true); return false; }
    return true;
  }

  function deleteSubscriber(btn) {
    var email = btn.getAttribute('data-email');
    var id = btn.getAttribute('data-id');
    vpConfirm({
      title: vpT('Delete this subscriber'),
      message: vpT('Permanently delete {email}? This cannot be undone.', { email: email }),
      confirm: vpT('Delete'),
    }, function () {
      btn.disabled = true;
      api('DELETE', '/os/api/newsletter/subscribers/' + encodeURIComponent(id))
        .then(function (res) {
          if (res.ok) { done(vpT('Deleted {email}.', { email: email })); }
          else { btn.disabled = false; show(errText(res.d), true); }
        })
        .catch(function (e) { btn.disabled = false; show(failed(e), true); });
    });
  }

  function sendTest(testBtn) {
    var p = payload();
    if (!validate(p)) { return; }
    var toEl = document.getElementById('nl-test-to');
    var to = toEl ? toEl.value.trim() : '';
    if (!to) { show(vpT('Enter a test recipient address first.'), true); return; }
    p.to = to;
    testBtn.disabled = true;
    show(vpT('Sending test…'), false);
    api('POST', '/os/api/newsletter/test', p).then(function (res) {
      testBtn.disabled = false;
      show(res.ok ? vpT('Test sent to {address}', { address: to }) : errText(res.d), !res.ok);
    }).catch(function (e) { testBtn.disabled = false; show(failed(e), true); });
  }

  function sendBroadcast(sendBtn) {
    var p = payload();
    if (!validate(p)) { return; }
    vpConfirm({
      title: vpT('Send this broadcast'),
      message: vpT('Send this broadcast to all confirmed subscribers?'),
      confirm: vpT('Send'),
    }, function () {
      sendBtn.disabled = true;
      show(vpT('Queuing broadcast…'), false);
      api('POST', '/os/api/newsletter/broadcast', p).then(function (res) {
        if (res.ok) {
          done(vpT('{n, plural, one {Broadcast queued to # subscriber.} other {Broadcast queued to # subscribers.}}', { n: res.d.queued || 0 }));
        } else {
          sendBtn.disabled = false;
          show(errText(res.d), true);
        }
      }).catch(function (e) { sendBtn.disabled = false; show(failed(e), true); });
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
