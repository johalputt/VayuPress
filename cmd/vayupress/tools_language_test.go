// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Tools and plugins is drawn in its reader's language, module by module, and
// its switch refuses in the language of the page that asked. A module's id
// is not translated: it is what the switch sends.
func TestToolsIsDrawnInThePagesLanguage(t *testing.T) {
	a := &App{}
	r := httptest.NewRequest(http.MethodGet, "/os/tools", nil)
	r.Header.Set("Accept-Language", "de")
	rec := httptest.NewRecorder()
	consoleLanguage(http.HandlerFunc(a.handleOSTools)).ServeHTTP(rec, r)
	page := withoutScriptWords(rec.Body.String())
	for _, want := range []string{"Werkzeuge und Plugins", "Plugins in der Sandbox", "Versionsverlauf", "Weiterleitungen", `data-tool-card="comments"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the German Tools page lacks %q", want)
		}
	}
	for _, english := range []string{"Sandboxed plugins", "Version history", "Redirects", "Reader comments with moderation queue"} {
		if strings.Contains(page, english) {
			t.Errorf("the German Tools page still says %q", english)
		}
	}

	toggle := httptest.NewRequest(http.MethodPost, "/os/api/tools/toggle", strings.NewReader(`not json`))
	toggle.Header.Set(pageLangHeader, "de")
	rec = httptest.NewRecorder()
	consoleLanguage(http.HandlerFunc(a.handleOSToolToggle)).ServeHTTP(rec, toggle)
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Error.Message != "Die Anfrage konnte nicht gelesen werden." {
		t.Errorf("the refusal to a German page reads %q", rec.Body.String())
	}
}
