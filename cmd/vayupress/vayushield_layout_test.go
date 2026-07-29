// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/vayushield"
)

// The VayuOS house style is a standing rule, and a page drifts out of it one
// well-meaning addition at a time. These assertions are about the SHAPE of the
// page rather than its wording, so they survive copy edits and fail on
// structural drift.

// TestTheShieldHeroIsFourTilesNotAWallOfNumbers.
//
// The house style asks for a stat-grid of tiles carrying the numbers that answer
// "what is the state of this?" at a glance. The hero grew to nine equal-weight
// counters instead, which is the opposite: a figure that never moves sits beside
// the one that matters and both read the same, so neither is read at all.
func TestTheShieldHeroIsFourTilesNotAWallOfNumbers(t *testing.T) {
	a := &App{}
	body := a.shieldHeroBody(context.Background())

	if !strings.Contains(body, `class="stat-grid"`) {
		t.Error("the hero is not a stat-grid, so the page opens with something other than the " +
			"house-style tiles every other VayuOS page opens with")
	}
	// Counted on the label, which appears exactly once per tile — the container
	// class is a prefix of stat-card__label and stat-card__value, so counting it
	// would report three per tile.
	if n := strings.Count(body, `class="stat-card__label"`); n != 4 {
		t.Errorf("the hero renders %d tiles, want 4. Four is the point: it is how many numbers "+
			"can be read at a glance, and the eleventh counter is what made the previous "+
			"version unreadable", n)
	}
	for _, want := range []string{"Status", "Visitors now", "Requests / sec", "Sentences active"} {
		if !strings.Contains(body, want) {
			t.Errorf("the tile %q is missing", want)
		}
	}
	// The detail has to survive somewhere, or "fewer tiles" just means less
	// information.
	tp := a.shieldThroughputBody()
	for _, want := range []string{"In-flight", "Fair-shed (L2)", "Jailed / suspects (L5)", "Inspection hits (L7)"} {
		if !strings.Contains(tp, want) {
			t.Errorf("the throughput band lost %q — the counters were removed from the headline "+
				"and not put anywhere, so an operator has no way to read them during an "+
				"incident", want)
		}
	}
}

// TestObserveModeIsTheTileThatWarns.
//
// stat-card--warn is for a number that WANTS ATTENTION, not one that is merely
// large. A jail count is normal operation. An install that is observing and
// therefore enforcing nothing is the state that goes wrong by being left on and
// forgotten, so that is what the tone is spent on.
func TestObserveModeIsTheTileThatWarns(t *testing.T) {
	a := &App{vayuShield: vayushield.New(vayushield.Config{Enabled: true})}
	a.vayuShield.ApplySettings(vayushield.Settings{Enabled: true})
	calm := a.shieldHeroBody(context.Background())
	if strings.Contains(calm, "stat-card--warn") {
		t.Error("a healthy install shows a warned tile, which trains the operator to ignore " +
			"the tone")
	}

	a.vayuShield.ApplySettings(vayushield.Settings{Enabled: true, ObserveOnly: true})
	observing := a.shieldHeroBody(context.Background())
	if !strings.Contains(observing, "stat-card--warn") {
		t.Error("observe-only mode does not warn on the tiles. It is the one state where every " +
			"other number on the page describes enforcement that is not happening")
	}
	if !strings.Contains(observing, "Observing") {
		t.Errorf("the status tile does not say the install is observing: %q", observing)
	}
}

// TestTheShieldPageFollowsTheHouseStyle — the structural elements the standing
// rule names, checked as a set so a page rebuilt from scratch cannot quietly
// drop one.
func TestTheShieldPageFollowsTheHouseStyle(t *testing.T) {
	a := &App{}
	body := a.shieldHeroBody(context.Background())
	if strings.Contains(body, `style="`) {
		t.Error("the hero carries an inline style attribute, which assertCSPSafe rejects")
	}

	// The page-actions live region. Settings here save over HTMX with no page
	// reload, so without it a screen-reader user gets no confirmation at all.
	page := shieldPageChrome() + shieldThroughputBand("x")
	for _, want := range []string{
		`class="page-header"`,
		`class="page-actions"`,
		`role="status"`,
		`aria-live="polite"`,
		`class="page-sub"`,
		`class="section-head"`,
		`class="mon-stack"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing %q, which the VayuOS house style requires", want)
		}
	}
}
