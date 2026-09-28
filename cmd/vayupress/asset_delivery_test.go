// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/render"
)

// TestVersionedAssetsAreKeptAYear — a public script or stylesheet asked for by
// its versioned address may be kept without asking again; one asked for without
// a version still expires within a day, because nothing says it is current.
func TestVersionedAssetsAreKeptAYear(t *testing.T) {
	a := &App{}
	for _, tc := range []struct {
		url, want string
	}{
		{"/static/chroma.css?v=abcd", "public, immutable, max-age=31536000"},
		{"/static/chroma.css", "public, max-age=86400"},
	} {
		rr := httptest.NewRecorder()
		a.handleChromaCSS(rr, httptest.NewRequest(http.MethodGet, tc.url, nil))
		if got := rr.Header().Get("Cache-Control"); got != tc.want {
			t.Errorf("%s: Cache-Control %q, want %q", tc.url, got, tc.want)
		}
		if rr.Body.String() != render.ChromaCSSBody() {
			t.Errorf("%s: served a different stylesheet from the one its link is versioned by", tc.url)
		}
	}
}

// TestTheBrandMarkIsDrawnAtTheSizePagesShowIt — pages show the mark at 24 px
// and drew it from the 256 px favicon. brand-48.png is the same mark at the
// size pages need, the operator's own when there is one.
func TestTheBrandMarkIsDrawnAtTheSizePagesShowIt(t *testing.T) {
	get := func(a *App) []byte {
		rr := httptest.NewRecorder()
		a.serveFavicon(faviconLightPNG, 48)(rr, httptest.NewRequest(http.MethodGet, "/static/brand-48.png", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("brand-48.png: %d", rr.Code)
		}
		return rr.Body.Bytes()
	}

	def := get(newIconApp(t, nil))
	if w, h, ok := pngSize(def); !ok || w != 48 || h != 48 {
		t.Errorf("the default mark served %dx%d, want 48x48", w, h)
	}
	if len(def) >= len(faviconLightPNG)/2 {
		t.Errorf("the 48 px mark is %d bytes against the favicon's %d; it saves the reader nothing", len(def), len(faviconLightPNG))
	}

	logo := squarePNG(t, 300, 300, color.RGBA{R: 255, G: 128, A: 255})
	own := get(newIconApp(t, logo))
	if w, h, ok := pngSize(own); !ok || w != 48 || h != 48 {
		t.Errorf("the operator's mark served %dx%d, want 48x48", w, h)
	}
	if bytes.Equal(own, def) {
		t.Error("with a mark uploaded, brand-48.png still serves the VayuPress one")
	}

	// An upload with no stdlib decoder is served as stored, as the favicon
	// routes serve it, rather than replaced by the VayuPress mark.
	ico := []byte("\x00\x00\x01\x00 not a decodable icon")
	if got := get(newIconApp(t, ico)); !bytes.Equal(got, ico) {
		t.Errorf("an undecodable upload served %d other bytes instead of itself", len(got))
	}
}

// TestPagesDrawTheMarkFromTheSmallRendition holds the templates to the route:
// a header or footer <img> of the full favicon is the 15 KB image again. The
// templates are Go string constants in two packages, so their source is what
// is read.
func TestPagesDrawTheMarkFromTheSmallRendition(t *testing.T) {
	fullMark := regexp.MustCompile(`<img[^>]*src="/static/favicon-light\.png"[^>]*width="2[0-9]"`)
	small := 0
	for _, dir := range []string{".", filepath.Join("..", "..", "internal", "render")} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range fullMark.FindAll(src, -1) {
				t.Errorf("%s draws a small mark from the 256 px favicon: %s", f, m)
			}
			small += bytes.Count(src, []byte(`src="/static/brand-48.png"`))
		}
	}
	if small == 0 {
		t.Error("no template draws its mark from brand-48.png: the scan is reading the wrong files")
	}
}
