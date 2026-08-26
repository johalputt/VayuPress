// SPDX-License-Identifier: Apache-2.0

package analytics

import "testing"

// TestAggregateAttributionPure exercises the exact five-row dataset the CI dump
// showed, without touching SQLite (runs anywhere, no CGO needed).
func TestAggregateAttributionPure(t *testing.T) {
	first := map[attributionKey]float64{}
	last := map[string]attributionKey{}
	distinct := map[string]map[attributionKey]bool{}

	fold := func(sess, s, m, c string) {
		creditTouch(first, last, distinct, sess, attributionKey{s, m, c})
	}
	// Session A: google → newsletter → (converter row carries no UTM).
	fold("A", "google", "cpc", "spring")
	fold("A", "newsletter", "email", "april")
	fold("A", "", "", "")
	// Session B: newsletter → newsletter.
	fold("B", "newsletter", "email", "april")
	fold("B", "newsletter", "email", "april")

	out := aggregateAttribution(first, last, distinct)
	get := func(src string) AttributionRow {
		for _, r := range out {
			if r.Source == src {
				return r
			}
		}
		t.Fatalf("no row for %q in %+v", src, out)
		return AttributionRow{}
	}
	if len(out) != 3 {
		t.Fatalf("want 3 rows, got %+v", out)
	}
	g := get("google") // first touch of A; ⅓ linear share of A
	if g.FirstTouch != 1 || g.LastTouch != 0 || g.Linear < 0.32 || g.Linear > 0.35 {
		t.Fatalf("google wrong: %+v", g)
	}
	nl := get("newsletter") // first touch of B; last touch of B; ⅓+1 linear
	if nl.FirstTouch != 1 || nl.LastTouch != 1 || nl.Linear < 1.31 || nl.Linear > 1.36 {
		t.Fatalf("newsletter wrong: %+v", nl)
	}
	b := get("") // A's converter row is its LAST touch; ⅓ linear
	if b.FirstTouch != 0 || b.LastTouch != 1 || b.Linear < 0.32 || b.Linear > 0.35 {
		t.Fatalf("blank wrong: %+v", b)
	}
}
