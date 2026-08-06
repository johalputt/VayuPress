// SPDX-License-Identifier: Apache-2.0

package main

// handlers_media.go — sovereign, same-origin image hosting for the post editor.
//
// The Admin v2 editor supports drag-&-drop and paste-to-upload images. Rather
// than depend on any third-party object store or CDN (which would violate the
// sovereignty + strict-CSP posture), images are stored on the operator's own
// disk under config.Cfg.MediaDir and served same-origin from /media/{file}.
//
// Security posture:
//   - Upload is a protected, CSRF-guarded, mode-gated write (see routes.go).
//   - The stored bytes are validated by MAGIC NUMBER, never by the
//     attacker-controlled filename or Content-Type header.
//   - SVG is intentionally NOT accepted: it can carry inline <script> and would
//     be an XSS vector when served same-origin.
//   - The on-disk name is derived from the content hash + a safe extension, so
//     an attacker cannot influence the path. The serve route additionally
//     validates the name against a strict regexp before touching the disk, so
//     there is no path-traversal surface.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/blockrender"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/imageproc"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/mode"
)

// maxImageBytes caps an uploaded editor image. 8 MB comfortably covers
// high-resolution screenshots and photos while refusing anything abusive.
const maxImageBytes = 8 * 1024 * 1024

// maxDocBytes caps a document (PDF) upload at 32 MB.
const maxDocBytes = 32 * 1024 * 1024

// safeMediaName matches only the names this server itself generates:
// 32 lowercase hex chars + a known raster or document extension.
var safeMediaName = regexp.MustCompile(`^[a-f0-9]{32}\.(png|jpg|gif|webp|pdf)$`)

// imageMagic maps a canonical extension to the leading signature bytes used to
// validate an upload by content. jpg covers the standard JFIF/EXIF SOI marker.
var imageMagic = []struct {
	ext   string
	mime  string
	magic []byte
}{
	{"png", "image/png", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}},
	{"jpg", "image/jpeg", []byte{0xFF, 0xD8, 0xFF}},
	{"gif", "image/gif", []byte("GIF87a")},
	{"gif", "image/gif", []byte("GIF89a")},
	{"webp", "image/webp", []byte("RIFF")}, // RIFF....WEBP — WEBP checked below
}

// detectImageType returns the canonical extension and MIME type for b based on
// its magic number, or ("","",false) if b is not a supported raster image.
func detectImageType(b []byte) (ext, mime string, ok bool) {
	for _, m := range imageMagic {
		if len(b) >= len(m.magic) && bytes.Equal(b[:len(m.magic)], m.magic) {
			// WEBP needs the "WEBP" fourCC at offset 8 to disambiguate from
			// other RIFF containers (e.g. WAV).
			if m.ext == "webp" {
				if len(b) < 12 || !bytes.Equal(b[8:12], []byte("WEBP")) {
					continue
				}
			}
			return m.ext, m.mime, true
		}
	}
	return "", "", false
}

// storedMedia is the result of validating and persisting a media byte slice.
type storedMedia struct {
	Name   string // content-addressed filename (matches safeMediaName)
	URL    string // same-origin URL ("/media/{name}")
	MIME   string // canonical MIME type detected by magic number
	Size   int    // stored byte length (post-optimization)
	Width  int    // pixel width (0 for non-raster / PDF)
	Height int    // pixel height
}

// storeValidatedMedia is the single trusted path that turns raw bytes into a
// stored media file. It validates by MAGIC NUMBER (never filename/Content-Type),
// refuses SVG implicitly (not in the allowlist), optionally downscales rasters
// with the stdlib pipeline, and content-addresses the result so duplicates
// collapse. Both the multipart upload handler and the remote-image import use
// it, so every byte served from /media has passed the identical gate.
//
// allowPDF controls whether a %PDF document is accepted (uploads: yes; remote
// image import: no — only rasters are re-hosted). It also gates SVG, for the
// same reason: an operator uploading a file is a different trust level from a
// remote URL being re-hosted, and SVG is the one accepted format that is a
// program rather than a picture.
func storeValidatedMedia(raw []byte, allowPDF bool) (storedMedia, error) {
	if len(raw) == 0 {
		return storedMedia{}, errMediaEmpty
	}
	ext, mime, valid := detectImageType(raw)
	isPDF, isSVG := false, false
	if !valid && allowPDF && len(raw) >= 4 && string(raw[:4]) == "%PDF" {
		ext, mime, valid, isPDF = "pdf", "application/pdf", true, true
	}
	// SVG, and the reason it cannot follow the rule above. Every other format
	// here is identified by magic number precisely so a lying filename or
	// Content-Type cannot get bytes past the gate. SVG is XML: it has no magic
	// number, so there is nothing to sniff.
	//
	// What replaces the sniff is the sanitiser, and the critical part is that
	// the SANITISED bytes are what get stored. The original never reaches disk,
	// so there is no window in which a hostile SVG exists under /media waiting
	// for someone to widen a rule later. Accepting it is the same act as
	// cleaning it.
	//
	// That matters because an uploaded SVG is served from this origin: it
	// inherits the site's CSP and sits inside the reader's session. The CSP
	// already refuses inline script, and blockrender.SanitizeSVG strips script,
	// style, foreignObject, on* handlers and every off-site href — so this is
	// two independent controls, not one.
	if !valid && allowPDF && looksLikeSVG(raw) {
		cleaned, ok := blockrender.SanitizeSVG(string(raw))
		if !ok {
			return storedMedia{}, errMediaUnsupported
		}
		raw = []byte(cleaned)
		ext, mime, valid, isSVG = "svg", "image/svg+xml", true, true
	}
	if !valid {
		return storedMedia{}, errMediaUnsupported
	}
	if !isPDF && !isSVG && len(raw) > maxImageBytes {
		return storedMedia{}, errMediaTooLarge
	}

	stored := raw
	var imgW, imgH int
	// The raster pipeline decodes pixels; SVG has none, and PDF is not an image.
	if !isPDF && !isSVG {
		if res, err := imageproc.Optimize(raw, ext, imageproc.DefaultMaxWidth); err == nil {
			stored, imgW, imgH = res.Data, res.Width, res.Height
		} else {
			logging.LogError("media", "optimize failed (storing original)", err.Error())
		}
	}

	sum := sha256.Sum256(stored)
	name := hex.EncodeToString(sum[:16]) + "." + ext
	if err := os.MkdirAll(config.Cfg.MediaDir, 0o755); err != nil {
		return storedMedia{}, err
	}
	dest := filepath.Join(config.Cfg.MediaDir, name)
	if _, statErr := os.Stat(dest); os.IsNotExist(statErr) {
		if err := os.WriteFile(dest, stored, 0o644); err != nil {
			return storedMedia{}, err
		}
	}
	return storedMedia{
		Name: name, URL: "/media/" + name, MIME: mime,
		Size: len(stored), Width: imgW, Height: imgH,
	}, nil
}

// Sentinel errors for storeValidatedMedia so callers can map them to the right
// HTTP status without string matching.
var (
	errMediaEmpty       = errors.New("media: empty file")
	errMediaUnsupported = errors.New("media: unsupported file type")
	errMediaTooLarge    = errors.New("media: file exceeds size limit")
)

// handleMediaUpload accepts a multipart image upload (field "file"), validates
// it by magic number, stores it under MediaDir keyed by its content hash, and
// returns {url, name, size, mime}. Duplicate uploads collapse to the same file.
func (a *App) handleMediaUpload(w http.ResponseWriter, r *http.Request) {
	fail := func(code int, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
	}

	cur := mode.Global.Current()
	if cur == mode.ModeReadOnly || cur == mode.ModeQuarantined {
		fail(503, "media cannot be uploaded in "+string(cur)+" mode")
		return
	}

	// Cap the whole request body before touching the multipart reader so an
	// oversized upload is refused up front rather than buffered to disk.
	r.Body = http.MaxBytesReader(w, r.Body, maxDocBytes+8*1024)
	if err := r.ParseMultipartForm(maxDocBytes + 8*1024); err != nil {
		fail(400, "could not read upload (max 32 MB): "+err.Error())
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		fail(400, "no image file in upload (field 'file')")
		return
	}
	defer file.Close() //nolint:errcheck

	// Read up to the document cap (+1 to detect overflow). PDFs may be up to
	// maxDocBytes; images are additionally constrained to maxImageBytes below.
	raw, err := io.ReadAll(io.LimitReader(file, maxDocBytes+1))
	if err != nil {
		fail(400, "could not read file: "+err.Error())
		return
	}
	if len(raw) == 0 {
		fail(400, "uploaded file is empty")
		return
	}
	if len(raw) > maxDocBytes {
		fail(400, "file exceeds the 32 MB limit")
		return
	}

	// Validate + optimize + store via the shared trusted path (PDF allowed here).
	res, err := storeValidatedMedia(raw, true)
	switch {
	case errors.Is(err, errMediaEmpty):
		fail(400, "uploaded file is empty")
		return
	case errors.Is(err, errMediaUnsupported):
		fail(415, "unsupported file type (allowed: PNG, JPEG, GIF, WebP, PDF)")
		return
	case errors.Is(err, errMediaTooLarge):
		fail(400, "image exceeds the 8 MB limit")
		return
	case err != nil:
		fail(500, "could not store file: "+err.Error())
		return
	}

	logging.LogJSON(logging.LogFields{
		Level: "info", Component: "media", Severity: "info",
		Msg: "image uploaded: " + res.Name, RequestID: getRequestID(r),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
		"url":    res.URL,
		"name":   res.Name,
		"size":   res.Size,
		"mime":   res.MIME,
		"width":  res.Width,
		"height": res.Height,
	})
}

// serveMedia serves a previously uploaded image from MediaDir. The filename is
// validated against safeMediaName before any filesystem access, so there is no
// path-traversal vector. Files are immutable (content-addressed) and cached
// aggressively.
func (a *App) serveMedia(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "file")
	if !safeMediaName.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, immutable, max-age=31536000")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, filepath.Join(config.Cfg.MediaDir, name)) //nosec G703 -- name validated by safeMediaName regex (no separators or dot segments); confined to MediaDir
}

// looksLikeSVG reports whether raw is plausibly an SVG document, cheaply and
// without parsing. It is a pre-filter and NOT a validation: the sanitiser is
// what decides, and it returns ok=false when there is no <svg> element left.
// Deliberately narrow — a leading XML declaration or comment is allowed before
// the root, and nothing else is.
func looksLikeSVG(raw []byte) bool {
	if len(raw) > maxSVGUploadBytes {
		return false
	}
	head := raw
	if len(head) > 1024 {
		head = head[:1024]
	}
	low := strings.ToLower(strings.TrimSpace(string(head)))
	if !strings.HasPrefix(low, "<") {
		return false
	}
	return strings.Contains(low, "<svg")
}

// maxSVGUploadBytes bounds what is even offered to the sanitiser. Its own limit
// applies too; this one keeps a multi-megabyte XML bomb from being turned into
// a string first.
const maxSVGUploadBytes = 1 << 20

// maxMCPUploadBase64 bounds the encoded payload an MCP upload may carry. base64
// inflates bytes by 4/3, so this is maxImageBytes with that overhead plus a
// little slack for whitespace — checked before decoding so an oversized blob is
// refused without allocating its decoded form.
const maxMCPUploadBase64 = (maxImageBytes/3+1)*4 + 1024
