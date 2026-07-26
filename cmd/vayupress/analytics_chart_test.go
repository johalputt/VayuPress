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

// TestTrendChartInteractiveAndCSPSafe: the traffic chart must carry the pure-CSS
// hover layer (hit band, guide, dots, styled tooltip with date + counts) and stay
// strictly CSP-safe (no inline styles, no scripts, no external hosts).
func TestTrendChartInteractiveAndCSPSafe(t *testing.T) {
	out := osTrendChart(sampleSeries(30), "Traffic")
	assertCSPSafe(t, "osTrendChart", out)
	for _, want := range []string{
		`class="vp-pt__hit"`, // hoverable band
		`class="vp-pt__guide"`,
		`vp-pt__dot--pv`, `vp-pt__dot--vis`,
		`class="vp-pt__tip"`, // styled tooltip
		`vp-pt__tip-date`, `vp-pt__tip-val`,
		`id="vpTrendFill"`, // gradient area def (filled via CSS url())
		`Pageviews`, `Visitors`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("chart missing %q", want)
		}
	}
	// A concrete day's counts must be embedded in a tooltip (hover shows date+count).
	if !strings.Contains(out, "Jul 1") {
		t.Error("tooltip should show a human date")
	}
}

// TestTrendChartLongWindowStaysLight: beyond the styled-tooltip cap the chart
// must switch to lightweight native <title> tooltips (bounded DOM), never
// rendering hundreds of styled tooltip groups.
func TestTrendChartLongWindowStaysLight(t *testing.T) {
	out := osTrendChart(sampleSeries(400), "Traffic — long")
	if strings.Contains(out, `class="vp-pt__tip"`) {
		t.Error("long window must not render styled tooltip groups (DOM bloat)")
	}
	if !strings.Contains(out, "<title>") {
		t.Error("long window must still be hoverable via native <title>")
	}
	assertCSPSafe(t, "osTrendChart-long", out)
}

// TestLiveCardStructureCSPSafe: the Live panel must expose the poller hooks the
// JS fills (count, window, updated, and the three bar containers) and stay
// strictly CSP-safe (no inline styles/scripts). The bar containers are .vp-bars
// so the poller can render the same coloured bars the server renders elsewhere.
func TestLiveCardStructureCSPSafe(t *testing.T) {
	out := osLiveCard()
	assertCSPSafe(t, "osLiveCard", out)
	for _, want := range []string{
		`data-live`,
		`data-live-count`,
		`data-live-window`,
		`data-live-updated`,
		`vp-bars vm-live-list" data-live-countries`,
		`vp-bars vm-live-list" data-live-pages`,
		`vp-bars vm-live-list" data-live-referrers`,
		`vm-live-badge`,
		`vm-live-rings`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("live card missing %q", want)
		}
	}
	// The old table markup must be gone — bars, not <tbody> rows.
	if strings.Contains(out, "<tbody") || strings.Contains(out, "data-live-pages><table") {
		t.Error("live card should use .vp-bars containers, not tables")
	}
}

func TestTrendChartEdgeCases(t *testing.T) {
	if osTrendChart(nil, "x") != "" {
		t.Error("empty series must render nothing")
	}
	out := osTrendChart(sampleSeries(1), "single")
	assertCSPSafe(t, "osTrendChart-single", out)
	if !strings.Contains(out, "vp-trend-svg") {
		t.Error("single-point chart should still render")
	}
}
