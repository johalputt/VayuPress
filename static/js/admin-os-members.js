/* admin-os-members.js — VayuOS Members console interactions.
 *
 * Wires the tier editor modal, tier archive, per-member tier changes, and
 * label add/remove to the session-authenticated /os/api/members/* endpoints.
 * Every mutating request carries the double-submit CSRF token read from the
 * vp_csrf cookie. No inline handlers — listeners are attached here so the page
 * stays within the strict script-src 'self' 'nonce-…' CSP. */
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

  function reload() { window.location.reload(); }

  // ── Tier editor modal ──────────────────────────────────────────────────
  var modal = document.getElementById('tier-modal');
  var form = document.getElementById('tier-form');
  var titleEl = document.getElementById('tier-modal-title');
  var $ = function (id) { return document.getElementById(id); };

  function openModal(data) {
    data = data || {};
    $('tier-id').value = data.id || '';
    $('tier-name').value = data.name || '';
    $('tier-desc').value = data.description || '';
    $('tier-monthly').value = data.monthly || 0;
    $('tier-yearly').value = data.yearly || 0;
    $('tier-currency').value = data.currency || 'USD';
    $('tier-visibility').value = data.visibility || 'public';
    $('tier-benefits').value = data.benefits || '';
    if (titleEl) { titleEl.textContent = data.id ? 'Edit tier' : 'New tier'; }
    if (modal) { modal.removeAttribute('hidden'); }
  }
  function closeModal() { if (modal) { modal.setAttribute('hidden', ''); } }

  var newBtn = document.querySelector('[data-new-tier]');
  if (newBtn) { newBtn.addEventListener('click', function () { openModal(null); }); }

  document.querySelectorAll('[data-edit-tier]').forEach(function (btn) {
    btn.addEventListener('click', function () { openModal(btn.dataset); });
  });

  ['tier-cancel', 'tier-cancel-2'].forEach(function (id) {
    var b = $(id);
    if (b) { b.addEventListener('click', closeModal); }
  });
  if (modal) {
    modal.addEventListener('click', function (e) { if (e.target === modal) { closeModal(); } });
  }

  if (form) {
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      var id = $('tier-id').value;
      var benefits = $('tier-benefits').value.split('\n').map(function (s) { return s.trim(); }).filter(Boolean);
      var payload = {
        name: $('tier-name').value.trim(),
        description: $('tier-desc').value.trim(),
        monthly_cents: parseInt($('tier-monthly').value, 10) || 0,
        yearly_cents: parseInt($('tier-yearly').value, 10) || 0,
        currency: ($('tier-currency').value || 'USD').trim(),
        visibility: $('tier-visibility').value,
        benefits: benefits,
      };
      var method = id ? 'PUT' : 'POST';
      var url = '/os/api/members/tiers' + (id ? '/' + encodeURIComponent(id) : '');
      var save = $('tier-save');
      if (save) { save.disabled = true; }
      api(method, url, payload).then(function (r) {
        if (r.ok) { reload(); } else { if (save) { save.disabled = false; } alert('Could not save the tier.'); }
      }).catch(function () { if (save) { save.disabled = false; } alert('Network error.'); });
    });
  }

  // ── Archive a tier ─────────────────────────────────────────────────────
  document.querySelectorAll('[data-archive-tier]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      if (!window.confirm('Archive this tier? Existing members keep their plan.')) { return; }
      api('DELETE', '/os/api/members/tiers/' + encodeURIComponent(btn.dataset.id)).then(function (r) {
        if (r.ok) { reload(); } else { alert('Could not archive the tier.'); }
      });
    });
  });

  // ── Change a member's tier ─────────────────────────────────────────────
  document.querySelectorAll('[data-member-tier]').forEach(function (sel) {
    var previous = sel.value;
    sel.addEventListener('change', function () {
      var email = sel.dataset.email;
      api('PUT', '/os/api/members/' + encodeURIComponent(email) + '/tier', { tier: sel.value }).then(function (r) {
        if (r.ok) { reload(); } else { sel.value = previous; alert('Could not change the tier.'); }
      }).catch(function () { sel.value = previous; });
    });
  });

  // ── Labels ─────────────────────────────────────────────────────────────
  document.querySelectorAll('[data-add-label]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var label = window.prompt('Add a label for ' + btn.dataset.email);
      if (!label) { return; }
      api('POST', '/os/api/members/' + encodeURIComponent(btn.dataset.email) + '/labels', { label: label }).then(function (r) {
        if (r.ok) { reload(); } else { alert('Could not add the label.'); }
      });
    });
  });
  document.querySelectorAll('[data-remove-label]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      api('DELETE', '/os/api/members/' + encodeURIComponent(btn.dataset.email) + '/labels/' + encodeURIComponent(btn.dataset.label)).then(function (r) {
        if (r.ok) { reload(); } else { alert('Could not remove the label.'); }
      });
    });
  });

  // ── Team & roles (admin only) ──────────────────────────────────────────
  var teamForm = document.querySelector('[data-new-user]');
  if (teamForm) {
    teamForm.addEventListener('submit', function (e) {
      e.preventDefault();
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
        if (res.ok) { reload(); }
        else if (status) { status.textContent = (res.d && (res.d.message || res.d.error)) || 'Could not create the account.'; }
      }).catch(function () { if (status) { status.textContent = 'Network error.'; } });
    });
  }

  document.querySelectorAll('[data-user-role]').forEach(function (sel) {
    var previous = sel.value;
    sel.addEventListener('change', function () {
      api('PUT', '/os/api/users/' + encodeURIComponent(sel.dataset.email) + '/role', { role: sel.value }).then(function (r) {
        return r.json().then(function (d) { return { ok: r.ok, d: d }; });
      }).then(function (res) {
        if (res.ok) { reload(); }
        else { sel.value = previous; alert((res.d && (res.d.message || res.d.error)) || 'Could not change the role.'); }
      }).catch(function () { sel.value = previous; });
    });
  });

  document.querySelectorAll('[data-delete-user]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      if (!window.confirm('Remove ' + btn.dataset.email + '? Their account and access are revoked.')) { return; }
      api('DELETE', '/os/api/users/' + encodeURIComponent(btn.dataset.email)).then(function (r) {
        if (r.ok) { reload(); } else { alert('Could not remove the account.'); }
      });
    });
  });
})();
