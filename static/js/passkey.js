/* SPDX-License-Identifier: Apache-2.0 */
/* Passkeys: "Sign in with a passkey" on the sign-in page, and "Add a passkey"
   where an account or a mailbox manages its own sign-in. The server starts
   each ceremony and checks its answer (cmd/vayupress/passkey.go); this file
   only carries the options to the browser and the browser's answer back,
   converting between the base64url the server speaks and the bytes the
   browser's API takes. */
(function () {
  'use strict';
  if (!window.PublicKeyCredential || !navigator.credentials) {
    document.querySelectorAll('[data-passkey-signin],[data-passkey-add]').forEach(function (b) { b.hidden = true; });
    return;
  }

  function fromB64(s) {
    s = s.replace(/-/g, '+').replace(/_/g, '/');
    while (s.length % 4) s += '=';
    var bin = atob(s), out = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out.buffer;
  }
  function toB64(buf) {
    var bytes = new Uint8Array(buf), bin = '';
    for (var i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
    return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }
  function csrf() {
    var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }
  function post(url, body) {
    return fetch(url, {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() },
      body: body === undefined ? '{}' : JSON.stringify(body)
    }).then(function (r) {
      return r.json().catch(function () { return {}; }).then(function (j) {
        if (!r.ok) throw new Error((j && j.error && j.error.message) || ('That did not go through (' + r.status + ').'));
        return j;
      });
    });
  }

  // Sign in: no name is asked for; the browser offers the passkeys it holds.
  var signin = document.querySelector('[data-passkey-signin]');
  if (signin) {
    var said = document.querySelector('[data-passkey-signin-say]');
    signin.hidden = false;
    signin.addEventListener('click', function () {
      if (said) said.textContent = '';
      var next = (document.querySelector('input[name=next]') || {}).value || '';
      post('/os/passkey/begin').then(function (start) {
        var o = start.options.publicKey;
        o.challenge = fromB64(o.challenge);
        (o.allowCredentials || []).forEach(function (c) { c.id = fromB64(c.id); });
        return navigator.credentials.get({ publicKey: o }).then(function (cred) {
          return post('/os/passkey/finish?ceremony=' + encodeURIComponent(start.ceremony) + '&next=' + encodeURIComponent(next), {
            id: cred.id, rawId: toB64(cred.rawId), type: cred.type,
            response: {
              clientDataJSON: toB64(cred.response.clientDataJSON),
              authenticatorData: toB64(cred.response.authenticatorData),
              signature: toB64(cred.response.signature),
              userHandle: cred.response.userHandle ? toB64(cred.response.userHandle) : null
            }
          });
        });
      }).then(function (done) {
        window.location.assign(done.next || '/os');
      }).catch(function (e) {
        if (said) said.textContent = e && e.name === 'NotAllowedError' ? 'No passkey was used.' : (e.message || 'The passkey did not sign you in.');
      });
    });
  }

  // Add: a passkey for the account or mailbox this session is.
  document.querySelectorAll('[data-passkey-add]').forEach(function (btn) {
    var box = btn.closest('[data-passkeys]');
    var say = box && box.querySelector('[data-passkey-say]');
    var nameEl = box && box.querySelector('[data-passkey-name]');
    btn.addEventListener('click', function () {
      if (say) say.textContent = '';
      btn.disabled = true;
      post('/os/api/passkeys/begin').then(function (start) {
        var o = start.options.publicKey;
        o.challenge = fromB64(o.challenge);
        o.user.id = fromB64(o.user.id);
        (o.excludeCredentials || []).forEach(function (c) { c.id = fromB64(c.id); });
        return navigator.credentials.create({ publicKey: o }).then(function (cred) {
          var name = nameEl && nameEl.value ? nameEl.value : '';
          return post('/os/api/passkeys/finish?ceremony=' + encodeURIComponent(start.ceremony) + '&name=' + encodeURIComponent(name), {
            id: cred.id, rawId: toB64(cred.rawId), type: cred.type,
            response: {
              clientDataJSON: toB64(cred.response.clientDataJSON),
              attestationObject: toB64(cred.response.attestationObject),
              transports: cred.response.getTransports ? cred.response.getTransports() : []
            }
          });
        });
      }).then(function () {
        window.location.reload();
      }).catch(function (e) {
        btn.disabled = false;
        if (say) say.textContent = e && e.name === 'InvalidStateError' ? 'This device already has a passkey here.'
          : e && e.name === 'NotAllowedError' ? 'No passkey was made.' : (e.message || 'The passkey was not added.');
      });
    });
  });
})();
