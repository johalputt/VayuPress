// SPDX-License-Identifier: Apache-2.0

package main

// handlers_theme_draft.go — Theme Studio resumable drafts (Wave C).
//
// The Studio autosaves its editor state (base64 JSON snapshot) under the
// reserved settings key theme_studio_draft for the operator's scope. A draft
// NEVER touches the live render pipeline — unlike the generic settings API,
// these handlers deliberately skip reloadRenderSettings — and is consumed by
// handleOSTheme to render the resume banner.

import (
	"encoding/json"
	"net/http"
)

const themeDraftKey = "theme_studio_draft"

func (a *App) handleThemeDraftSave(w http.ResponseWriter, r *http.Request) {
	if a.siteSettings == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "settings-error", "settings not initialised", "")
		return
	}
	var body struct {
		Draft   string `json:"draft"`
		Discard bool   `json:"discard"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	value := body.Draft
	if body.Discard || value == "" {
		value = "" // sentinel: no draft
	}
	if err := a.siteSettings.SetMany(r.Context(), osScope(r), map[string]string{themeDraftKey: value}); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "settings-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// themeDraftFor returns the stored draft ("" when none) for the page render.
func themeDraftFor(vals map[string]string) string {
	v := vals[themeDraftKey]
	return v
}
