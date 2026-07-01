/* os-theme.js — light / dark / auto theme switch for the VayuOS auth pages.
 * Same-origin (satisfies script-src 'self', no nonce). "auto" follows the OS
 * via CSS prefers-color-scheme; "light"/"dark" force the choice. The preference
 * is remembered in localStorage. The theme attribute lives on <html> (which
 * carries the .vp-os class on these pages), so the .vp-os[data-theme] token
 * overrides apply. */
(function () {
  'use strict';
  var KEY = 'vp-os-theme';
  var root = document.documentElement;

  function get() {
    try { return localStorage.getItem(KEY) || 'auto'; } catch (e) { return 'auto'; }
  }
  function norm(t) { return (t === 'light' || t === 'dark') ? t : 'auto'; }
  function mark(t) {
    var b = document.querySelectorAll('[data-set-theme]');
    for (var i = 0; i < b.length; i++) {
      b[i].setAttribute('aria-pressed', b[i].getAttribute('data-set-theme') === t ? 'true' : 'false');
    }
  }
  function apply(t) {
    t = norm(t);
    root.setAttribute('data-theme', t);
    try { localStorage.setItem(KEY, t); } catch (e) {}
    mark(t);
  }

  // Apply the stored preference as early as possible to minimise any flash.
  var cur = norm(get());
  root.setAttribute('data-theme', cur);

  function wire() {
    mark(cur);
    var b = document.querySelectorAll('[data-set-theme]');
    for (var i = 0; i < b.length; i++) {
      b[i].addEventListener('click', function () { apply(this.getAttribute('data-set-theme')); });
    }
  }
  if (document.readyState !== 'loading') { wire(); }
  else { document.addEventListener('DOMContentLoaded', wire); }
})();
