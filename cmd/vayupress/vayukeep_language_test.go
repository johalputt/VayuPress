// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/users"
	"github.com/johalputt/vayupress/internal/vayukeep"
)

// Backups is drawn in the page's language, in both its forms: the setup page
// before automatic backup is on, and the status page after, with its restore
// points, sheets and explanation.
func TestBackupsIsDrawnInThePagesLanguage(t *testing.T) {
	setup := osVayuKeepBody("de", "n", vayukeep.Status{}, "", nil, "", vkNow, "", false, keepPrefs{}, "", nil)
	for _, want := range []string{"Sicherungen sind noch nicht eingerichtet", "Automatische Sicherung einrichten", `aria-label="Schließen"`, "Eine erzeugen"} {
		if !strings.Contains(setup, want) {
			t.Errorf("the German setup page lacks %q", want)
		}
	}
	st := vayukeep.Status{Enabled: true, Target: "/mnt/b", NewestGen: vkNow.Add(-5 * time.Minute), LastSuccess: vkNow.Add(-5 * time.Minute)}
	gens := []vayukeep.Generation{{Name: "vk-1", Taken: vkNow.Add(-5 * time.Minute), Bytes: 2048}}
	status := osVayuKeepBody("de", "n", st, "", gens, "", vkNow, "", false, keepPrefs{}, "", nil)
	for _, want := range []string{"Jetzt sichern", "Wiederherstellungspunkte", "vor 5 Min.", "aber nie wiederhergestellt", "So funktioniert es", "Noch nicht getestet"} {
		if !strings.Contains(status, want) {
			t.Errorf("the German status page lacks %q", want)
		}
	}
	if strings.Contains(status, "Back up now") || strings.Contains(status, "Restore points") {
		t.Error("the German status page still says an English control")
	}
}

// A backup that ran in the background says how it went in the language of
// whoever opens the page next, not the one that started it.
func TestABackgroundRunIsSaidInTheReadersLanguage(t *testing.T) {
	k := &keepRun{}
	k.startManual(func(l string) string { return "x" }, vkNow)
	k.finishManual(keepResult{OK: true, Generation: "vk-1", Older: 2, Say: func(l string) string {
		return i18n.T(l, "{point} saved and tested: it restores, and the database inside checks out clean.", "point", "vk-1")
	}})
	if got := keepRunHTML("de", k); !strings.Contains(got, "vk-1 gesichert und getestet") || !strings.Contains(got, "2 ältere Wiederherstellungspunkte entfernen") {
		t.Errorf("the German run block reads:\n%s", got)
	}
	if got := keepRunHTML("en", k); !strings.Contains(got, "vk-1 saved and tested") {
		t.Errorf("the English run block reads:\n%s", got)
	}
}

// A refusal from Backups' API is in the language of the page that asked.
func TestBackupsRepliesInThePagesLanguage(t *testing.T) {
	h := consoleLanguage(http.HandlerFunc((&App{}).handleOSVayuKeepSetup))
	r := withUser(httptest.NewRequest(http.MethodPost, "/os/api/vayukeep/setup", strings.NewReader(`{}`)), &users.User{ID: "a", Role: users.RoleAdmin})
	r.Header.Set(pageLangHeader, "de")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if !strings.HasPrefix(out.Error.Message, "Einstellungen können noch nicht gespeichert werden") {
		t.Errorf("the refusal to a German page reads %q", rec.Body.String())
	}
}
