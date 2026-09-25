// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/vayuos/secwatch"
)

// The Security tab told an operator on goldmark v1.8.6 that v2.1.5 was an
// "update available" which the one-click updater would apply. v2 is another
// module; no release applies it. The row reads up to date on its own line and
// names the new major as the migration it is.
func TestANewerMajorReadsAsAMigrationNotAnUpdate(t *testing.T) {
	out := buildComponentTable([]secwatch.Component{
		{Name: "goldmark", Current: "v1.8.6", Latest: "v1.8.6", NewerMajor: "v2.1.5"},
	})
	if strings.Contains(out, "Update available") || !strings.Contains(out, "Up to date") {
		t.Errorf("a module at the newest release of its line is not up to date:\n%s", out)
	}
	if !strings.Contains(out, "v2.1.5 is a new major version") {
		t.Errorf("the new major is not named, so the migration is a surprise later:\n%s", out)
	}
}
