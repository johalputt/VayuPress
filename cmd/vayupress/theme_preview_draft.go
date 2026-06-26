package main

// theme_preview_draft.go — transient compiled-CSS drafts for the Theme Studio
// live preview.
//
// The Ghost-style customizer renders a real, full-page iframe preview that must
// reflect EVERY change the operator makes (colours, typography, layout, custom
// CSS, and the higher-level Options) — not just a recolour. Rather than try to
// reconstruct the theme client-side (which can't faithfully apply per-theme
// CustomCSS or the Options layer), the Studio posts the exact same token payload
// it would send to Apply; the server compiles it with the real CompileCSS
// pipeline and parks the resulting stylesheet here under a short-lived id. The
// preview iframe then loads (and hot-swaps) that stylesheet by id.
//
// This keeps the preview pixel-accurate and CSP-safe: no compiled CSS is ever
// inlined or parsed client-side, the stylesheet is served same-origin as
// text/css, and drafts are never persisted as the live theme.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/theme"
)

const (
	// previewDraftTTL bounds how long a transient preview stylesheet is kept.
	previewDraftTTL = 15 * time.Minute
	// previewDraftMax caps the number of concurrent drafts to bound memory.
	previewDraftMax = 256
)

type previewDraftEntry struct {
	css string
	exp time.Time
}

var (
	previewDraftMu    sync.Mutex
	previewDraftStore = map[string]previewDraftEntry{}
)

// previewDraftID returns a 128-bit hex token used to key a preview draft.
func previewDraftID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// previewDraftPut stores compiled CSS and returns its id. Expired entries are
// reaped first; if the store is still at capacity, entries are dropped to make
// room (drafts are disposable, so eviction order is unimportant).
func previewDraftPut(css string) string {
	id := previewDraftID()
	now := time.Now()
	previewDraftMu.Lock()
	defer previewDraftMu.Unlock()
	for k, d := range previewDraftStore {
		if now.After(d.exp) {
			delete(previewDraftStore, k)
		}
	}
	for len(previewDraftStore) >= previewDraftMax {
		for k := range previewDraftStore {
			delete(previewDraftStore, k)
			break
		}
	}
	previewDraftStore[id] = previewDraftEntry{css: css, exp: now.Add(previewDraftTTL)}
	return id
}

// previewDraftGet returns the CSS for an id if it exists and hasn't expired.
func previewDraftGet(id string) (string, bool) {
	previewDraftMu.Lock()
	defer previewDraftMu.Unlock()
	d, ok := previewDraftStore[id]
	if !ok {
		return "", false
	}
	if time.Now().After(d.exp) {
		delete(previewDraftStore, id)
		return "", false
	}
	return d.css, true
}

// handleOSThemePreviewDraft compiles the operator's in-progress theme (the same
// payload the Studio sends to Apply) and parks the resulting stylesheet under a
// short-lived id, returning {id, css_href}. Nothing is persisted as the live
// theme — this only feeds the Studio's live preview iframe.
//
//	POST /os/api/theme/preview-draft
func (a *App) handleOSThemePreviewDraft(w http.ResponseWriter, r *http.Request) {
	// Generous cap: a customized design theme carries its full component CSS
	// (~33 KB) plus tokens. 512 KB bounds the request while fitting any theme.
	r.Body = http.MaxBytesReader(w, r.Body, 512*1024)

	var body struct {
		Preset string            `json:"preset"`
		Tokens *theme.Tokens     `json:"tokens"`
		Fields map[string]string `json:"fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	t, ok := a.resolvePreviewTokens(r, body.Preset, body.Tokens, body.Fields)
	if !ok {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": "body must include 'preset', 'tokens', or 'fields'"})
		return
	}

	css, err := theme.CompileCSS(t)
	if err != nil {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	id := previewDraftPut(css)
	writeJSON(w, r, http.StatusOK, map[string]string{
		"id":       id,
		"css_href": "/os/theme/preview.css?draft=" + id,
	})
}

// resolvePreviewTokens builds a Tokens value from a preview/apply-shaped body:
// an explicit token set, a named preset, or partial field overrides on the
// active theme. Mirrors handleThemeApply's selection logic (without persisting).
func (a *App) resolvePreviewTokens(r *http.Request, preset string, tokens *theme.Tokens, fields map[string]string) (theme.Tokens, bool) {
	switch {
	case tokens != nil:
		return *tokens, true
	case preset != "":
		if p, found := findPreset(preset); found {
			return p, true
		}
		return theme.Tokens{}, false
	case len(fields) > 0:
		base, err := theme.Load(r.Context(), dbpkg.DB)
		if err != nil {
			return theme.Tokens{}, false
		}
		applyOverrides(&base, fields)
		return base, true
	default:
		return theme.Tokens{}, false
	}
}
