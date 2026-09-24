// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_components.go — string adapters over internal/ui for page bodies.
//
// A page used to say "this failed" with an emoji and an inline colour
// (`style="color:#d9844f">⚠ …`), which ignores the colour scheme, the Tor
// accent and forced colours alike. These primitives carry the tone as a class,
// so the stylesheet decides what "warning" looks like in every scheme.

import "github.com/johalputt/vayupress/internal/ui"

// saCallout is ui.Callout for pages that build markup as strings: a failure
// (danger), a risk (warn), a result (ok) or a fact (info). bodyHTML is trusted
// markup; callers escape what they put in it.
func saCallout(tone, bodyHTML string) string {
	return string(ui.Callout(tone, ui.HTML(bodyHTML)))
}
