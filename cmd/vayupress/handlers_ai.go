package main

import (
	"encoding/json"
	"net/http"

	"github.com/johalputt/vayupress/internal/aiassist"
)

// GET /api/v1/admin/ai/status — reports whether the assistant is configured.
func (a *App) handleAIStatus(w http.ResponseWriter, r *http.Request) {
	enabled := a.aiAssist != nil && a.aiAssist.Enabled()
	resp := map[string]interface{}{"enabled": enabled, "ops": aiassist.SupportedOps()}
	if enabled {
		resp["model"] = a.aiAssist.Model()
	}
	writeJSON(w, r, http.StatusOK, resp)
}

// POST /api/v1/admin/ai/assist  {op, text}
// Runs a local-LLM writing operation and returns the suggestion. The assistant
// only suggests — it never mutates content (ethics: no autonomous actions).
func (a *App) handleAIAssist(w http.ResponseWriter, r *http.Request) {
	if a.aiAssist == nil || !a.aiAssist.Enabled() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "ai-disabled", "AI assistant not configured (set VAYU_AI_URL)", "")
		return
	}
	var body struct {
		Op   string `json:"op"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	result, err := a.aiAssist.Assist(r.Context(), body.Op, body.Text)
	if err != nil {
		writeAPIError(w, r, http.StatusBadGateway, "ai-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"op": body.Op, "result": result})
}
