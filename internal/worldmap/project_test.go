// SPDX-License-Identifier: Apache-2.0

package worldmap

import (
	"math"
	"regexp"
	"strconv"
	"testing"
)

// The projection's frame: the meridian and the equator cross at the middle
// of the width, the antimeridian is at its edges, the pole at its top, and
// 60°S at the drawing's foot.
func TestTheProjectionsFrame(t *testing.T) {
	near := func(a, b float64) bool { return math.Abs(a-b) < 0.5 }
	for _, c := range []struct{ lat, lon, x, y float64 }{
		{0, 0, 500, 243.4},
		{0, -180, 0, 243.4},
		{0, 180, 1000, 243.4},
		{90, 0, 500, 0},
		{-60, 0, 500, 444.4},
	} {
		if x, y := Project(c.lat, c.lon); !near(x, c.x) || !near(y, c.y) {
			t.Errorf("Project(%v, %v) = %.1f, %.1f, want %.1f, %.1f", c.lat, c.lon, x, y, c.x, c.y)
		}
	}
}

var pathPoint = regexp.MustCompile(`[ML](\d+),(\d+)`)

// A city's dot lands inside its country's outline as drawn: the outlines
// were generated with the projection the dots are placed by, and a change
// to either alone moves the dots off their countries.
func TestACityLandsInsideItsCountry(t *testing.T) {
	shape := map[string]string{}
	for _, s := range Shapes {
		shape[s.Code] = s.D
	}
	for _, c := range []struct {
		code     string
		lat, lon float64
	}{
		{"IN", 18.5, 73.9},   // Pune
		{"FR", 48.9, 2.4},    // Paris
		{"US", 30.3, -97.7},  // Austin
		{"AU", -33.9, 151.2}, // Sydney
		{"BR", -23.6, -46.6}, // São Paulo
	} {
		d, ok := shape[c.code]
		if !ok {
			t.Fatalf("no outline for %s", c.code)
		}
		minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
		for _, m := range pathPoint.FindAllStringSubmatch(d, -1) {
			x, _ := strconv.ParseFloat(m[1], 64)
			y, _ := strconv.ParseFloat(m[2], 64)
			minX, maxX, minY, maxY = math.Min(minX, x), math.Max(maxX, x), math.Min(minY, y), math.Max(maxY, y)
		}
		if x, y := Project(c.lat, c.lon); x < minX || x > maxX || y < minY || y > maxY {
			t.Errorf("%s's city at %.0f, %.0f is outside its outline's box %.0f–%.0f, %.0f–%.0f", c.code, x, y, minX, maxX, minY, maxY)
		}
	}
	if len(Shapes) < 170 {
		t.Errorf("%d countries drawn", len(Shapes))
	}
}
