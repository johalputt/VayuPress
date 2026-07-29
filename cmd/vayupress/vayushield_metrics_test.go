// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/shieldaudit"
	"github.com/johalputt/vayupress/internal/vayushield"
)

// VayuShield's live state was already in memory and thrown away every ten
// seconds into an HTML fragment, so a panel was the only way to see it. A panel
// cannot page anyone at 3am, retain history, or alert on a trend — which is
// exactly what a shield's series are needed for.

// TestShieldMetricsAreExported — the whole gap: `grep -c vayushield` in the
// metrics handler was 0, next to ~37 vayupress_* series.
func TestShieldMetricsAreExported(t *testing.T) {
	a := &App{vayuShield: vayushield.New(vayushield.Config{Enabled: true})}
	a.vayuShield.ApplySettings(vayushield.Settings{Enabled: true})

	var b strings.Builder
	a.writeShieldMetrics(&b)
	out := b.String()

	for _, series := range []string{
		"vayushield_under_attack",
		"vayushield_surge_active",
		"vayushield_requests_per_second",
		"vayushield_in_flight",
		"vayushield_blocklisted",
		"vayushield_suspects",
		"vayushield_reputation_jailed",
		"vayushield_fair_shed_total",
		"vayushield_pardons_total",
		"vayushield_challenges_served_total",
		"vayushield_calibration_bias",
	} {
		if !strings.Contains(out, series+" ") {
			t.Errorf("missing series %q", series)
		}
		// Prometheus text format wants HELP and TYPE, or the series is untyped
		// and a rate() over a gauge silently produces nonsense.
		if !strings.Contains(out, "# TYPE "+series+" ") {
			t.Errorf("series %q has no TYPE line", series)
		}
		if !strings.Contains(out, "# HELP "+series+" ") {
			t.Errorf("series %q has no HELP line", series)
		}
	}
}

// TestPostureFailuresAreAlertableSeparately — the report carries a permanent
// failing row (volumetric absorption) that no configuration can turn green. An
// alert on the raw fail count would therefore fire forever on a healthy install,
// which is how a page gets muted and then ignored. The actionable count has to
// be its own series.
func TestPostureFailuresAreAlertableSeparately(t *testing.T) {
	a := &App{vayuShield: vayushield.New(vayushield.Config{Enabled: true})}
	a.vayuShield.ApplySettings(vayushield.Settings{Enabled: true})
	t.Setenv("VAYUSHIELD_CONTROL_DIR", t.TempDir())

	var b strings.Builder
	a.writeShieldMetrics(&b)
	out := b.String()

	if !strings.Contains(out, "vayushield_posture_failures_actionable ") {
		t.Fatal("no actionable-failures series — an operator can only alert on a count that never reaches zero")
	}
	if !strings.Contains(out, `vayushield_posture_checks{status="fail"}`) {
		t.Error("the raw per-status breakdown is missing")
	}
	// The discriminating assertion, and a surviving mutant is why it is here:
	// checking only that the series EXISTS passes just as happily when it is the
	// raw fail count under a different name. It has to be the raw count minus the
	// permanent baseline, or the alert it exists for fires forever.
	raw := metricValue(t, out, `vayushield_posture_checks{status="fail"}`)
	actionable := metricValue(t, out, "vayushield_posture_failures_actionable")
	want := raw - float64(shieldaudit.BaselineFails)
	if want < 0 {
		want = 0
	}
	if actionable != want {
		t.Errorf("actionable = %v with %v raw failures and a baseline of %d, want %v — "+
			"an alert on this would fire on a healthy install", actionable, raw, shieldaudit.BaselineFails, want)
	}
	if raw == actionable && shieldaudit.BaselineFails > 0 {
		t.Error("the actionable series is identical to the raw count — it is the same number twice")
	}
	if actionable < 0 {
		t.Errorf("actionable failures went negative: %v", actionable)
	}
}

// metricValue reads a single Prometheus sample's value out of an exposition body.
func metricValue(t *testing.T, body, name string) float64 {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, name+" ") {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(line, name)), 64)
		if err != nil {
			t.Fatalf("unparseable value for %s: %q", name, line)
		}
		return v
	}
	t.Fatalf("series %q not found in:\n%s", name, body)
	return 0
}

// TestMetricsSurviveANilShield — /metrics is scraped continuously, including
// while the shield is disabled or still booting. A nil dereference there takes
// out the endpoint an operator watches everything else through.
func TestMetricsSurviveANilShield(t *testing.T) {
	a := &App{}
	var b strings.Builder
	a.writeShieldMetrics(&b) // must not panic
	if b.Len() != 0 {
		t.Errorf("emitted %d bytes with no shield — a scraper would record zeros as fact", b.Len())
	}
}
