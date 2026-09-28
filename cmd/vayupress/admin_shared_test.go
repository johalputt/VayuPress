// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// assertCSPSafe is the shared admin-rendering assertion: a fragment must carry
// no inline style attribute, never reference unsafe-eval, and never pull from an
// external asset host. Used across the VayuOS surface tests. (Previously lived in
// admin_ui_test.go, removed with Admin v2 in v1.6.0.)
func assertCSPSafe(t *testing.T, name, htmlOut string) {
	t.Helper()
	if strings.Contains(htmlOut, `style="`) {
		t.Errorf("%s: contains inline style attribute (violates style-src 'self')", name)
	}
	if strings.Contains(htmlOut, "unsafe-eval") {
		t.Errorf("%s: references unsafe-eval", name)
	}
	for _, bad := range []string{"cdn", "googleapis", "unpkg", "jsdelivr"} {
		if strings.Contains(strings.ToLower(htmlOut), bad) {
			t.Errorf("%s: references external asset host %q", name, bad)
		}
	}
}
