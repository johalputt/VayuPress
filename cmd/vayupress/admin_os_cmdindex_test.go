// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// The command palette's index is read by admin-os.js as item.label, item.slug,
// item.fn, item.href. It shipped marshalling "Label", "Slug", "Fn", "Href"
// (untagged fields), so every row rendered blank and a query threw. This holds
// the server's keys against the script's reads.
func TestCommandPaletteIndexUsesTheKeysTheScriptReads(t *testing.T) {
	a := ownershipApp(t)
	seedOwnedArticle(t, "hello-palette", "u1", "")
	rec := httptest.NewRecorder()
	a.handleOSCmdIndex(rec, httptest.NewRequest(http.MethodGet, "/os/api/cmd-index", nil))
	var idx map[string][]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &idx); err != nil {
		t.Fatalf("index is not JSON: %v\n%s", err, rec.Body.String())
	}
	need := map[string][]string{
		"posts":    {"label", "slug"},
		"actions":  {"label", "fn"},
		"settings": {"label", "href"},
	}
	for group, keys := range need {
		items := idx[group]
		if len(items) == 0 {
			t.Fatalf("group %q is empty; the test proves nothing about it", group)
		}
		for _, it := range items {
			for _, k := range keys {
				if v, ok := it[k].(string); !ok || v == "" {
					t.Errorf("%s item %v has no %q, which the palette reads", group, it, k)
				}
			}
		}
	}
	js, err := os.ReadFile("../../static/js/admin-os.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, read := range []string{"item.label", "item.slug", "item.fn", "item.href"} {
		if !strings.Contains(string(js), read) {
			t.Errorf("admin-os.js no longer reads %s; update this test with the key it reads now", read)
		}
	}
}
