// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/theme"
)

func TestHandleThemeHarmony(t *testing.T) {
	body, _ := json.Marshal(theme.HarmonyRequest{Accent: "#e0562f", Mood: "vivid"})
	req := httptest.NewRequest("POST", "/os/api/theme/harmony", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleThemeHarmony(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var p theme.HarmonyPalette
	if err := json.NewDecoder(rec.Body).Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(p.AccentDark) != 7 || len(p.BgLight) != 7 {
		t.Fatalf("palette incomplete: %+v", p)
	}
}

func TestHandleThemeHarmonyRejectsGarbage(t *testing.T) {
	req := httptest.NewRequest("POST", "/os/api/theme/harmony",
		strings.NewReader(`{"accent":"not-a-colour","mood":"calm"}`))
	rec := httptest.NewRecorder()
	handleThemeHarmony(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestHandleThemeNearest(t *testing.T) {
	req := httptest.NewRequest("POST", "/os/api/theme/nearest",
		strings.NewReader(`{"fg":"#8a8a8a","bg":"#ffffff"}`))
	rec := httptest.NewRecorder()
	handleThemeNearest(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var out map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := out["fg"]; len(got) != 7 || got == "#8a8a8a" {
		t.Fatalf("fg not adjusted: %q", got)
	}
}
