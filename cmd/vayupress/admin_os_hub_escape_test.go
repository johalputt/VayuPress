// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/mode"
)

// A hub card escapes its title and description itself, so a caller that passes
// "&amp;" shows the reader a literal "&amp;". That shipped on the Operations
// hub. Every hub is rendered here and checked for the signature of escaping
// twice, whatever the cause.
func TestNoHubShowsADoubleEscapedEntity(t *testing.T) {
	hubs := map[string]string{
		"operations":         osOperationsGrid(mode.ModeNormal, 40, false, ""),
		"operations-offline": osOperationsGrid(mode.ModeReadOnly, 90, true, "Not set up"),
		"optimize":           osOptimizeGrid(3, []optimizeSite{{ID: "a1", Host: "example.org", Label: "Example"}}),
		"growth":             osGrowthGrid(12, 7, 2),
		"system":             osSystemGrid(3),
		"workspace":          osWorkspaceGrid(false, 4, 2, 1, 3, 9, 3),
		"workspace-onion":    osWorkspaceGrid(true, 4, 2, 1, 3, 9, 3),
	}
	for name, html := range hubs {
		for _, twice := range []string{"&amp;amp;", "&amp;lt;", "&amp;gt;", "&amp;#", "&amp;quot;"} {
			if i := strings.Index(html, twice); i >= 0 {
				lo := max(0, i-60)
				t.Errorf("%s hub shows %q to the reader: …%s…", name, strings.Replace(twice, "&amp;", "&", 1), html[lo:min(len(html), i+40)])
			}
		}
	}
}
