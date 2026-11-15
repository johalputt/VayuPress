// SPDX-License-Identifier: Apache-2.0

package render

import (
	"strings"
	"testing"
)

// The hash is the one a browser computes for the script's text. The values
// here were computed outside this package (sha256, base64), so a change to
// how the parser hashes cannot change what the test expects with it.
func TestASnippetsInlineScriptIsAllowedByTheHashABrowserComputes(t *testing.T) {
	s, err := ParseSnippet(`<script>alert(1)</script>`)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Hashes) != 1 || s.Hashes[0] != "'sha256-bhHHL3z2vDgxUt0W3dWQOrprscmda2Y5pLsLg4GF+pI='" {
		t.Errorf("hashes %v", s.Hashes)
	}
	// A browser reads "\r\n" as "\n" before it hashes a script, and the page
	// is sent with the snippet as stored, so both are made "\n" on save.
	s, err = ParseSnippet("<script>\r\nwindow.__snip=1;\r\n</script>")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Hashes) != 1 || s.Hashes[0] != "'sha256-mKhNJ/MgeBIfsckLstQbMw4Y/BgUzYCyNRsGjTbCANk='" || strings.Contains(s.HTML, "\r") {
		t.Errorf("hashes %v, html %q", s.Hashes, s.HTML)
	}
}

// A script with a src is allowed by its origin, not by a hash; its own text
// is never run by a browser, so it is not hashed.
func TestASnippetsScriptFromElsewhereNamesItsOrigin(t *testing.T) {
	s, err := ParseSnippet(`<script async src="https://www.googletagmanager.com/gtag/js?id=G-X"></script><script src="/js/mine.js">ignored</script>`)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.ScriptOrigins) != 1 || s.ScriptOrigins[0] != "https://www.googletagmanager.com" {
		t.Errorf("origins %v", s.ScriptOrigins)
	}
	if len(s.Hashes) != 0 {
		t.Errorf("a script with a src was hashed: %v", s.Hashes)
	}
}

// What a hash cannot allow, one seed per rule, each refused with its reason.
func TestASnippetRefusesWhatAHashCannotAllow(t *testing.T) {
	for _, c := range []struct{ snippet, reason string }{
		{`<button onclick="go()">Go</button>`, "inline handler"},
		{`<a href=" JavaScript:go()">Go</a>`, "javascript:"},
		{`<base href="https://elsewhere.example/">`, "<base>"},
		{`<meta http-equiv="refresh" content="0;url=https://elsewhere.example/">`, "http-equiv"},
		{`<script src="http://cdn.example.com/a.js"></script>`, "only over https"},
		{`<script>let a = 1;`, "not closed"},
		{`<script>` + strings.Repeat("x", MaxSnippetBytes) + `</script>`, "16 KB"},
	} {
		_, err := ParseSnippet(c.snippet)
		if err == nil || !strings.Contains(err.Error(), c.reason) {
			t.Errorf("%.40q: got %v, want a refusal naming %q", c.snippet, err, c.reason)
		}
	}
}

// A strict site with snippets gains their hashes and nothing else: not the
// services it has chosen but not turned on.
func TestAStrictPolicyCarriesOnlyItsSnippetsHashes(t *testing.T) {
	base := BuildCSP("N", nil)
	if got := (SitePolicy{Mode: PolicyStrict, Services: []string{"stripe"}}).Apply(base); got != base {
		t.Errorf("a strict policy with no snippet changed the header: %s", got)
	}
	got := (SitePolicy{Mode: PolicyStrict, Services: []string{"stripe"}, ScriptHashes: []string{"'sha256-x'"}}).Apply(base)
	if !strings.Contains(got, "'sha256-x'") || strings.Contains(got, "stripe") {
		t.Errorf("a strict policy with a snippet: %s", got)
	}
}
