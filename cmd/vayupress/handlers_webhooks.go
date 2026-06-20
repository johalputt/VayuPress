package main

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// GET /api/v1/admin/webhooks — list registered outbound webhooks.
func (a *App) handleWebhookList(w http.ResponseWriter, r *http.Request) {
	if a.webhooks == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "webhooks-disabled", "Webhooks not initialised", "")
		return
	}
	hooks, err := a.webhooks.List(r.Context())
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"webhooks": hooks, "count": len(hooks)})
}

// POST /api/v1/admin/webhooks  {url, secret?, events[]}
func (a *App) handleWebhookCreate(w http.ResponseWriter, r *http.Request) {
	if a.webhooks == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "webhooks-disabled", "Webhooks not initialised", "")
		return
	}
	var body struct {
		URL    string   `json:"url"`
		Secret string   `json:"secret"`
		Events []string `json:"events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	hook, err := a.webhooks.Create(r.Context(), body.URL, body.Secret, body.Events)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "create-error", err.Error(), "")
		return
	}
	// The secret is returned exactly once, on creation, so the operator can
	// configure their receiver. It is never echoed by the list endpoint.
	writeJSON(w, r, http.StatusCreated, map[string]interface{}{
		"webhook": hook, "secret": hook.Secret,
	})
}

// DELETE /api/v1/admin/webhooks/{id}
func (a *App) handleWebhookDelete(w http.ResponseWriter, r *http.Request) {
	if a.webhooks == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "webhooks-disabled", "Webhooks not initialised", "")
		return
	}
	id := chi.URLParam(r, "id")
	if err := a.webhooks.Delete(r.Context(), id); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "delete-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"deleted": id})
}

// GET /api/v1/admin/webhooks/{id}/deliveries — recent delivery audit records.
func (a *App) handleWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	if a.webhooks == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "webhooks-disabled", "Webhooks not initialised", "")
		return
	}
	id := chi.URLParam(r, "id")
	deliveries, err := a.webhooks.Deliveries(r.Context(), id, 50)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"deliveries": deliveries, "count": len(deliveries)})
}
