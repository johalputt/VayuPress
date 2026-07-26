package main

// pwa_health.go — "why is my installed app not a real app?"
//
// Installing a site on Android produces one of two things, and NOTHING in the
// install flow says which you got:
//
//   * a WebAPK — a real generated Android package that survives reboots, minted
//     only when the site passes Chrome's full installability check; or
//   * a legacy launcher shortcut — a row in the launcher's database, which many
//     Android builds discard on restart.
//
// You find out weeks later, when the icon is gone. Worse, a shortcut cannot
// upgrade itself into a WebAPK: fixing the site does nothing for an icon already
// on the home screen, which has to be removed and re-added.
//
// So this reports, item by item, what Chrome requires and what this instance
// actually serves — including the checks that only fail in ways you cannot see:
// an icon whose real pixel size differs from the size the manifest claims, and a
// manifest or worker that something in front of the origin is intercepting.
//
// The browser-side half lives in static/js/admin-os-pwa.js, because the decisive
// facts — is a worker actually registered, is a WebAPK actually installed, does
// the manifest survive the round trip through the CDN — are only knowable there.

import (
	"encoding/binary"
	"encoding/json"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/render"
)

// captureWriter records a handler's response so a check can inspect what this
// instance actually serves. A local 20 lines beats importing net/http/httptest
// into the shipped binary.
type captureWriter struct {
	header http.Header
	body   []byte
	status int
}

func newCapture() *captureWriter {
	return &captureWriter{header: http.Header{}, status: http.StatusOK}
}
func (c *captureWriter) Header() http.Header { return c.header }
func (c *captureWriter) WriteHeader(s int)   { c.status = s }
func (c *captureWriter) Write(b []byte) (int, error) {
	c.body = append(c.body, b...)
	return len(b), nil
}

// pwaCheck is one requirement and whether this instance meets it.
type pwaCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	// Why explains what breaks when this check fails — the difference between a
	// checklist and something you can act on.
	Why string `json:"why,omitempty"`
}

// pngSize reads the width and height out of a PNG's IHDR chunk. The manifest
// declaring a size the file does not have is not a hypothetical: it is what
// shipped before v3.15.31, where one 256x256 file was declared as both 192x192
// and 512x512, and Android was left to guess.
func pngSize(b []byte) (w, h int, ok bool) {
	// 8-byte signature, 4-byte length, 4-byte "IHDR", then width/height.
	if len(b) < 24 || string(b[1:4]) != "PNG" || string(b[12:16]) != "IHDR" {
		return 0, 0, false
	}
	return int(binary.BigEndian.Uint32(b[16:20])), int(binary.BigEndian.Uint32(b[20:24])), true
}

// pwaHealthChecks runs every server-side installability requirement.
func (a *App) pwaHealthChecks(r *http.Request) []pwaCheck {
	var out []pwaCheck
	add := func(name string, ok bool, detail, why string) {
		out = append(out, pwaCheck{Name: name, OK: ok, Detail: detail, Why: why})
	}

	// 1. The manifest itself.
	rec := newCapture()
	a.handlePWAManifest(rec, r)
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
	if err := json.Unmarshal(rec.body, &m); err != nil {
		add("Manifest is valid JSON", false, err.Error(),
			"Without a parseable manifest the browser has nothing to install.")
		return out
	}
	add("Manifest is served and parses", true, "/manifest.json", "")

	missing := []string{}
	for label, v := range map[string]string{
		"name": m.Name, "short_name": m.ShortName, "start_url": m.StartURL,
		"scope": m.Scope, "display": m.Display, "id": m.ID,
	} {
		if strings.TrimSpace(v) == "" {
			missing = append(missing, label)
		}
	}
	add("Manifest declares every required field", len(missing) == 0,
		strings.Join(missing, ", "),
		"A manifest missing any of these is not installable, and an absent id means the app's "+
			"identity is its start_url — change the landing page later and the browser treats it as a different app.")
	add("display is standalone", m.Display == "standalone", m.Display,
		"Anything else opens in browser chrome instead of as an app.")

	// 2. The icons — declared size vs the bytes actually served.
	var any192, any512, maskable bool
	iconDetail := []string{}
	iconsOK := true
	for _, ic := range m.Icons {
		body, served := webAppIconBytes(ic.Src)
		if !served {
			iconsOK = false
			iconDetail = append(iconDetail, ic.Src+" is not served by this build")
			continue
		}
		w, h, ok := pngSize(body)
		if !ok {
			iconsOK = false
			iconDetail = append(iconDetail, ic.Src+" is not a readable PNG")
			continue
		}
		got := strconv.Itoa(w) + "x" + strconv.Itoa(h)
		if got != ic.Sizes {
			iconsOK = false
			iconDetail = append(iconDetail, ic.Src+" is "+got+" but the manifest says "+ic.Sizes)
			continue
		}
		switch {
		case ic.Purpose == "maskable":
			maskable = true
		case ic.Sizes == "192x192":
			any192 = true
		case ic.Sizes == "512x512":
			any512 = true
		}
		iconDetail = append(iconDetail, ic.Src+" "+got+" ✓")
	}
	add("Every icon matches its declared size", iconsOK, strings.Join(iconDetail, " · "),
		"An icon whose real dimensions differ from the manifest leaves the launcher rescaling "+
			"whatever it gets, and can fail the minting check outright.")
	add("A real 192px and 512px icon exist", any192 && any512, "",
		"192 is the install minimum; 512 is what the launcher icon and splash screen use.")
	add("A separate maskable icon exists", maskable, "",
		"Android crops a maskable icon to its own shape. Without a padded one, the mark's edges get clipped.")

	// 3. The service worker. Serving it is not enough — see the registration check.
	swRec := newCapture()
	a.handleServiceWorker(swRec, r)
	ctOK := strings.Contains(swRec.header.Get("Content-Type"), "javascript")
	add("Service worker is served as JavaScript", ctOK, swRec.header.Get("Content-Type"),
		"A worker served with the wrong type is refused by the browser.")
	add("Service worker declares its scope", swRec.header.Get("Service-Worker-Allowed") == "/",
		swRec.header.Get("Service-Worker-Allowed"),
		"Without this a worker served from the root cannot claim the whole site.")

	// 4. Registration — the requirement that silently failed before v3.15.31.
	page, _ := render.RenderHome(config.Cfg.Domain, Version, nil, 0, 1, 1)
	registers := strings.Contains(page, "/static/js/pwa.js")
	add("Public pages register the worker", registers, "",
		"Serving /sw.js is not enough: a worker exists only once a page calls "+
			"navigator.serviceWorker.register. Without it the site fails the installability check and "+
			"the install silently becomes a launcher shortcut, which a restart can discard.")
	linksManifest := strings.Contains(page, `rel="manifest"`)
	add("Public pages link the manifest", linksManifest, "", "There is nothing to install without it.")
	appleIcon := strings.Contains(page, "apple-touch-icon")
	add("iOS install tags are present", appleIcon, "",
		"iPhone ignores the manifest for icons and standalone display; without apple-* tags an "+
			"iOS install gets a screenshot of the page as its icon.")

	// 5. Bot protection must not challenge the install surfaces. The WebAPK minting
	//    server fetches the manifest and icons, and it is not a browser.
	bypassed := containsPrefix(shieldBypassPrefixes, "/manifest.json") &&
		containsPrefix(shieldBypassPrefixes, "/sw.js") &&
		containsPrefix(shieldBypassPrefixes, "/static")
	add("Bot protection exempts the install surfaces", bypassed, "",
		"The WebAPK minting server downloads the manifest and icons to build the package. It cannot "+
			"solve a challenge, so a challenge there means no app gets minted.")

	return out
}

// webAppIconBytes returns the bytes this build serves for a manifest icon URL.
// Reading the embedded asset is the honest check: it is literally what the route
// writes, so a mismatch here is a mismatch on the wire.
//
// It deliberately does NOT prove the same bytes survive a CDN or reverse proxy in
// front of the origin — that is what the browser-side probe is for, and it is a
// real failure mode: an edge challenging /manifest.json breaks installation while
// the origin itself looks perfect.
func webAppIconBytes(src string) ([]byte, bool) {
	switch src {
	case "/static/icons/webapp-192.png":
		return webAppIcon192PNG, true
	case "/static/icons/webapp-512.png":
		return webAppIcon512PNG, true
	case "/static/icons/webapp-maskable-512.png":
		return webAppIconMaskablePNG, true
	case "/static/icons/webapp-apple-180.png":
		return webAppIconApplePNG, true
	}
	return nil, false
}

// pwaHealthCardHTML renders the checklist in the console's accordion grammar.
func (a *App) pwaHealthCardHTML(r *http.Request, nonce string) string {
	checks := a.pwaHealthChecks(r)
	failed := 0
	rows := ""
	for _, c := range checks {
		icon, cls := "✓", "ok"
		if !c.OK {
			icon, cls, failed = "✕", "bad", failed+1
		}
		why := ""
		if !c.OK && c.Why != "" {
			why = `<div class="pwa-why">` + html.EscapeString(c.Why) + `</div>`
		}
		detail := ""
		if c.Detail != "" {
			detail = `<div class="pwa-detail">` + html.EscapeString(c.Detail) + `</div>`
		}
		rows += `<div class="pwa-row pwa-row--` + cls + `"><span class="pwa-mark">` + icon + `</span>` +
			`<div><div class="pwa-name">` + html.EscapeString(c.Name) + `</div>` + detail + why + `</div></div>`
	}

	// The chip must not say "Installable" on the strength of server checks alone:
	// they prove what the ORIGIN serves, and the case this exists to catch is an
	// edge in front of it intercepting the manifest. Passing here means the origin
	// is right, and the browser half below is what settles it.
	chip := monChip(failed == 0, "Origin OK", strconv.Itoa(failed)+" failing")
	body := `<p class="text-sm muted mb-4">Installing this site should produce a real app that survives a
restart — on Android, a generated package rather than a launcher shortcut. Nothing in the install
flow tells you which one you got, so these are the requirements, checked against what this instance
actually serves.</p>` + rows +
		`<div class="pwa-browser" data-pwa-probe>
  <div class="section-head"><span class="section-head__title">From this browser</span>
    <span class="section-head__hint">Only the browser can answer these</span></div>
  <div class="pwa-probe-rows" data-pwa-probe-rows data-pwa-build="` + html.EscapeString(Version) + `"><span class="muted text-sm">Checking…</span></div>
</div>
<p class="text-sm muted mt-4"><strong>Already have the icon on your home screen?</strong> A shortcut
cannot turn itself into an app. Remove it and add it again — then confirm it appears in Android's
full app list, not only on the home screen.</p>
<script nonce="` + nonce + `" src="/os/static/js/admin-os-pwa.js?v=` + assetVer("js/admin-os-pwa.js") + `"></script>`

	// Always expanded. A diagnostic that hides itself when the server-side checks
	// look fine is precisely how the browser-side failure goes unnoticed — which is
	// the failure still in play once the origin is correct.
	return monAcc("📱", "Install health", "Whether this site installs as a real app", chip, true, body)
}
