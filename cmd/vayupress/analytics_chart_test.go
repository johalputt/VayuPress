// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/analytics"
)

func sampleSeries(n int) []analytics.DayPageviews {
	out := make([]analytics.DayPageviews, n)
	for i := 0; i < n; i++ {
		out[i] = analytics.DayPageviews{
			Date:     "2026-07-" + twoDigit(i%28+1),
			Count:    100 + i*7,
			Visitors: 60 + i*4,
		}
	}
	return out
}

func twoDigit(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// Every day of the chart is a band saying its counts in words, which the
// browser shows without the script and admin-os-intel.js shows in the
// chart's readout; and the chart stays CSP-safe (no inline styles, no
// scripts, no external hosts).
func TestTrendChartSaysEachDaysCounts(t *testing.T) {
	out := osTrendChart(sampleSeries(30), "Traffic", nil)
	assertCSPSafe(t, "osTrendChart", out)
	if n := strings.Count(out, `<rect class="vp-pt__hit"`); n != 30 {
		t.Errorf("%d day bands, want 30", n)
	}
	if !strings.Contains(out, "<title>Jul 1: 100 page views, 60 visitors</title>") {
		t.Error("the first day does not say its counts")
	}
	for _, want := range []string{`<div class="vp-trend-tip" hidden></div>`, `>Page views<`, `>Visitors<`} {
		if !strings.Contains(out, want) {
			t.Errorf("chart missing %q", want)
		}
	}
}

// No word is drawn in the SVG: it is stretched to its box, so its text would
// be stretched with it, or shrunk to 5 pixels on a phone. The axis is HTML:
// the first and last day, the middle one when it falls on a day, and the
// three gridlines' values.
func TestTheTrendsWordsAreHTML(t *testing.T) {
	out := osTrendChart(sampleSeries(27), "Traffic", nil)
	svg := out[strings.Index(out, "<svg"):strings.Index(out, "</svg>")]
	if strings.Contains(svg, "<text") {
		t.Error("the SVG draws text")
	}
	if !strings.Contains(out, `<div class="vp-trend-x"><span>Jul 1</span><span>Jul 14</span><span class="vp-trend-x__end">Jul 27</span></div>`) {
		t.Errorf("27 days: axis is not first, middle and last:\n%s", out[strings.Index(out, `<div class="vp-trend-x"`):])
	}
	// 28 days have no middle day; a label between two days would name one
	// it is not over.
	if even := osTrendChart(sampleSeries(28), "Traffic", nil); !strings.Contains(even, `<div class="vp-trend-x"><span>Jul 1</span><span></span><span class="vp-trend-x__end">Jul 28</span></div>`) {
		t.Error("28 days: a middle label is placed between two days")
	}
	// The highest count in sampleSeries(27) is day 27's page views, 282.
	for _, want := range []string{`vp-trend-y--top">282<`, `vp-trend-y--mid">141<`, `vp-trend-y--base">0<`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing gridline value %q", want)
		}
	}
}

// The chart is two lines in the page's ink and nothing under them: no
// gradient, no filled area, no colour of its own.
func TestTrendChartIsTwoLinesAndNoFill(t *testing.T) {
	out := osTrendChart(sampleSeries(30), "Traffic", nil)
	if n := strings.Count(out, "<polyline"); n != 2 {
		t.Errorf("%d lines, want page views and visitors", n)
	}
	for _, banned := range []string{"<linearGradient", "<path", "url(#", "vp-trend-area", "chart-1", "chart-2"} {
		if strings.Contains(out, banned) {
			t.Errorf("chart carries %q", banned)
		}
	}
}

// A line is drawn from two days with views. One day is a figure, which the
// band already gives; a range of days with none is no trend at all. The
// zero days are counted out, not the series' length: a week with one day
// of views is still one day.
func TestATrendNeedsTwoDaysWithViews(t *testing.T) {
	week := sampleSeries(7)
	for i := range week {
		week[i].Count, week[i].Visitors = 0, 0
	}
	if osTrendChart(nil, "x", nil) != "" || osTrendChart(week, "x", nil) != "" {
		t.Error("a range with no views draws a chart")
	}
	week[3].Count, week[3].Visitors = 5, 2
	if osTrendChart(week, "x", nil) != "" {
		t.Error("one day with views draws a chart")
	}
	week[5].Count, week[5].Visitors = 3, 1
	if out := osTrendChart(week, "x", nil); !strings.Contains(out, "vp-trend-svg") {
		t.Error("two days with views draw no chart")
	}
}

// Who is reading now: the count is a figure like the band's, with the lists
// the script fills (pages, referrers, channels, countries, cities, devices,
// browsers), and nothing that restates it (a LIVE pill, pulsing rings).
func TestLiveCardIsAFigureAndItsLists(t *testing.T) {
	out := osLiveCard("/os/api/analytics/realtime", "")
	assertCSPSafe(t, "osLiveCard", out)
	for _, want := range []string{
		`data-live-src="/os/api/analytics/realtime"`,
		`<div class="stat-card__label">Reading now</div><div class="stat-card__value" data-live-count>`,
		`data-live-window`, `data-live-updated`,
		`<div class="vp-bars" data-live-pages data-live-dim="path">`,
		`<div class="vp-bars" data-live-referrers data-live-dim="visit_referrer">`,
		`<div class="vp-bars" data-live-channels data-live-dim="visit_channel">`,
		`<div class="vp-bars" data-live-countries data-live-dim="country">`,
		`<div class="vp-bars" data-live-cities data-live-dim="city">`,
		`<div class="vp-bars" data-live-devices data-live-dim="device">`,
		`<div class="vp-bars" data-live-browsers data-live-dim="browser">`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("live card missing %q", want)
		}
	}
	for _, banned := range []string{"vm-live-badge", "vm-live-rings", "<tbody"} {
		if strings.Contains(out, banned) {
			t.Errorf("live card carries %q", banned)
		}
	}
}

// Every bar is one ink: no row carries a colour of its own.
func TestEveryBarIsOneInk(t *testing.T) {
	var rows []osChartBar
	for i := 0; i < 10; i++ {
		rows = append(rows, osChartBar{Label: "/p" + twoDigit(i), Value: 10 - i})
	}
	out := osBarList(rows, osShareOfListed(), "")
	if n := strings.Count(out, `<span class="vp-bar__fill w-`); n != 10 {
		t.Errorf("%d plain bars of 10:\n%s", n, out)
	}
	if strings.Contains(out, "vp-bar--") {
		t.Errorf("a bar carries a colour class:\n%s", out)
	}
}

// An empty list is one sentence in the page's empty state, where its rows
// would be.
func TestAnEmptyBarListIsOneSentence(t *testing.T) {
	if got := osBarList(nil, osShareOfListed(), "No search engine sent a reader."); got != `<p class="table-empty">No search engine sent a reader.</p>` {
		t.Errorf("empty list = %q", got)
	}
}
