// SPDX-License-Identifier: Apache-2.0

package render

import (
	"crypto/sha256"
	"encoding/hex"
	"html/template"
)

// LinkProofJS is the proof of work a browser does before the install mails a
// sign-in link to an address that is not a member yet. The server hands out a
// signed challenge (/members/link-proof); this solves it and sends the answer
// with the form, as two hidden fields, or with the portal's request.
//
// Why work and not a puzzle: the form was abused by senders that post it
// straight from a script, hundreds of times, to mail links to strangers. A
// proof costs a reader's browser well under a second and each one buys one
// link, so a list of a thousand addresses costs a thousand proofs, and a
// sender that never runs the script never gets one. A member's own link
// needs no proof, so a page cached before this script existed still signs its
// members in.
//
// Hashes are asked for 256 at a time: SubtleCrypto answers each in a promise,
// and one at a time spends most of the budget waiting on the event loop.
const LinkProofJS = `(function () {
  'use strict';
  // A page with two forms carries the tag twice; the second copy stays out,
  // or each submit would be proved and sent twice.
  if (window.vpLinkProof) { return; }
  function hex(buf) { var b = new Uint8Array(buf), s = ''; for (var i = 0; i < b.length; i++) { s += ('0' + b[i].toString(16)).slice(-2); } return s; }
  function zeros(h, n) { for (var i = 0; i < n; i++) { if (h.charAt(i) !== '0') { return false; } } return true; }
  function digest(s) { return crypto.subtle.digest('SHA-256', new TextEncoder().encode(s)).then(hex); }
  function solve(ch) {
    var from = 0;
    function batch() {
      var hs = [];
      for (var k = 0; k < 256; k++) { hs.push(digest(ch.salt + ':' + (from + k))); }
      return Promise.all(hs).then(function (out) {
        for (var k = 0; k < out.length; k++) { if (zeros(out[k], ch.difficulty)) { return String(from + k); } }
        from += out.length;
        return from > 8000000 ? null : batch();
      });
    }
    return batch();
  }
  window.vpLinkProof = function () {
    return fetch('/members/link-proof', { credentials: 'same-origin', cache: 'no-store' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (ch) {
        if (!ch || !ch.salt) { return null; }
        return solve(ch).then(function (n) { return n === null ? null : { proof: JSON.stringify(ch), nonce: n }; });
      })
      .catch(function () { return null; });
  };
  function put(f, name, value) {
    var el = f.querySelector('input[name="' + name + '"]');
    if (!el) { el = document.createElement('input'); el.type = 'hidden'; el.name = name; f.appendChild(el); }
    el.value = value;
  }
  // Every form that asks for a link posts to /members/login. It is held,
  // proved, and sent by submit(), which does not fire this event again.
  document.addEventListener('submit', function (e) {
    var f = e.target;
    if (!f || f.tagName !== 'FORM' || f.getAttribute('action') !== '/members/login') { return; }
    e.preventDefault();
    window.vpLinkProof().then(function (p) {
      if (p) { put(f, 'proof', p.proof); put(f, 'nonce', p.nonce); }
      HTMLFormElement.prototype.submit.call(f);
    });
  }, true);
})();
`

var linkProofJSHash = func() string {
	sum := sha256.Sum256([]byte(LinkProofJS))
	return hex.EncodeToString(sum[:8])
}()

// LinkProofJSLink is the <script> tag a page with a sign-in link form carries.
func LinkProofJSLink() template.HTML {
	return template.HTML(`<script src="/static/js/link-proof.js?v=` + linkProofJSHash + `" defer></script>`)
}
