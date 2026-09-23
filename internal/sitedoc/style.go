// SPDX-License-Identifier: Apache-2.0

package sitedoc

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Fonts are the typefaces a site may choose, as system font stacks. No web
// fonts: nothing is downloaded from a third party, so the choice costs a
// visitor no request, no privacy and no layout shift, and the CSP stays as it
// is.
var Fonts = map[string]string{
	"sans":    `system-ui,-apple-system,"Segoe UI",Roboto,Helvetica,Arial,sans-serif`,
	"serif":   `Georgia,"Iowan Old Style","Palatino Linotype",Palatino,serif`,
	"rounded": `ui-rounded,"SF Pro Rounded","Nunito","Varela Round",system-ui,sans-serif`,
	"mono":    `ui-monospace,"SF Mono",Menlo,Consolas,"Liberation Mono",monospace`,
}

// Corners are the roundings a site may choose.
var Corners = map[string]string{"square": "0", "soft": "8px", "round": "16px"}

var hexColour = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// The backgrounds the accent is read against: the base stylesheet's light and
// dark page colours. Each design tints its own slightly, and checking against
// these keeps a margin rather than tracking eleven near-identical values.
const (
	lightPage = "#fcfcfa"
	darkPage  = "#101210"
)

// minContrast is WCAG AA for normal text. The accent colours link text and
// the button label sits on it, so it is held to the text standard rather
// than the 3:1 allowed for large text and graphics.
const minContrast = 4.5

func validateStyle(st Style) error {
	if st.Accent != "" {
		if !hexColour.MatchString(st.Accent) {
			return fail("style.accent", "%q must be a colour written #rrggbb", st.Accent)
		}
		if c := contrast(st.Accent, lightPage); c < minContrast {
			return fail("style.accent", "%s is too light to read as text on the page or to carry white "+
				"button text (contrast %.1f:1, needs %.1f:1) — choose a darker shade", st.Accent, c, minContrast)
		}
	}
	if st.Font != "" {
		if _, ok := Fonts[st.Font]; !ok {
			return fail("style.font", "%q is not a font (%s)", st.Font, keys(Fonts))
		}
	}
	if st.Corners != "" {
		if _, ok := Corners[st.Corners]; !ok {
			return fail("style.corners", "%q is not a corner style (%s)", st.Corners, keys(Corners))
		}
	}
	return nil
}

func keys(m map[string]string) string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// StyleCSS is the stylesheet a site's brand adds after its design's own. It
// sets the design's custom properties on body.vb, which has the specificity
// of each design's body.vb--<key> rule and comes after it, so the brand wins
// without !important.
//
// Dark mode gets its own accent. The colour chosen passes on a light page,
// and the same colour on a dark page usually does not; it is lightened, hue
// kept, until it reads there, and the button label turns dark to stay
// readable on it.
//
// darkDesign is a design whose page is dark in both schemes (bizsite
// Template.Dark): there the dark-mode shade is the only one that reads.
func StyleCSS(st *Style, darkDesign bool) string {
	if st == nil {
		return ""
	}
	var light []string
	var dark []string
	if st.Accent != "" {
		a := strings.ToLower(st.Accent)
		d := lightenFor(a, darkPage)
		on := "#fff"
		if contrast("#111111", d) >= contrast("#ffffff", d) {
			on = "#111"
		}
		if darkDesign {
			light = append(light, "--vb-accent:"+d, "--vb-on-accent:"+on)
		} else {
			light = append(light, "--vb-accent:"+a, "--vb-on-accent:#fff")
			dark = append(dark, "--vb-accent:"+d, "--vb-on-accent:"+on)
		}
	}
	if r, ok := Corners[st.Corners]; ok {
		light = append(light, "--vb-radius:"+r)
	}
	if f, ok := Fonts[st.Font]; ok {
		light = append(light, "font-family:"+f)
	}
	if len(light) == 0 {
		return ""
	}
	css := "body.vb{" + strings.Join(light, ";") + "}\n"
	if len(dark) > 0 {
		css += "@media(prefers-color-scheme:dark){body.vb{" + strings.Join(dark, ";") + "}}\n"
	}
	return css
}

// lightenFor mixes c toward white until it has minContrast against bg.
func lightenFor(c, bg string) string {
	r, g, b := rgb(c)
	for i := 0; i <= 20; i++ {
		t := float64(i) / 20
		mix := fmt.Sprintf("#%02x%02x%02x", mixTo(r, t), mixTo(g, t), mixTo(b, t))
		if contrast(mix, bg) >= minContrast {
			return mix
		}
	}
	return "#ffffff"
}

func mixTo(v int, t float64) int { return int(math.Round(float64(v) + (255-float64(v))*t)) }

func rgb(hex string) (int, int, int) {
	v, _ := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	return int(v >> 16 & 0xff), int(v >> 8 & 0xff), int(v & 0xff)
}

// contrast is the WCAG 2 contrast ratio between two #rrggbb colours.
func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(hex string) float64 {
	r, g, b := rgb(hex)
	ch := func(v int) float64 {
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(r) + 0.7152*ch(g) + 0.0722*ch(b)
}
