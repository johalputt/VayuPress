// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_icons.go — the console's string-building side of the icon set, which
// lives in internal/ui (one set, one source). Pages here build markup as
// strings, so these convert at the boundary rather than every call site.

import (
	"strings"

	"github.com/johalputt/vayupress/internal/ui"
)

// saIcon renders one icon from the set (ui.Icon).
func saIcon(name string) string { return string(ui.Icon(name)) }

// saSprite is the set as one hidden sprite, emitted once per page.
var saSprite = string(ui.Sprite)

// saMark is the VayuOS mark: three strokes of moving air, the middle one in the
// accent.
var saMark = strings.Join([]string{
	`<svg class="sa-mark__glyph" viewBox="0 0 20 20" fill="none" aria-hidden="true">`,
	`<path d="M2.5 6.2c2.6-1.6 5.3-1.6 8 0s5.4 1.6 7 .4" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/>`,
	`<path class="sa-mark__accent" d="M2.5 10.4c2.6-1.6 5.3-1.6 8 0s5.4 1.6 7 .4" stroke-width="1.6" stroke-linecap="round"/>`,
	`<path d="M2.5 14.6c2.6-1.6 5.3-1.6 6.4-.6" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" opacity=".55"/>`,
	`</svg>`,
}, "")
