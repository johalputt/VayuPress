package render

// pwa.go — the public site's service-worker registration.
//
// Serving /sw.js is not enough: a service worker only exists once a page calls
// navigator.serviceWorker.register. Until this script shipped, the public site
// served a worker nobody registered, so the site never met Chrome's installability
// bar. "Install" then quietly produced a legacy home-screen shortcut instead of a
// WebAPK — a launcher database entry rather than an installed package, which many
// Android builds discard when the device restarts.
//
// It is a same-origin external file so it satisfies the strict script-src 'self'
// CSP without a nonce, and it is deferred, so registration never competes with
// first paint.

import (
	"crypto/sha256"
	"encoding/hex"
	"html/template"
)

// PWARegisterJS registers the public service worker. Deliberately tiny and
// failure-tolerant: an unsupported or blocked worker must never break the page,
// and registration is delayed to the load event so it cannot contend with
// rendering on a slow phone.
const PWARegisterJS = `(function () {
  'use strict';
  if (!('serviceWorker' in navigator)) { return; }
  // Mark a standalone launch so CSS can adapt (an installed app has no browser
  // chrome of its own). Detected from display-mode rather than a query flag on
  // start_url: a flag would split analytics between "/" and "/?app=1" and give the
  // cache two entries for one page.
  try {
    if (window.matchMedia('(display-mode: standalone)').matches ||
        window.matchMedia('(display-mode: minimal-ui)').matches ||
        window.navigator.standalone === true) {
      document.documentElement.setAttribute('data-app', 'installed');
    }
  } catch (e) { /* matchMedia unavailable — not worth failing over */ }

  function register() {
    navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch(function () {
      // A failed registration only costs offline support and installability; it
      // must never surface to the reader.
    });
  }
  if (document.readyState === 'complete') { register(); }
  else { window.addEventListener('load', register); }
})();`

// pwaRegisterJSHash versions the script URL so a change is picked up.
var pwaRegisterJSHash = func() string {
	sum := sha256.Sum256([]byte(PWARegisterJS))
	return hex.EncodeToString(sum[:8])
}()

// PWARegisterJSLink returns the deferred <script> tag for the registration script.
func PWARegisterJSLink() template.HTML {
	return template.HTML(`<script src="/static/js/pwa.js?v=` + pwaRegisterJSHash + `" defer></script>`)
}

// PWAHeadTags returns the head tags an installable app needs beyond the manifest
// link: iOS has no manifest support for icons or standalone display, so it is
// driven entirely by these apple-* tags. Without them an iPhone "Add to Home
// Screen" gets a screenshot of the page as its icon and opens in a browser chrome.
func PWAHeadTags() template.HTML {
	return template.HTML(`<link rel="manifest" href="/manifest.json">` +
		`<meta name="mobile-web-app-capable" content="yes">` +
		`<meta name="apple-mobile-web-app-capable" content="yes">` +
		`<meta name="apple-mobile-web-app-status-bar-style" content="black-translucent">` +
		`<link rel="apple-touch-icon" sizes="180x180" href="/static/icons/webapp-apple-180.png">` +
		`<link rel="icon" type="image/png" sizes="192x192" href="/static/icons/webapp-192.png">`)
}
