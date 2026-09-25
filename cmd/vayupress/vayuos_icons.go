// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_icons.go — the console's string-building side of the icon set, which
// lives in internal/ui (one set, one source). Pages here build markup as
// strings, so these convert at the boundary rather than every call site.

import "github.com/johalputt/vayupress/internal/ui"

// saIcon renders one icon from the set (ui.Icon).
func saIcon(name string) string { return string(ui.Icon(name)) }

// saSprite is the set as one hidden sprite, emitted once per page.
var saSprite = string(ui.Sprite)

// saMark draws the VayuPress mark as the brand's own image, not a redrawing:
// the mark cut from docs/site/assets/logo-dark.png (the white logo, above its
// wordmark) with every pixel kept, and its black twin (the same alpha, black)
// for the light schemes. Both are in the page; the scheme's --mark-white and
// --mark-black tokens show one, so every scheme variant follows without its
// own selector.
func saMark() string {
	img := func(tone string) string {
		rel := "img/vayupress-mark-" + tone + ".png"
		return `<img class="sa-mark__img sa-mark__img--` + tone + `" src="/os/static/` + rel + `?v=` + assetVer(rel) + `" alt="" width="571" height="427">`
	}
	return `<span class="sa-mark__glyph" aria-hidden="true">` + img("white") + img("black") + `</span>`
}
