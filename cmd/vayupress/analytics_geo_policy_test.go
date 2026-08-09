// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/johalputt/vayupress/internal/vayushield"
	"github.com/johalputt/vayupress/internal/vayushield/policy"
)

// A REFUSED COUNTRY MUST NOT APPEAR AS AN AUDIENCE.
//
// From a live install: the operator marked Singapore never-serve. Enforcement
// worked — a Singapore visitor is refused at the page, and cannot escape it even
// by solving a challenge, so it never loads a page and never runs the tracking
// script. Singapore nevertheless stayed at the top of their Analytics, because
// the ingest is public and on the shield's /api bypass, and a client can POST
// beacons there without ever asking for a page. The panel told its own operator
// that a country they had refused was their largest source of readers.

func shieldWithPolicy(t *testing.T, cfg policy.Config) *App {
	t.Helper()
	m := vayushield.New(vayushield.Config{Enabled: true})
	m.ApplySettings(vayushield.Settings{
		Enabled: true, PoWThreshold: 0.4, JSThreshold: 0.6, BlockThreshold: 0.8,
		Policy: cfg,
	})
	return &App{vayuShield: m}
}

func TestARefusedCountryCannotWriteIntoAnalytics(t *testing.T) {
	a := shieldWithPolicy(t, policy.Config{DenyCountries: []string{"SG", "CN"}})
	before := analyticsGeoRefusals()
	if !a.analyticsGeoRefused("SG") {
		t.Fatal("a beacon claiming to be a page view from a REFUSED country was accepted.\n\n" +
			"The page path refuses that country outright, so this beacon cannot have come from " +
			"a page this site served — and recording it tells the operator their refused " +
			"country is their audience.")
	}
	if analyticsGeoRefusals() <= before {
		t.Error("the refusal was not counted; a number that quietly falls out of a report is " +
			"the same defect as one that should not be in it")
	}
}

// A CHALLENGED country is NOT a refused one. The operator asked its readers to
// prove they are people; those who do are welcome, and dropping their page views
// would silently under-report an audience the operator deliberately kept.
func TestAChallengedCountryIsStillCounted(t *testing.T) {
	a := shieldWithPolicy(t, policy.Config{
		DenyCountries:      []string{"SG"},
		ChallengeCountries: []string{"RU"},
	})
	if a.analyticsGeoRefused("RU") {
		t.Error("a CHALLENGED country's readers were dropped from analytics.\n\n" +
			"They were asked to prove they are people, not turned away — and the ones who " +
			"solved it are real readers the operator chose to keep.")
	}
	// ...and an unlisted country is untouched.
	if a.analyticsGeoRefused("DE") {
		t.Error("a country under no rule at all was dropped")
	}
}

// No rules, no shield, no country: never drop anything. An install with an empty
// policy losing its analytics would be a far worse bug than the one being fixed.
func TestNothingIsDroppedWithoutAnExplicitRule(t *testing.T) {
	empty := shieldWithPolicy(t, policy.Config{})
	if empty.analyticsGeoRefused("SG") {
		t.Error("an install with NO country rules dropped a beacon")
	}
	if (&App{}).analyticsGeoRefused("SG") {
		t.Error("an install with no shield at all dropped a beacon")
	}
	denied := shieldWithPolicy(t, policy.Config{DenyCountries: []string{"SG"}})
	if denied.analyticsGeoRefused("") {
		t.Error("a beacon whose country could not be resolved was dropped; unknown is not denied")
	}
}
