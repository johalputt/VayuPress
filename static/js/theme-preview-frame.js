/*
 * theme-preview-frame.js — runs INSIDE the Theme Studio live-preview iframe.
 *
 * Served same-origin under /os/static/js, so it is always allowed by the strict
 * CSP (script-src 'self') without needing a per-request nonce — which makes the
 * live hot-swap robust where an inline nonce'd script can silently fail.
 *
 * Protocol (same-origin only):
 *   - On load, posts {type:'vayu-preview-ready'} to the parent.
 *   - On {type:'vayu-preview-css', href} from the parent, swaps the theme
 *     stylesheet WITHOUT reloading the document (no flicker, scroll preserved),
 *     then posts {type:'vayu-preview-ack'} so the parent knows it landed.
 *   - Only accepts same-origin messages and same-origin preview stylesheet hrefs.
 */
(function () {
  'use strict';

  var link = document.getElementById('vayu-theme-css');

  function okHref(h) {
    return typeof h === 'string' && h.indexOf('/os/theme/preview.css?') === 0;
  }

  function ackParent() {
    try {
      if (window.parent && window.parent !== window) {
        window.parent.postMessage({ type: 'vayu-preview-ack' }, location.origin);
      }
    } catch (_) { /* ignore */ }
  }

  window.addEventListener('message', function (e) {
    if (e.origin !== location.origin) return;
    var d = e.data || {};
    if (d.type !== 'vayu-preview-css' || !okHref(d.href) || !link) return;
    // Swap by cloning the <link> with the new href; remove the old one only
    // after the new stylesheet has loaded, so there is no flash of unstyled
    // content and the scroll position is preserved.
    var next = link.cloneNode(false);
    next.href = d.href;
    next.addEventListener('load', function () {
      if (link && link !== next && link.parentNode) link.parentNode.removeChild(link);
      link = next;
      ackParent();
    });
    // If the swap errors, still ack so the parent doesn't hang on its fallback.
    next.addEventListener('error', ackParent);
    link.parentNode.insertBefore(next, link.nextSibling);
  });

  // Announce readiness so the parent can flush any pending stylesheet.
  try {
    if (window.parent && window.parent !== window) {
      window.parent.postMessage({ type: 'vayu-preview-ready' }, location.origin);
    }
  } catch (_) { /* ignore */ }
})();
