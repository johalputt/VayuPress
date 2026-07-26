// SPDX-License-Identifier: Apache-2.0

package avatar

import (
	"strings"
	"testing"
)

// TestFaceDeterministicAndValid checks the generated avatars are stable per seed,
// well-formed SVG, self-contained (no <style>/<script>/external refs so they are
// CSP-safe as an <img>), and vary across seeds.
func TestFaceDeterministicAndValid(t *testing.T) {
	a := Auto("ankush@example.com", "male")
	b := Auto("ankush@example.com", "male")
	if a != b {
		t.Error("Auto must be deterministic for the same seed+gender")
	}
	if Auto("ankush@example.com", "male") == Auto("someone-else@example.com", "male") {
		t.Error("different seeds should produce different avatars")
	}
	for _, svg := range []string{a, Cartoon(0, "x"), Cartoon(7, "y"), Auto("f@x.com", "female"), Auto("n@x.com", "")} {
		if !strings.HasPrefix(svg, "<svg") || !strings.HasSuffix(svg, "</svg>") {
			t.Fatalf("not a well-formed svg: %.60s", svg)
		}
		// Self-contained: no styles/scripts, no external fetches (the xmlns
		// namespace URL is not a fetch and is expected). Internal gradient
		// references — url(#id) resolving inside the same document — are allowed
		// (pure paint, no fetch, no script); only external url() fetches are not.
		for _, bad := range []string{"<style", "<script", "<image", "xlink:href", `href="http`, "url(http", "url(//", "url('", `url("`} {
			if strings.Contains(svg, bad) {
				t.Errorf("avatar must be self-contained/CSP-safe; found %q", bad)
			}
		}
		// The gradients we do use must be internal fragment refs.
		if strings.Contains(svg, "url(") && !strings.Contains(svg, "url(#") {
			t.Errorf("avatar uses a non-internal url() reference")
		}
	}
}

// TestCartoonBounds ensures out-of-range cartoon indices are clamped, never panic.
func TestCartoonBounds(t *testing.T) {
	for _, n := range []int{-5, 0, CartoonCount - 1, CartoonCount, 999} {
		if svg := Cartoon(n, "seed"); !strings.HasPrefix(svg, "<svg") {
			t.Errorf("Cartoon(%d) not an svg", n)
		}
	}
}
