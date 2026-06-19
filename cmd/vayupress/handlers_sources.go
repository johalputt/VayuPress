package main

// handlers_sources.go — editable source side-car for the Admin v2 editor.
//
// The core article pipeline stores rendered, sanitized HTML (see render.go);
// that's what the public page serves. To give the editor true multi-format
// round-tripping — write in Markdown, reopen in Markdown — we persist the
// *editable source* and its format in a side table, completely separate from
// the write queue and render path. This keeps the powerful authoring UX without
// adding any risk to the published-content pipeline.
//
// Security: writes are CSRF-protected and mode-gated (registered in routes.go),
// the format is allow-listed, and the source is size-capped. The source is
// never rendered server-side, so it carries no XSS surface of its own.

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/mode"
)

// maxSourceBytes caps a stored editable source (generous for long-form posts).
const maxSourceBytes = 2 * 1024 * 1024

// handleArticleSourceGet returns the saved editable source + format for a slug,
// or an empty payload (format "markdown") when none has been stored yet.
func (a *App) handleArticleSourceGet(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	var format, source string
	err := dbpkg.DB.QueryRowContext(r.Context(),
		`SELECT format, source FROM article_sources WHERE slug=?`, slug).Scan(&format, &source)
	if err != nil {
		// No side-car yet — the editor falls back to the stored HTML content.
		writeJSON(w, r, http.StatusOK, map[string]interface{}{"slug": slug, "format": "", "source": ""})
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"slug": slug, "format": format, "source": source})
}

// handleArticleSourcePut upserts the editable source + format for a slug.
func (a *App) handleArticleSourcePut(w http.ResponseWriter, r *http.Request) {
	cur := mode.Global.Current()
	if cur == mode.ModeReadOnly || cur == mode.ModeQuarantined {
		writeJSON(w, r, http.StatusServiceUnavailable, map[string]string{"error": "cannot save in " + string(cur) + " mode"})
		return
	}
	slug := chi.URLParam(r, "slug")

	var body struct {
		Format string `json:"format"`
		Source string `json:"source"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSourceBytes+4096)).Decode(&body); err != nil {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if body.Format != "markdown" && body.Format != "html" {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": "format must be 'markdown' or 'html'"})
		return
	}
	if len(body.Source) > maxSourceBytes {
		writeJSON(w, r, http.StatusRequestEntityTooLarge, map[string]string{"error": "source exceeds 2 MB"})
		return
	}

	_, err := dbpkg.DB.ExecContext(r.Context(),
		`INSERT INTO article_sources(slug,format,source,updated_at) VALUES(?,?,?,CURRENT_TIMESTAMP)
		 ON CONFLICT(slug) DO UPDATE SET format=excluded.format,source=excluded.source,updated_at=CURRENT_TIMESTAMP`,
		slug, body.Format, body.Source)
	if err != nil {
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "could not save source"})
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok", "slug": slug, "format": body.Format})
}
