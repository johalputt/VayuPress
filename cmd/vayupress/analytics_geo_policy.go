// SPDX-License-Identifier: Apache-2.0

package main

import (
	"sync/atomic"

	"github.com/johalputt/vayupress/internal/vayushield/policy"
)

// A COUNTRY THE OPERATOR REFUSED MUST NOT BE ABLE TO WRITE INTO THEIR ANALYTICS.
//
// The analytics ingest is public, unauthenticated, and on VayuShield's /api
// bypass list. That bypass is right and stays: a beacon is a machine request and
// cannot solve a browser challenge, so shielding it would break every honest
// visitor's page view. But "cannot be challenged" is not the same as "exempt
// from an explicit operator refusal", and the two were conflated.
//
// The consequence, from a live install: the operator marked Singapore
// never-serve. Enforcement worked — a Singapore visitor is refused at the page
// and, verified by test, cannot escape it even by solving a challenge, so it
// never loads a page and never runs the tracking script. Singapore nevertheless
// kept appearing in Analytics as most of the audience, because a client can POST
// beacons straight at the ingest without ever requesting a page, and nothing on
// that path consulted the operator's rules. The operator was told, by their own
// panel, that a country they had refused was their largest source of readers.
//
// This is only safe BECAUSE the page path provably refuses those countries: no
// legitimately-served traffic can be hidden by it, because there is none to hide.
// If that ever stops being true this must be revisited, which is why it says so.

// beaconsRefusedByGeo counts ingest events dropped by an operator country rule.
//
// Counted rather than silently dropped. A number that falls out of a report
// without explanation is the same defect as a number that should not be in it.
var beaconsRefusedByGeo atomic.Int64

// analyticsGeoRefused reports whether an operator rule refuses this country, and
// records the refusal.
//
// Deny only — never the challenge verdict. A country the operator asked to
// CHALLENGE has not been refused; they asked for its readers to prove they are
// people, and those who do are welcome. Dropping their page views would silently
// under-report an audience the operator deliberately kept.
// An unresolved country is NOT handled here on purpose. policy.Rules.Country("")
// already returns VerdictNone, and its own comment records why; repeating the
// check here would be a second copy of one rule, which is a future divergence.
// The mutation that removed a duplicate guard survived, which is exactly how a
// duplicate announces itself.
func (a *App) analyticsGeoRefused(country string) bool {
	if a.vayuShield == nil {
		return false
	}
	rules := a.vayuShield.Policy()
	if rules.Empty() || !rules.GeoActive() || rules.Country(country) != policy.VerdictDeny {
		return false
	}
	beaconsRefusedByGeo.Add(1)
	return true
}

// analyticsGeoRefusals is the count, for the posture report.
func analyticsGeoRefusals() int64 { return beaconsRefusedByGeo.Load() }
