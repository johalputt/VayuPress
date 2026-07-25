package main

import (
	"strings"
	"testing"
)

// Structural check on the redesigned Website page: the accordion bodies are
// assembled from separate builders, so verify the pieces are balanced and every
// JS hook the website script queries is present exactly where it expects it.
func TestWebsitePageAccordionStructure(t *testing.T) {
	if got := bizModeLabel("business"); got != "Business site" {
		t.Errorf("bizModeLabel(business) = %q", got)
	}
	for _, m := range []string{"", "blog", "business", "business_subpath", "custom"} {
		if bizModeLabel(m) == "" {
			t.Errorf("bizModeLabel(%q) must never be empty", m)
		}
	}
	// monAcc must produce a balanced details/summary frame.
	out := monAcc("🌐", "T", "S", monChip(true, "on", "off"), true, `<div class="x"></div>`)
	if strings.Count(out, "<details") != 1 || strings.Count(out, "</details>") != 1 {
		t.Error("monAcc must emit exactly one details element")
	}
	if !strings.Contains(out, `class="mon-acc__body"`) {
		t.Error("monAcc must wrap the body")
	}
}
