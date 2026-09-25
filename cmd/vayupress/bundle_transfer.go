// SPDX-License-Identifier: Apache-2.0

package main

// bundle_transfer.go — getting a hand-built website onto the server and back
// off it, at any size: a chunked upload, a deploy of what was uploaded, and a
// download of what is live. One set of handlers serves the primary site and
// every hosted site; only the directory differs.
//
// WHY CHUNKED. The bundle was once one multipart POST capped at 60 MiB. Lifting
// that cap in the app alone would have been a claim, not a control: the nginx
// this project installs refuses bodies over 50M, the Caddyfile over 50MiB, and
// a Cloudflare-proxied domain over 100 MB — each answers 413 before VayuPress
// sees a byte. Pieces of bundleChunkBytes pass all three with no change to
// anybody's proxy, and a dropped connection costs one piece rather than the
// whole upload. What bounds a bundle now is the disk (bundleBudget).

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/customsite"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/render"
)

const (
	// bundleChunkBytes is the piece size the console sends. Under every proxy
	// limit named above with room to spare.
	bundleChunkBytes = 8 << 20
	// bundleChunkMax is what the server accepts for one piece: the console's
	// size plus slack, and still far below the smallest proxy cap.
	bundleChunkMax = 16 << 20
	// bundleReserveFloor is the least free space an upload may leave behind.
	bundleReserveFloor = int64(1) << 30
	// bundleReserveCeiling bounds the reserve on a large disk: 5% of two
	// terabytes is a hundred gigabytes held back for a database that needs a
	// fraction of it, which would refuse uploads a machine has ample room for.
	bundleReserveCeiling = int64(10) << 30
	// bundleUploadMaxAge is how long an abandoned upload is kept before the
	// next upload to any site deletes it.
	bundleUploadMaxAge = 24 * time.Hour
)

// bundleDisk reports the size and free space of the filesystem holding path.
// A variable so tests can stand in a nearly full disk.
var bundleDisk = diskUsage

// bundleBudget is how many more bytes a bundle may put on disk: the free space
// on the volume holding the bundles, less a reserve.
//
// The reserve is the point. The database, the mail store and the logs share
// this volume, and SQLite cannot commit into a full disk — an upload allowed
// to take the last byte would take the whole install down with it. The
// reserve is 5% of the volume, at least 1 GiB and at most 10 GiB.
//
// STORAGE_QUOTA_GB was the other candidate and was rejected: it is
// self-declared, counts only the database and the render cache, and is never
// compared with the disk, so it could admit a bundle the volume cannot hold.
//
// Where the platform cannot report free space (anything but Linux, which is
// not a supported deployment), the budget is unbounded and ENOSPC is what
// stops a write; Deploy then discards its staging directory.
func bundleBudget() int64 {
	// Measured at the bundle root once it exists, else at the data root it is
	// created under: rendering the Website page must not create directories.
	dir := customSiteRoot()
	if _, err := os.Stat(dir); err != nil {
		dir = filepath.Dir(dir)
	}
	total, free := bundleDisk(dir)
	if total == 0 {
		return 1<<63 - 1
	}
	reserve := max(int64(total/20), bundleReserveFloor) //nolint:gosec // a volume size, far below 2^63
	if reserve > bundleReserveCeiling {
		reserve = bundleReserveCeiling
	}
	return max(int64(free)-reserve, 0) //nolint:gosec // as above
}

// bundleRoomLine is the upload card's statement of how large a bundle may be:
// no number chosen in advance, and the room this disk actually has.
func bundleRoomLine() string {
	room := bundleBudget()
	if room == 1<<63-1 {
		return `<p class="text-sm muted">No fixed size limit — a bundle may use this server's free disk.</p>`
	}
	return `<p class="text-sm muted">No fixed size limit. This server has room for <strong>` +
		htmlEscape(humanBytes(room)) + `</strong> more, after keeping space free for the database.</p>`
}

// bundleUploadMu serialises the offset check and the append for a piece, so
// a retried piece racing its original cannot be written twice.
var bundleUploadMu sync.Mutex

// bundleSite resolves the bundle directory, and a name for the audit log, for
// the site a request is about.
type bundleSite func(r *http.Request) (dir, name string, ok bool)

func primaryBundleSite(a *App) bundleSite {
	return func(r *http.Request) (string, string, bool) {
		name := config.Cfg.Domain
		if name == "" {
			name = "site"
		}
		return a.customSiteDir(r), name, true
	}
}

func scopedBundleSite(r *http.Request) (string, string, bool) {
	d, ok := osScopedDomain(r)
	if !ok {
		return "", "", false
	}
	return scopedBundleDir(d), d.Host, true
}

// uploadPath is the partial archive for one upload id. The file lives in the
// site's own bundle directory, so one site's upload cannot be deployed to
// another. The id is checked as hex before it is joined into a path: chi will
// not hand a "/" to a route parameter today, and a path built on what a router
// happens to do is one refactor from traversal — the reasoning customSiteDirFor
// records for domain ids.
func uploadPath(dir, id string) (string, bool) {
	if !isHexScope(id) {
		return "", false
	}
	return filepath.Join(dir, ".upload-"+id+".zip"), true
}

// sweepStaleUploads deletes partial uploads nobody finished, for every site.
// A closed tab leaves one behind, each can be as large as the disk allowed,
// and a site whose operator gave up may never start another upload — so this
// is not left to the next upload to the SAME site.
func sweepStaleUploads() {
	root := customSiteRoot()
	primary, _ := filepath.Glob(filepath.Join(root, ".upload-*.zip"))
	hosted, _ := filepath.Glob(filepath.Join(root, "*", ".upload-*.zip"))
	for _, m := range append(primary, hosted...) {
		if fi, err := os.Stat(m); err == nil && time.Since(fi.ModTime()) > bundleUploadMaxAge {
			_ = os.Remove(m)
		}
	}
}

func (a *App) bundleSiteOr404(w http.ResponseWriter, r *http.Request, site bundleSite) (string, string, bool) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return "", "", false
	}
	dir, name, ok := site(r)
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, "unknown-domain", "no such site", "")
		return "", "", false
	}
	return dir, name, true
}

// noSpace answers a refusal for lack of disk. 507, not 400: the bundle is
// fine and sending it again will not help, so the message names the room
// there is and what to do about it.
func noSpace(w http.ResponseWriter, r *http.Request, need string) {
	writeAPIError(w, r, http.StatusInsufficientStorage, "no-space",
		need+"This server has room for "+humanBytes(bundleBudget())+" more, after keeping space free for the "+
			"database. Delete old backups or media from Storage, or enlarge the disk, then upload again.", "")
}

// handleBundleUploadStart opens an upload: POST …/uploads?size=N → {id,
// chunk_bytes}. The size is the archive's, sent so an upload that cannot fit
// is refused before its first piece rather than after gigabytes of them.
func (a *App) handleBundleUploadStart(site bundleSite) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dir, _, ok := a.bundleSiteOr404(w, r, site)
		if !ok {
			return
		}
		size, err := strconv.ParseInt(r.URL.Query().Get("size"), 10, 64)
		if err != nil || size < 0 {
			writeAPIError(w, r, http.StatusBadRequest, "bad-size", "size must be the archive's byte count", "")
			return
		}
		if size > bundleBudget() {
			noSpace(w, r, "This .zip is "+humanBytes(size)+". ")
			return
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "mkdir-failed", err.Error(), "")
			return
		}
		sweepStaleUploads()
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "rand-failed", err.Error(), "")
			return
		}
		id := hex.EncodeToString(raw[:])
		p, _ := uploadPath(dir, id)
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // p is dir + validated hex id
		if err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "create-failed", err.Error(), "")
			return
		}
		_ = f.Close()
		writeJSON(w, r, http.StatusCreated, map[string]any{"id": id, "chunk_bytes": bundleChunkBytes})
	}
}

// handleBundleUploadChunk appends one piece: POST …/uploads/{upload}?offset=N.
//
// The offset must equal what the server already holds. A piece whose response
// was lost is resent by the console; without this it would be appended twice
// and the archive would be corrupt in a way only the deploy discovers. On a
// mismatch the answer carries the server's offset, so the console resumes from
// there instead of starting over.
func (a *App) handleBundleUploadChunk(site bundleSite) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dir, _, ok := a.bundleSiteOr404(w, r, site)
		if !ok {
			return
		}
		p, ok := uploadPath(dir, chi.URLParam(r, "upload"))
		if !ok {
			writeAPIError(w, r, http.StatusNotFound, "unknown-upload", "no such upload", "")
			return
		}
		offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
		if err != nil || offset < 0 {
			writeAPIError(w, r, http.StatusBadRequest, "bad-offset", "offset must be a byte count", "")
			return
		}
		// A piece is small, but the connection it arrives on may be slow; the
		// server's 15-second read timeout is for strangers, and this caller is
		// a signed-in administrator.
		_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
		piece, err := io.ReadAll(http.MaxBytesReader(w, r.Body, bundleChunkMax))
		if err != nil {
			writeAPIError(w, r, http.StatusRequestEntityTooLarge, "chunk-too-large",
				"one piece of an upload may be at most "+humanBytes(bundleChunkMax), "")
			return
		}

		bundleUploadMu.Lock()
		defer bundleUploadMu.Unlock()
		fi, err := os.Stat(p)
		if err != nil {
			writeAPIError(w, r, http.StatusNotFound, "unknown-upload", "no such upload", "")
			return
		}
		if fi.Size() != offset {
			writeJSON(w, r, http.StatusConflict, map[string]any{
				"error":  map[string]string{"code": "offset-mismatch", "message": "resume from offset"},
				"offset": fi.Size(),
			})
			return
		}
		if int64(len(piece)) > bundleBudget() {
			_ = os.Remove(p)
			noSpace(w, r, "")
			return
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // p is dir + validated hex id
		if err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "open-failed", err.Error(), "")
			return
		}
		_, werr := f.Write(piece)
		cerr := f.Close()
		if werr != nil || cerr != nil {
			// A short write leaves the file at an offset the console does not
			// know; it resumes from the one the next 409 reports.
			writeAPIError(w, r, http.StatusInternalServerError, "write-failed", errors.Join(werr, cerr).Error(), "")
			return
		}
		writeJSON(w, r, http.StatusOK, map[string]any{"offset": offset + int64(len(piece))})
	}
}

// handleBundleUploadDeploy deploys a finished upload: POST
// …/uploads/{upload}/deploy. The partial file is removed whatever the outcome:
// a refused bundle has to be fixed and uploaded again anyway, and leaving it
// would hold its size of disk for a day.
func (a *App) handleBundleUploadDeploy(site bundleSite) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dir, name, ok := a.bundleSiteOr404(w, r, site)
		if !ok {
			return
		}
		p, ok := uploadPath(dir, chi.URLParam(r, "upload"))
		if !ok {
			writeAPIError(w, r, http.StatusNotFound, "unknown-upload", "no such upload", "")
			return
		}
		defer os.Remove(p) //nolint:errcheck // best-effort cleanup; the sweep catches a failure
		zr, err := zip.OpenReader(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeAPIError(w, r, http.StatusNotFound, "unknown-upload", "no such upload", "")
				return
			}
			writeAPIError(w, r, http.StatusBadRequest, "invalid_bundle", "not a valid .zip file: "+err.Error(), "")
			return
		}
		defer zr.Close()
		// Unpacking a large site outlasts the server's write timeout; the
		// answer must still reach the console that is waiting for it.
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		m, err := customsite.Deploy(dir, &zr.Reader, bundleBudget())
		if errors.Is(err, customsite.ErrNoSpace) {
			noSpace(w, r, "Unpacked, this site is larger than the free space. ")
			return
		}
		if err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_bundle", err.Error(), "")
			return
		}
		dbpkg.AuditLog("vayudomains.website.bundle", dbpkg.AuditActor(r), name,
			"deployed "+itoaSafe(m.Files)+" file"+plural(m.Files)+", "+humanBytes(m.Bytes))
		writeJSON(w, r, http.StatusOK, map[string]any{
			"status": "deployed", "files": m.Files, "bytes": m.Bytes, "entry": m.Entry,
			// What was dropped, so the deploy never quietly differs from the zip.
			"skipped": m.Skipped, "skipped_names": m.SkippedNames,
		})
	}
}

// handleBundleDownload streams the live bundle as a .zip: GET …/download.
func (a *App) handleBundleDownload(site bundleSite) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dir, name, ok := a.bundleSiteOr404(w, r, site)
		if !ok {
			return
		}
		if !customsite.Deployed(dir) {
			writeAPIError(w, r, http.StatusNotFound, "no-bundle", "No website has been uploaded for this site.", "")
			return
		}
		filename := sanitizeFilename(strings.ReplaceAll(name, ":", "-") + "-website-" +
			customsite.ReadManifest(dir).DeployedAt.UTC().Format("20060102-1504") + ".zip")
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		dbpkg.AuditLog("vayudomains.website.bundle", dbpkg.AuditActor(r), name, "downloaded")
		if err := customsite.Export(w, dir); err != nil {
			// The status line is already sent; the browser sees a truncated
			// download, and the log says why.
			logging.LogJSON(logging.LogFields{Level: "error", Component: "website",
				Msg: "bundle download interrupted", Error: err.Error(), RequestID: getRequestID(r)})
		}
	}
}

// handleBundleRestore makes one earlier upload live again: POST
// …/generations/{gen}/restore. What was live joins the history, so the
// restore is itself undoable from the same list.
func (a *App) handleBundleRestore(site bundleSite) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dir, name, ok := a.bundleSiteOr404(w, r, site)
		if !ok {
			return
		}
		gen := chi.URLParam(r, "gen")
		if err := customsite.Restore(dir, gen); err != nil {
			writeAPIError(w, r, http.StatusNotFound, "unknown-generation", err.Error(), "")
			return
		}
		render.CachePurgeAll()
		dbpkg.AuditLog("vayudomains.website.bundle", dbpkg.AuditActor(r), name, "restored upload "+gen)
		writeJSON(w, r, http.StatusOK, map[string]string{"status": "restored"})
	}
}

// bundleHistoryHTML lists a site's earlier uploads with a restore button
// each; restoreBase is the API base the buttons post to.
func bundleHistoryHTML(dir, restoreBase string) string {
	gens := customsite.History(dir)
	if len(gens) == 0 {
		return ""
	}
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString(`<details class="bundle-history"><summary class="text-sm">Earlier uploads (` + itoaSafe(len(gens)) + `)</summary><ul class="bundle-history__list">`)
	for _, g := range gens {
		when := "earlier"
		if !g.Manifest.DeployedAt.IsZero() {
			when = config.FormatSiteStamp(g.Manifest.DeployedAt)
		}
		detail := ""
		if g.Manifest.Files > 0 {
			detail = " · " + itoaSafe(g.Manifest.Files) + " files, " + humanBytes(g.Manifest.Bytes)
		}
		b.WriteString(`<li><span class="text-sm">` + esc(when) + esc(detail) + `</span>` +
			`<button type="button" class="btn btn--ghost btn--sm" data-bundle-restore="` +
			esc(restoreBase+"/generations/"+g.ID+"/restore") + `">Restore</button></li>`)
	}
	b.WriteString(`</ul><p class="text-sm muted">The ` + itoaSafe(customsite.KeptGenerations) +
		` most recent are kept. Restoring one keeps what is live now in this list.</p></details>`)
	return b.String()
}
