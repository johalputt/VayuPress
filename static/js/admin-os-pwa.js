(function () {
  'use strict';

  // The browser half of the install-health check.
  //
  // The server can prove what IT serves. It cannot prove what reaches a client:
  // a CDN or reverse proxy in front of the origin can challenge /manifest.json or
  // the icons, and the WebAPK minting server — which is not a browser and cannot
  // solve a challenge — then fails to build the package. The origin looks perfect
  // while installation quietly degrades to a launcher shortcut.
  //
  // These checks run from the page, through whatever sits in front of the origin,
  // and report what actually came back.

  var host = document.querySelector('[data-pwa-probe-rows]');
  if (!host) { return; }

  function row(ok, name, detail) {
    var el = document.createElement('div');
    el.className = 'pwa-row pwa-row--' + (ok === null ? 'warn' : (ok ? 'ok' : 'bad'));
    var mark = document.createElement('span');
    mark.className = 'pwa-mark';
    mark.textContent = ok === null ? '?' : (ok ? '✓' : '✕');
    var body = document.createElement('div');
    var t = document.createElement('div');
    t.className = 'pwa-name';
    t.textContent = name;
    body.appendChild(t);
    if (detail) {
      var d = document.createElement('div');
      d.className = 'pwa-detail';
      d.textContent = detail;
      body.appendChild(d);
    }
    el.appendChild(mark);
    el.appendChild(body);
    host.appendChild(el);
  }

  host.textContent = '';

  // 1. Is a worker actually registered and active? This is the check that would
  //    have caught the original bug: /sw.js was served, and nothing registered it.
  //    Careful: this runs from the CONSOLE, which registers /os/sw.js with scope
  //    /os/. The PUBLIC worker (scope /) is registered by public pages, so its
  //    absence here means "the public site has not been opened in this browser
  //    yet", not "registration is broken". Reporting that as a failure would make
  //    the diagnostic lie, which is worse than not having one.
  if (!('serviceWorker' in navigator)) {
    row(false, 'Service workers supported', 'This browser does not support them');
  } else {
    navigator.serviceWorker.getRegistrations().then(function (regs) {
      var scopes = (regs || []).map(function (r) {
        var s = r.scope.replace(location.origin, '');
        var state = (r.active && 'active') || (r.installing && 'installing') ||
          (r.waiting && 'waiting') || 'unknown';
        return s + ' (' + state + ')';
      });
      var pub = (regs || []).filter(function (r) {
        return r.scope.replace(location.origin, '') === '/' && r.active;
      });
      if (pub.length) {
        row(true, 'The public service worker is registered and active', scopes.join(', '));
      } else {
        row(null, 'The public service worker is registered',
          (scopes.length ? 'Registered here: ' + scopes.join(', ') + '. ' : 'None registered. ') +
          'Scope / is registered by the PUBLIC site, not the console — open ' + location.origin +
          ' once in this browser, then reload this page to confirm.');
      }
    }).catch(function (e) {
      row(false, 'The public service worker is registered', String(e && e.message || e));
    });
  }

  // 2. Does the manifest survive the trip to the client? A CDN challenge answers
  //    with HTML, so checking the content type and parsing the body catches it
  //    where a status code alone would not.
  fetch('/manifest.json', { credentials: 'omit' }).then(function (res) {
    var ct = res.headers.get('Content-Type') || '';
    if (!res.ok) {
      row(false, 'Manifest reaches the browser', 'HTTP ' + res.status +
        ' — something in front of the origin is refusing it');
      return null;
    }
    return res.text().then(function (body) {
      if (ct.indexOf('json') === -1) {
        row(false, 'Manifest reaches the browser',
          'Served as "' + ct + '" — an edge or proxy replaced it, most likely with a challenge page');
        return null;
      }
      try {
        var m = JSON.parse(body);
        row(true, 'Manifest reaches the browser intact', (m.name || '') + ' · id ' + (m.id || '(none)'));
        return m;
      } catch (e) {
        row(false, 'Manifest reaches the browser', 'Body is not JSON — something rewrote it in transit');
        return null;
      }
    });
  }).then(function (m) {
    if (!m || !m.icons) { return; }
    // 3. Every icon, fetched the way the minting server would.
    return Promise.all(m.icons.map(function (ic) {
      return fetch(ic.src, { credentials: 'omit' }).then(function (r) {
        var t = r.headers.get('Content-Type') || '';
        return { src: ic.src, ok: r.ok && t.indexOf('image') === 0, status: r.status, type: t };
      }).catch(function () { return { src: ic.src, ok: false, status: 0, type: 'network error' }; });
    })).then(function (results) {
      var bad = results.filter(function (r) { return !r.ok; });
      if (bad.length === 0) {
        row(true, 'Every manifest icon is fetchable', results.length + ' icons');
      } else {
        row(false, 'Every manifest icon is fetchable',
          bad.map(function (b) { return b.src + ' → ' + (b.status || b.type); }).join(', ') +
          ' — the package cannot be built without them');
      }
    });
  }).catch(function () { /* reported above */ });

  // 4. Is this page already running as an installed app?
  try {
    var standalone = window.matchMedia('(display-mode: standalone)').matches ||
      window.navigator.standalone === true;
    row(standalone ? true : null, 'Running as an installed app',
      standalone ? 'Yes — opened from the home screen' : 'No — this is a browser tab (expected here)');
  } catch (e) { /* matchMedia unavailable */ }

  // 5. The decisive one on Android: is a real package installed? getInstalledRelatedApps
  //    reports a WebAPK for this origin. A launcher SHORTCUT is not a package and
  //    does not appear — which is exactly how you tell the two apart.
  if (navigator.getInstalledRelatedApps) {
    navigator.getInstalledRelatedApps().then(function (apps) {
      if (apps && apps.length) {
        row(true, 'A real app package is installed for this site',
          apps.map(function (x) { return x.platform + (x.id ? ' ' + x.id : ''); }).join(', '));
      } else {
        row(null, 'A real app package is installed for this site',
          'None reported. If you have an icon on your home screen, it is a shortcut, not an app — ' +
          'remove it and install again.');
      }
    }).catch(function () { /* not permitted in this context */ });
  } else {
    row(null, 'Installed-app check', 'This browser cannot report installed packages (Chrome on Android can)');
  }
})();
