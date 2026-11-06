// SPDX-License-Identifier: Apache-2.0

package theme

import "testing"

// A site's stored options are its options' own choices and nothing else; one
// seed per rule.
func TestASitesHalcyonOptionsTakeOnlyTheirChoices(t *testing.T) {
	for raw, want := range map[string]bool{
		``:                                  true,
		`{"face":"sans","home":"river"}`:    true,
		`{"nosuch":"on"}`:                   false, // an unknown option
		`{"face":"comic"}`:                  false, // not one of its choices
		`{"accent":"site"}`:                 false, // the accent is the site's colours
		`["face","sans"]`:                   false, // not an object
		`{"face":"sans","textsize":"9999"}`: false, // one bad value spoils the rest
	} {
		if _, ok := ParseSiteHalcyon(raw); ok != want {
			t.Errorf("%s: ok=%v, want %v", raw, ok, want)
		}
	}
	if SiteHalcyon(``) != nil || SiteHalcyon(`{"face":"comic"}`) != nil {
		t.Error("a site that has chosen nothing valid has a config of its own")
	}
	if c := SiteHalcyon(`{"face":"sans"}`); c == nil || c.Face != "sans" {
		t.Errorf("a chosen face reads %+v", c)
	}
}
