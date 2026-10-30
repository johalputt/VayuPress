// SPDX-License-Identifier: Apache-2.0

package main

// analytics_map.go — where readers are, drawn on the world.
//
// Audience shades each country by its visitors over the period and puts a
// dot on each city the city table placed; Live draws the same map empty and
// admin-os-intel.js shades it from the live panel's poll. Hovering a country
// or a city says who is there; clicking it narrows the page to them. The
// outlines are Natural Earth's (internal/worldmap), inline: no tile, font or
// script comes from anyone else, as nothing does in the console. The map is
// a picture of the lists beside it, which hold every figure it shows and are
// what a screen reader reads, so the drawing itself is hidden from one.

import (
	"html"
	"math"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/worldmap"
)

// mapCountry is one country's shading and what its card says.
type mapCountry struct {
	n      int    // its readers, which set its shade
	said   string // its count in words: "1,234 visitors"
	detail string // the card's further lines, one a line
	href   string // the page narrowed to it, or ""
}

// mapDot is a city on the map.
type mapDot struct {
	lat, lon float64
	n        int
	name     string // "Pune, India"
	said     string
	href     string
}

// mapLevel is a count's shade, 1 to 5 on a logarithmic scale against the
// busiest country, so a country of one reader is seen beside one of
// thousands; 0 is none.
func mapLevel(n, max int) int {
	if n <= 0 || max <= 0 {
		return 0
	}
	l := 1 + int(4*math.Log1p(float64(n))/math.Log1p(float64(max)))
	if l > 5 {
		l = 5
	}
	return l
}

// osWorldMap is the map of countries and dots; live marks the one the live
// panel's script fills, whose countries and dots arrive with each poll.
func osWorldMap(countries map[string]mapCountry, dots []mapDot, live bool) string {
	max := 0
	for _, c := range countries {
		if c.n > max {
			max = c.n
		}
	}
	var b strings.Builder
	attr := ""
	if live {
		attr = " data-live-map"
	}
	b.WriteString(`<div class="sa-map" data-map` + attr + `><svg class="sa-map__svg" viewBox="0 0 ` + strconv.Itoa(int(worldmap.Width)) + ` ` +
		strconv.Itoa(int(worldmap.Height)) + `" aria-hidden="true" focusable="false"><g>`)
	for _, s := range worldmap.Shapes {
		b.WriteString(`<path class="sa-map__c" d="` + s.D + `"`)
		if s.Code != "" {
			b.WriteString(` data-code="` + s.Code + `" data-name="` + html.EscapeString(s.Name) + `"`)
		}
		if c, ok := countries[s.Code]; ok && s.Code != "" && c.n > 0 {
			b.WriteString(` data-level="` + strconv.Itoa(mapLevel(c.n, max)) + `" data-n="` + html.EscapeString(c.said) + `"`)
			if c.detail != "" {
				b.WriteString(` data-detail="` + html.EscapeString(c.detail) + `"`)
			}
			if c.href != "" {
				b.WriteString(` data-href="` + html.EscapeString(c.href) + `"`)
			}
		}
		b.WriteString(`></path>`)
	}
	b.WriteString(`</g><g data-map-dots>`)
	b.WriteString(osMapDots(dots))
	b.WriteString(`</g></svg><div class="sa-map__tip" hidden></div></div>`)
	return b.String()
}

// osMapDots is dots as circles, each sized by its readers against the
// busiest city's.
func osMapDots(dots []mapDot) string {
	max := 0
	for _, d := range dots {
		if d.n > max {
			max = d.n
		}
	}
	var b strings.Builder
	for _, d := range dots {
		x, y := worldmap.Project(d.lat, d.lon)
		r := 3.0
		if max > 0 {
			r += 5 * math.Sqrt(float64(d.n)/float64(max))
		}
		b.WriteString(`<circle class="sa-map__dot" cx="` + strconv.FormatFloat(x, 'f', 1, 64) + `" cy="` + strconv.FormatFloat(y, 'f', 1, 64) +
			`" r="` + strconv.FormatFloat(r, 'f', 1, 64) + `" data-name="` + html.EscapeString(d.name) + `" data-n="` + html.EscapeString(d.said) + `"`)
		if d.href != "" {
			b.WriteString(` data-href="` + html.EscapeString(d.href) + `"`)
		}
		b.WriteString(`></circle>`)
	}
	return b.String()
}
