// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
)

// The Update page is drawn in its reader's language, the provisioning card
// and the cost of a restart with it, and its actions refuse in the language
// of the page that asked. What the updater recorded is shown as recorded.
func TestTheUpdatePageIsDrawnInThePagesLanguage(t *testing.T) {
	a := &App{}
	r := withUser(httptest.NewRequest(http.MethodGet, "/os/update", nil),
		&users.User{ID: "a1", Email: "root@example.com", Role: users.RoleAdmin})
	r.Header.Set("Accept-Language", "de")
	rec := httptest.NewRecorder()
	consoleLanguage(http.HandlerFunc(a.handleOSUpdate)).ServeHTTP(rec, r)
	page := withoutScriptWords(rec.Body.String())
	for _, want := range []string{"Ein Update installieren", "Jetzt aktualisieren", "Nach Updates suchen", "Was ein Neustart kostet",
		"Subdomains und Zertifikate", "Zuerst die Datenbank sichern"} {
		if !strings.Contains(page, want) {
			t.Errorf("the German Update page lacks %q", want)
		}
	}
	for _, english := range []string{"Install an update", "Update now", "Check for updates", "What a restart costs", "Subdomains and certificates", "Not checked yet"} {
		if strings.Contains(page, english) {
			t.Errorf("the German Update page still says %q", english)
		}
	}

	apply := httptest.NewRequest(http.MethodPost, "/os/api/update/apply", strings.NewReader(`{}`))
	apply.Header.Set(pageLangHeader, "de")
	rec = httptest.NewRecorder()
	consoleLanguage(http.HandlerFunc(a.handleOSUpdateApply)).ServeHTTP(rec, apply)
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Error.Message != "Das kann nur ein Administrator." {
		t.Errorf("the refusal to a German page reads %q", rec.Body.String())
	}
}
