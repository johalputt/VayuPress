/*
 * admin-os-vayuflow.js — VayuFlow's arm, run, switch, delete and save buttons.
 * One script, no inline handlers, no Alpine. Its words go through vpT, so the
 * page says them in its own language; the engine's words in a reply (a mode,
 * a write class, a role, an outcome) are named through word() for the same
 * reason.
 */
(function () {
  'use strict';

  var status = document.getElementById('flow-status');
  function say(t) { if (status) { status.textContent = t; } }

  // The console's double-submit cookie. This page read "vayu_csrf", a cookie
  // nothing sets, so every one of its buttons was refused by the CSRF check.
  function csrf() { var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/); return m ? decodeURIComponent(m[1]) : ''; }

  // A refusal says why, in the page's language when the server wrote one; a
  // flow's validation refusal is the engine's, shown as it wrote it.
  function refusal(d, fallback) {
    return (d && (d.detail || (d.error && d.error.message) || (typeof d.error === 'string' && d.error) || d.title)) || fallback;
  }

  function word(v) {
    switch (v) {
      case 'dry-run': return vpT('dry-run');
      case 'live': return vpT('live');
      case 'none': return vpT('none');
      case 'draft': return vpT('draft');
      case 'admin': return vpT('Admin');
      case 'editor': return vpT('Editor');
      case 'author': return vpT('Author');
      case 'succeeded': return vpT('Succeeded');
      case 'failed': return vpT('Failed');
      case 'refused': return vpT('Refused');
      case 'interrupted': return vpT('Interrupted');
    }
    return v;
  }

  function post(url, done) {
    fetch(url, { method: 'POST', headers: { 'X-CSRF-Token': csrf() } })
      .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, d: d }; }); })
      .then(function (res) { done(res); })
      .catch(function () { say(vpT('The server could not be reached.')); });
  }

  document.addEventListener('click', function (ev) {
    var arm = ev.target.closest('[data-flow-arm]');
    if (arm) {
      var id = arm.getAttribute('data-flow-arm'), mode = arm.getAttribute('data-flow-mode');
      say(vpT('Working…'));
      post('/os/api/vayuflow/arm?id=' + encodeURIComponent(id) + '&mode=' + encodeURIComponent(mode), function (res) {
        if (res.ok) { say(vpT('Mode: {from} → {to}. Reload to refresh.', { from: word(res.d.prior), to: word(res.d.mode) })); }
        else { say(refusal(res.d, vpT('The mode was not changed.'))); }
      });
      return;
    }
    var run = ev.target.closest('[data-flow-run]');
    if (run) {
      say(vpT('Running…'));
      post('/os/api/vayuflow/run?id=' + encodeURIComponent(run.getAttribute('data-flow-run')), function (res) {
        if (res.ok) { say(vpT('Run {outcome}, writes {writes}. Reload to see the trail.', { outcome: word(res.d.outcome), writes: res.d.writes })); }
        else { say(refusal(res.d, vpT('The run did not go through.'))); }
      });
      return;
    }
    var en = ev.target.closest('[data-flow-enable]');
    if (en) {
      var eid = en.getAttribute('data-flow-enable'), on = en.getAttribute('data-flow-on');
      say(vpT('Working…'));
      post('/os/api/vayuflow/enable?id=' + encodeURIComponent(eid) + '&on=' + encodeURIComponent(on), function (res) {
        if (res.ok) { say(res.d.enabled ? vpT('Switched on. Reload to refresh.') : vpT('Switched off. Reload to refresh.')); }
        else { say(refusal(res.d, vpT('That did not go through.'))); }
      });
      return;
    }
    var del = ev.target.closest('[data-flow-delete]');
    if (del) {
      var did = del.getAttribute('data-flow-delete');
      // Confirmed by NAME, not by id. An operator asked to confirm an opaque hex
      // string is being asked to agree to something they cannot read.
      vpConfirm({
        title: vpT('Delete flow'),
        message: vpT('Delete {name}? Its runs stay in the trail.', { name: del.getAttribute('data-flow-name') }),
        confirm: vpT('Delete')
      }, function () {
        say(vpT('Deleting…'));
        post('/os/api/vayuflow/delete?id=' + encodeURIComponent(did), function (res) {
          if (res.ok) { say(vpT('Deleted. Reload to refresh.')); }
          else { say(refusal(res.d, vpT('That did not go through.'))); }
        });
      });
    }
  });

  function val(id) { var el = document.getElementById(id); return el ? el.value.trim() : ''; }
  function num(id) { var n = parseInt(val(id), 10); return isNaN(n) ? 0 : n; }
  // Parameters are key=value per line. Split on the FIRST '=' only, so a value
  // containing one — a URL with a query string, a prompt with an equation —
  // survives intact.
  function params(id) {
    var out = {}, lines = val(id).split('\n');
    for (var i = 0; i < lines.length; i++) {
      var line = lines[i].trim(); if (!line) { continue; }
      var at = line.indexOf('='); if (at < 1) { continue; }
      out[line.slice(0, at).trim()] = line.slice(at + 1).trim();
    }
    return out;
  }

  var saveBtn = document.getElementById('ff-save');
  if (saveBtn) {
    saveBtn.addEventListener('click', function () {
      var steps = [];
      for (var i = 1; i <= 4; i++) {
        var a = val('ff-act' + i); if (!a) { continue; }
        steps.push({ action: a, params: params('ff-par' + i) });
      }
      var doc = {
        id: val('ff-id'), name: val('ff-name'),
        trigger: { kind: val('ff-trigger'), cron: val('ff-cron'), event: val('ff-event') },
        condition: { kind: val('ff-cond'), value: val('ff-condval') },
        steps: steps,
        budget: {
          max_steps_per_run: num('ff-bsteps'), max_runs_per_hour: num('ff-bruns'),
          max_writes_per_run: num('ff-bwrites'), max_egress_per_run: num('ff-begress'),
          timeout_seconds: num('ff-btimeout')
        }
      };
      say(vpT('Saving…'));
      fetch('/os/api/vayuflow/save', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() },
        body: JSON.stringify(doc)
      })
        .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, d: d }; }); })
        .then(function (res) {
          if (res.ok) {
            // The store is the authority on what was accepted, so the
            // confirmation quotes what came BACK rather than what was sent.
            var d = res.d;
            say(d.enabled
              ? vpT('Saved as v{version}: writes at most {write}, its owner must hold {role}. It is on, in {mode}. Reload to see it listed.',
                { version: d.version, write: word(d.highest_write), role: word(d.min_owner), mode: word(d.mode) })
              : vpT('Saved as v{version}: writes at most {write}, its owner must hold {role}. It is off, in {mode}. Reload to see it listed.',
                { version: d.version, write: word(d.highest_write), role: word(d.min_owner), mode: word(d.mode) }));
          } else {
            // The store's refusal is shown verbatim. It is written to be read by
            // the person who filled the form, and paraphrasing it here would
            // lose the one sentence that says which field is wrong.
            say(refusal(res.d, vpT('The flow was not saved.')));
          }
        })
        .catch(function () { say(vpT('The server could not be reached.')); });
    });
  }

  var resetBtn = document.getElementById('ff-reset');
  if (resetBtn) {
    resetBtn.addEventListener('click', function () {
      ['ff-id', 'ff-name', 'ff-cron', 'ff-condval', 'ff-par1', 'ff-par2', 'ff-par3', 'ff-par4'].forEach(function (id) {
        var el = document.getElementById(id); if (el) { el.value = ''; }
      });
      ['ff-act1', 'ff-act2', 'ff-act3', 'ff-act4', 'ff-event'].forEach(function (id) {
        var el = document.getElementById(id); if (el) { el.selectedIndex = 0; }
      });
      say(vpT('Form cleared.'));
    });
  }
})();
