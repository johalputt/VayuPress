// SPDX-License-Identifier: Apache-2.0

package sitedoc

import (
	"errors"
	"math"
	"regexp"
	"strings"
	"testing"
)

// The contrast helper judges the dark-mode shade below, so it is held to
// published WCAG values first rather than trusted.
func TestContrastMatchesWCAG(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want float64
	}{
		{"#000000", "#ffffff", 21},
		{"#777777", "#ffffff", 4.48},
		{"#ffffff", "#ffffff", 1},
	} {
		if got := contrast(c.a, c.b); math.Abs(got-c.want) > 0.01 {
			t.Errorf("contrast(%s, %s) = %.3f, want %.2f", c.a, c.b, got, c.want)
		}
	}
}

// One seed per rule of the brand.
func TestTheBrandIsValidated(t *testing.T) {
	for _, c := range []struct {
		style Style
		msg   string
	}{
		{Style{Accent: "teal"}, "#rrggbb"},
		{Style{Accent: "#0f766"}, "#rrggbb"},
		{Style{Accent: "#f5d90a"}, "too light"},
		// Just under the line: 4.2:1 on the page. A weaker threshold would
		// let it through, where the yellow above fails any.
		{Style{Accent: "#7a7a7a"}, "too light"},
		{Style{Font: "comic"}, "not a font"},
		{Style{Corners: "wavy"}, "not a corner style"},
	} {
		d := validDoc()
		st := c.style
		d.Style = &st
		var fe *FieldError
		if err := Validate(d); !errors.As(err, &fe) || !strings.HasPrefix(fe.Path, "style.") || !strings.Contains(fe.Msg, c.msg) {
			t.Errorf("%+v: got %v, want a style.* refusal saying %q", c.style, err, c.msg)
		}
	}
	d := validDoc()
	d.Style = &Style{Accent: "#0F766E", Font: "serif", Corners: "round"}
	if err := Validate(d); err != nil {
		t.Errorf("a valid brand was refused: %v", err)
	}
}

var accentRule = regexp.MustCompile(`--vb-accent:(#[0-9a-f]{6})`)

// The brand's stylesheet: the chosen accent on light pages, and on dark
// pages a lighter shade of it that still reads, with dark button text if
// that is what reads on it.
func TestTheBrandStylesheet(t *testing.T) {
	css := StyleCSS(&Style{Accent: "#1d4ed8", Font: "mono", Corners: "square"}, false)
	light, dark, ok := strings.Cut(css, "@media(prefers-color-scheme:dark)")
	if !ok {
		t.Fatalf("no dark-mode rule:\n%s", css)
	}
	for _, want := range []string{"--vb-accent:#1d4ed8", "--vb-on-accent:#fff", "--vb-radius:0", "font-family:" + Fonts["mono"]} {
		if !strings.Contains(light, want) {
			t.Errorf("light rule lacks %s:\n%s", want, light)
		}
	}
	m := accentRule.FindStringSubmatch(dark)
	if m == nil {
		t.Fatalf("dark rule has no accent:\n%s", dark)
	}
	if m[1] == "#1d4ed8" {
		t.Error("dark mode kept an accent chosen for a light page")
	}
	if c := contrast(m[1], darkPage); c < minContrast {
		t.Errorf("dark-mode accent %s reads at %.2f:1 on a dark page", m[1], c)
	}
	if StyleCSS(nil, false) != "" || StyleCSS(&Style{}, false) != "" {
		t.Error("an empty brand produced a stylesheet")
	}
}

// A design dark in both schemes (Forge) gets the readable shade throughout.
func TestADarkDesignGetsTheDarkShadeEverywhere(t *testing.T) {
	css := StyleCSS(&Style{Accent: "#1d4ed8"}, true)
	if strings.Contains(css, "@media") {
		t.Errorf("a dark design's brand is split by colour scheme:\n%s", css)
	}
	m := accentRule.FindStringSubmatch(css)
	if m == nil || contrast(m[1], darkPage) < minContrast {
		t.Errorf("a dark design's accent does not read on its page: %v", m)
	}
}

func TestLayoutsAreValidatedAndDrawn(t *testing.T) {
	d := validDoc()
	d.Pages[0].Sections[0].Variant = "diagonal"
	var fe *FieldError
	if err := Validate(d); !errors.As(err, &fe) || fe.Path != "pages[0].sections[0].variant" {
		t.Errorf("an unknown hero layout: %v", err)
	}
	d = validDoc()
	d.Pages[0].Sections[1].Variant = "grid" // a text section has no grid
	if err := Validate(d); !errors.As(err, &fe) || fe.Path != "pages[0].sections[1].variant" {
		t.Errorf("a layout of another kind: %v", err)
	}

	d = validDoc()
	d.Pages[0].Sections[0].Variant = "split"
	d.Pages[0].Sections[2].Variant = "list"
	d.Pages[0].Sections[3].Variant = "wide"
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
	home, _ := d.Home()
	out := Render(d, home, Options{})
	for _, want := range []string{`class="vb-hero vb-hero--split"`, `class="vb-services vb-services--list"`,
		`class="vb-gallery vb-gallery--wide"`, `<a class="vb-zoom" href="https://cdn.example/a.jpg">`, GalleryScriptTag()} {
		if !strings.Contains(out, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	// The first layout is the design's own and carries no modifier.
	d.Pages[0].Sections[0].Variant = "center"
	if out := Render(d, home, Options{}); strings.Contains(out, "vb-hero--center") {
		t.Error("the default layout was drawn with a modifier class")
	}
	menu, _ := d.Page("menu")
	if strings.Contains(Render(d, menu, Options{}), "site-gallery.js") {
		t.Error("a page without a gallery loads the gallery script")
	}
}
