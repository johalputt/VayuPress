// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johalputt/vayupress/internal/settings"
)

// squarePNG builds a solid-colour PNG of the given size, standing in for an
// operator's uploaded logo.
func squarePNG(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return buf.Bytes()
}

// TestRenderAppIconHitsTheDeclaredSize is the whole point of re-rendering rather
// than passing the upload through. The manifest promises a 192 and a 512, and
// Android mints a package from what it is promised; an icon that is not the size
// it claims leaves the launcher rescaling, and one under 192 fails installability
// outright — which downgrades the install to a shortcut a reboot deletes.
func TestRenderAppIconHitsTheDeclaredSize(t *testing.T) {
	// Deliberately a small, non-square source: a 64x32 favicon is exactly the kind
	// of upload that used to be served verbatim at a URL claiming 512x512.
	src, _, err := image.Decode(bytes.NewReader(squarePNG(t, 64, 32, color.RGBA{R: 200, G: 30, B: 90, A: 255})))
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	for _, size := range []int{180, 192, 512} {
		for _, maskable := range []bool{false, true} {
			got := renderAppIcon(src, size, maskable)
			b := got.Bounds()
			if b.Dx() != size || b.Dy() != size {
				t.Errorf("renderAppIcon(%d, maskable=%v) = %dx%d, want a %d square",
					size, maskable, b.Dx(), b.Dy(), size)
			}
		}
	}
}

// TestMaskableIconIsOpaqueAndInset pins the two properties Android's maskable
// contract actually requires. It crops a maskable icon to its own shape, so a
// transparent or edge-to-edge mark gets its own edges clipped and shows the
// launcher's wallpaper through the gaps.
func TestMaskableIconIsOpaqueAndInset(t *testing.T) {
	// A fully transparent source: if the backdrop were missing, every pixel below
	// would still be transparent and the assertion would catch it.
	src := image.NewRGBA(image.Rect(0, 0, 100, 100))
	out := renderAppIcon(src, 512, true)

	for _, p := range []image.Point{{X: 0, Y: 0}, {X: 511, Y: 0}, {X: 0, Y: 511}, {X: 511, Y: 511}, {X: 256, Y: 256}} {
		if _, _, _, alpha := out.At(p.X, p.Y).RGBA(); alpha != 0xffff {
			t.Errorf("maskable icon is transparent at %v (alpha=%d); Android will crop through it", p, alpha)
		}
	}

	// The mark must sit inside the safe zone, so the corners are the backdrop
	// colour and therefore disposable. An opaque red source drawn edge to edge
	// would put red in the corner and fail here.
	red := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			red.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	inset := renderAppIcon(red, 512, true)
	r, g, b, _ := inset.At(2, 2).RGBA()
	if r>>8 != uint32(appIconBG.R) || g>>8 != uint32(appIconBG.G) || b>>8 != uint32(appIconBG.B) {
		t.Errorf("maskable corner = #%02x%02x%02x, want the backdrop — the mark is not inside the safe zone",
			r>>8, g>>8, b>>8)
	}
	// ...and the centre must still be the mark, or the "inset" is just a blank square.
	cr, _, _, _ := inset.At(256, 256).RGBA()
	if cr>>8 < 200 {
		t.Errorf("maskable centre red = %d, want the source mark drawn there", cr>>8)
	}
}

// TestNonMaskableIconKeepsTransparency — the "any" purpose icon is composited by
// the launcher itself, so painting a backdrop onto it would put a dark square
// behind a logo designed to sit on the user's wallpaper.
func TestNonMaskableIconKeepsTransparency(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 100, 100)) // fully transparent
	out := renderAppIcon(src, 192, false)
	if _, _, _, alpha := out.At(0, 0).RGBA(); alpha != 0 {
		t.Errorf("non-maskable icon corner alpha = %d, want 0 (no backdrop on an 'any' icon)", alpha)
	}
}

// TestServeAppIconFallsBackWithoutASettingsStore covers the path every install
// takes before a logo is uploaded, and the one an ICO/WebP upload takes forever:
// an icon route that errors breaks installability rather than degrading, so it
// must always write something.
func TestServeAppIconFallsBackWithoutASettingsStore(t *testing.T) {
	rr := httptest.NewRecorder()
	(&App{}).serveAppIcon(192, false, webAppIcon192PNG)(rr, httptest.NewRequest(http.MethodGet, "/static/icons/webapp-192.png", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !bytes.Equal(rr.Body.Bytes(), webAppIcon192PNG) {
		t.Error("no custom logo must serve the embedded mark byte for byte")
	}
	if ct := rr.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	// The default must NOT be immutable. serveFavicon documents why: a browser
	// that cached an immutable default keeps it for a year and never notices a
	// later logo upload, so the operator's new logo appears to do nothing.
	if cc := rr.Header().Get("Cache-Control"); cc == "" || bytes.Contains([]byte(cc), []byte("immutable")) {
		t.Errorf("Cache-Control = %q; the default must revalidate or a later logo upload can never displace it", cc)
	}
	if rr.Header().Get("ETag") == "" {
		t.Error("missing ETag — revalidation needs one or every request re-sends the whole icon")
	}
}

// TestUndecodableUploadFallsBack — ICO and WebP have no stdlib decoder, and a
// truncated file is always possible. Neither may produce a broken icon route.
func TestUndecodableUploadFallsBack(t *testing.T) {
	if _, _, err := image.Decode(bytes.NewReader([]byte("this is not an image"))); err == nil {
		t.Fatal("fixture decoded; the test proves nothing")
	}
	rr := httptest.NewRecorder()
	(&App{}).serveAppIcon(512, true, webAppIconMaskablePNG)(rr, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rr.Code != http.StatusOK || rr.Body.Len() == 0 {
		t.Fatalf("undecodable upload must still serve an icon (status=%d, %d bytes)", rr.Code, rr.Body.Len())
	}
}

// TestRenderedIconIsADecodablePNGOfTheRightSize closes the loop through the real
// encode path, for a JPEG source — the format an operator is most likely to
// upload and the one most likely to be non-square.
func TestRenderedIconIsADecodablePNGOfTheRightSize(t *testing.T) {
	var jbuf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 300, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 300; x++ {
			img.Set(x, y, color.RGBA{R: 12, G: 180, B: 200, A: 255})
		}
	}
	if err := jpeg.Encode(&jbuf, img, nil); err != nil {
		t.Fatalf("encode jpeg fixture: %v", err)
	}
	src, _, err := image.Decode(bytes.NewReader(jbuf.Bytes()))
	if err != nil {
		t.Fatalf("decode jpeg fixture: %v", err)
	}
	var out bytes.Buffer
	if err := png.Encode(&out, renderAppIcon(src, 512, false)); err != nil {
		t.Fatalf("encode rendered icon: %v", err)
	}
	// pngSize is what the install-health card reads, so agreeing with it is the
	// check that matters: the card must report the size the manifest declares.
	w, h, ok := pngSize(out.Bytes())
	if !ok {
		t.Fatal("rendered icon is not a readable PNG")
	}
	if w != 512 || h != 512 {
		t.Errorf("rendered icon is %dx%d, want 512x512", w, h)
	}
}

// newIconApp returns an App with a real settings store holding an uploaded logo,
// so the CUSTOM branch is exercised end to end. Without this every icon test
// takes the nil-store fallback path and the render is never actually run through
// customAppIcon — which is where the size the manifest promises is decided.
func newIconApp(t *testing.T, logo []byte) *App {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE site_settings (scope TEXT NOT NULL DEFAULT '', key TEXT NOT NULL, value TEXT NOT NULL DEFAULT '', updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY(scope,key))`); err != nil {
		t.Fatalf("settings schema: %v", err)
	}
	st := settings.New(db)
	if logo != nil {
		if err := st.SetMany(t.Context(), settings.ForPrimary(), map[string]string{
			settings.KeyBrandFavicon:     base64.StdEncoding.EncodeToString(logo),
			settings.KeyBrandFaviconType: "image/png",
		}); err != nil {
			t.Fatalf("store logo: %v", err)
		}
	}
	return &App{siteSettings: st}
}

// TestCustomLogoIsServedAtTheDeclaredSize is the test for the reported bug:
// installing johal.in put the VayuPress mark on the home screen because the icon
// routes never consulted the operator's uploaded logo.
//
// It asserts the size too, not just "different from the default". An upload is
// typically a small favicon, and serving those bytes at a URL declaring 512x512
// is the mismatch the install-health card exists to catch — and below 192 it
// fails installability outright, which is what turns an install into a shortcut
// a reboot deletes.
func TestCustomLogoIsServedAtTheDeclaredSize(t *testing.T) {
	logo := squarePNG(t, 64, 64, color.RGBA{R: 240, G: 120, B: 10, A: 255})
	a := newIconApp(t, logo)

	for _, tc := range []struct {
		url      string
		size     int
		maskable bool
		fallback []byte
	}{
		{"/static/icons/webapp-192.png", 192, false, webAppIcon192PNG},
		{"/static/icons/webapp-512.png", 512, false, webAppIcon512PNG},
		{"/static/icons/webapp-maskable-512.png", 512, true, webAppIconMaskablePNG},
		{"/static/icons/webapp-apple-180.png", 180, false, webAppIconApplePNG},
	} {
		rr := httptest.NewRecorder()
		a.serveAppIcon(tc.size, tc.maskable, tc.fallback)(rr, httptest.NewRequest(http.MethodGet, tc.url, nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", tc.url, rr.Code)
		}
		if bytes.Equal(rr.Body.Bytes(), tc.fallback) {
			t.Errorf("%s served the embedded mark; the operator's logo was ignored", tc.url)
		}
		w, h, ok := pngSize(rr.Body.Bytes())
		if !ok {
			t.Fatalf("%s: served bytes are not a readable PNG", tc.url)
		}
		if w != tc.size || h != tc.size {
			t.Errorf("%s served %dx%d, but the manifest declares %d — the launcher rescales whatever it gets",
				tc.url, w, h, tc.size)
		}
		// The render must carry the source's colour, not just be a correctly-sized
		// blank square — a resizer that silently produced an empty canvas would
		// otherwise pass every assertion above.
		img, err := png.Decode(bytes.NewReader(rr.Body.Bytes()))
		if err != nil {
			t.Fatalf("%s: decode served icon: %v", tc.url, err)
		}
		cr, cg, cb, _ := img.At(tc.size/2, tc.size/2).RGBA()
		if cr>>8 < 200 || cg>>8 < 80 || cb>>8 > 80 {
			t.Errorf("%s centre = #%02x%02x%02x, want the uploaded logo's colour", tc.url, cr>>8, cg>>8, cb>>8)
		}
	}
}

// TestUploadingANewLogoInvalidatesEveryDerivedSize — the cache is keyed on a hash
// of the source bytes precisely so a second upload does not keep serving the
// first one's render. A stale icon here is invisible until somebody reinstalls.
func TestUploadingANewLogoInvalidatesEveryDerivedSize(t *testing.T) {
	first := squarePNG(t, 64, 64, color.RGBA{R: 255, A: 255})
	a := newIconApp(t, first)
	before := a.customAppIcon(t.Context(), 192, false)
	if len(before) == 0 {
		t.Fatal("first logo did not render")
	}

	second := squarePNG(t, 64, 64, color.RGBA{B: 255, A: 255})
	if err := a.siteSettings.SetMany(t.Context(), settings.ForPrimary(), map[string]string{
		settings.KeyBrandFavicon: base64.StdEncoding.EncodeToString(second),
	}); err != nil {
		t.Fatalf("replace logo: %v", err)
	}
	after := a.customAppIcon(t.Context(), 192, false)
	if bytes.Equal(before, after) {
		t.Error("a replaced logo still renders the old icon — the cache key is not tracking the source bytes")
	}
}

// TestUndecodableStoredLogoFallsBack — ICO and WebP are accepted by the favicon
// upload but have no stdlib decoder, so they must fall back to the embedded mark
// rather than producing a broken or zero-byte icon route.
func TestUndecodableStoredLogoFallsBack(t *testing.T) {
	a := newIconApp(t, []byte("\x00\x00\x01\x00 not a decodable icon"))
	if b := a.customAppIcon(t.Context(), 192, false); len(b) != 0 {
		t.Errorf("undecodable upload produced %d bytes; it must fall back to the embedded mark", len(b))
	}
	rr := httptest.NewRecorder()
	a.serveAppIcon(192, false, webAppIcon192PNG)(rr, httptest.NewRequest(http.MethodGet, "/x", nil))
	if !bytes.Equal(rr.Body.Bytes(), webAppIcon192PNG) {
		t.Error("route must serve the embedded mark when the upload cannot be decoded")
	}
}

// TestHealthCheckReadsWhatIsServed pins the coupling between the icon routes and
// the install-health card. The card's honesty rests on reading the bytes the
// route writes; when the routes began serving a rendered custom logo, a check
// still reading only the embedded asset would report a green "every icon matches
// its declared size" for bytes nobody receives.
func TestHealthCheckReadsWhatIsServed(t *testing.T) {
	a := &App{}
	for _, src := range []string{
		"/static/icons/webapp-192.png",
		"/static/icons/webapp-512.png",
		"/static/icons/webapp-maskable-512.png",
		"/static/icons/webapp-apple-180.png",
	} {
		b, ok := a.webAppIconBytes(t.Context(), src)
		if !ok || len(b) == 0 {
			t.Fatalf("%s: health check found no bytes for a URL the router serves", src)
		}
		rr := httptest.NewRecorder()
		size, maskable := 192, false
		switch src {
		case "/static/icons/webapp-512.png":
			size = 512
		case "/static/icons/webapp-maskable-512.png":
			size, maskable = 512, true
		case "/static/icons/webapp-apple-180.png":
			size = 180
		}
		fallback, _ := a.webAppIconBytes(t.Context(), src)
		a.serveAppIcon(size, maskable, fallback)(rr, httptest.NewRequest(http.MethodGet, src, nil))
		if !bytes.Equal(rr.Body.Bytes(), b) {
			t.Errorf("%s: the health check reads different bytes than the route serves", src)
		}
	}
	if _, ok := a.webAppIconBytes(t.Context(), "/static/icons/not-an-icon.png"); ok {
		t.Error("webAppIconBytes claimed to serve a URL the router does not route")
	}

	// And with a logo uploaded, the card must read the RENDERED bytes. Reading the
	// embedded default here would report a green "every icon matches its declared
	// size" for bytes nobody is served.
	custom := newIconApp(t, squarePNG(t, 64, 64, color.RGBA{G: 200, A: 255}))
	got, ok := custom.webAppIconBytes(t.Context(), "/static/icons/webapp-512.png")
	if !ok {
		t.Fatal("health check found no bytes for the 512 icon")
	}
	if bytes.Equal(got, webAppIcon512PNG) {
		t.Error("health check read the embedded mark while the route serves the operator's logo")
	}
	if w, h, ok := pngSize(got); !ok || w != 512 || h != 512 {
		t.Errorf("health check reads a %dx%d icon for a URL declaring 512x512", w, h)
	}
}
