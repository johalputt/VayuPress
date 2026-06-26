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

// maxHeroBytes caps an uploaded hero/cover image. Hero photos are larger than a
// favicon but still bounded so the base64 blob doesn't bloat the settings row.
const maxHeroBytes = 2 * 1024 * 1024 // 2 MB

// detectHeroImageType validates an uploaded hero image by magic number (not the
// attacker-controlled filename/Content-Type) and returns its canonical MIME.
// Accepts PNG, JPEG and WebP — the formats browsers render as a CSS background.
func detectHeroImageType(b []byte) (string, bool) {
	switch {
	case len(b) >= 8 && bytes.Equal(b[:8], []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png", true
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "image/jpeg", true
	case len(b) >= 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return "image/webp", true
	default:
		return "", false
	}
}

// handleHeroUpload accepts a multipart hero-image upload (field "image") or a
// removal (remove=1), validates by magic number, and persists it base64-encoded
// in site_settings. CSRF-protected and mode-gated, mirroring the favicon path.
func (a *App) handleHeroUpload(w http.ResponseWriter, r *http.Request) {
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

	r.Body = http.MaxBytesReader(w, r.Body, maxHeroBytes+16*1024)
	if err := r.ParseMultipartForm(maxHeroBytes + 16*1024); err != nil {
		fail(400, "could not read upload (max 2 MB): "+err.Error())
		return
	}

	if r.FormValue("remove") == "1" {
		if err := a.siteSettings.SetMany(r.Context(), map[string]string{
			settings.KeyThemeHeroImage:     "",
			settings.KeyThemeHeroImageType: "",
		}); err != nil {
			fail(500, "remove failed: "+err.Error())
			return
		}
		logging.LogJSON(logging.LogFields{Level: "info", Component: "theme", Severity: "info", Msg: "hero image removed", RequestID: getRequestID(r)})
		ok("hero image removed")
		return
	}

	file, _, err := r.FormFile("image")
	if err != nil {
		fail(400, "no image file in upload")
		return
	}
	defer file.Close() //nolint:errcheck

	raw, err := io.ReadAll(io.LimitReader(file, maxHeroBytes+1))
	if err != nil {
		fail(400, "could not read file: "+err.Error())
		return
	}
	if len(raw) == 0 {
		fail(400, "uploaded file is empty")
		return
	}
	if len(raw) > maxHeroBytes {
		fail(400, "image exceeds the 2 MB limit")
		return
	}
	mime, valid := detectHeroImageType(raw)
	if !valid {
		fail(400, "file is not a PNG, JPEG or WebP image")
		return
	}

	if err := a.siteSettings.SetMany(r.Context(), map[string]string{
		settings.KeyThemeHeroImage:     base64.StdEncoding.EncodeToString(raw),
		settings.KeyThemeHeroImageType: mime,
	}); err != nil {
		fail(500, "save failed: "+err.Error())
		return
	}
	logging.LogJSON(logging.LogFields{Level: "info", Component: "theme", Severity: "info", Msg: "hero image uploaded", RequestID: getRequestID(r)})
	ok("hero image updated")
}

// serveHeroImage serves the operator's uploaded hero image (same-origin, so the
// strict CSP img-src 'self' covers it). Returns 404 when none is set — CSS
// background-image then simply renders nothing, so the option degrades cleanly.
func (a *App) serveHeroImage(w http.ResponseWriter, r *http.Request) {
	if a.siteSettings == nil {
		http.NotFound(w, r)
		return
	}
	enc := a.siteSettings.Get(r.Context(), settings.KeyThemeHeroImage)
	if enc == "" {
		http.NotFound(w, r)
		return
	}
	b, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(b) == 0 {
		http.NotFound(w, r)
		return
	}
	ct := a.siteSettings.Get(r.Context(), settings.KeyThemeHeroImageType)
	if ct == "" {
		ct = "image/jpeg"
	}
	sum := sha256.Sum256(b)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	w.Header().Set("Content-Type", ct)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(b)
}

// maxOGBytes caps an uploaded social/share image. 1.5 MB is generous for an
// og:image (typically 1200×630) while keeping the base64 settings row bounded.
const maxOGBytes = 1536 * 1024

// handleOGUpload accepts a multipart social/share image (field "image") or a
// removal (remove=1), validates by magic number, and persists it base64-encoded
// in site_settings. CSRF-protected and mode-gated, mirroring the hero path.
func (a *App) handleOGUpload(w http.ResponseWriter, r *http.Request) {
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

	r.Body = http.MaxBytesReader(w, r.Body, maxOGBytes+16*1024)
	if err := r.ParseMultipartForm(maxOGBytes + 16*1024); err != nil {
		fail(400, "could not read upload (max 1.5 MB): "+err.Error())
		return
	}

	if r.FormValue("remove") == "1" {
		if err := a.siteSettings.SetMany(r.Context(), map[string]string{
			settings.KeyThemeOGImage:     "",
			settings.KeyThemeOGImageType: "",
		}); err != nil {
			fail(500, "remove failed: "+err.Error())
			return
		}
		logging.LogJSON(logging.LogFields{Level: "info", Component: "theme", Severity: "info", Msg: "og image removed", RequestID: getRequestID(r)})
		ok("share image removed")
		return
	}

	file, _, err := r.FormFile("image")
	if err != nil {
		fail(400, "no image file in upload")
		return
	}
	defer file.Close() //nolint:errcheck

	raw, err := io.ReadAll(io.LimitReader(file, maxOGBytes+1))
	if err != nil {
		fail(400, "could not read file: "+err.Error())
		return
	}
	if len(raw) == 0 {
		fail(400, "uploaded file is empty")
		return
	}
	if len(raw) > maxOGBytes {
		fail(400, "image exceeds the 1.5 MB limit")
		return
	}
	mime, valid := detectHeroImageType(raw)
	if !valid {
		fail(400, "file is not a PNG, JPEG or WebP image")
		return
	}

	if err := a.siteSettings.SetMany(r.Context(), map[string]string{
		settings.KeyThemeOGImage:     base64.StdEncoding.EncodeToString(raw),
		settings.KeyThemeOGImageType: mime,
	}); err != nil {
		fail(500, "save failed: "+err.Error())
		return
	}
	logging.LogJSON(logging.LogFields{Level: "info", Component: "theme", Severity: "info", Msg: "og image uploaded", RequestID: getRequestID(r)})
	ok("share image updated")
}

// serveOGImage serves the operator's uploaded social/share image (same-origin).
// Returns 404 when none is set so the templates simply omit the og:image tag.
func (a *App) serveOGImage(w http.ResponseWriter, r *http.Request) {
	if a.siteSettings == nil {
		http.NotFound(w, r)
		return
	}
	enc := a.siteSettings.Get(r.Context(), settings.KeyThemeOGImage)
	if enc == "" {
		http.NotFound(w, r)
		return
	}
	b, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(b) == 0 {
		http.NotFound(w, r)
		return
	}
	ct := a.siteSettings.Get(r.Context(), settings.KeyThemeOGImageType)
	if ct == "" {
		ct = "image/jpeg"
	}
	sum := sha256.Sum256(b)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	w.Header().Set("Content-Type", ct)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(b)
}
