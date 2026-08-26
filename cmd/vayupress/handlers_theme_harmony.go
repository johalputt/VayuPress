// SPDX-License-Identifier: Apache-2.0

package main

// handlers_theme_harmony.go — Theme Studio colour intelligence (Wave B).
//
// POST /os/api/theme/harmony  {accent, mood} → full dark+light token set.
// POST /os/api/theme/nearest  {fg, bg}      → fg nudged to WCAG AA.
// Both are pure functions over internal/theme; no storage, session-gated by
// the same route group as the rest of the Studio API.

import (
	"encoding/json"
	"net/http"

	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/theme"
)

func writeThemeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logging.LogWarn("vayuauth", "theme harmony encode: "+err.Error())
	}
}

// handleThemeHarmony derives a palette from {accent, mood}.
func handleThemeHarmony(w http.ResponseWriter, r *http.Request) {
	var req theme.HarmonyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&req); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", "invalid JSON body", "")
		return
	}
	palette, ok := theme.Harmony(req)
	if !ok {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error",
			"accent must be a #rgb or #rrggbb hex colour", "")
		return
	}
	writeThemeJSON(w, r, http.StatusOK, palette)
}

// handleThemeNearest returns a foreground nudged to AA against a background.
func handleThemeNearest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Fg string `json:"fg"`
		Bg string `json:"bg"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&req); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", "invalid JSON body", "")
		return
	}
	fixed, ok := theme.NearestAccessible(req.Fg, req.Bg)
	if !ok {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error",
			"fg and bg must be #hex colours", "")
		return
	}
	writeThemeJSON(w, r, http.StatusOK, map[string]string{"fg": fixed})
}
