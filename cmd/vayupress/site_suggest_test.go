// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/sitedoc"
)

// logo is a 100×100 mark: transparent corners, a white field, a black ring
// and an orange disc — the colour a person would call the brand's.
func logo() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			dx, dy := x-50, y-50
			d := dx*dx + dy*dy
			switch {
			case d < 30*30:
				img.Set(x, y, color.NRGBA{249, 115, 22, 255})
			case d < 34*34:
				img.Set(x, y, color.NRGBA{10, 10, 10, 255})
			case d < 48*48:
				img.Set(x, y, color.NRGBA{255, 255, 255, 255})
			}
		}
	}
	return img
}

func TestTheLogosBrandColourIsFound(t *testing.T) {
	c, ok := dominantColour(logo())
	if !ok || c != "#f97316" {
		t.Errorf("dominant colour %q, %v — want the orange disc, not the white field, the black ring or the transparent corners", c, ok)
	}
	// One seed per rule: each image has something that would outvote the
	// orange disc if that one rule were missing.
	for name, fill := range map[string]func(img *image.NRGBA){
		"a faint wash": func(img *image.NRGBA) { // semi-transparent blue over corners and field, larger than the disc
			for y := 0; y < 100; y++ {
				for x := 0; x < 100; x++ {
					if c := img.NRGBAAt(x, y); c.A == 0 || c.G == 255 {
						img.Set(x, y, color.NRGBA{0, 0, 255, 100})
					}
				}
			}
		},
		"a near-black field": func(img *image.NRGBA) { // saturated but nearly black
			for y := 0; y < 100; y++ {
				for x := 0; x < 100; x++ {
					if c := img.NRGBAAt(x, y); c.A == 0 || c.R == 255 {
						img.Set(x, y, color.NRGBA{0, 0, 25, 255})
					}
				}
			}
		},
		"a small patch seen first": func(img *image.NRGBA) { // green, top-left, fewer pixels
			for y := 0; y < 6; y++ {
				for x := 0; x < 6; x++ {
					img.Set(x, y, color.NRGBA{22, 163, 74, 255})
				}
			}
		},
	} {
		img := logo()
		fill(img)
		if c, ok := dominantColour(img); !ok || c != "#f97316" {
			t.Errorf("with %s: dominant colour %q — want the orange disc", name, c)
		}
	}
	grey := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			grey.Set(x, y, color.NRGBA{120, 120, 120, 255})
		}
	}
	if c, ok := dominantColour(grey); ok {
		t.Errorf("a grey logo produced a brand colour %s", c)
	}
}

func TestTheEditorSuggestsAColourFromTheLogo(t *testing.T) {
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example")
	a.siteSettings = settings.New(dbpkg.DB)
	h := editorRouter(a, d, true)
	if rec, j := editorCall(t, h, http.MethodGet, "/sd/suggest-accent", nil); rec.Code != http.StatusNotFound || errCode(j) != "no-logo" {
		t.Fatalf("with no logo: %d %s", rec.Code, rec.Body)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, logo()); err != nil {
		t.Fatal(err)
	}
	if err := a.siteSettings.SetMany(context.Background(), settings.ForDomain(d.ID), map[string]string{
		settings.KeyBrandFavicon: base64.StdEncoding.EncodeToString(buf.Bytes()), settings.KeyBrandFaviconType: "image/png",
	}); err != nil {
		t.Fatal(err)
	}
	rec, j := editorCall(t, h, http.MethodGet, "/sd/suggest-accent", nil)
	if rec.Code != http.StatusOK || j["found"] != "#f97316" {
		t.Fatalf("suggest: %d %s", rec.Code, rec.Body)
	}
	// Orange cannot carry white text, so the suggestion is a darker orange
	// the validator accepts — not the raw logo colour it would refuse.
	accent := j["accent"].(string)
	doc := twoPageDoc("Harbour")
	doc.Style = &sitedoc.Style{Accent: accent}
	if rec, _ := editorCall(t, h, http.MethodPost, "/sd/draft", withDoc(doc)); rec.Code != http.StatusOK || !strings.HasPrefix(accent, "#") || accent == "#f97316" {
		t.Errorf("suggested %s; saving it gave %d", accent, rec.Code)
	}
}
