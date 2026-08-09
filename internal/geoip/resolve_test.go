// SPDX-License-Identifier: Apache-2.0

package geoip

import "testing"

// ONE REQUEST MUST NOT HAVE TWO COUNTRIES.
//
// A live install refused Singapore. Analytics reported Singapore as 91% of its
// audience for a week while the shield's own trail held not one request from
// there. Neither side was broken — they read different sources. This is the
// function that ends that, so its precedence is the property under test.
func TestTheEdgeAnswerWinsBecauseItIsWhatTheOperatorSees(t *testing.T) {
	// 8.8.8.8 is US in the embedded table. The edge says SG. The operator writing
	// "never serve SG" is looking at the edge's answer in Analytics, so that is
	// the one the rule has to be judged against — otherwise the rule and the
	// report are about different things and the operator is never told.
	country, source := Resolve("SG", "8.8.8.8")
	if country != "SG" {
		t.Errorf("country = %q, want SG — the CDN's per-request answer must win over a "+
			"release-time table, or a rule written against what Analytics shows can never match", country)
	}
	if source != SourceEdge {
		t.Errorf("source = %q, want %q", source, SourceEdge)
	}
}

// Without an edge answer the table must still work: a direct-served origin with
// no CDN is the sovereign default this product is built for, and losing
// geography there would be a regression dressed as a fix.
func TestTheTableAnswersWhenNoEdgeDoes(t *testing.T) {
	country, source := Resolve("", "8.8.8.8")
	if country != "US" || source != SourceTable {
		t.Errorf("got (%q, %q), want (US, %q)", country, source, SourceTable)
	}
}

// Neither source knowing is not a country. Returning something here would make
// policy.Rules.Country() match a bucket that means "no idea".
func TestNeitherSourceKnowingYieldsNoCountry(t *testing.T) {
	if c, s := Resolve("", "not-an-ip"); c != "" || s != SourceNone {
		t.Errorf("got (%q, %q), want empty", c, s)
	}
}

// The edge's placeholders are not countries. "XX" is Cloudflare for "unknown"
// and "T1" is Tor; treating either as a code would let "deny XX" look like a
// control while matching the edge's own ignorance — and would attribute every
// Tor reader to a country named T1.
func TestEdgePlaceholdersAreRefusedAndFallThrough(t *testing.T) {
	for _, ph := range []string{"XX", "T1", "xx", "t1"} {
		country, source := Resolve(ph, "8.8.8.8")
		if country != "US" || source != SourceTable {
			t.Errorf("placeholder %q gave (%q, %q); it must be discarded and the table consulted",
				ph, country, source)
		}
	}
}

// Anything that is not a two-letter code is refused outright — including a
// three-letter code, which is the shape a well-meaning proxy config produces.
func TestOnlyTwoLetterCodesAreAccepted(t *testing.T) {
	for _, bad := range []string{"USA", "S", "S1", "12", "", "  ", "S G"} {
		if c, _ := Resolve(bad, "not-an-ip"); c != "" {
			t.Errorf("Resolve(%q) returned %q; only ISO alpha-2 is a country here", bad, c)
		}
	}
	// ...and case is normalised, because nginx and CDNs are inconsistent about it
	// and an operator's rule is compared case-insensitively everywhere else.
	if c, _ := Resolve("sg", "not-an-ip"); c != "SG" {
		t.Errorf("lowercase edge answer = %q, want SG", c)
	}
}
