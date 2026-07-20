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

// maxFaviconBytes caps an uploaded logo/favicon. This same asset doubles as the
// nav-bar logo (see the Theme page), so operators upload real logo images, not
// just tiny 16px favicons — 1 MB is generous headroom for a crisp PNG/WebP logo
// while still refusing anything that would bloat the settings row (the bytes are
// base64-encoded into the DB).
const maxFaviconBytes = 1024 * 1024

// The leading signature bytes used to validate an uploaded logo/favicon by
// CONTENT rather than trusting its filename or the browser-supplied Content-Type
// (both attacker-controlled). Raster formats only — SVG is deliberately NOT
// accepted, since an SVG served same-origin can carry active content (XSS).
var (
	pngMagic = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	icoMagic = []byte{0x00, 0x00, 0x01, 0x00}
)

// detectFaviconType returns the canonical MIME type for b based on its magic
// number, or ("", false) if b is not a supported logo image. Any of these renders
// fine at the fixed /static/favicon-*.png + /favicon.ico URLs because browsers
// honour the Content-Type, not the extension — so a JPEG/WebP/GIF logo works too,
// which is what most operators actually have (the PNG/ICO-only limit was the
// usual reason "the logo won't change": the upload was silently rejected).
func detectFaviconType(b []byte) (string, bool) {
	switch {
	case len(b) >= len(pngMagic) && bytes.Equal(b[:len(pngMagic)], pngMagic):
		return "image/png", true
	case len(b) >= len(icoMagic) && bytes.Equal(b[:len(icoMagic)], icoMagic):
		return "image/x-icon", true
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "image/jpeg", true
	case len(b) >= 6 && (bytes.Equal(b[:6], []byte("GIF87a")) || bytes.Equal(b[:6], []byte("GIF89a"))):
		return "image/gif", true
	case len(b) >= 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return "image/webp", true
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
		fail(400, "could not read upload (max 1 MB): "+err.Error())
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
		fail(400, "logo exceeds the 1 MB limit")
		return
	}

	mime, valid := detectFaviconType(raw)
	if !valid {
		fail(400, "file is not a supported image (PNG, JPEG, WebP, GIF or ICO)")
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
		// Default embedded mark. Serve it with an ETag + short revalidation (NOT a
		// year-long immutable cache): a browser that cached an immutable default
		// would keep showing it for a YEAR and never pick up a later custom-logo
		// upload, so the operator's new logo silently "won't change". This bit the
		// Tor world especially — a fresh child starts with the shared default, so a
		// custom logo uploaded there appeared to do nothing. Revalidation makes the
		// default→custom switch show within a minute.
		serveFaviconBytes(w, r, fallback, "image/png")
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
