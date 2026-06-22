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
	htmpl "html/template"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/render"
)

// mediaItem is one stored asset as surfaced to the library UI.
type mediaItem struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Size    int64  `json:"size"`
	ModUnix int64  `json:"mod"`
	IsPDF   bool   `json:"isPdf"`
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

// handleOSMediaList returns the media library contents as JSON.
func (a *App) handleOSMediaList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"items": listMediaItems()})
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

<div class="media-grid" data-media-grid aria-live="polite">
  <div class="skeleton skeleton--media"></div>
  <div class="skeleton skeleton--media"></div>
  <div class="skeleton skeleton--media"></div>
  <div class="skeleton skeleton--media"></div>
</div>`

	writeOSHTML(w, adminOSLayout(nonce, "Media", "media", cfg, htmpl.HTML(body)))
}
