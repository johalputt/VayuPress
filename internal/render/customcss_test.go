// SPDX-License-Identifier: Apache-2.0

package render

import (
	"strings"
	"testing"
)

// TestSanitizeCustomCSS guards audit L4: operator theme CSS must not be able to
// reach an external origin via @import or an absolute/protocol-relative url(),
// while same-origin/relative and data: url()s are preserved.
func TestSanitizeCustomCSS(t *testing.T) {
	cases := []struct {
		in              string
		wantContains    string
		wantNotContains string
	}{
		{`body{background:url(https://evil.example/beacon)}`, "url()", "evil.example"},
		{`@import url('https://evil.example/x.css'); body{color:red}`, "color:red", "evil.example"},
		{`.a{background:url("http://evil.example/x")}`, "url()", "evil.example"},
		{`.b{background:url(//cdn.evil/x.png)}`, "url()", "cdn.evil"},
		{`.c{background:url(data:image/png;base64,AAAA)}`, "data:image/png", ""},
		{`.d{background:url(/local/img.png)}`, "url(/local/img.png)", ""},
	}
	for _, c := range cases {
		got := sanitizeCustomCSS(c.in)
		if c.wantContains != "" && !strings.Contains(got, c.wantContains) {
			t.Errorf("sanitizeCustomCSS(%q) = %q, want contains %q", c.in, got, c.wantContains)
		}
		if c.wantNotContains != "" && strings.Contains(got, c.wantNotContains) {
			t.Errorf("sanitizeCustomCSS(%q) = %q, must NOT contain %q", c.in, got, c.wantNotContains)
		}
	}
}
