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
//   - SVG IS accepted on an operator upload, and only as blockrender.SanitizeSVG
//     leaves it — the cleaned bytes are the ones that reach disk. It is the one
//     stored format that is a program rather than a picture, so serveMedia gives
//     its response a deny-all, sandboxed policy of its own. Two independent
//     controls, which is what makes accepting it defensible. The remote-image
//     import still refuses it (handlers_media_import.go).
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
	"sync"
	"time"

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

// safeMediaName matches the INERT names this server generates: 32 lowercase hex
// chars plus a raster or document extension. A file in this set is a picture or
// a document — nothing in it can execute — so serveMedia hands it back with no
// policy of its own.
//
// It is deliberately the smaller of the two patterns, and the gap between them
// is what tells serveMedia which response a name gets. Deriving that from the
// inert set rather than from a `.svg` suffix test fails safe: a format added to
// storedMediaName and forgotten here is sandboxed by default, which is the wrong
// way round to be wrong only if the new format is provably not a program.
var safeMediaName = regexp.MustCompile(`^[a-f0-9]{32}\.(png|jpg|gif|webp|pdf)$`)

// storedMediaName matches every name storeValidatedMedia can put on disk —
// safeMediaName's formats plus svg, which the upload path accepts sanitised.
//
// The two answer different questions and were being answered by one pattern.
// safeMediaName asks "is this inert". This one asks "did this server write it",
// which is what the media library, the quota and the serve route have to go on.
//
// Reusing the inert set for both is what let the ceiling charge for files the
// panel could neither show nor remove. An uploaded SVG lands under MediaDir and
// counts against MEDIA_QUOTA_GB, while the Media page listed nothing and the
// delete endpoint refused the name — so a media:write key, the narrowest
// credential the panel issues and the one the quota exists to contain, could
// fill the ceiling with files that left every later upload refused by a 507
// telling the operator to delete what they could not see. A full library has to
// have a way down from it that is the panel, or the ceiling is a worse failure
// than the disk-fill it replaced.
var storedMediaName = regexp.MustCompile(`^[a-f0-9]{32}\.(png|jpg|gif|webp|pdf|svg)$`)

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
	Name   string // content-addressed filename (matches storedMediaName)
	URL    string // same-origin URL ("/media/{name}")
	MIME   string // canonical MIME type detected by magic number
	Size   int    // stored byte length (post-optimization)
	Width  int    // pixel width (0 for non-raster / PDF)
	Height int    // pixel height
}

// storeValidatedMedia is the single trusted path that turns raw bytes into a
// stored media file. It validates by MAGIC NUMBER (never filename/Content-Type),
// takes SVG only from an operator upload and only as the sanitiser leaves it
// (see allowPDF below), optionally downscales rasters with the stdlib pipeline,
// and content-addresses the result so duplicates
// collapse. Both the multipart upload handler and the remote-image import use
// it, so every byte served from /media has passed the identical gate.
//
// The media-directory quota is enforced here too, in writeMediaFile, for exactly
// the same reason the type gate lives here: the browser upload, the remote
// import, the embed-thumbnail fetch and the upload_media connector tool all end
// up on this line, so a ceiling placed here is one every entry point inherits
// without knowing it exists. A second copy at a caller is how one of them ends
// up being the one that was never updated.
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
	if err := writeMediaFile(name, stored); err != nil {
		return storedMedia{}, err
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
	// errMediaQuotaExceeded is a different failure from errMediaTooLarge and has
	// to read as one: the file was fine, the library is full. A caller that
	// collapsed the two would tell an operator to shrink an 80 KB image.
	errMediaQuotaExceeded = errors.New("media: directory quota reached")
)

// ── media directory quota ────────────────────────────────────────────────────
//
// Content addressing bounds DUPLICATE uploads and nothing else: distinct bytes
// were unlimited, so an API key scoped to media:write alone — a deliberately
// weak credential — could write until the filesystem was full. On this product
// that is not a media outage, it is the install: SQLite cannot write into a full
// disk, and the site starts answering 502.
//
// Accounting. Summing the directory costs one stat per file, which is fine once
// and wasteful on every upload of a library with thousands of entries. So the
// total is counted at most once per mediaUsageTTL and then carried forward
// incrementally by adding each newly written file's size.
//
// The failure mode of that cached value, stated plainly because a quota that
// misreads its own usage is worse than none:
//
//   - Files REMOVED behind the counter's back (an operator deleting from the
//     filesystem) leave it overstating usage until the next recount — up to one
//     TTL of refusing uploads that would in fact have fit. That is the direction
//     to be wrong in; the opposite error admits bytes past the ceiling. The one
//     in-product delete path (handleOSMediaDelete) calls invalidateMediaUsage,
//     so the window only exists for changes made outside the product.
//   - The counter is per-directory, and a MediaDir that changes forces a
//     recount, so a total can never be read against the wrong tree.
//   - What is counted is the library, not the tree — see mediaLibraryBytes.
//     Bytes the Media page cannot delete are not charged, because a ceiling
//     reached by files no control removes is a ceiling with no way down from it.
//
// The check and the write share one mutex. Without that, N concurrent uploads
// all read the same "under quota" and all write, overshooting by N files —
// bounded per-file at 32 MB but unbounded in N. Serialising the write is the
// cost; uploads here are operator-scale and the alternative is a ceiling that
// leaks under exactly the concurrency an attacker would use.
var mediaUsage struct {
	mu    sync.Mutex
	dir   string    // the directory `bytes` was counted for
	bytes int64     // total size of the media directory
	at    time.Time // when it was last counted; zero forces a recount
}

// mediaUsageTTL bounds how long a counted total is trusted before it is redone.
const mediaUsageTTL = time.Minute

// mediaQuotaBytes returns the ceiling the media directory is held to.
//
// A non-positive configured value means "unset", not "unlimited": every command
// that does not call config.Load() leaves Cfg zeroed, and a zero there must not
// be the way the quota switches itself off.
func mediaQuotaBytes() int64 {
	if config.Cfg.MediaQuotaBytes > 0 {
		return config.Cfg.MediaQuotaBytes
	}
	return config.DefaultMediaQuotaGB * 1024 * 1024 * 1024
}

// invalidateMediaUsage drops the cached total so the next upload recounts.
// Called after media is deleted, so freed space is usable immediately instead of
// after a TTL of refusing uploads that now fit.
func invalidateMediaUsage() {
	mediaUsage.mu.Lock()
	mediaUsage.at = time.Time{}
	mediaUsage.mu.Unlock()
}

// mediaLibraryBytes sums the stored media directly under dir: the files the
// Media page lists and its delete endpoint can remove, and nothing else.
//
// Deliberately not dirSize, which the storage panel uses for the true on-disk
// footprint and is the right measure there. What the ceiling CHARGES and what
// the panel can FREE have to be the same set of bytes. Charge something the
// operator cannot delete and a full library becomes a state with no way out of
// it from the panel — the 507 says "delete files you no longer need from Media"
// and the page it points at cannot show them, so the only remaining remedy is a
// shell on the box.
//
// Top level only, matching the library: every name this server writes is a bare
// content hash in MediaDir, so nothing it stored is ever in a subdirectory.
func mediaLibraryBytes(dir string) int64 {
	if dir == "" {
		return 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var total int64
	for _, e := range entries {
		if e.IsDir() || !storedMediaName.MatchString(e.Name()) {
			continue
		}
		if info, infoErr := e.Info(); infoErr == nil {
			total += info.Size()
		}
	}
	return total
}

// mediaBytesUsedLocked returns the media library's total size, recounting it
// when the cached value is stale or belongs to a different directory. The caller
// must hold mediaUsage.mu.
func mediaBytesUsedLocked() int64 {
	dir := config.Cfg.MediaDir
	if mediaUsage.dir != dir || mediaUsage.at.IsZero() || time.Since(mediaUsage.at) > mediaUsageTTL {
		mediaUsage.dir = dir
		mediaUsage.bytes = mediaLibraryBytes(dir)
		mediaUsage.at = time.Now()
	}
	return mediaUsage.bytes
}

// writeMediaFile persists one already-validated media file under MediaDir,
// refusing it with errMediaQuotaExceeded if storing it would take the directory
// over its quota. The refusal happens BEFORE the write: a quota checked after
// the bytes are on disk is not a quota, it is a report.
func writeMediaFile(name string, data []byte) error {
	mediaUsage.mu.Lock()
	defer mediaUsage.mu.Unlock()

	dest := filepath.Join(config.Cfg.MediaDir, name)
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		// The name is the content hash, so this file already holds exactly these
		// bytes: re-uploading it consumes no additional space and is admitted even
		// at quota. Refusing it would fail an operation that costs nothing, and an
		// editor re-pasting the same screenshot would look like a broken library.
		return nil
	}
	if used := mediaBytesUsedLocked(); used+int64(len(data)) > mediaQuotaBytes() {
		return errMediaQuotaExceeded
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return err
	}
	mediaUsage.bytes += int64(len(data))
	return nil
}

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
		// SVG belongs in this list because this path accepts it. Leaving it out told
		// an operator whose drawing failed the sanitiser that the format itself was
		// refused, so the repair they attempted was a conversion rather than a look
		// at what was in the file.
		fail(415, "unsupported file type (allowed: PNG, JPEG, GIF, WebP, SVG, PDF)")
		return
	case errors.Is(err, errMediaTooLarge):
		fail(400, "image exceeds the 8 MB limit")
		return
	case errors.Is(err, errMediaQuotaExceeded):
		// 507, not 400: the file is fine and resending it will not help. Name the
		// ceiling and the two things that move it, or the operator is left with a
		// refusal and no next step.
		fail(507, "the media library is full — it has reached its "+humanBytes(mediaQuotaBytes())+
			" quota. Delete files you no longer need from Media, or raise MEDIA_QUOTA_GB.")
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

// serveMedia serves a previously stored asset from MediaDir. The filename is
// validated against storedMediaName before any filesystem access, so there is no
// path-traversal vector. Files are immutable (content-addressed) and cached
// aggressively.
//
// storedMediaName, not safeMediaName, and the difference between the two was a
// whole feature that did not work. The upload path accepts SVG, sanitises it,
// stores it, lists it on the Media page and charges it against MEDIA_QUOTA_GB —
// and this route answered 404 to every one of them, because its allowlist was
// never widened past the rasters. The caller is handed a /media/<hash>.svg URL
// and the connector tool calls that URL public and ready to paste into a post,
// so the symptom reached readers as a broken image while the panel showed the
// asset present and the ceiling went on counting it. An asset the library lists
// and the quota charges has to be one this route hands back.
//
// SVG then needs a second control, because it is the one stored format that is a
// program rather than a picture. The sanitiser is the first: script, style,
// foreignObject, on* handlers and off-site references are gone before the file
// exists. This response carries the second — deny everything, sandbox the
// document — so a miss in the sanitiser still cannot execute in this origin on
// direct navigation. It is set HERE rather than left to securityHeadersMiddleware
// because that policy is built for pages and is widened per-route elsewhere (ad
// origins, video frame-src); the file that is a program must not be able to
// inherit a relaxation meant for something else. Rasters and PDFs are inert and
// keep the plain response, which is also what keeps this header meaningful.
func (a *App) serveMedia(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "file")
	if !storedMediaName.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, immutable, max-age=31536000")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if true {
		// style-src is admitted because the sanitiser keeps style attributes (with
		// every url() that is not a local #fragment stripped), and an SVG that
		// renders without its own fill is not the picture the operator uploaded.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	}
	http.ServeFile(w, r, filepath.Join(config.Cfg.MediaDir, name)) //nosec G703 -- name validated by storedMediaName regex (no separators or dot segments); confined to MediaDir
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

// maxSVGUploadBytes bounds what is even offered to the sanitiser.
//
// NOT independently proven, and marked so rather than left looking tested:
// removing it fails nothing, because SanitizeSVG carries its own size limit and
// refuses an oversized document anyway. A mutation found that.
//
// It stays for one narrow reason. The sanitiser takes a string, so without this
// pre-filter an arbitrarily large []byte is copied into one before anything
// checks its size. That is an allocation an uploader chooses the size of, and
// refusing it a step earlier is cheaper than the alternative — but the control
// that actually decides is the sanitiser, not this line.
const maxSVGUploadBytes = 1 << 20

// maxMCPUploadBase64 bounds the encoded payload an MCP upload may carry. base64
// inflates bytes by 4/3, so this is maxImageBytes with that overhead plus a
// little slack for whitespace — checked before decoding so an oversized blob is
// refused without allocating its decoded form.
const maxMCPUploadBase64 = (maxImageBytes/3+1)*4 + 1024
