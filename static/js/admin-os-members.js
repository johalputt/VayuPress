/* admin-os-members.js — VayuOS Members console interactions.
 *
 * Wires the tier editor modal, tier archive, per-member tier changes, and
 * label add/remove to the session-authenticated /os/api/members/* endpoints.
 * Every mutating request carries the double-submit CSRF token read from the
 * vp_csrf cookie. No inline handlers — listeners are attached here so the page
 * stays within the strict script-src 'self' 'nonce-…' CSP.
 *
 * Every listener is delegated from document and every element is looked up when
 * it is used: vpRefresh replaces the page's content after each change, so a
 * listener bound to an element, or a reference kept to one, would outlive it. */
(function () {
  'use strict';

  function cookie(name) {
    var m = document.cookie.match('(^|;)\\s*' + name + '\\s*=\\s*([^;]+)');
    return m ? m.pop() : '';
  }

  function api(method, url, body) {
    return fetch(url, {
      method: method,
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': cookie('vp_csrf') },
      body: body ? JSON.stringify(body) : undefined,
    });
  }

  // After a change: say so, then re-render the page in place. A reload used to
  // do this, and threw the confirmation away with the page.
  function done(msg) {
    if (msg && window.vpToast) { window.vpToast(msg, 'ok'); }
    if (window.vpRefresh) { window.vpRefresh(); } else { window.location.reload(); }
  }

  // The value a <select> was rendered with — what to put back when a change is
  // refused. Read from the markup, so it is right after every in-place refresh.
  function renderedValue(sel) {
    for (var i = 0; i < sel.options.length; i++) {
      if (sel.options[i].defaultSelected) { return sel.options[i].value; }
    }
    return sel.options.length ? sel.options[0].value : '';
  }

  // Extracts a human message from an API JSON body. Errors use the shape
  // {error:{code,message}}; success/other bodies may use {message}.
  function errText(d) {
    if (!d) { return ''; }
    if (d.error && d.error.message) { return d.error.message; }
    return d.message || '';
  }

  // The console's own feedback, not the browser's dialogs.
  //
  // alert() here is a deliberate LOCAL shim: it shadows the native one, so the many
  // error paths below route to the shell's toast without editing each call site.
  // window.prompt/confirm return a value and cannot be shimmed this way, so those
  // are migrated to vpPrompt/vpConfirm individually.
  function alert(msg) {
    var text = String(msg == null ? '' : msg);
    if (window.vpToast) { window.vpToast(text, 'error'); return; }
    // Never fall back to the native dialog — it is the thing being replaced.
    // Announce it in the shell's live region so the message is not simply lost.
    var live = document.getElementById('vp-live');
    if (live) { live.textContent = text; }
  }

  var $ = function (id) { return document.getElementById(id); };

  // ── Tier editor modal ──────────────────────────────────────────────────
  function openModal(data) {
    data = data || {};
    $('tier-id').value = data.id || '';
    $('tier-name').value = data.name || '';
    $('tier-desc').value = data.description || '';
    $('tier-monthly').value = data.monthly || 0;
    $('tier-yearly').value = data.yearly || 0;
    $('tier-currency').value = data.currency || 'USD';
    $('tier-visibility').value = data.visibility || 'public';
    if ($('tier-trial')) { $('tier-trial').value = data.trial || 0; }
    if ($('tier-stripe-monthly')) { $('tier-stripe-monthly').value = data.stripeMonthly || ''; }
    if ($('tier-stripe-yearly')) { $('tier-stripe-yearly').value = data.stripeYearly || ''; }
    if ($('tier-mail-enabled')) { $('tier-mail-enabled').checked = (data.mailEnabled === '1' || data.mailEnabled === 'true'); }
    if ($('tier-mail-quota')) { $('tier-mail-quota').value = data.mailQuota || 0; }
    $('tier-benefits').value = data.benefits || '';
    if ($('tier-modal-title')) { $('tier-modal-title').textContent = data.id ? 'Edit tier' : 'New tier'; }
    if ($('tier-modal')) { $('tier-modal').removeAttribute('hidden'); }
  }
  function closeModal() { if ($('tier-modal')) { $('tier-modal').setAttribute('hidden', ''); } }

  function saveTier() {
    var id = $('tier-id').value;
    var benefits = $('tier-benefits').value.split('\n').map(function (s) { return s.trim(); }).filter(Boolean);
    var payload = {
      name: $('tier-name').value.trim(),
      description: $('tier-desc').value.trim(),
      monthly_cents: parseInt($('tier-monthly').value, 10) || 0,
      yearly_cents: parseInt($('tier-yearly').value, 10) || 0,
      currency: ($('tier-currency').value || 'USD').trim(),
      visibility: $('tier-visibility').value,
      trial_days: $('tier-trial') ? (parseInt($('tier-trial').value, 10) || 0) : 0,
      stripe_monthly_price: $('tier-stripe-monthly') ? $('tier-stripe-monthly').value.trim() : '',
      stripe_yearly_price: $('tier-stripe-yearly') ? $('tier-stripe-yearly').value.trim() : '',
      mail_enabled: $('tier-mail-enabled') ? !!$('tier-mail-enabled').checked : false,
      mail_quota_mb: $('tier-mail-quota') ? (parseInt($('tier-mail-quota').value, 10) || 0) : 0,
      benefits: benefits,
    };
    var method = id ? 'PUT' : 'POST';
    var url = '/os/api/members/tiers' + (id ? '/' + encodeURIComponent(id) : '');
    var save = $('tier-save');
    if (save) { save.disabled = true; }
    api(method, url, payload).then(function (r) {
      if (r.ok) { closeModal(); done(id ? 'Tier saved.' : 'Tier created.'); }
      else { if (save) { save.disabled = false; } alert('Could not save the tier.'); }
    }).catch(function () { if (save) { save.disabled = false; } alert('Network error.'); });
  }

  // ── Team & roles (admin only) ──────────────────────────────────────────
  function createUser(teamForm) {
    var status = teamForm.querySelector('[data-team-status]');
    var payload = {
      email: teamForm.querySelector('[data-u-email]').value.trim(),
      name: teamForm.querySelector('[data-u-name]').value.trim(),
      password: teamForm.querySelector('[data-u-pass]').value,
      role: teamForm.querySelector('[data-u-role]').value,
    };
    if (status) { status.textContent = 'Creating…'; }
    api('POST', '/os/api/users', payload).then(function (r) {
      return r.json().then(function (d) { return { ok: r.ok, d: d }; });
    }).then(function (res) {
      if (res.ok) { done('Account created for ' + payload.email + '.'); }
      else if (status) { status.textContent = errText(res.d) || 'Could not create the account.'; }
    }).catch(function () { if (status) { status.textContent = 'Network error.'; } });
  }

  // Assign / change a member's VayuMail mailbox (custom email).
  function assignMailbox(btn) {
    var domain = btn.dataset.domain || '';
    var current = btn.dataset.current || '';
    var suggestion = current ? current.split('@')[0] : (btn.dataset.email || '').split('@')[0];
    // Two steps, both in the console's own dialog (window.prompt was the shortest
    // way to ask twice; vpPrompt keeps the same contract — null when cancelled).
    vpPrompt({
      title: 'Mailbox for this member',
      message: 'The part before @' + domain + '.',
      label: 'Mailbox name',
      value: suggestion,
      placeholder: 'name',
      confirm: 'Continue',
    }, function (local) {
      if (local === null) { return; }
      local = (local || '').trim();
      if (!local) { alert('Enter a mailbox name.'); return; }
      vpPrompt({
        title: 'Set the mailbox password',
        message: 'For ' + local + '@' + domain + ' — at least 8 characters.',
        label: 'Password',
        type: 'password',
        autocomplete: 'new-password',
        confirm: 'Create mailbox',
      }, function (pass) {
        if (pass === null) { return; }
        if ((pass || '').length < 8) { alert('Password must be at least 8 characters.'); return; }
        api('POST', '/os/api/users/' + encodeURIComponent(btn.dataset.email) + '/mailbox', { local: local, pass: pass }).then(function (r) {
          return r.json().then(function (d) { return { ok: r.ok, d: d }; });
        }).then(function (res) {
          if (res.ok) { done('Mailbox ' + local + '@' + domain + ' assigned.'); }
          else { alert(errText(res.d) || 'Could not assign the mailbox.'); }
        }).catch(function () { alert('Network error.'); });
      });
    });
  }

  // ── Clicks ─────────────────────────────────────────────────────────────
  document.addEventListener('click', function (e) {
    var t = e.target;
    if (!t || !t.closest) { return; }
    var btn;
    if (t.id === 'tier-modal') { closeModal(); return; } // the backdrop, not the dialog
    if (t.closest('#tier-cancel, #tier-cancel-2')) { closeModal(); return; }
    if (t.closest('[data-new-tier]')) { openModal(null); return; }
    if ((btn = t.closest('[data-edit-tier]'))) { openModal(btn.dataset); return; }

    if ((btn = t.closest('[data-archive-tier]'))) {
      var tierID = btn.dataset.id;
      vpConfirm({ title: 'Archive this tier', message: 'Existing members keep their plan.', confirm: 'Archive' }, function () {
        api('DELETE', '/os/api/members/tiers/' + encodeURIComponent(tierID)).then(function (r) {
          if (r.ok) { done('Tier archived.'); } else { alert('Could not archive the tier.'); }
        });
      });
      return;
    }

    if ((btn = t.closest('[data-add-label]'))) {
      var forEmail = btn.dataset.email;
      vpPrompt({
        title: 'Add a label',
        message: 'Labels are private to the operator; members never see them.',
        label: 'Label for ' + forEmail,
        placeholder: 'vip',
      }, function (label) {
        if (!label) { return; }
        api('POST', '/os/api/members/' + encodeURIComponent(forEmail) + '/labels', { label: label }).then(function (r) {
          if (r.ok) { done('Label added.'); } else { alert('Could not add the label.'); }
        });
      });
      return;
    }
    if ((btn = t.closest('[data-remove-label]'))) {
      api('DELETE', '/os/api/members/' + encodeURIComponent(btn.dataset.email) + '/labels/' + encodeURIComponent(btn.dataset.label)).then(function (r) {
        if (r.ok) { done('Label removed.'); } else { alert('Could not remove the label.'); }
      });
      return;
    }

    // Cancel a member's subscription. Default is a graceful cancellation at the
    // end of the paid period; holding Shift while clicking revokes access now.
    if ((btn = t.closest('[data-cancel-member]'))) {
      var immediate = !!e.shiftKey;
      var cancelEmail = btn.dataset.email;
      vpConfirm({
        title: immediate ? 'Cancel and revoke access now' : 'Cancel at period end',
        message: immediate
          ? 'Immediately cancel and revoke access for ' + cancelEmail + '?'
          : 'Cancel ' + cancelEmail + ' at the end of their paid period? They keep access until then.',
        confirm: immediate ? 'Revoke now' : 'Cancel at period end',
      }, function () {
        api('PUT', '/os/api/members/' + encodeURIComponent(cancelEmail) + '/cancel', { immediate: immediate }).then(function (r) {
          if (r.ok) { done(immediate ? 'Access revoked.' : 'Cancelled at the end of the paid period.'); }
          else { alert('Could not cancel the subscription.'); }
        }).catch(function () { alert('Network error.'); });
      });
      return;
    }

    // Remove a member who never confirmed their address. Only ever offered on
    // unconfirmed rows, and the server refuses to delete a verified member, so a
    // real account cannot be lost here by mistake.
    if ((btn = t.closest('[data-remove-member]'))) {
      var removeEmail = btn.dataset.email;
      vpConfirm({
        title: 'Remove this unconfirmed address',
        message: 'Remove ' + removeEmail + '? This address was never confirmed. If the person is real they can still join later by opening a sign-in link.',
        confirm: 'Remove',
      }, function () {
        api('DELETE', '/os/api/members/' + encodeURIComponent(removeEmail)).then(function (r) {
          if (r.ok) { done('Removed ' + removeEmail + '.'); } else { alert('Could not remove that member.'); }
        }).catch(function () { alert('Network error.'); });
      });
      return;
    }

    if ((btn = t.closest('[data-purge-unverified]'))) {
      var purge = btn;
      var n = purge.dataset.count || 'all';
      vpConfirm({
        title: 'Remove unconfirmed addresses',
        message: 'Remove ' + n + ' unconfirmed ' + (n === '1' ? 'address' : 'addresses') + '? Verified members are not affected.',
        confirm: 'Remove',
      }, function () {
        purge.disabled = true;
        api('POST', '/os/api/members/unverified/purge', {}).then(function (r) {
          if (r.ok) { done('Unconfirmed addresses removed.'); } else { purge.disabled = false; alert('Could not remove them.'); }
        }).catch(function () { purge.disabled = false; alert('Network error.'); });
      });
      return;
    }

    if ((btn = t.closest('[data-delete-user]'))) {
      var userEmail = btn.dataset.email;
      vpConfirm({
        title: 'Remove this account',
        message: 'Remove ' + userEmail + '? Their account and access are revoked.',
        confirm: 'Remove',
      }, function () {
        api('DELETE', '/os/api/users/' + encodeURIComponent(userEmail)).then(function (r) {
          if (r.ok) { done('Account removed.'); } else { alert('Could not remove the account.'); }
        });
      });
      return;
    }

    if ((btn = t.closest('[data-assign-mailbox]'))) { assignMailbox(btn); }
  });

  // ── Selects: a member's tier, a user's role ─────────────────────────────
  document.addEventListener('change', function (e) {
    var sel = e.target;
    if (!sel || !sel.matches) { return; }
    if (sel.matches('[data-member-tier]')) {
      api('PUT', '/os/api/members/' + encodeURIComponent(sel.dataset.email) + '/tier', { tier: sel.value }).then(function (r) {
        if (r.ok) { done('Tier changed.'); } else { sel.value = renderedValue(sel); alert('Could not change the tier.'); }
      }).catch(function () { sel.value = renderedValue(sel); });
      return;
    }
    if (sel.matches('[data-user-role]')) {
      api('PUT', '/os/api/users/' + encodeURIComponent(sel.dataset.email) + '/role', { role: sel.value }).then(function (r) {
        return r.json().then(function (d) { return { ok: r.ok, d: d }; });
      }).then(function (res) {
        if (res.ok) { done('Role changed.'); }
        else { sel.value = renderedValue(sel); alert(errText(res.d) || 'Could not change the role.'); }
      }).catch(function () { sel.value = renderedValue(sel); });
    }
  });

  // ── Forms ──────────────────────────────────────────────────────────────
  document.addEventListener('submit', function (e) {
    var f = e.target;
    if (!f || !f.matches) { return; }
    if (f.id === 'tier-form') { e.preventDefault(); saveTier(); return; }
    if (f.matches('[data-new-user]')) { e.preventDefault(); createUser(f); }
  });

  // ── Client-side member search ───────────────────────────────────────────
  document.addEventListener('input', function (e) {
    var search = e.target;
    if (!search || !search.matches || !search.matches('[data-member-search]')) { return; }
    var q = search.value.trim().toLowerCase();
    var shown = 0;
    document.querySelectorAll('[data-member-row]').forEach(function (row) {
      var hit = !q || (row.dataset.search || '').indexOf(q) !== -1;
      row.hidden = !hit;
      if (hit) { shown++; }
    });
    var emptyEl = document.querySelector('[data-members-empty]');
    if (emptyEl) { emptyEl.hidden = shown !== 0; }
  });
})();
