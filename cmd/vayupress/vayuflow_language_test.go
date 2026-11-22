// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// VayuFlow is drawn in its reader's language: the page, its posture report
// (the flowaudit package's sentences), the action registry's words and the
// flow form; its actions refuse in the language of the page that asked. An
// action's name and an event's are the engine's, as written.
func TestVayuFlowIsDrawnInThePagesLanguage(t *testing.T) {
	a := &App{}
	r := httptest.NewRequest(http.MethodGet, "/os/vayuflow", nil)
	r.Header.Set("Accept-Language", "de")
	rec := httptest.NewRecorder()
	consoleLanguage(http.HandlerFunc(a.handleOSVayuFlow)).ServeHTTP(rec, r)
	page := withoutScriptWords(rec.Body.String())
	for _, want := range []string{"Ablauf erstellen", "Was ein Ablauf tun darf", "Ablauf speichern",
		"Die Automatisierungs-Engine läuft in diesem Build nicht. Nichts auf dieser Seite", ">Lage<", "Bis zu vier Schritte"} {
		if !strings.Contains(page, want) {
			t.Errorf("the German VayuFlow page lacks %q", want)
		}
	}
	for _, english := range []string{"Create a flow", "What a flow may do", "Save flow", "The automation engine is not running in this build",
		">Posture<", "Owner must be", "manual — only when you press Run"} {
		if strings.Contains(page, english) {
			t.Errorf("the German VayuFlow page still says %q", english)
		}
	}

	arm := httptest.NewRequest(http.MethodPost, "/os/api/vayuflow/arm?id=f1&mode=live", nil)
	arm.Header.Set(pageLangHeader, "de")
	rec = httptest.NewRecorder()
	consoleLanguage(http.HandlerFunc(a.handleOSVayuFlowArm)).ServeHTTP(rec, arm)
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
