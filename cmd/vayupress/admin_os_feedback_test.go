// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// TestFeedbackBody checks the composer template prompts for the report type and
// stamps in the running version and world, so every report arrives actionable.
func TestFeedbackBody(t *testing.T) {
	b := feedbackBody()
	for _, want := range []string{"Bug", "Improvement", "Feature request", "Steps to reproduce", "VayuOS v" + Version, "Space"} {
		if !strings.Contains(b, want) {
			t.Errorf("feedback body missing %q\n---\n%s", want, b)
		}
	}
}
