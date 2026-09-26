// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"strings"
	"testing"
)

// A job whose length is not known yet shows a moving bar, not a bar stuck at
// zero, and a yielding job (easing or waiting) is marked as such beside the
// pacer's reason.
func TestAJobShowsItsLengthOnlyWhenKnownAndWhyItYields(t *testing.T) {
	unknown := string(Job{Title: "Sealing", Percent: -1}.HTML())
	if strings.Contains(unknown, `value=`) {
		t.Errorf("a job of unknown length drew a fixed bar: %s", unknown)
	}
	known := string(Job{Title: "Copying", Percent: 41, Pace: "Easing", Why: "disk stalled 34% of the last 10 s"}.HTML())
	for _, want := range []string{`value="41"`, `sa-dot--warn"></span>Easing`, "disk stalled 34% of the last 10 s"} {
		if !strings.Contains(known, want) {
			t.Errorf("the easing job is missing %q: %s", want, known)
		}
	}
	if got := string(Job{Title: "Copying", Percent: 10, Pace: "Running"}.HTML()); !strings.Contains(got, `sa-dot--ok"></span>Running`) {
		t.Errorf("a job at full pace is not marked as running: %s", got)
	}
	if got := string(Job{Title: "<b>x</b>", Percent: 1}.HTML()); strings.Contains(got, "<b>") {
		t.Errorf("the title was not escaped: %s", got)
	}
}
