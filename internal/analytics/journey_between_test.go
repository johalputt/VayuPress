// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"reflect"
	"testing"
)

// The flows between pages leave out where visits began and ended, which the
// landing and exit pages already report; one seed for each end.
func TestBetweenPagesLeavesOutEntriesAndExits(t *testing.T) {
	got := BetweenPages([]PathFlow{
		{From: entryMarker, To: "/a", Count: 9},
		{From: "/a", To: "/b", Count: 4},
		{From: "/b", To: exitMarker, Count: 7},
		{From: "/b", To: "/a", Count: 2},
	})
	if want := []PathFlow{{From: "/a", To: "/b", Count: 4}, {From: "/b", To: "/a", Count: 2}}; !reflect.DeepEqual(got, want) {
		t.Errorf("between pages = %+v, want %+v", got, want)
	}
}
