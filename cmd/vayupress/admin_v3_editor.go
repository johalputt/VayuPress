package main

// admin_v3_editor.go — Admin v3 block editor server endpoints (ADR-0068, Phase 3).
//
// The editor is a vanilla-JS, CSP-strict block editor (static/js/admin-v3-editor.js).
// The canonical document is a JSON array of typed blocks. On save the server:
//   1. renders the blocks to sanitised HTML via internal/blockrender,
//   2. updates articles.content (so every reader/feed/search path is unchanged),
//   3. persists the raw blocks_json so the editor can re-hydrate losslessly.
//
// Security: block text is escaped + UGC-sanitised in blockrender (never trusted
// verbatim). Saves are session/API-key gated and CSRF-protected.

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/blockrender"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// loadBlocksJSON returns the stored block document for a slug, or "" if the
// article predates the block editor (or does not exist).
func loadBlocksJSON(ctx context.Context, slug string) string {
	if dbpkg.DB == nil {
		return ""
	}
	var bj string
	_ = dbpkg.DB.QueryRowContext(ctx,
		`SELECT COALESCE(blocks_json,'') FROM articles WHERE slug = ?`, slug).Scan(&bj)
	return bj
}

// persistBlocksJSON writes the raw block document for a slug. It is a direct
// column update: the rendered HTML is saved through the normal article service
// so the write pipeline (cache purge, search index, feeds) stays authoritative.
func persistBlocksJSON(ctx context.Context, slug, blocksJSON string) error {
	if dbpkg.DB == nil {
		return nil
	}
	_, err := dbpkg.DB.ExecContext(ctx,
		`UPDATE articles SET blocks_json = ? WHERE slug = ?`, blocksJSON, slug)
	return err
}

// handleV3EditorSave persists a block document for an existing article. It
// renders blocks → HTML, updates the article content+title via the service,
// then stores the raw blocks for re-hydration.
func (a *App) handleV3EditorSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Slug   string            `json:"slug"`
		Title  string            `json:"title"`
		Blocks []json.RawMessage `json:"blocks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	slug := strings.TrimSpace(body.Slug)
	if slug == "" {
		writeAPIError(w, r, http.StatusBadRequest, "missing-slug", "slug is required", "")
		return
	}

	// Re-marshal the blocks array to a canonical JSON string for storage+render.
	blocksJSON := "[]"
	if len(body.Blocks) > 0 {
		if raw, err := json.Marshal(body.Blocks); err == nil {
			blocksJSON = string(raw)
		}
	}

	contentHTML, _, err := blockrender.Render(blocksJSON)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "render-error", "Could not render blocks: "+err.Error(), "")
		return
	}

	// Update title + content through the article service (authoritative pipeline).
	title := strings.TrimSpace(body.Title)
	var titlePtr *string
	if title != "" {
		titlePtr = &title
	}
	if _, err := a.articles.Update(r.Context(), slug, titlePtr, &contentHTML, nil); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "update-error", err.Error(), "")
		return
	}

	// Persist the raw block document for lossless re-hydration.
	if err := persistBlocksJSON(r.Context(), slug, blocksJSON); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "persist-error", err.Error(), "")
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]string{"status": "saved", "slug": slug})
}

// handleV3EditorPreview renders a block document to sanitised HTML without
// persisting anything — used by the editor's live preview pane.
func (a *App) handleV3EditorPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Blocks []json.RawMessage `json:"blocks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	blocksJSON := "[]"
	if len(body.Blocks) > 0 {
		if raw, err := json.Marshal(body.Blocks); err == nil {
			blocksJSON = string(raw)
		}
	}
	contentHTML, excerpt, err := blockrender.Render(blocksJSON)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "render-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"html": contentHTML, "excerpt": excerpt})
}

// v3EditorBody builds the block-editor shell. The editor hydrates from the
// data-blocks attribute (raw JSON) on first paint; an empty value starts a
// fresh document.
func v3EditorBody(slug, title, blocksJSON string) string {
	// Every interpolated value is escaped with a direct html.EscapeString call so
	// static analysis recognises the sanitiser barrier (an indirect call through a
	// function-typed variable is not recognised as a barrier).
	// data-blocks carries the raw JSON document; the layout/editor JS reads it.
	return `<div class="editor-shell" data-editor data-slug="` + html.EscapeString(slug) + `" data-blocks="` + html.EscapeString(blocksJSON) + `">
  <div class="editor-main">
    <input class="editor-title" data-editor-title type="text" placeholder="Post title…" value="` + html.EscapeString(title) + `" aria-label="Post title">
    <div class="editor-canvas" data-editor-canvas aria-label="Editor canvas"></div>
  </div>
  <aside class="editor-sidebar" aria-label="Editor tools">
    <div class="editor-status" data-editor-status>Ready</div>
    <div class="editor-actions">
      <button type="button" class="btn btn--primary btn--sm" data-editor-save>Save</button>
      <button type="button" class="btn btn--ghost btn--sm" data-editor-preview-btn>Preview</button>
    </div>
    <div class="editor-hint text-xs muted">Press <kbd>/</kbd> on an empty block for commands.</div>
  </aside>
  <div class="editor-preview-modal" data-editor-preview hidden role="dialog" aria-modal="true" aria-label="Preview">
    <div class="editor-preview-panel">
      <div class="editor-preview-head">
        <span>Preview</span>
        <button type="button" class="btn--icon" data-editor-preview-close aria-label="Close preview">✕</button>
      </div>
      <article class="editor-preview-body article" data-editor-preview-body></article>
    </div>
  </div>
</div>`
}
