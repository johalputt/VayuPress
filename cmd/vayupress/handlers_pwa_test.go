package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/render"
)

// TestServiceWorkerNeverCachesPrivatePages guards the fix for the bug where the
// worker served a stale /os/login from Cache Storage — surviving hard refresh and
// browser restarts — which silently defeated the server-side "already signed in →
// redirect to /os" logic (and risked serving one operator's dashboard to another on
// a shared device).
//
// The guard now covers every per-user surface, not just the console: member
// account, plans, sign-in and verification pages depend on who is asking, and the
// worker must send all of them straight to the network. That guard MUST run before
// the navigate branch, or a navigation would be answered from cache first.
func TestServiceWorkerNeverCachesPrivatePages(t *testing.T) {
	sw := serviceWorkerJS

	for _, want := range []string{
		`p === '/os'`,
		`p.indexOf('/os/') === 0`,
		`p === '/admin'`,
		`p.indexOf('/admin/') === 0`,
		`p.indexOf('/members') === 0`,
		`p.indexOf('/signup') === 0`,
		`p.indexOf('/login') === 0`,
		`p.indexOf('/pricing') === 0`,
	} {
		if !strings.Contains(sw, want) {
			t.Errorf("service worker missing network-only guard %q", want)
		}
	}

	guardIdx := strings.Index(sw, "if (isPrivatePath(url.pathname))")
	navIdx := strings.Index(sw, "req.mode === 'navigate'")
	if guardIdx < 0 || navIdx < 0 {
		t.Fatal("service worker is missing the private-path guard or the navigate branch")
	}
	if guardIdx > navIdx {
		t.Error("the private-path guard must precede the navigate branch")
	}

	staticList := sw[strings.Index(sw, "STATIC_ASSETS"):strings.Index(sw, "function isPrivatePath")]
	if strings.Contains(staticList, "/os") {
		t.Error("STATIC_ASSETS must not precache any /os console page")
	}
}

// TestServiceWorkerHonoursServerCacheHeaders pins that the worker cannot undo the
// server's cache correctness. A response marked no-store or private, or one that
// varies on the cookie, is per-user by definition: storing it in a shared Cache
// Storage would replay one visitor's page to the next.
func TestServiceWorkerHonoursServerCacheHeaders(t *testing.T) {
	sw := serviceWorkerJS
	for _, want := range []string{
		"function mayStore(response)",
		"no-store|private",
		"Vary",
		"cookie",
		"Set-Cookie",
	} {
		if !strings.Contains(sw, want) {
			t.Errorf("mayStore must consider %q before storing a response", want)
		}
	}
	// Every cache.put must be gated on mayStore — an ungated put is the bug.
	for _, chunk := range strings.Split(sw, "cache.put(")[1:] {
		_ = chunk
	}
	if strings.Count(sw, "mayStore(res)") != strings.Count(sw, "cache.put(req, copy)") {
		t.Error("every cache.put must be guarded by mayStore")
	}
}

// TestServiceWorkerIsNetworkFirstForPages pins that an online reader gets today's
// page. The previous worker returned "cached || network", so a stale copy won
// whenever one existed — the reader had to be offline to notice it was wrong.
func TestServiceWorkerIsNetworkFirstForPages(t *testing.T) {
	sw := serviceWorkerJS
	nav := sw[strings.Index(sw, "req.mode === 'navigate'"):]
	fetchIdx := strings.Index(nav, "networkThenStore(req)")
	cacheIdx := strings.Index(nav, "caches.match(req)")
	if fetchIdx < 0 || cacheIdx < 0 {
		t.Fatal("the navigate branch must try the network and fall back to the cache")
	}
	if fetchIdx > cacheIdx {
		t.Error("pages must be network-first; the cache is only an offline fallback")
	}
	if strings.Contains(nav, "cached || network") {
		t.Error("stale-first page serving must not come back")
	}
}

// TestServiceWorkerCacheRollsWithTheBuild is the root-cause guard for "I updated the
// server and my phone still shows the old version, even after a refresh".
//
// A browser replaces a worker only when the SCRIPT BYTES change. A hand-written
// cache name is identical across releases, so the update check finds no difference,
// activate never runs, the previous cache is never purged — and because a reload is
// answered by that same worker, refreshing cannot break the loop either. Deriving
// the name from the build version is what makes an upgrade reach an installed
// device at all.
func TestServiceWorkerCacheRollsWithTheBuild(t *testing.T) {
	if !strings.Contains(serviceWorkerJS, "CACHE_NAME = 'vayupress-"+Version+"'") {
		t.Errorf("CACHE_NAME must carry the build version %q, or an upgrade never invalidates a device", Version)
	}
	if strings.Contains(serviceWorkerJS, "__BUILD__") {
		t.Error("the build placeholder was not substituted — every release would ship the same worker")
	}
	// Two different builds must produce different worker bytes; that difference IS
	// the update signal.
	a := strings.ReplaceAll(serviceWorkerJSTemplate, "__BUILD__", "1.0.0")
	b := strings.ReplaceAll(serviceWorkerJSTemplate, "__BUILD__", "1.0.1")
	if a == b {
		t.Fatal("the worker must differ between builds, or the browser never sees an update")
	}
	// And activating must drop everything that is not this build's cache.
	if !strings.Contains(serviceWorkerJS, "keys.filter(k => k !== CACHE_NAME)") {
		t.Error("activate must delete every cache that is not this build's")
	}
}

// TestServiceWorkerNeverPinsAnUnversionedAsset covers the second half of the same
// bug. Cache-first is only safe when the URL changes with the bytes. chroma.css,
// the favicons and the app icons keep one URL forever, so serving those cache-first
// pinned a device to whatever it stored on its very first visit.
func TestServiceWorkerNeverPinsAnUnversionedAsset(t *testing.T) {
	sw := serviceWorkerJS
	if !strings.Contains(sw, "function isContentVersioned(url)") ||
		!strings.Contains(sw, "url.searchParams.has('v')") {
		t.Fatal("the worker must distinguish content-versioned URLs from stable ones")
	}
	staticBranch := sw[strings.Index(sw, "url.pathname.indexOf('/static/') === 0"):strings.Index(sw, "req.mode === 'navigate'")]
	if !strings.Contains(staticBranch, "if (isContentVersioned(url))") {
		t.Error("cache-first must be gated on the URL being content-versioned")
	}
	// The unversioned path must still revalidate, and the revalidation must outlive
	// the response or the stale entry is never replaced.
	if !strings.Contains(staticBranch, "event.waitUntil(fresh") {
		t.Error("the stale-while-revalidate branch must keep its revalidation alive with waitUntil")
	}
}

// TestInstalledAppChecksForANewBuild pins the client half. An installed app is
// resumed, not navigated, and the browser's own worker update check is tied to
// navigation and throttled to once a day — so without an explicit update() on
// resume a phone can run a replaced build for a very long time.
func TestInstalledAppChecksForANewBuild(t *testing.T) {
	js := withoutComments(render.PWARegisterJS)
	for _, want := range []string{
		"visibilitychange", // resume is the only reliable moment on a phone
		"reg.update()",     // ask the server whether the worker changed
		"controllerchange", // a new worker took over → a new build is live
		"window.location.reload()",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("the registration script must %q so an installed app picks up a new build", want)
		}
	}
	// Reloading on a FIRST install would reload every first-ever page view: the
	// controller goes from none to one, which is not an update.
	if !strings.Contains(js, "hadController") {
		t.Error("the reload must be gated on an existing controller being replaced, not a first install")
	}
	// And it must not yank the page out from under somebody typing a comment.
	if !strings.Contains(js, "function editing()") {
		t.Error("an update landing mid-edit must be deferred, not applied under the reader")
	}
}

// TestSiteRegistersItsServiceWorker is the root-cause guard for "I installed the
// app and a restart deleted it".
//
// Serving /sw.js is not enough — a worker exists only once a page registers it.
// Without a registration the site fails Chrome's installability check, so Android
// silently substitutes a legacy launcher shortcut for a WebAPK. A shortcut is a
// launcher database entry, not an installed package, and many Android builds
// discard it when the device restarts. Nothing about the install flow reveals which
// of the two you got, which is why this needs a test rather than a code review.
func TestSiteRegistersItsServiceWorker(t *testing.T) {
	if !strings.Contains(render.PWARegisterJS, "navigator.serviceWorker.register('/sw.js'") {
		t.Fatal("the public site must register /sw.js, or it is not installable as a real app")
	}
	// Registration must not be able to break the page it runs on.
	if !strings.Contains(render.PWARegisterJS, "'serviceWorker' in navigator") {
		t.Error("registration must feature-detect before touching navigator.serviceWorker")
	}
	if !strings.Contains(render.PWARegisterJS, ".catch(") {
		t.Error("a failed registration must be swallowed, never surfaced to the reader")
	}

	// And the script has to actually be on the public pages. A registration nobody
	// loads is exactly the state this fixes.
	head := string(render.PWARegisterJSLink())
	if !strings.Contains(head, `src="/static/js/pwa.js?v=`) || !strings.Contains(head, "defer") {
		t.Errorf("registration script tag = %q, want a versioned deferred same-origin script", head)
	}
}

// TestPWAHeadTagsCoverBothPlatforms checks the head carries what each platform
// needs: Android reads the manifest, iOS ignores it for icons and standalone
// display and is driven entirely by the apple-* tags. Without those, an iPhone
// "Add to Home Screen" gets a page screenshot as its icon.
func TestPWAHeadTagsCoverBothPlatforms(t *testing.T) {
	head := string(render.PWAHeadTags())
	for _, want := range []string{
		`rel="manifest" href="/manifest.json"`,
		`name="apple-mobile-web-app-capable" content="yes"`,
		`rel="apple-touch-icon" sizes="180x180"`,
		`/static/icons/webapp-apple-180.png`,
	} {
		if !strings.Contains(head, want) {
			t.Errorf("PWA head tags missing %q", want)
		}
	}
}

// TestManifestMeetsInstallCriteria checks the manifest itself is installable, and
// that its icons do not lie. The previous manifest declared ONE 256x256 file as
// being both 192x192 and 512x512, and declared the unpadded brand mark maskable —
// which asks Android to crop the mark's own edges.
func TestManifestMeetsInstallCriteria(t *testing.T) {
	a := &App{}
	req := httptest.NewRequest(http.MethodGet, "/manifest.json", nil)
	rec := httptest.NewRecorder()
	a.handlePWAManifest(rec, req)

	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "manifest+json") {
		t.Errorf("Content-Type = %q, want application/manifest+json", ct)
	}

	var m struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		ShortName string `json:"short_name"`
		StartURL  string `json:"start_url"`
		Scope     string `json:"scope"`
		Display   string `json:"display"`
		Icons     []struct {
			Src     string `json:"src"`
			Sizes   string `json:"sizes"`
			Type    string `json:"type"`
			Purpose string `json:"purpose"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}

	// id pins app identity. Without it, identity comes from start_url, so changing
	// the landing page would orphan an already-installed app.
	if m.ID == "" {
		t.Error("manifest must declare a stable id")
	}
	if m.Name == "" || m.ShortName == "" || m.StartURL == "" || m.Scope == "" {
		t.Error("name, short_name, start_url and scope are all required to install")
	}
	if m.Display != "standalone" {
		t.Errorf("display = %q, want standalone", m.Display)
	}
	// Android truncates short_name under the launcher icon.
	if len(m.ShortName) > 12 {
		t.Errorf("short_name %q is %d chars; it will be clipped under the icon", m.ShortName, len(m.ShortName))
	}

	// The hard requirement: a real 192 and a real 512, each with purpose any, plus
	// a separate maskable asset. Distinct files, since one file cannot be two sizes.
	var any192, any512, maskable bool
	srcBySize := map[string]string{}
	for _, ic := range m.Icons {
		if ic.Type != "image/png" {
			t.Errorf("icon %s has type %q, want image/png", ic.Src, ic.Type)
		}
		switch {
		case ic.Purpose == "maskable":
			maskable = true
		case ic.Sizes == "192x192":
			any192 = true
			srcBySize["192"] = ic.Src
		case ic.Sizes == "512x512":
			any512 = true
			srcBySize["512"] = ic.Src
		}
	}
	if !any192 {
		t.Error("manifest needs a 192x192 icon with purpose any — the install minimum")
	}
	if !any512 {
		t.Error("manifest needs a 512x512 icon with purpose any for the launcher and splash")
	}
	if !maskable {
		t.Error("manifest needs a separate maskable icon")
	}
	if srcBySize["192"] != "" && srcBySize["192"] == srcBySize["512"] {
		t.Error("the 192 and 512 icons must be different files — one file cannot be both sizes")
	}
}

// TestServiceWorkerServedNoStore verifies the SW script itself is served
// uncacheable, so browsers pick up a new version promptly (this is what lets the
// fixed worker replace the buggy one without manual intervention).
func TestServiceWorkerServedNoStore(t *testing.T) {
	a := &App{}
	req := httptest.NewRequest(http.MethodGet, "/sw.js", nil)
	rec := httptest.NewRecorder()
	a.handleServiceWorker(rec, req)

	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") && !strings.Contains(cc, "no-store") {
		t.Errorf("/sw.js Cache-Control = %q, want no-cache/no-store so the worker updates", cc)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("/sw.js Content-Type = %q, want a javascript type", ct)
	}
	// Scope must be advertised, or a worker served from the root cannot claim it.
	if got := rec.Header().Get("Service-Worker-Allowed"); got != "/" {
		t.Errorf("Service-Worker-Allowed = %q, want /", got)
	}
}
