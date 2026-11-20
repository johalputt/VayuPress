// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/i18n"
)

// A Settings page is drawn in the page's language: its category, bands, rows
// and hints, its placeholders and the scheme control. The registry it reads
// stays English, since its tests and every key are written against it.
func TestASettingsPageIsDrawnInThePagesLanguage(t *testing.T) {
	ctx := i18n.WithLang(context.Background(), "de")
	general, _ := settingsCategoryFor("general")
	body := string(settingsPageBody(ctx, nil, general))
	for _, want := range []string{"Identität der Website", "Name der Website", "Wird im Kopfbereich, im Tab und in jeder E-Mail angezeigt.", `placeholder="Meine Publikation"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the German Site identity page lacks %q", want)
		}
	}
	appearance, _ := settingsCategoryFor("appearance")
	if body := string(settingsPageBody(ctx, nil, appearance)); !strings.Contains(body, ">Hell<") || !strings.Contains(body, ">System<") {
		t.Errorf("the scheme control is not German:\n%s", body)
	}
	if general.Label != "Site identity" || general.Groups[0].Fields[0].Label != "Site name" {
		t.Error("drawing in German changed the registry")
	}
}

// The command palette's index is in the language the page sends: its
// actions and every setting, found under their translated names.
func TestThePalettesIndexIsInThePagesLanguage(t *testing.T) {
	a := &App{}
	get := func(lang string) map[string][]map[string]any {
		rec := httptest.NewRecorder()
		a.handleOSCmdIndex(rec, httptest.NewRequest(http.MethodGet, "/os/api/cmd-index?lang="+lang, nil))
		var out map[string][]map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
		return out
	}
	has := func(list []map[string]any, label string) bool {
		for _, it := range list {
			if it["label"] == label {
				return true
			}
		}
		return false
	}
	de := get("de")
	if !has(de["actions"], "Jetzt eine Sicherung erstellen") || !has(de["settings"], "Name der Website") {
		t.Errorf("the German index: %v", de)
	}
	if xx := get("xx"); !has(xx["settings"], "Site name") {
		t.Errorf("a language the console lacks was not English: %v", xx["settings"])
	}
}

// Settings › Team is drawn in the page's language: the roles, the mail note
// and the form that adds someone. The role values posted stay the store's.
func TestTheTeamIsDrawnInThePagesLanguage(t *testing.T) {
	a, db := ownMailboxApp(t)
	if _, err := db.Exec(`INSERT INTO users(id,email,name,password_hash,role) VALUES('u1','dana@example.com','Dana','x','editor')`); err != nil {
		t.Fatal(err)
	}
	body := a.teamRoster(i18n.WithLang(context.Background(), "de"))
	for _, want := range []string{`<option value="editor" selected>Redakteur</option>`, "Jemanden hinzufügen", "Konto anlegen", "verwaltet Team und Einstellungen"} {
		if !strings.Contains(body, want) {
			t.Errorf("the German team lacks %q:\n%s", want, body)
		}
	}
}

// The words a Settings script writes come from the page, in its language,
// with the count left for the script to fill.
func TestTheSettingsScriptsWordsAreInThePagesLanguage(t *testing.T) {
	words := string(settingsScriptWords("de"))
	for _, want := range []string{`data-one="1 ungespeicherte Änderung"`, `data-many="{n} ungespeicherte Änderungen"`, `data-remove-link="Link entfernen"`} {
		if !strings.Contains(words, want) {
			t.Errorf("the German script words lack %q:\n%s", want, words)
		}
	}
	if !strings.Contains(settingsPageScript, "sw('many'") {
		t.Error("the page script does not read its words from the page")
	}
}
