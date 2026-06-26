/* admin-os-profile.js — self-service author profile editor.
 *
 * Gathers the profile form (name, bio, avatar, social links), posts it as JSON
 * with the CSRF token from the vp_csrf cookie, and shows a live character count
 * for the 250-char bio. No inline handlers (strict CSP). */
(function () {
  'use strict';

  function cookie(name) {
    var m = document.cookie.match('(^|;)\\s*' + name + '\\s*=\\s*([^;]+)');
    return m ? m.pop() : '';
  }

  var form = document.querySelector('[data-profile-form]');
  if (!form) { return; }

  var bio = form.querySelector('[data-p-bio]');
  var counter = form.querySelector('[data-bio-count]');
  function updateCount() {
    if (bio && counter) {
      counter.textContent = bio.value.length + ' / 250';
    }
  }
  if (bio) { bio.addEventListener('input', updateCount); updateCount(); }

  // Live avatar preview — reflects the URL into the fixed cropped thumbnail.
  var avatarInput = form.querySelector('[data-p-avatar]');
  var preview = form.querySelector('[data-avatar-preview]');
  var emptyHint = form.querySelector('[data-avatar-empty]');
  if (avatarInput && preview) {
    avatarInput.addEventListener('input', function () {
      var url = avatarInput.value.trim();
      if (url) {
        preview.src = url;
        preview.removeAttribute('hidden');
        if (emptyHint) { emptyHint.setAttribute('hidden', ''); }
      } else {
        preview.setAttribute('hidden', '');
        preview.removeAttribute('src');
        if (emptyHint) { emptyHint.removeAttribute('hidden'); }
      }
    });
    // A broken image URL falls back to the "no photo" hint.
    preview.addEventListener('error', function () {
      preview.setAttribute('hidden', '');
      if (emptyHint) { emptyHint.removeAttribute('hidden'); }
    });
  }

  form.addEventListener('submit', function (e) {
    e.preventDefault();
    var status = form.querySelector('[data-p-status]');
    var socials = {};
    form.querySelectorAll('[data-social]').forEach(function (inp) {
      var v = inp.value.trim();
      if (v) { socials[inp.getAttribute('data-social')] = v; }
    });
    var payload = {
      name: form.querySelector('[data-p-name]').value.trim(),
      bio: bio ? bio.value.trim() : '',
      avatar_url: form.querySelector('[data-p-avatar]').value.trim(),
      socials: socials,
    };
    if (status) { status.textContent = 'Saving…'; }
    fetch('/os/api/profile', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': cookie('vp_csrf') },
      body: JSON.stringify(payload),
    }).then(function (r) {
      return r.json().then(function (d) { return { ok: r.ok, d: d }; });
    }).then(function (res) {
      if (res.ok) {
        if (status) { status.textContent = 'Saved.'; }
        window.setTimeout(function () { window.location.reload(); }, 400);
      } else {
        if (status) { status.textContent = (res.d && (res.d.message || res.d.error)) || 'Could not save.'; }
      }
    }).catch(function () {
      if (status) { status.textContent = 'Network error.'; }
    });
  });
})();
