// SPDX-License-Identifier: Apache-2.0

package main

import (
	"regexp"
	"strings"
	"testing"
)

// TestPageScriptsHaveCSRF — the API keys, VayuMCP, Claude Code and Buzz
// scripts call csrf() without defining it, relying on the page-script wrapper.
// When the wrapper lost it, every one of their writes threw in the browser and
// nothing on the server noticed. Each such script, as the shell emits it, must
// sit in a scope that defines the function before the script runs.
func TestPageScriptsHaveCSRF(t *testing.T) {
	def := regexp.MustCompile(`function csrf\(\)\{[^}]*vp_csrf`)
	for name, script := range map[string]string{
		"osAPIKeysScript":   osAPIKeysScript,
		"osConnectorScript": osConnectorScript,
		"mcpClientScript":   mcpClientScript,
	} {
		if !strings.Contains(script, "csrf()") || def.MatchString(script) {
			t.Fatalf("%s no longer relies on the wrapper's csrf(); this test no longer guards it", name)
		}
		out := adminOSShellFoot("n0nce", script, false)
		at := strings.Index(out, script)
		loc := def.FindStringIndex(out)
		if loc == nil || loc[0] > at {
			t.Errorf("%s: the shell defines no csrf() ahead of the page script, so its writes throw", name)
		}
	}
}
