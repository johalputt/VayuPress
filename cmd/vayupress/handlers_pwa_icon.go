// SPDX-License-Identifier: Apache-2.0

package main

// handlers_pwa_icon.go — the installed public app wears the SITE's logo.
//
// Installing example.test put the VayuPress mark on the home screen, because the
// public manifest pointed at the embedded brand icons and nothing consulted the
// operator's uploaded logo. That is the wrong identity: the app is the
// operator's site, not the software it happens to run on. (The VayuOS console is
// the opposite case and deliberately keeps the VayuPress mark — that app IS
// VayuPress, so handlers_pwa_os.go is untouched.)
//
// The override happens at the SERVING layer, exactly as serveFavicon does, so
// the manifest, the HTML <link> tags and the WebAPK minting server all keep
// their fixed URLs and a custom logo propagates everywhere without a template
// change.
//
// Why re-render rather than hand the browser the upload as-is: the manifest
// promises a 192 and a 512, and Android mints a package from what it is
// promised. Serving a 64x64 favicon at those URLs is the "declaring a size the
// file does not have" mistake that leaves the launcher rescaling whatever it
// gets — and an icon below 192 fails installability outright, which downgrades
// the install to a shortcut that a reboot deletes. So the upload is decoded once
// and drawn to the exact square the manifest advertises.
//
// Maskable is generated separately and deliberately: Android crops a maskable
// icon to its own shape, so it needs an OPAQUE square with the mark inside the
// safe zone. Handing it a transparent, edge-to-edge logo gets the logo's own
// edges clipped.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"  // decode GIF uploads
	_ "image/jpeg" // decode JPEG uploads
	"image/png"
	"math"
	"net/http"
	"strconv"
	"sync"

	"github.com/johalputt/vayupress/internal/settings"
)

// appIconBG is the opaque backdrop for maskable icons. It matches the manifest's
// background_color so the installed app's icon, splash and shell agree.
var appIconBG = color.RGBA{R: 0x0a, G: 0x0f, B: 0x1a, A: 0xff}

// maskableSafeZone is the fraction of the square a maskable icon's content may
// occupy. Android may crop to a circle inscribed in the square, so the mark is
// drawn at 80% and centred, leaving the corners disposable.
const maskableSafeZone = 0.8

// appIconCache memoises rendered icons. The key includes a hash of the SOURCE
// bytes, so uploading a new logo invalidates every derived size at once without
// any explicit purge — a stale-icon bug that would otherwise be invisible until
// someone reinstalled the app.
var appIconCache sync.Map // string -> []byte

// customAppIcon renders the operator's uploaded logo to a size x size PNG, or
// returns nil when there is no usable custom logo. A nil return is the signal to
// fall back to the embedded VayuPress mark — never an error to the client, since
// an icon route that fails breaks installability rather than degrading.
func (a *App) customAppIcon(ctx context.Context, size int, maskable bool) []byte {
	if a.siteSettings == nil {
		return nil
	}
	enc := a.siteSettings.Get(ctx, settings.ForPrimary(), settings.KeyBrandFavicon)
	if enc == "" {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(raw) == 0 {
		return nil
	}

	sum := sha256.Sum256(raw)
	key := hex.EncodeToString(sum[:8]) + ":" + strconv.Itoa(size) + ":" + strconv.FormatBool(maskable)
	if v, ok := appIconCache.Load(key); ok {
		b, _ := v.([]byte)
		return b
	}

	// ICO and WebP have no stdlib decoder. Rather than add a dependency for two
	// formats, those uploads keep the embedded mark for the APP icon while still
	// working everywhere the raw bytes are served (the favicon routes pass them
	// through untouched). Caching the nil keeps the failed decode from being
	// retried on every request.
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		appIconCache.Store(key, []byte(nil))
		return nil
	}

	out := renderAppIcon(src, size, maskable)
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		appIconCache.Store(key, []byte(nil))
		return nil
	}
	b := buf.Bytes()
	appIconCache.Store(key, b)
	return b
}

// renderAppIcon draws src centred in a size x size canvas, preserving its aspect
// ratio. A maskable icon is inset into the safe zone on an opaque background; a
// normal icon fills the square and keeps any transparency the source had.
func renderAppIcon(src image.Image, size int, maskable bool) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	inset := 1.0
	if maskable {
		draw.Draw(dst, dst.Bounds(), &image.Uniform{C: appIconBG}, image.Point{}, draw.Src)
		inset = maskableSafeZone
	}

	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		return dst
	}
	box := float64(size) * inset
	scale := math.Min(box/float64(sw), box/float64(sh))
	tw := int(math.Round(float64(sw) * scale))
	th := int(math.Round(float64(sh) * scale))
	if tw < 1 {
		tw = 1
	}
	if th < 1 {
		th = 1
	}
	ox := (size - tw) / 2
	oy := (size - th) / 2

	// Area-average sampling. Logos are almost always LARGER than the target, and
	// picking one nearest pixel out of a big source is what makes a downscaled
	// mark look ragged; averaging the source pixels that map to each destination
	// pixel keeps thin strokes readable at 192.
	for y := 0; y < th; y++ {
		y0 := b.Min.Y + int(float64(y)*float64(sh)/float64(th))
		y1 := b.Min.Y + int(float64(y+1)*float64(sh)/float64(th))
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < tw; x++ {
			x0 := b.Min.X + int(float64(x)*float64(sw)/float64(tw))
			x1 := b.Min.X + int(float64(x+1)*float64(sw)/float64(tw))
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var rs, gs, bs, as uint64
			var n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					r, g, bl, al := src.At(sx, sy).RGBA()
					rs += uint64(r)
					gs += uint64(g)
					bs += uint64(bl)
					as += uint64(al)
					n++
				}
			}
			if n == 0 {
				continue
			}
			px := color.RGBA64{
				R: uint16(rs / n), G: uint16(gs / n), B: uint16(bs / n), A: uint16(as / n),
			}
			// Over-composite so a transparent logo lands correctly on the maskable
			// backdrop instead of punching a hole in it.
			draw.Draw(dst, image.Rect(ox+x, oy+y, ox+x+1, oy+y+1),
				&image.Uniform{C: px}, image.Point{}, draw.Over)
		}
	}
	return dst
}

// serveAppIcon returns a handler for one public app-icon URL. It serves the
// operator's logo rendered to this exact size when one is usable, otherwise the
// embedded VayuPress mark.
//
// BOTH branches revalidate — the default is deliberately not cached immutable.
// serveFavicon documents why: a browser that cached an immutable default keeps
// serving it for a YEAR and never notices a later custom-logo upload, so the
// operator's new logo appears to do nothing. That is worse here than on a
// favicon, because these bytes are what the launcher copies at install time.
func (a *App) serveAppIcon(size int, maskable bool, fallback []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if b := a.customAppIcon(r.Context(), size, maskable); len(b) > 0 {
			serveFaviconBytes(w, r, b, "image/png")
			return
		}
		serveFaviconBytes(w, r, fallback, "image/png")
	}
}
