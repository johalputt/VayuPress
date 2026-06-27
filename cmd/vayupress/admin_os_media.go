package main

// admin_os_media.go — VayuOS media library (ADR-0068, Phase 4).
//
// The upload + storage backend already exists (handlers_media.go): content-
// addressed files under config.Cfg.MediaDir, served same-origin from /media/{file},
// with a strict type allowlist (PNG/JPEG/GIF/WebP/PDF — SVG is refused because it
// can carry inline script). This file adds the os browsing surface: a grid page
// and a JSON listing endpoint. Listing only ever exposes server-generated names
// (validated by safeMediaName), so there is no path-traversal or info-leak vector.

import (
	"context"
	"encoding/json"
	htmpl "html/template"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
)

// mediaItem is one stored asset as surfaced to the library UI.
type mediaItem struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Size    int64  `json:"size"`
	ModUnix int64  `json:"mod"`
	IsPDF   bool   `json:"isPdf"`
	Alt     string `json:"alt"`
}

// mediaAltMap returns the persisted filename→alt-text map (best-effort).
func (a *App) mediaAltMap(ctx context.Context) map[string]string {
	out := map[string]string{}
	if a.siteSettings == nil {
		return out
	}
	raw := a.siteSettings.Get(ctx, settings.KeyMediaAlt)
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

// listMediaItems reads MediaDir and returns the stored assets newest-first. Only
// names matching safeMediaName (the content-addressed pattern this server itself
// produces) are included, so stray or hostile filenames are ignored.
func listMediaItems() []mediaItem {
	items := []mediaItem{}
	entries, err := os.ReadDir(config.Cfg.MediaDir)
	if err != nil {
		return items
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !safeMediaName.MatchString(name) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, mediaItem{
			Name:    name,
			URL:     "/media/" + name,
			Size:    info.Size(),
			ModUnix: info.ModTime().Unix(),
			IsPDF:   strings.HasSuffix(name, ".pdf"),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ModUnix > items[j].ModUnix })
	return items
}

// handleOSMediaList returns the media library contents as JSON, merging each
// asset's persisted alt text.
func (a *App) handleOSMediaList(w http.ResponseWriter, r *http.Request) {
	items := listMediaItems()
	alts := a.mediaAltMap(r.Context())
	for i := range items {
		items[i].Alt = alts[items[i].Name]
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"items": items})
}

// handleOSMediaDelete removes one or more content-addressed media files. Names
// are validated against safeMediaName (so only server-generated assets can be
// targeted — no path traversal), their alt entries are pruned, and the count of
// successful deletions is returned.
func (a *App) handleOSMediaDelete(w http.ResponseWriter, r *http.Request) {
	if cur := mode.Global.Current(); cur == mode.ModeReadOnly || cur == mode.ModeQuarantined {
		writeAPIError(w, r, http.StatusServiceUnavailable, "read-only", "media cannot be deleted in "+string(cur)+" mode", "")
		return
	}
	var body struct {
		Names []string `json:"names"`
	}
	if err := readJSONDirect(r, &body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	alts := a.mediaAltMap(r.Context())
	deleted := 0
	for _, n := range body.Names {
		if !safeMediaName.MatchString(n) {
			continue // ignore anything not a server-generated asset name
		}
		if err := os.Remove(filepath.Join(config.Cfg.MediaDir, n)); err == nil {
			deleted++
			delete(alts, n)
		}
	}
	if a.siteSettings != nil {
		if b, err := json.Marshal(alts); err == nil {
			_ = a.siteSettings.SetMany(r.Context(), map[string]string{settings.KeyMediaAlt: string(b)})
		}
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"deleted": deleted})
}

// handleOSMediaAlt sets (or clears) the alt text for one media asset.
func (a *App) handleOSMediaAlt(w http.ResponseWriter, r *http.Request) {
	if a.siteSettings == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "settings-error", "settings not initialised", "")
		return
	}
	var body struct {
		Name string `json:"name"`
		Alt  string `json:"alt"`
	}
	if err := readJSONDirect(r, &body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	if !safeMediaName.MatchString(body.Name) {
		writeAPIError(w, r, http.StatusBadRequest, "bad-name", "Unknown media asset", "")
		return
	}
	alt := strings.TrimSpace(body.Alt)
	if len(alt) > 300 {
		alt = alt[:300]
	}
	alts := a.mediaAltMap(r.Context())
	if alt == "" {
		delete(alts, body.Name)
	} else {
		alts[body.Name] = alt
	}
	b, err := json.Marshal(alts)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "encode-error", err.Error(), "")
		return
	}
	if err := a.siteSettings.SetMany(r.Context(), map[string]string{settings.KeyMediaAlt: string(b)}); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "save-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// handleOSMedia renders the media library page: a responsive grid populated by
// admin-os.js from the listing endpoint, plus an upload dropzone that POSTs to
// the existing /api/v1/admin/media handler.
func (a *App) handleOSMedia(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	count := len(listMediaItems())

	body := `<div class="page-header">
  <h1>Media</h1>
  <span class="muted text-sm">` + strconv.Itoa(count) + ` items</span>
</div>

<div class="media-dropzone" data-media-dropzone tabindex="0" role="button"
     aria-label="Upload media — click or drop files">
  <div class="media-dropzone__icon" aria-hidden="true">⬆</div>
  <div class="media-dropzone__text">Drop an image or PDF here, or <span class="media-dropzone__link">browse</span></div>
  <div class="media-dropzone__hint text-xs muted">PNG · JPEG · GIF · WebP · PDF — up to 32 MB. SVG is refused for security.</div>
  <input type="file" data-media-input accept="image/png,image/jpeg,image/gif,image/webp,application/pdf" hidden>
</div>

<div class="toolbar-row mt-3">
  <input type="search" class="input" data-media-search placeholder="Search by filename…" aria-label="Search media" autocomplete="off" style="flex:1;min-width:160px">
  <div class="seg-filter" role="group" aria-label="Filter by type">
    <button type="button" class="seg-btn is-active" data-media-filter="all">All</button>
    <button type="button" class="seg-btn" data-media-filter="image">Images</button>
    <button type="button" class="seg-btn" data-media-filter="pdf">PDFs</button>
  </div>
  <button type="button" class="btn btn--ghost btn--sm" data-media-delete-selected disabled>Delete selected (<span data-media-sel-count>0</span>)</button>
</div>
<div class="media-empty text-sm muted" data-media-empty hidden>No media match your search.</div>

<div class="media-grid" data-media-grid aria-live="polite">
  <div class="skeleton skeleton--media"></div>
  <div class="skeleton skeleton--media"></div>
  <div class="skeleton skeleton--media"></div>
  <div class="skeleton skeleton--media"></div>
</div>`

	writeOSHTML(w, adminOSLayout(nonce, "Media", "media", cfg, htmpl.HTML(body)))
}
