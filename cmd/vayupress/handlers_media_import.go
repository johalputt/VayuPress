package main

// handlers_media_import.go — sovereign import of a remote image (ADR-0070).
//
// "Embed any picture" is implemented as IMPORT, not hotlink: the server fetches
// the remote URL through the SSRF-hardened safefetch client, validates the bytes
// by magic number, re-encodes them through the same stdlib pipeline as a direct
// upload, and stores the result content-addressed under MediaDir. The editor
// then references the local /media/{name} URL.
//
// Why import beats hotlinking:
//   - img-src 'self' never has to be relaxed — the strict CSP stays intact.
//   - the reader's IP is never leaked to a third-party host.
//   - no mixed-content, no hotlink rot, no tracking pixel.
//   - SVG is refused (not in the raster allowlist) exactly as for uploads.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/safefetch"
)

// remoteImageFetcher is the shared SSRF-safe client for image import. Images are
// capped at maxImageBytes; only http(s) is permitted.
var remoteImageFetcher = safefetch.New(safefetch.Options{
	MaxBytes:       maxImageBytes,
	Timeout:        15 * time.Second,
	AllowedSchemes: []string{"https", "http"},
})

// handleMediaImport accepts {"url": "..."} pointing at a remote image, fetches
// it safely, re-hosts it locally, and returns the same shape as an upload:
// {url, name, size, mime, width, height}.
func (a *App) handleMediaImport(w http.ResponseWriter, r *http.Request) {
	fail := func(code int, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
	}

	cur := mode.Global.Current()
	if cur == mode.ModeReadOnly || cur == mode.ModeQuarantined {
		fail(503, "media cannot be imported in "+string(cur)+" mode")
		return
	}

	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&req); err != nil {
		fail(400, "invalid JSON body")
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	if req.URL == "" {
		fail(400, "missing 'url'")
		return
	}

	res, err := remoteImageFetcher.Get(r.Context(), req.URL)
	switch {
	case errors.Is(err, safefetch.ErrBlockedAddress):
		fail(400, "that URL is not allowed (private/blocked address or scheme)")
		return
	case errors.Is(err, safefetch.ErrTooLarge):
		fail(400, "remote image exceeds the 8 MB limit")
		return
	case err != nil:
		fail(502, "could not fetch the remote image")
		return
	}
	if res.Status < 200 || res.Status >= 300 {
		fail(502, "remote host returned an error")
		return
	}

	// Validate + re-encode + store via the shared trusted path (no PDF import).
	stored, err := storeValidatedMedia(res.Body, false)
	switch {
	case errors.Is(err, errMediaEmpty):
		fail(400, "remote response was empty")
		return
	case errors.Is(err, errMediaUnsupported):
		fail(415, "remote URL is not a supported image (PNG, JPEG, GIF, WebP)")
		return
	case errors.Is(err, errMediaTooLarge):
		fail(400, "remote image exceeds the 8 MB limit")
		return
	case err != nil:
		fail(500, "could not store the imported image")
		return
	}

	logging.LogJSON(logging.LogFields{
		Level: "info", Component: "media", Severity: "info",
		Msg: "remote image imported: " + stored.Name, RequestID: getRequestID(r),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
		"url":    stored.URL,
		"name":   stored.Name,
		"size":   stored.Size,
		"mime":   stored.MIME,
		"width":  stored.Width,
		"height": stored.Height,
	})
}
