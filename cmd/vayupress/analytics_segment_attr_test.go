// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"

	"github.com/johalputt/vayupress/internal/analytics"
)

// The filters to save ride in an attribute of the Save button, and a filter's
// value is whatever the reader typed. Whatever it holds, the attribute ends
// where it should and reads back as the same filters: one seed for each
// character that could break out (a double quote, an apostrophe, markup, an
// ampersand that would start an entity).
func TestTheFiltersToSaveReadBackWhateverTheyHold(t *testing.T) {
	for _, value := range []string{`/a"b`, `/O'Brien`, `"><script>alert(1)</script>`, `/a&quot;b`} {
		filters := []analytics.ExploreFilter{{Dim: "path", Op: analytics.FilterPrefix, Value: value}}
		out := exploreSegments("/os/analytics/explore", "/os/api/analytics/segments", "", nil,
			exploreAsk{q: analytics.ExploreQuery{Filters: filters}}, "n")
		doc, err := xhtml.Parse(strings.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		var saves, scripts int
		var got []analytics.ExploreFilter
		var walk func(*xhtml.Node)
		walk = func(n *xhtml.Node) {
			if n.Type == xhtml.ElementNode {
				if n.Data == "script" {
					scripts++
				}
				for _, a := range n.Attr {
					if a.Key == "data-filters" {
						saves++
						if err := json.Unmarshal([]byte(a.Val), &got); err != nil {
							t.Errorf("%q: the attribute is not the filters: %v (%s)", value, err, a.Val)
						}
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(doc)
		if saves != 1 || !reflect.DeepEqual(got, filters) {
			t.Errorf("%q: %d Save buttons, reading back %+v", value, saves, got)
		}
		if scripts != 1 {
			t.Errorf("%q: %d scripts, want the page's own", value, scripts)
		}
	}
}
