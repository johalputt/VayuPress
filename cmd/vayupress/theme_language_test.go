// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/i18n"
)

// The Theme Studio is drawn in its reader's language, the options the theme
// package names included, and its code editor refuses in the language of the
// page that asked. A theme's own name is not translated: it is the theme's.
func TestTheThemeStudioIsDrawnInThePagesLanguage(t *testing.T) {
	a := &App{}
	r := httptest.NewRequest(http.MethodGet, "/os/theme", nil)
	r = r.WithContext(i18n.WithLang(r.Context(), "de"))
	rec := httptest.NewRecorder()
	a.handleOSTheme(rec, r)
	page := withoutScriptWords(rec.Body.String())
	for _, want := range []string{"Vorlagen", "Live-Vorschau", "Ein Theme übertragen", "Schriftpaar", "Lesbarkeit", "Fließtext auf hellem Hintergrund", "Theme-Standard"} {
		if !strings.Contains(page, want) {
			t.Errorf("the German Theme Studio lacks %q", want)
		}
	}
	for _, english := range []string{">Presets<", "Live preview", "Move a theme", "Font pairing", "Readability", "Body text on light background", "Theme default", "Layout archetype"} {
		if strings.Contains(page, english) {
			t.Errorf("the German Theme Studio still says %q", english)
		}
	}

	code := httptest.NewRequest(http.MethodPost, "/os/api/theme/code", strings.NewReader(`{"robots":"index, sometimes"}`))
	code.Header.Set(pageLangHeader, "de")
	rec = httptest.NewRecorder()
	consoleLanguage(http.HandlerFunc(a.handleOSThemeCode)).ServeHTTP(rec, code)
	var out struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Error != "Das ist keine Robots-Anweisung, die diese Seite anbietet." {
		t.Errorf("the refusal to a German page reads %q", rec.Body.String())
	}
}
