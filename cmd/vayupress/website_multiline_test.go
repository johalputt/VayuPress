// SPDX-License-Identifier: Apache-2.0

package main

// A single-line <input> strips line breaks from its value (the HTML value
// sanitization algorithm), so a line-oriented field edited through one loses
// every line but the first on the next save. vayupress.johal.in's hours were
// persisted as "18:00–23:00Closed Mondays" that way. The per-site editor was
// fixed; the primary site's editor still edited Address with an <input>.
//
// Asserted on the rendered page: every line-oriented field is a <textarea>.

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

func TestWebsiteEditorLineFieldsAreTextareas(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/os/website", nil)
	req.Header.Set("X-API-Key", "test-key")
	(&App{}).handleOSWebsite(rec, req)
	page := rec.Body.String()
	for _, key := range []string{"about", "address", "hours", "services", "gallery"} {
		ta := regexp.MustCompile(`<textarea[^>]*data-biz-f="` + key + `"`)
		in := regexp.MustCompile(`<input[^>]*data-biz-f="` + key + `"`)
		if in.MatchString(page) || !ta.MatchString(page) {
			t.Errorf("%s is not edited through a <textarea>: a save would collapse its lines into one", key)
		}
	}
}
