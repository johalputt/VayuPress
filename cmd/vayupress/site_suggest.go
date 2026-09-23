// SPDX-License-Identifier: Apache-2.0

package main

// site_suggest.go — a brand colour suggested from the site's own logo.
//
// Deterministic, no model: the logo's most common saturated colour, then the
// nearest shade of it that passes as an accent (sitedoc.ReadableAccent). The
// editor offers it; the operator decides.

import (
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // decoders for the formats a mark is uploaded in
	_ "image/jpeg" //
	_ "image/png"  //
	"net/http"

	"github.com/johalputt/vayupress/internal/imageproc"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/sitedoc"
)

// dominantColour is the most common clearly-coloured pixel colour of img, as
// #rrggbb. Faint (mostly transparent) pixels, near-black, near-white and
// greys are skipped:
// a logo's background and outline are not its brand colour. Colours are
// counted in 16 steps per channel so anti-aliasing does not split one
// colour into many; the winner is the average of its bucket.
func dominantColour(img image.Image) (string, bool) {
	b := img.Bounds()
	step := max(1, max(b.Dx(), b.Dy())/200)
	type acc struct{ n, r, g, b int }
	buckets := map[int]*acc{}
	var best *acc
	for y := b.Min.Y; y < b.Max.Y; y += step {
		for x := b.Min.X; x < b.Max.X; x += step {
			r16, g16, b16, a16 := img.At(x, y).RGBA()
			if a16 < 0x8000 {
				continue
			}
			r, g, bl := int(r16>>8), int(g16>>8), int(b16>>8)
			hi, lo := max(r, g, bl), min(r, min(g, bl))
			// Near-black is not a colour however saturated its channels read;
			// near-white needs no rule of its own — it is never saturated.
			if hi < 32 || float64(hi-lo)/float64(hi) < 0.25 {
				continue
			}
			key := r>>4<<8 | g>>4<<4 | bl>>4
			c := buckets[key]
			if c == nil {
				c = &acc{}
				buckets[key] = c
			}
			c.n++
			c.r += r
			c.g += g
			c.b += bl
			if best == nil || c.n > best.n {
				best = c
			}
		}
	}
	if best == nil {
		return "", false
	}
	return fmt.Sprintf("#%02x%02x%02x", best.r/best.n, best.g/best.n, best.b/best.n), true
}

// handleSiteDocSuggestAccent answers GET …/suggest-accent with a brand colour
// taken from the site's uploaded mark.
func (a *App) handleSiteDocSuggestAccent(target siteDocTarget) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scope, _, ok := a.siteDocTargetOr404(w, r, target)
		if !ok {
			return
		}
		sc := settings.ForPrimary()
		if scope != "" {
			sc = settings.ForDomain(scope)
		}
		mark, _, ok := a.brandMark(r.Context(), sc)
		if !ok {
			writeAPIError(w, r, http.StatusNotFound, "no-logo", "Upload a logo under Branding first, then this reads its colour.", "")
			return
		}
		img, err := imageproc.Decode(mark)
		if errors.Is(err, imageproc.ErrTooLarge) {
			writeAPIError(w, r, http.StatusUnprocessableEntity, "logo-too-large",
				"The logo is too large an image to read colours from; upload a smaller one under Branding.", "")
			return
		}
		if err != nil {
			writeAPIError(w, r, http.StatusUnprocessableEntity, "logo-unreadable",
				"The logo is in a format this cannot read colours from (PNG, JPEG and GIF can be read).", "")
			return
		}
		c, ok := dominantColour(img)
		if !ok {
			writeAPIError(w, r, http.StatusUnprocessableEntity, "logo-no-colour",
				"The logo is black, white or grey, so there is no brand colour in it to take.", "")
			return
		}
		writeJSON(w, r, http.StatusOK, map[string]string{"accent": sitedoc.ReadableAccent(c), "found": c})
	}
}
