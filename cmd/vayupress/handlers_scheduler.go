// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/api"
)

// =============================================================================
// Scheduled publishing (Tier 1)
// =============================================================================

// POST /api/v1/admin/schedule
// Body: {slug, title, content, tags[], publish_at (RFC3339)}
// Stages a post for future publication. When publish_at is in the past the
// post is promoted on the next scheduler tick (effectively "publish now").
//
// The body is validated with the SAME rules the publish pipeline will apply
// (ValidateArticleInput): staging used to accept anything, so an operator got
// 201 Created and then the post silently never published — discoverable only
// by polling this list (audit). Tags may not contain commas: staging joins
// them with one, so a comma would silently become several tags at promotion.
func (a *App) handleScheduleCreate(w http.ResponseWriter, r *http.Request) {
	if a.scheduler == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "scheduler-disabled", "Scheduler not initialised", "")
		return
	}
	var body struct {
		Slug      string   `json:"slug"`
		Title     string   `json:"title"`
		Content   string   `json:"content"`
		Tags      []string `json:"tags"`
		PublishAt string   `json:"publish_at"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 5<<20)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	publishAt, err := time.Parse(time.RFC3339, strings.TrimSpace(body.PublishAt))
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-time", "publish_at must be RFC3339 (e.g. 2026-07-01T09:00:00Z)", "")
		return
	}
	slug := strings.TrimSpace(body.Slug)
	if slug == "" {
		slug = api.Slugify(body.Title)
	}
	for _, tag := range body.Tags {
		if strings.Contains(tag, ",") {
			writeAPIError(w, r, http.StatusBadRequest, "bad-tag",
				"Tags cannot contain commas — they are stored comma-joined until publish", "")
			return
		}
	}
	if verr := api.ValidateArticleInput(strings.TrimSpace(body.Title), slug, body.Content, body.Tags); verr != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-input", verr.Error(), "")
		return
	}
	post, err := a.scheduler.Schedule(r.Context(), slug, body.Title, body.Content, body.Tags, publishAt)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "schedule-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusCreated, map[string]interface{}{"scheduled": post})
}

// GET /api/v1/admin/schedule — lists staged posts (newest first).
func (a *App) handleScheduleList(w http.ResponseWriter, r *http.Request) {
	if a.scheduler == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "scheduler-disabled", "Scheduler not initialised", "")
		return
	}
	posts, err := a.scheduler.List(r.Context(), 100)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	pending, _ := a.scheduler.PendingCount(r.Context())
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"scheduled": posts, "pending": pending, "count": len(posts),
	})
}

// DELETE /api/v1/admin/schedule/{id} — cancels a still-scheduled post.
func (a *App) handleScheduleCancel(w http.ResponseWriter, r *http.Request) {
	if a.scheduler == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "scheduler-disabled", "Scheduler not initialised", "")
		return
	}
	id := chi.URLParam(r, "id")
	if err := a.scheduler.Cancel(r.Context(), id); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "cancel-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"canceled": id})
}
