// SPDX-License-Identifier: Apache-2.0

package shieldaudit

import (
	"strings"
	"testing"
)

// A COUNTRY RULE THAT CANNOT SEE THE VISITOR MUST SAY SO.
//
// Reported from a live install: "I have blocked visitors from Singapore but I
// am still getting traffic from there." Nothing was misconfigured. The site is
// proxied, the origin was not resolving the reader's address, and so the
// enforcement path looked up the country of the CDN's edge for every proxied
// visitor — a country that is never the one the operator refused. Seven days of
// the shield's own trail contained not one request from either denied country,
// while Analytics reported one of them as 91% of the audience: Analytics reads
// the country from the CDN's header, so it was right and the rule was inert.
//
// The panel already knew. The real-IP row was raised and red the whole time,
// and it described only the rate-limiting consequence — a shared bucket — which
// is a nuisance an operator hears about from readers. It said nothing about the
// control that was silently not running.
//
// That is the difference this test pins: the failure of a rate limit announces
// itself, and the failure of a geo rule does not.

func proxiedWithoutRealIP() Inputs {
	in := healthy()
	in.BehindCDN = true
	in.ClientIPResolved = false
	in.ClientIPFromVisitorTraffic = false
	return in
}

func TestTheRealIPFailureNamesTheGeoRulesItIsSilentlyDisabling(t *testing.T) {
	in := proxiedWithoutRealIP()
	in.GeoRulesSet = true

	c, ok := find(Run(in), "Real visitor IP")
	if !ok {
		t.Fatal("no Real visitor IP row at all")
	}
	if c.Status != Fail {
		t.Fatalf("status = %v, want Fail: the site is proxied and the reader's address is not being resolved", c.Status)
	}

	// Asserted as three independent facts the operator needs, not as one string
	// match. Each is a separate thing they cannot act without.
	d := c.Detail
	if !strings.Contains(d, "country rules are not being applied") {
		t.Errorf("the row does not say the country rules are not being applied.\n\nIt is the only "+
			"place the panel knows this, and an operator reading it learns their rate limiting is "+
			"pooled while their \"never serve\" list quietly serves.\n\ngot: %s", d)
	}
	if !strings.Contains(d, "Analytics") {
		t.Errorf("the row does not explain why Analytics still shows the refused country.\n\nThat "+
			"contradiction is what an operator sees FIRST — two of their own screens disagreeing — "+
			"and without this sentence the natural conclusion is that the shield is broken rather "+
			"than blind.\n\ngot: %s", d)
	}
	if !strings.Contains(d, "edge") && !strings.Contains(d, "proxy") {
		t.Errorf("the row never names what is being looked up instead of the reader.\n\ngot: %s", d)
	}
}

// The other half, and the one that keeps the row worth reading: it must not say
// this when it is not true. A report that cries about geography on an install
// with no geo rules is the same defect as silence, one direction over — it
// teaches the operator to skim the row.
func TestTheGeoSentenceIsAbsentWhenNoCountryRuleIsSet(t *testing.T) {
	in := proxiedWithoutRealIP()
	in.GeoRulesSet = false

	c, _ := find(Run(in), "Real visitor IP")
	if c.Status != Fail {
		t.Fatalf("status = %v, want Fail — the pooling failure is real either way", c.Status)
	}
	if strings.Contains(c.Detail, "country rules") {
		t.Errorf("the row warns about country rules on an install that has none:\n%s", c.Detail)
	}
}

// Resolution WORKING must clear it, whatever else is set. This is the mutation
// that matters most: if the geo sentence were attached to GeoRulesSet alone
// rather than to the failure, it would appear on every healthy proxied install
// with a country rule — permanently, and wrongly.
func TestAResolvedVisitorAddressLeavesNoGeoWarningAtAll(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   Inputs
	}{
		{"proxied and resolving", func() Inputs {
			in := healthy()
			in.BehindCDN, in.ClientIPResolved, in.GeoRulesSet = true, true, true
			return in
		}()},
		{"proxied, verified from visitor traffic", func() Inputs {
			in := healthy()
			in.BehindCDN, in.ClientIPResolved = true, true
			in.ClientIPFromVisitorTraffic, in.GeoRulesSet = true, true
			return in
		}()},
		// Direct traffic with no proxy: the peer IS the visitor, so geography
		// works perfectly. Warning here would be a red row that is wrong.
		{"not proxied, direct traffic", func() Inputs {
			in := healthy()
			in.BehindCDN, in.ClientIPResolved, in.GeoRulesSet = false, false, true
			return in
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, ok := find(Run(tc.in), "Real visitor IP")
			if !ok {
				t.Fatal("no Real visitor IP row")
			}
			if c.Status == Fail {
				t.Errorf("status = Fail on a healthy install:\n%s", c.Detail)
			}
			if strings.Contains(c.Detail, "country rules are not being applied") {
				t.Errorf("a working install is told its country rules are inert:\n%s", c.Detail)
			}
		})
	}
}
