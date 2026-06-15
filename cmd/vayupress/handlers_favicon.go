package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"

	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/settings"
)

// maxFaviconBytes caps an uploaded favicon. Real favicons are a few KB; 256 KB
// is generous headroom while refusing anything that could bloat the settings
// row (the bytes are base64-encoded into the DB).
const maxFaviconBytes = 256 * 1024

// pngMagic and icoMagic are the leading signature bytes used to validate an
// uploaded favicon by content rather than trusting its filename or the
// browser-supplied Content-Type (both of which are attacker-controlled).
var (
	pngMagic = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	icoMagic = []byte{0x00, 0x00, 0x01, 0x00}
)

// detectFaviconType returns the canonical MIME type for b based on its magic
// number, or ("", false) if b is neither a PNG nor an ICO. Browsers render PNG
// bytes served as image/png at /favicon.ico fine, so PNG is the primary path;
// ICO is accepted for operators who already have a classic .ico.
func detectFaviconType(b []byte) (string, bool) {
	switch {
	case len(b) >= len(pngMagic) && bytes.Equal(b[:len(pngMagic)], pngMagic):
		return "image/png", true
	case len(b) >= len(icoMagic) && bytes.Equal(b[:len(icoMagic)], icoMagic):
		return "image/x-icon", true
	default:
		return "", false
	}
}

// handleFaviconUpload accepts a multipart favicon upload (field "favicon") or a
// removal request (form value remove=1), validates the bytes by magic number,
// and persists them base64-encoded into site_settings. It is a CSRF-protected,
// mode-gated governed write, mirroring the theme Save/Reset handlers.
func (a *App) handleFaviconUpload(w http.ResponseWriter, r *http.Request) {
	fail := func(code int, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
	}
	ok := func(msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": msg}) //nolint:errcheck
	}

	cur := mode.Global.Current()
	if cur == mode.ModeReadOnly || cur == mode.ModeQuarantined {
		fail(503, "branding cannot be changed in "+string(cur)+" mode")
		return
	}

	// Cap the whole request body before touching the multipart reader so an
	// oversized upload is refused up front rather than buffered.
	r.Body = http.MaxBytesReader(w, r.Body, maxFaviconBytes+8*1024)
	if err := r.ParseMultipartForm(maxFaviconBytes + 8*1024); err != nil {
		fail(400, "could not read upload (max 256 KB): "+err.Error())
		return
	}

	// Removal path — clear the stored favicon so the embedded default returns.
	if r.FormValue("remove") == "1" {
		if err := a.siteSettings.SetMany(r.Context(), map[string]string{
			settings.KeyBrandFavicon:     "",
			settings.KeyBrandFaviconType: "",
		}); err != nil {
			fail(500, "remove failed: "+err.Error())
			return
		}
		logging.LogJSON(logging.LogFields{
			Level: "info", Component: "theme", Severity: "info",
			Msg: "custom favicon removed", RequestID: getRequestID(r),
		})
		ok("favicon removed — default restored")
		return
	}

	file, _, err := r.FormFile("favicon")
	if err != nil {
		fail(400, "no favicon file in upload")
		return
	}
	defer file.Close() //nolint:errcheck

	raw, err := io.ReadAll(io.LimitReader(file, maxFaviconBytes+1))
	if err != nil {
		fail(400, "could not read file: "+err.Error())
		return
	}
	if len(raw) == 0 {
		fail(400, "uploaded file is empty")
		return
	}
	if len(raw) > maxFaviconBytes {
		fail(400, "favicon exceeds the 256 KB limit")
		return
	}

	mime, valid := detectFaviconType(raw)
	if !valid {
		fail(400, "file is not a PNG or ICO image")
		return
	}

	if err := a.siteSettings.SetMany(r.Context(), map[string]string{
		settings.KeyBrandFavicon:     base64.StdEncoding.EncodeToString(raw),
		settings.KeyBrandFaviconType: mime,
	}); err != nil {
		fail(500, "save failed: "+err.Error())
		return
	}

	logging.LogJSON(logging.LogFields{
		Level: "info", Component: "theme", Severity: "info",
		Msg: "custom favicon uploaded", RequestID: getRequestID(r),
	})
	ok("favicon updated")
}

// serveFavicon returns a handler for a favicon route. It serves the operator's
// uploaded favicon when one is stored, otherwise the embedded default bytes.
// Because every public template references the favicon by these fixed URLs,
// overriding at the serving layer means a custom upload propagates everywhere
// without touching a single template.
func (a *App) serveFavicon(fallback []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.siteSettings != nil {
			if enc := a.siteSettings.Get(r.Context(), settings.KeyBrandFavicon); enc != "" {
				if b, err := base64.StdEncoding.DecodeString(enc); err == nil && len(b) > 0 {
					ct := a.siteSettings.Get(r.Context(), settings.KeyBrandFaviconType)
					if ct == "" {
						ct = "image/png"
					}
					serveFaviconBytes(w, r, b, ct)
					return
				}
			}
		}
		// Default embedded mark — safe to cache aggressively (immutable).
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, immutable, max-age=31536000")
		_, _ = w.Write(fallback)
	}
}

// serveFaviconBytes writes b with an ETag so an updated upload propagates
// promptly (short max-age + revalidation) rather than being pinned by the
// year-long immutable cache the default marks use.
func serveFaviconBytes(w http.ResponseWriter, r *http.Request, b []byte, contentType string) {
	sum := sha256.Sum256(b)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=60")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(b)
}
