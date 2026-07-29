// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/shieldaudit"
	"github.com/johalputt/vayupress/internal/vayushield"
	"github.com/johalputt/vayupress/internal/vayushield/botdb"
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

// --- Audit trail --------------------------------------------------------------

// TestTrailWindowIsClampedToSomethingAnswerable — the window comes straight off
// the query string. Unbounded, a caller could ask for a scan over the whole
// table on the same SQLite the site is served from.
func TestTrailWindowIsClampedToSomethingAnswerable(t *testing.T) {
	for _, tc := range []struct {
		q    string
		want int
	}{
		{"", 24},
		{"hours=1", 1},
		{"hours=168", 168},
		{"hours=0", 24},
		{"hours=-5", 24},
		{"hours=999999", 24 * 90},
		{"hours=notanumber", 24},
	} {
		r := httptest.NewRequest(http.MethodGet, "/os/shield/section/trail?"+tc.q, nil)
		if got := shieldTrailHours(r); got != tc.want {
			t.Errorf("shieldTrailHours(%q) = %d, want %d", tc.q, got, tc.want)
		}
	}
	if got := shieldTrailHours(nil); got != 24 {
		t.Errorf("shieldTrailHours(nil) = %d, want the 24h default — this is called from the "+
			"boot path where there is no request", got)
	}
}

// TestRetentionIsStatedWithTheReport — an empty stretch beyond the retention
// boundary means the rows were deleted, not that nothing happened. Presenting
// the second as the first is a wrong answer delivered confidently.
func TestRetentionIsStatedWithTheReport(t *testing.T) {
	got := shieldTrailRetentionNote(botdb.Trail{RetentionDays: 14})
	if !strings.Contains(got, "14") {
		t.Errorf("the retention note does not state the boundary: %q", got)
	}
	if !strings.Contains(got, "deleted") {
		t.Errorf("the note does not distinguish deleted history from an absence of events: %q", got)
	}
	if got := shieldTrailRetentionNote(botdb.Trail{}); !strings.Contains(got, "indefinitely") {
		t.Errorf("an install with no pruning is not described: %q", got)
	}
}

// TestTrailRendersNoInlineStyles — assertCSPSafe fails the whole page on a
// style="…" attribute, so a section that renders one takes the panel down.
func TestTrailRendersNoInlineStyles(t *testing.T) {
	tr := botdb.Trail{
		TotalBlocks: 12, TotalChallenges: 40, TotalSolved: 30,
		Reasons: []botdb.Count{{Key: "bot_score>=block_threshold", Count: 12}},
		Hours: []botdb.HourBucket{
			{Hour: "2026-01-01 00:00", Blocks: 4, Challenges: 20, Solved: 18},
			{Hour: "2026-01-01 01:00", Blocks: 8, Challenges: 20, Solved: 12},
		},
		RetentionDays: 30,
	}
	out := shieldTrailTable("Why", tr.Reasons, tr.TotalBlocks) + shieldTrailHourly(tr) +
		shieldStat("Blocked", "12", "requests refused outright")
	if strings.Contains(out, "style=") {
		t.Errorf("the trail section emits an inline style, which fails assertCSPSafe:\n%s", out)
	}
	for _, bad := range []string{"cdn", "googleapis", "unpkg", "jsdelivr"} {
		if strings.Contains(strings.ToLower(out), bad) {
			t.Errorf("the trail section contains the forbidden literal %q", bad)
		}
	}
	// The pass rate must be rendered for hours with enough samples, and the
	// sparkline must be there — that is the time dimension the panel lacked.
	if !strings.Contains(out, "%") || !strings.Contains(out, "Activity by hour") {
		t.Errorf("the hourly section is missing its series or rate:\n%s", out)
	}
}

// TestSparseHoursAreOmittedFromTheRate — an hour with three challenges has no
// meaningful pass rate, and printing one invites an operator to retune
// thresholds on noise.
func TestSparseHoursAreOmittedFromTheRate(t *testing.T) {
	out := shieldTrailHourly(botdb.Trail{
		Hours: []botdb.HourBucket{
			{Hour: "2026-01-01 00:00", Challenges: 3, Solved: 3},
			{Hour: "2026-01-01 01:00", Challenges: 40, Solved: 20},
		},
	})
	if strings.Contains(out, "2026-01-01 00:00</td><td class=\"vs-trail-n\">3") {
		t.Errorf("a 3-sample hour appears in the pass-rate table:\n%s", out)
	}
	if !strings.Contains(out, "50%") {
		t.Errorf("the 40-sample hour's rate is missing:\n%s", out)
	}
}
