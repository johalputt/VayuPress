// SPDX-License-Identifier: Apache-2.0

package flowaudit

import (
	"strings"
	"testing"
)

// The panel shows these sentences, and its wording rules forbid "flow(s)":
// the count is written out, with the verb agreeing with it.
func TestFlowCountsAgreeWithTheirVerb(t *testing.T) {
	for _, c := range []struct {
		n    int
		want string
	}{
		{1, "1 armed flow calls a LOCAL model provider"},
		{3, "3 armed flows call a LOCAL model provider"},
		{0, "No armed flow calls a model."},
	} {
		got := modelDetail(c.n, Inputs{ModelConfigured: true, ModelLocal: true})
		if !strings.HasPrefix(got, c.want) {
			t.Errorf("%d flows: %q, want it to begin %q", c.n, got, c.want)
		}
		if strings.Contains(got, "(s)") {
			t.Errorf("%d flows: a hedged plural in %q", c.n, got)
		}
	}
	if got := brokenDetail([]string{"nightly"}); !strings.HasPrefix(got, "1 enabled flow no longer validates and will NOT fire") {
		t.Errorf("one broken flow: %q", got)
	}
}
