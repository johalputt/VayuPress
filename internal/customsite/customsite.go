// SPDX-License-Identifier: Apache-2.0

// Package customsite deploys and serves an operator-uploaded static website
// bundle (a .zip of HTML/CSS/JS/images) at the domain root — the "custom" site
// mode in VayuOS → Website. It exists so a site built elsewhere (by hand or with
// an AI assistant) can be published in one click, without a build step, while
// the blog stays reachable at /blog and posts keep their /slug URLs.
//
// Security posture (this is untrusted operator upload handled with defence in
// depth):
//   - Zip-slip proof: every entry name is cleaned and confined under the target
//     directory; absolute paths, "..", and anything that escapes are rejected.
//   - No symlinks, no directories-as-files, no device/irregular entries.
//   - Extension allowlist (static web assets only) — no executables, and
//     notably no .svg-as-markup surprises beyond what the allowlist permits.
//   - Size + count + per-file caps, enforced against the DECLARED and the ACTUAL
//     decompressed bytes (zip-bomb resistant via a hard copy limit).
//   - Serving is traversal-safe, never lists directories, sets nosniff, and only
//     ever reads from the confined current/ directory.
package customsite

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Limits bound an upload. They are deliberately generous for an image-rich
// marketing site but firmly cap abuse.
const (
	MaxTotalBytes = int64(50) << 20 // 50 MiB decompressed total
	MaxFileBytes  = int64(25) << 20 // 25 MiB per file
	MaxFiles      = 3000
)

// allowedExt is the static-web-asset allowlist. Anything else in the zip is
// rejected (the deploy fails) so an operator notices, rather than silently
// dropping files.
var allowedExt = map[string]bool{
	".html": true, ".htm": true, ".css": true, ".js": true, ".mjs": true,
	".json": true, ".txt": true, ".xml": true, ".map": true, ".webmanifest": true,
	".svg": true, ".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".avif": true, ".ico": true, ".bmp": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	".pdf": true, ".mp4": true, ".webm": true, ".mp3": true, ".ogg": true,
	".csv": true,
}

// ignorableJunk reports whether a zip entry is operating-system metadata that a
// person never meant to include and that no site ever needs.
//
// WHY THIS EXISTS, and it is the difference between a product a stranger can use
// and one they cannot. The extension allowlist below refuses anything it does
// not recognise, and ONE refused entry rejects the whole upload. `.DS_Store` is
// not on the allowlist — and macOS writes a `.DS_Store` into every folder a
// person has ever opened in Finder, so right-click → Compress produces a zip
// that this deploy refuses. Windows does the same with `Thumbs.db`. That is the
// single most common way a human makes a zip, which made "upload your site"
// fail for most people on their first attempt.
//
// These entries are DROPPED, not accepted: nothing is written to disk, so this
// widens no surface. It is strictly narrower than the alternative of putting
// their extensions on the allowlist, which would let them be served.
//
// Deliberately a short, exact list. A `.psd`, a `.zip`, a `.mov` is somebody's
// real content in the wrong place, and refusing THAT loudly is correct — they
// need to know it will not be served.
func ignorableJunk(rel string) bool {
	base := filepath.Base(rel)
	switch base {
	case ".DS_Store", "Thumbs.db", "thumbs.db", "desktop.ini", "Desktop.ini",
		".gitignore", ".gitattributes", ".gitkeep", ".nojekyll", ".editorconfig":
		return true
	}
	// AppleDouble sidecars ("._index.html") and the __MACOSX tree that macOS
	// Archive Utility adds beside every real file.
	if strings.HasPrefix(base, "._") {
		return true
	}
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == "__MACOSX" {
			return true
		}
	}
	return false
}

// ExtAllowed reports whether a file name carries an extension this package will
// accept in a bundle.
//
// Exported so a BUILD-side gate can ask the deploy what it accepts rather than
// keeping a second copy of the list. The second copy is what went wrong: the
// site bundle shipped a README.md, `.md` is not on this list, and a single
// disallowed file rejects the WHOLE deploy — so every upload of that bundle was
// refused and the previously deployed site stayed live. From the operator's
// side that looked like "I uploaded it and nothing changed".
func ExtAllowed(name string) bool {
	return allowedExt[strings.ToLower(filepath.Ext(name))]
}

// Manifest is metadata about the currently deployed bundle, persisted next to it
// so the console can show what is live.
type Manifest struct {
	DeployedAt time.Time `json:"deployed_at"`
	Files      int       `json:"files"`
	Bytes      int64     `json:"bytes"`
	Entry      string    `json:"entry"`
	HasPrev    bool      `json:"has_prev"`

	// Skipped counts operating-system metadata dropped from the upload, and
	// SkippedNames lists the first few. Reported rather than swallowed: a deploy
	// that quietly differs from what somebody zipped is the kind of gap that only
	// shows up much later, as a file they were sure they included.
	Skipped      int      `json:"skipped,omitempty"`
	SkippedNames []string `json:"skipped_names,omitempty"`
}

// dirs returns the three managed directories under base.
func dirs(base string) (current, previous, staging string) {
	return filepath.Join(base, "current"),
		filepath.Join(base, "previous"),
		filepath.Join(base, ".staging")
}

// Deploy validates zipData and atomically installs it as the live bundle under
// base/current, preserving the prior deployment under base/previous for
// one-click rollback. It never partially replaces the live site: extraction
// happens in a staging dir and is swapped in only after full success.
func Deploy(base string, zipData []byte) (Manifest, error) {
	current, previous, staging := dirs(base)

	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return Manifest{}, fmt.Errorf("not a valid .zip file: %w", err)
	}

	// Clean any leftover staging from a previous failed attempt.
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return Manifest{}, fmt.Errorf("create staging: %w", err)
	}
	// Confine ALL extraction to the staging directory via an OS-level root
	// (os.Root, Go 1.24+). Every create/mkdir through it is refused by the
	// kernel if it would escape the root — through "..", an absolute path, or a
	// symlink — so a hostile zip entry name can never write outside staging
	// (defeats Zip Slip at the syscall boundary, not just by string checks).
	root, err := os.OpenRoot(staging)
	if err != nil {
		return Manifest{}, fmt.Errorf("open staging root: %w", err)
	}
	defer root.Close()
	// On any error below, don't leave a half-written staging dir around.
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(staging)
		}
	}()

	var total int64
	var fileCount int
	var sawIndex bool
	var skipped []string
	var skippedCount int

	for _, f := range zr.File {
		name := f.Name
		// Directories are implied by file paths; skip explicit dir entries.
		if strings.HasSuffix(name, "/") {
			continue
		}
		// Reject anything but regular files (symlinks, devices, …).
		if !f.Mode().IsRegular() {
			return Manifest{}, fmt.Errorf("rejected non-regular entry %q (symlinks/specials are not allowed)", name)
		}
		rel, err := safeRel(name)
		if err != nil {
			return Manifest{}, err
		}
		// Operating-system metadata is dropped rather than allowed to reject the
		// whole upload. Recorded, so the panel can say what it ignored instead of
		// quietly differing from what the person zipped.
		if ignorableJunk(rel) {
			if len(skipped) < 20 {
				skipped = append(skipped, rel)
			}
			skippedCount++
			continue
		}
		// ExtAllowed, not the map directly: the build-side gate calls the same
		// function, so what a bundle is checked against and what the deploy
		// enforces are one implementation rather than two that can drift. Two
		// copies of this rule is precisely how a bundle came to be published that
		// no install would accept.
		if !ExtAllowed(rel) {
			return Manifest{}, fmt.Errorf("rejected file %q: extension not allowed — "+
				"a site bundle may contain only static web files, and this one entry stops the whole upload", rel)
		}
		if int64(f.UncompressedSize64) > MaxFileBytes {
			return Manifest{}, fmt.Errorf("file %q exceeds the %d MiB per-file limit", rel, MaxFileBytes>>20)
		}
		fileCount++
		if fileCount > MaxFiles {
			return Manifest{}, fmt.Errorf("bundle has too many files (limit %d)", MaxFiles)
		}

		// Create parent directories and the file THROUGH the confined root, so
		// containment is enforced by the OS regardless of the entry name.
		if dir := path.Dir(rel); dir != "." {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return Manifest{}, fmt.Errorf("mkdir for %q: %w", rel, err)
			}
		}
		written, err := extractOne(root, rel, f, MaxTotalBytes-total)
		if err != nil {
			return Manifest{}, err
		}
		total += written
		if total > MaxTotalBytes {
			return Manifest{}, fmt.Errorf("bundle exceeds the %d MiB total limit", MaxTotalBytes>>20)
		}
		if rel == "index.html" {
			sawIndex = true
		}
	}

	if fileCount == 0 {
		return Manifest{}, errors.New("the .zip is empty")
	}
	if !sawIndex {
		return Manifest{}, errors.New("the bundle must contain an index.html at its root")
	}

	// Swap: move current→previous (replacing any older previous), staging→current.
	hadCurrent := dirExists(current)
	if hadCurrent {
		_ = os.RemoveAll(previous)
		if err := os.Rename(current, previous); err != nil {
			return Manifest{}, fmt.Errorf("archive current deployment: %w", err)
		}
	}
	if err := os.Rename(staging, current); err != nil {
		// Best-effort restore of the previous live site.
		if hadCurrent {
			_ = os.Rename(previous, current)
		}
		return Manifest{}, fmt.Errorf("activate new deployment: %w", err)
	}
	success = true

	m := Manifest{
		DeployedAt: time.Now().UTC(),
		Files:      fileCount,
		Bytes:      total,
		Entry:      "index.html",
		HasPrev:    hadCurrent,

		Skipped:      skippedCount,
		SkippedNames: skipped,
	}
	writeManifest(base, m)
	return m, nil
}

// extractOne copies a single zip entry to rel within the confined root,
// refusing to write more than remaining bytes (a hard stop against a lying
// UncompressedSize64 / zip bomb). Writing via root.Create guarantees the file
// cannot land outside the root, whatever rel resolves to.
func extractOne(root *os.Root, rel string, f *zip.File, remaining int64) (int64, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, fmt.Errorf("open %q in zip: %w", f.Name, err)
	}
	defer rc.Close()

	out, err := root.Create(rel)
	if err != nil {
		return 0, fmt.Errorf("create %q: %w", rel, err)
	}
	defer out.Close()

	// +1 so we can detect an overrun past the remaining budget.
	limit := remaining + 1
	if limit < 1 {
		limit = 1
	}
	n, err := io.Copy(out, io.LimitReader(rc, limit))
	if err != nil {
		return n, fmt.Errorf("write %q: %w", rel, err)
	}
	if n > remaining {
		return n, errors.New("bundle exceeds the total size limit")
	}
	return n, nil
}

// safeRel normalises a zip entry name to a clean, relative, forward-slash path
// confined to the site root, or errors if it is absolute or escapes.
func safeRel(name string) (string, error) {
	if name == "" {
		return "", errors.New("empty entry name in zip")
	}
	if strings.ContainsRune(name, 0) {
		return "", errors.New("entry name contains a NUL byte")
	}
	n := strings.ReplaceAll(name, `\`, "/")
	// Reject outright (rather than silently confine) so a malformed or hostile
	// bundle fails loudly and the operator notices.
	if strings.HasPrefix(n, "/") {
		return "", fmt.Errorf("rejected entry %q: absolute path", name)
	}
	for _, seg := range strings.Split(n, "/") {
		if seg == ".." {
			return "", fmt.Errorf("rejected entry %q: path traversal", name)
		}
	}
	cleaned := path.Clean(n)
	if cleaned == "" || cleaned == "." {
		return "", fmt.Errorf("rejected entry %q: resolves to the root", name)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("rejected entry %q: path traversal", name)
	}
	return cleaned, nil
}

// Rollback swaps the current and previous deployments. It errors if there is no
// previous deployment to restore.
func Rollback(base string) error {
	current, previous, _ := dirs(base)
	if !dirExists(previous) {
		return errors.New("no previous deployment to roll back to")
	}
	tmp := filepath.Join(base, ".swap")
	_ = os.RemoveAll(tmp)
	if dirExists(current) {
		if err := os.Rename(current, tmp); err != nil {
			return err
		}
	}
	if err := os.Rename(previous, current); err != nil {
		if dirExists(tmp) {
			_ = os.Rename(tmp, current)
		}
		return err
	}
	if dirExists(tmp) {
		_ = os.Rename(tmp, previous)
	}
	m := ReadManifest(base)
	m.DeployedAt = time.Now().UTC()
	m.HasPrev = dirExists(previous)
	writeManifest(base, m)
	return nil
}

// Deployed reports whether a live custom bundle exists.
func Deployed(base string) bool {
	current, _, _ := dirs(base)
	return fileExists(filepath.Join(current, "index.html"))
}

// Serve writes the file for urlPath from base/current. It returns true if it
// served a response (2xx/redirect) and false if no matching file exists (the
// caller should then fall through to a 404). Traversal-safe; "/" maps to
// index.html; directories map to their index.html.
func Serve(w http.ResponseWriter, r *http.Request, base, urlPath string) bool {
	current, _, _ := dirs(base)

	// Open the live bundle as a confined OS root: every lookup below is enforced
	// by the kernel to stay inside it, so an attacker-controlled URL path can
	// never reach a file outside the deployed bundle (no traversal, no symlink
	// escape). Missing deployment → no root → 404 fallback.
	root, err := os.OpenRoot(current)
	if err != nil {
		return false
	}
	defer root.Close()

	rel := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(urlPath, "/")), "/")
	if rel == "" {
		rel = "index.html"
	}

	fi, err := root.Stat(rel)
	if err == nil && fi.IsDir() {
		rel = path.Join(rel, "index.html")
		fi, err = root.Stat(rel)
	}
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}

	f, err := root.Open(rel)
	if err != nil {
		return false
	}
	defer f.Close()

	ct := mime.TypeByExtension(path.Ext(rel))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Static bundle assets are safe to cache briefly; HTML is revalidated so a
	// redeploy shows promptly.
	if strings.HasSuffix(rel, ".html") || strings.HasSuffix(rel, ".htm") {
		w.Header().Set("Cache-Control", "public, max-age=60, must-revalidate")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
	// Serve from the already-open, root-confined file handle — no path is passed
	// to the filesystem here — with Range / If-Modified-Since support.
	http.ServeContent(w, r, rel, fi.ModTime(), f)
	return true
}

// Has reports whether the live bundle contains a regular file at urlPath.
//
// The existence half of Serve, sharing its confined root and its path cleaning
// rather than re-deriving them: a second implementation of "which file does this
// URL mean" is a second place for traversal to be got wrong, and only one of the
// two would be the one anybody reviews.
//
// It exists because the console has to DECIDE something before serving — whether
// a hosted site has a mark of its own to draw — and answering that by writing a
// response into a throwaway recorder would make a decision out of a side effect.
func Has(base, urlPath string) bool {
	current, _, _ := dirs(base)
	root, err := os.OpenRoot(current)
	if err != nil {
		return false
	}
	defer root.Close()

	rel := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(urlPath, "/")), "/")
	if rel == "" {
		return false
	}
	fi, err := root.Stat(rel)
	return err == nil && fi.Mode().IsRegular()
}

// IconPath returns the URL path of the icon the live bundle DECLARES for itself,
// and false when it declares none that exists.
//
// It reads index.html and honours <link rel="icon">, because that is what the
// site itself says its icon is and what a browser actually fetches. Guessing at
// /favicon.ico instead was wrong about the bundles this project builds: the
// marketing site declares assets/favicon-32.png and has nothing at its root, so
// a root-only lookup found no icon for a site that plainly has one.
//
// Parsed rather than pattern-matched. A regexp over markup gets attribute order,
// single quotes and self-closing tags wrong in ways that are invisible until a
// bundle happens to be written the other way — and the answer here decides which
// file a panel shows for somebody's business.
//
// Relative hrefs resolve against the bundle root, which is what a browser does
// for a page served at "/". Anything absolute, protocol-relative or off-origin
// is refused: a bundle must not be able to point the console at another host.
// htmlNode is html.Node under a shorter name, so the walker below reads as the
// tree walk it is.
type htmlNode = html.Node

func IconPath(base string) (string, bool) {
	current, _, _ := dirs(base)
	root, err := os.OpenRoot(current)
	if err != nil {
		return "", false
	}
	defer root.Close()

	f, err := root.Open("index.html")
	if err != nil {
		return "", false
	}
	defer f.Close()

	// Bounded: an index.html large enough to matter is not one whose <head> is
	// worth scanning further.
	doc, err := html.Parse(io.LimitReader(f, 1<<20))
	if err != nil {
		return "", false
	}

	var found string
	var walk func(*htmlNode)
	walk = func(n *htmlNode) {
		if found != "" || n == nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "link" {
			var rel, href string
			for _, a := range n.Attr {
				switch strings.ToLower(a.Key) {
				case "rel":
					rel = strings.ToLower(a.Val)
				case "href":
					href = a.Val
				}
			}
			// "icon", "shortcut icon", "apple-touch-icon" all qualify; the first
			// declared wins, which is the one a browser prefers too.
			if strings.Contains(rel, "icon") && href != "" {
				if p, ok := bundleRelativePath(href); ok && Has(base, p) {
					found = p
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if found != "" {
		return found, true
	}

	// Then the conventional root names, for a bundle that ships one without
	// declaring it.
	for _, p := range []string{"/favicon.ico", "/favicon.png", "/favicon.svg"} {
		if Has(base, p) {
			return p, true
		}
	}
	return "", false
}

// bundleRelativePath turns an icon href into a path inside the bundle, refusing
// anything that leaves it.
func bundleRelativePath(href string) (string, bool) {
	href = strings.TrimSpace(href)
	if href == "" {
		return "", false
	}
	// One check, because one is what does the work. Explicit "//" and "data:"
	// prefix tests stood here until mutation showed neither could be made to
	// fail: url.Parse already reports a Host for the first and a Scheme for the
	// second, so they were reassuring rather than load-bearing.
	if u, err := url.Parse(href); err != nil || u.Scheme != "" || u.Host != "" {
		return "", false
	}
	// Strip any query or fragment a cache-buster may carry.
	if i := strings.IndexAny(href, "?#"); i >= 0 {
		href = href[:i]
	}
	p := path.Clean("/" + strings.TrimPrefix(href, "/"))
	if p == "/" {
		return "", false
	}
	return p, true
}

func manifestPath(base string) string { return filepath.Join(base, "manifest.json") }

// ReadManifest returns the persisted manifest (zero value if none).
func ReadManifest(base string) Manifest {
	var m Manifest
	if b, err := os.ReadFile(manifestPath(base)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func writeManifest(base string, m Manifest) {
	if b, err := json.Marshal(m); err == nil {
		_ = os.WriteFile(manifestPath(base), b, 0o644)
	}
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}
