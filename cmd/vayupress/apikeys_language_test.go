// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/secrets"
	"github.com/johalputt/vayupress/internal/users"
)

// The API keys page is drawn in the page's language, while what it stores is
// not: a provider's credential is saved under its English name whoever saves
// it, and a capability is shown as the token a key is granted.
func TestAPIKeysIsDrawnInThePagesLanguage(t *testing.T) {
	page := osAPIKeysPage("de", mustSampleKeys(), nil, 0)
	for _, want := range []string{"API-Schlüssel", "Einen Schlüssel erstellen", "Dienste von Drittanbietern", "Lokale KI (Ollama)",
		`data-filter-none="Keine Schlüssel passen zum Filter."`, `aria-label="Schließen"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the German page lacks %q", want)
		}
	}
	if !strings.Contains(page, `data-label="Local AI (Ollama)"`) {
		t.Error("a provider's credential would be saved under the page's language, not its English name")
	}
	for _, english := range []string{">Create a key<", "Third-party services", "Issued keys"} {
		if strings.Contains(page, english) {
			t.Errorf("the German page still says %q", english)
		}
	}
	if !strings.Contains(page, `<th scope="col" translate="no">`) || !strings.Contains(page, `<th scope="row" class="ak-grid-section" translate="no">`) {
		t.Error("the permission grid's tokens are not marked as code")
	}
}

// A refusal from the API keys page's endpoints is in the language of the page
// that asked.
func TestAPIKeysRepliesInThePagesLanguage(t *testing.T) {
	h := consoleLanguage(http.HandlerFunc((&App{}).handleOSCredentialSave))
	r := withUser(httptest.NewRequest(http.MethodPost, "/os/api/credentials/save",
		strings.NewReader(`{"provider":"`+secrets.ProviderVayuKeep+`"}`)), &users.User{ID: "a", Role: users.RoleAdmin})
	r.Header.Set(pageLangHeader, "de")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Error.Message != "Die Sicherungs-Passphrase wird auf der Seite Sicherungen festgelegt." {
		t.Errorf("the refusal to a German page reads %q", rec.Body.String())
	}
}
