// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_components.go — markup primitives shared by page bodies.
//
// A page used to say "this failed" with an emoji and an inline colour
// (`style="color:#d9844f">⚠ …`), which ignores the colour scheme, the Tor
// accent and forced colours alike. These primitives carry the tone as a class,
// so the stylesheet decides what "warning" looks like in every scheme.

// saCallout is a block that tells the operator something about the page: a
// failure (danger), a risk (warn), a result (ok) or a fact (info). bodyHTML is
// trusted markup; callers escape what they put in it. A danger callout is
// announced (role="alert"); the others are polite status.
func saCallout(tone, bodyHTML string) string {
	icon, role := "info", "status"
	switch tone {
	case "danger":
		icon, role = "error", "alert"
	case "warn":
		icon = "warn"
	case "ok":
		icon = "check-c"
	}
	return `<div class="callout callout--` + tone + `" role="` + role + `">` + saIcon(icon) + `<div class="callout__body">` + bodyHTML + `</div></div>`
}
