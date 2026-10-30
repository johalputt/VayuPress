// SPDX-License-Identifier: Apache-2.0

// Package worldmap is the world the console draws its readers on: the
// countries' outlines (shapes.go, generated from Natural Earth's public-domain
// 1:110m countries by scripts/worldmap) and the projection that places a
// city on them, one function for both so a dot lands where its country is
// drawn. The projection is Equal Earth (Šavrič, Patterson and Jenny, 2018):
// equal-area, so a country's size on the map is its size, unlike the
// stretched poles of a plain longitude-by-latitude grid.
package worldmap

import "math"

// Width and Height are the drawing's units: the world from 180°W to 180°E,
// and from the pole to 60°S, below which no reader lives.
const (
	Width  = 1000.0
	Height = 445.0
)

// Equal Earth's polynomial.
const (
	a1 = 1.340264
	a2 = -0.081106
	a3 = 0.000893
	a4 = 0.003796
)

var (
	m     = math.Sqrt(3) / 2
	scale = Width / (2 * raw(0, math.Pi).x)
)

type pt struct{ x, y float64 }

func raw(lat, lon float64) pt {
	t := math.Asin(m * math.Sin(lat))
	t2 := t * t
	t6 := t2 * t2 * t2
	return pt{
		x: 2 * math.Sqrt(3) * lon * math.Cos(t) / (3 * (9*a4*t6*t2 + 7*a3*t6 + 3*a2*t2 + a1)),
		y: t * (a1 + a2*t2 + t6*(a3+a4*t2)),
	}
}

// Project is where a point at lat, lon (degrees) is drawn, in the drawing's
// units, x from the left and y from the top.
func Project(lat, lon float64) (x, y float64) {
	p := raw(lat*math.Pi/180, lon*math.Pi/180)
	return Width/2 + p.x*scale, raw(math.Pi/2, 0).y*scale - p.y*scale
}

// Shape is one country: its ISO 3166 code, its name, and its outline as an
// SVG path in Project's units.
type Shape struct{ Code, Name, D string }
