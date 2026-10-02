// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"strings"
	"testing"
)

// A document page says its kind once, keeps its title the page's one h1, and
// stands its inspector beside the document rather than over it; text passed as
// a string is escaped.
func TestDocumentPage(t *testing.T) {
	out := string(Document(DocumentPage{Title: "Web<site>", State: State("ok", "Live"), Actions: `<button class="btn btn--primary">Publish</button>`,
		Inspector: InspectorSection("Design", InspectorRow("Look", Text("Bistro"))), Label: "Site settings"}, `<iframe></iframe>`))
	for _, want := range []string{`data-page-kind="document"`, `<h1 class="sa-doc__title">Web&lt;site&gt;</h1>`, `sa-dot--ok`,
		`<div class="sa-doc__main"><iframe></iframe></div>`, `<aside class="sa-doc__inspector" aria-label="Site settings">`,
		`<h2 class="sa-insp__head">Design</h2>`, `<span class="sa-insp__label">Look</span><span class="sa-insp__value">Bistro</span>`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	if n := strings.Count(out, "data-page-kind"); n != 1 {
		t.Errorf("%d page kinds", n)
	}
	if strings.Contains(string(Document(DocumentPage{Title: "T"}, "x")), "sa-doc__inspector") {
		t.Error("a document without settings drew an empty inspector")
	}
}
