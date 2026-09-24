// SPDX-License-Identifier: Apache-2.0

package main

import (
	"regexp"
	"strings"
	"testing"
)

// The offline pages carry their colours themselves (there is no network to
// fetch the stylesheet), so the values are copies. This holds each copy to the
// stylesheet's token of the same name, in both schemes, and holds both workers
// to that page.
func TestOfflinePagesUseTheStylesheetsTokens(t *testing.T) {
	dark, light := stillAirTokens(t)
	page := stillAirOfflineHTML("T", "H", "D")
	blocks := regexp.MustCompile(`:root\{([^}]*)\}`).FindAllStringSubmatch(page, -1)
	if len(blocks) != 2 {
		t.Fatalf("want a dark and a light :root block, got %d", len(blocks))
	}
	for i, want := range []map[string]string{dark, light} {
		for _, m := range regexp.MustCompile(`(--[a-z0-9-]+):(#[0-9a-f]{6})`).FindAllStringSubmatch(blocks[i][1], -1) {
			if got := strings.ToLower(m[2]); got != want[m[1]] {
				t.Errorf("scheme %d: offline %s is %s, the stylesheet's is %s", i, m[1], got, want[m[1]])
			}
		}
	}
	// Both workers carry the page as a JavaScript string; its style block, as
	// that string spells it, must be there.
	i := strings.Index(page, "<style>")
	style := strings.Trim(jsString(page[i:strings.Index(page, "</style>")]), `"`)
	for name, js := range map[string]string{"reader worker": serviceWorkerJS, "console worker": osServiceWorkerJS} {
		if !strings.Contains(js, style) {
			t.Errorf("the %s does not answer offline with the Still Air page", name)
		}
	}
}
