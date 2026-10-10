// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

func TestSidebarAdsFillTheRail(t *testing.T) {
	page := `<aside>` + sidebarAdAnchor + `</aside>`
	got := fillSidebarAds(page, `<div class="vp-ads">AD</div>`)
	if !strings.Contains(got, `<div class="h-rail-ads" data-ads="sidebar"><div class="vp-ads">AD</div></div>`) {
		t.Errorf("the sidebar slots did not reach the rail: %s", got)
	}
}

// With nothing to show the rail is left as it was, empty, so its own rule
// (.h-rail-ads:empty) hides it rather than a labelled box with no ad.
func TestNoSidebarAdsLeaveTheRailEmpty(t *testing.T) {
	page := `<aside>` + sidebarAdAnchor + `</aside>`
	if got := fillSidebarAds(page, ""); got != page {
		t.Errorf("an empty placement changed the page: %s", got)
	}
}
