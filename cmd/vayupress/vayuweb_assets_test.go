// SPDX-License-Identifier: Apache-2.0

package main

// vayuweb_assets_test.go — the first-party web-building assets, and the favicon
// a hosted domain must not borrow from the primary.
//
// Both come from the same root cause: isolation and the strict CSP were correct
// everywhere except at the exact points a person looks at. A hand-built site
// could not load a framework or a typeface without reaching off-origin and being
// silently refused, and the browser tab on a client's domain showed the studio's
// logo.

import (
	"io/fs"
	"strings"
	"testing"
)

// The assets have to actually BE in the binary. A route that allowlists a
// filename the embedded tree does not contain is a 404 with extra steps, and it
// would pass any test that only inspects the allowlist.
func TestTheFirstPartyWebAssetsAreEmbedded(t *testing.T) {
	for name := range vayuWebAllowlist {
		b, err := fs.ReadFile(embeddedStaticFS, "vayuweb/"+name)
		if err != nil {
			t.Errorf("vayuweb/%s is allowlisted but not embedded, so the route 404s: %v", name, err)
			continue
		}
		if len(b) < 1024 {
			t.Errorf("vayuweb/%s is only %d bytes — that is not a usable build", name, len(b))
		}
	}
}

// The Alpine build served here MUST be the CSP variant. The standard build
// compiles inline expression strings with new Function(), which needs
// 'unsafe-eval' — shipping it would mean weakening script-src for every page on
// the install so that one page could be interactive.
func TestTheAlpineBuildServedIsTheCSPVariant(t *testing.T) {
	b, err := fs.ReadFile(embeddedStaticFS, "vayuweb/alpine-csp.min.js")
	if err != nil {
		t.Fatalf("the Alpine build is missing: %v", err)
	}
	src := string(b)
	// The CSP build registers components through Alpine.data and does not carry
	// an expression compiler.
	if !strings.Contains(src, "data") {
		t.Error("the served build does not look like Alpine at all")
	}
	if strings.Contains(src, "new Function(") {
		t.Fatal("the served Alpine build constructs functions from strings, so it needs " +
			"'unsafe-eval'. Serving it would force script-src open for every page on the " +
			"install — which is the opposite of what this asset exists to avoid")
	}
}

// A typeface a bundle cannot request is a typeface it does not have. font-src is
// 'self', so every family the product's own site uses has to be reachable from
// this origin or the freedom is theoretical.
func TestTheFontsABundleNeedsAreServable(t *testing.T) {
	need := []string{
		"space-grotesk-latin-400.woff2",  // display
		"inter-latin-400.woff2",          // body
		"jetbrains-mono-latin-400.woff2", // mono
	}
	for _, f := range need {
		if !fontAllowlist[f] {
			t.Errorf("%s is not allowlisted, so a hand-built bundle cannot load it and the "+
				"operator's only remaining option is a font host the CSP will refuse", f)
			continue
		}
		if _, err := fs.ReadFile(embeddedStaticFS, "fonts/"+f); err != nil {
			t.Errorf("%s is allowlisted but not embedded: %v", f, err)
		}
	}
}

// The allowlist must stay an allowlist. It is the only thing standing between a
// URL parameter and the embedded filesystem.
func TestTheWebAssetRouteRefusesAnythingNotAllowlisted(t *testing.T) {
	for _, bad := range []string{"../js/admin-os.js", "../../go.mod", "index.html", ""} {
		if _, ok := vayuWebAllowlist[bad]; ok {
			t.Errorf("%q is allowlisted and should not be", bad)
		}
	}
	// And the values are content types, not paths — a blank one would make the
	// route serve bytes with no declared type.
	for name, ctype := range vayuWebAllowlist {
		if !strings.Contains(ctype, "/") {
			t.Errorf("%s has no usable Content-Type (%q)", name, ctype)
		}
	}
}
