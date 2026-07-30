// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// swHarnessJS runs the real console service-worker source against a minimal fake
// ServiceWorkerGlobalScope and asserts what the fetch handler DOES, rather than
// what its source text looks like. Two behaviours are pinned:
//
//	online  navigation -> the worker must NOT call respondWith, so the browser
//	                      performs the navigation itself and resolves any server
//	                      redirect natively.
//	offline navigation -> the worker MUST answer with the branded 503 notice.
//
// The first is the regression guard. Proxying an online navigation is what
// produced ERR_TOO_MANY_REDIRECTS on every launch of the installed app: the
// proxy turned each redirect in the sign-in chain into an opaqueredirect that
// re-entered this handler, and the console signs in by redirect.
const swHarnessJS = `
const fs = require('fs');
const src = fs.readFileSync(process.argv[2], 'utf8');

const listeners = {};
global.self = { addEventListener: (t, fn) => { listeners[t] = fn; }, skipWaiting: () => {} };
global.caches = { keys: async () => [], delete: async () => true };
global.navigator = { onLine: true };
global.Response = class {
  constructor(body, init) { this.body = body; this.status = (init && init.status) || 200; }
};
global.fetch = () => { throw new Error('the worker called fetch() for a navigation'); };

(0, eval)(src);

if (typeof listeners.fetch !== 'function') { console.log('FAIL no fetch listener'); process.exit(1); }

function navigate() {
  let answered = null;
  listeners.fetch({
    request: { method: 'GET', mode: 'navigate', url: 'https://example.test/os/' },
    respondWith: (r) => { answered = r; },
  });
  return answered;
}

// 1. Online: the browser must be left alone.
global.navigator.onLine = true;
let r = navigate();
if (r !== null) { console.log('FAIL proxied an online navigation'); process.exit(1); }

// 2. Offline: the branded notice must be served.
global.navigator.onLine = false;
r = navigate();
if (r === null) { console.log('FAIL no response for an offline navigation'); process.exit(1); }
if (r.status !== 503) { console.log('FAIL offline status ' + r.status + ' want 503'); process.exit(1); }
if (String(r.body).indexOf('You are offline') < 0) { console.log('FAIL offline body'); process.exit(1); }

// 3. A non-navigation GET is never intercepted, in either state.
for (const online of [true, false]) {
  global.navigator.onLine = online;
  let answered = null;
  listeners.fetch({
    request: { method: 'GET', mode: 'cors', url: 'https://example.test/os/static/js/admin-os.js' },
    respondWith: (x) => { answered = x; },
  });
  if (answered !== null) { console.log('FAIL intercepted a subresource (onLine=' + online + ')'); process.exit(1); }
}

console.log('OK');
`

// TestOSServiceWorkerDoesNotProxyOnlineNavigations is the regression guard for
// the installed-app redirect loop. See swHarnessJS for what it asserts and why.
func TestOSServiceWorkerDoesNotProxyOnlineNavigations(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available; the worker's behaviour cannot be executed here")
	}
	dir := t.TempDir()
	swPath := filepath.Join(dir, "sw.js")
	harnessPath := filepath.Join(dir, "harness.js")
	if err := os.WriteFile(swPath, []byte(osServiceWorkerJS), 0o600); err != nil {
		t.Fatalf("write sw: %v", err)
	}
	if err := os.WriteFile(harnessPath, []byte(swHarnessJS), 0o600); err != nil {
		t.Fatalf("write harness: %v", err)
	}
	out, err := exec.Command(node, harnessPath, swPath).CombinedOutput() //nolint:gosec // fixed paths under t.TempDir
	got := strings.TrimSpace(string(out))
	if err != nil || got != "OK" {
		t.Fatalf("service-worker behaviour check failed: %s (err %v)", got, err)
	}
}
