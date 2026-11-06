// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/mode"
)

// Home's install section has the same rows before and after a request is
// timed, so its height does not change with the timing alone.
func TestHomesInstallSectionShowsResponseTimeBeforeItIsMeasured(t *testing.T) {
	got := saHomeInstall(&osSettings{}, &adminMetricsSnapshot{}, mode.ModeNormal)
	if !strings.Contains(got, "Response time") || !strings.Contains(got, "Not measured yet") {
		t.Errorf("no Response time row before a request is timed:\n%s", got)
	}
}
