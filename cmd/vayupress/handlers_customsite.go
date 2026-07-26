// SPDX-License-Identifier: Apache-2.0

package main

// handlers_customsite.go — VayuOS "custom website" deploy: upload a static site
// .zip and serve it at the domain root (the "custom" site mode), with the blog
// at /blog and posts still at /slug. See internal/customsite for the security-
// hardened validation/extraction/serving core, and ADR-0114.

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/customsite"
)

// maxCustomUploadBytes caps the uploaded .zip request body. The decompressed
// bundle is separately capped in customsite.Deploy; this bounds the transfer.
const maxCustomUploadBytes = int64(60) << 20 // 60 MiB

// customSiteDir is the persistent directory holding the deployed bundle. It sits
// next to the media library (a sibling of MEDIA_DIR, i.e. under the data root),
// NOT under CACHE_DIR — the render cache is purged on content changes and would
// otherwise wipe the uploaded site.
func customSiteDir() string {
	return filepath.Join(filepath.Dir(config.Cfg.MediaDir), "custom-site")
}

// customSiteActive reports whether the operator has selected the "custom" mode
// AND a bundle is actually deployed, so the root should serve the custom site.
func (a *App) customSiteActive(r *http.Request) bool {
	mode, _, _ := a.bizSettings(r)
	return mode == "custom" && customsite.Deployed(customSiteDir())
}

// serveCustomIfActive serves urlPath from the deployed custom bundle when the
// custom mode is active and the path resolves to a bundle file. Returns true if
// it wrote a response. Used at the root ("/") and as the 404 fallback, so custom
// pages/assets are served for any path that isn't a real post or reserved route.
func (a *App) serveCustomIfActive(w http.ResponseWriter, r *http.Request, urlPath string) bool {
	if !a.customSiteActive(r) {
		return false
	}
	return customsite.Serve(w, r, customSiteDir(), urlPath)
}

// handleOSWebsiteCustomUpload accepts a multipart .zip upload, validates and
// deploys it. Admin-only + CSRF-protected. The uploaded bundle becomes live
// only once the operator also selects the "custom" mode and saves; until then
// it is staged as the current bundle (previewable by switching mode, revertible
// by Rollback).
func (a *App) handleOSWebsiteCustomUpload(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxCustomUploadBytes)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "upload_too_large", "The upload is too large or malformed (limit 60 MiB).", "")
		return
	}
	file, hdr, err := r.FormFile("bundle")
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "no_file", "Attach a .zip file in the 'bundle' field.", "")
		return
	}
	defer file.Close()
	if filepath.Ext(hdr.Filename) != ".zip" {
		writeAPIError(w, r, http.StatusBadRequest, "not_zip", "The uploaded file must be a .zip.", "")
		return
	}
	data, err := io.ReadAll(file)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "read_failed", "Could not read the uploaded file.", "")
		return
	}
	m, err := customsite.Deploy(customSiteDir(), data)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_bundle", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"status":      "deployed",
		"files":       m.Files,
		"bytes":       m.Bytes,
		"deployed_at": m.DeployedAt,
		"note":        "Select \u201cCustom uploaded website\u201d and Save & publish to make it live.",
	})
}

// handleOSWebsiteCustomRollback swaps the current custom bundle for the previous
// one. Admin-only + CSRF-protected.
func (a *App) handleOSWebsiteCustomRollback(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	if err := customsite.Rollback(customSiteDir()); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "rollback_failed", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "rolled_back"})
}

// handleOSWebsiteCustomGuide serves the AI build guide as a downloadable Markdown
// file so an operator can hand it to any AI assistant to produce a compliant
// bundle. Public within the admin surface (no secrets); admin-gated by its route.
func (a *App) handleOSWebsiteCustomGuide(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="vayupress-custom-website-build-guide.md"`)
	w.Header().Set("Cache-Control", "no-store")
	dom := config.Cfg.Domain
	if dom == "" {
		dom = "yourdomain.com"
	}
	fmt.Fprint(w, customBuildGuide(dom))
}

// customBuildGuide returns the operator-facing, AI-ready build instructions.
func customBuildGuide(domain string) string {
	return `# Build a custom website for VayuPress (one-click .zip deploy)

Give this file to any AI assistant (or follow it yourself) to produce a website
that VayuPress can deploy in one click at **https://` + domain + `/**.

## What to produce
A single **.zip** file containing a **static** website:
- It MUST contain **index.html** at the root of the zip (not inside a subfolder).
- Put assets in folders like ` + "`assets/`, `images/`, `css/`, `js/`" + ` and
  reference them with **relative paths** (e.g. ` + "`assets/app.css`" + `,
  ` + "`images/hero.webp`" + `). Do NOT use absolute filesystem paths.

## Hard rules (the deploy is rejected if broken)
- **index.html at the zip root is required.**
- **Relative links only.** No ` + "`../`" + `, no absolute paths like ` + "`/C:/...`" + `,
  no zip entries beginning with ` + "`/`" + `.
- **Allowed file types only:** .html .htm .css .js .mjs .json .txt .xml .svg
  .png .jpg .jpeg .gif .webp .avif .ico .woff .woff2 .ttf .otf .pdf .mp4 .webm
  .mp3 .ogg .csv .webmanifest .map. Anything else (e.g. .php, .exe) is rejected.
- **Size limits:** 25 MiB per file, 50 MiB total (decompressed), 3000 files max.
- **Self-contained:** inline or bundle your CSS/JS and host fonts/images inside
  the zip. Avoid depending on external CDNs so the site works offline and under
  a strict content policy.
- **No server code.** This is a static site; forms should post to an external
  form service or to a VayuPress endpoint you know exists.

## Reserved paths (do not rely on these — VayuPress owns them)
` + "`/blog`" + ` (your blog homepage), ` + "`/blog/page/N`" + `, your post URLs
` + "`/<post-slug>`" + `, plus ` + "`/api`, `/os`, `/admin`, `/media`, `/static`, `/search`, `/tags`, `/feed.xml`, `/sitemap.xml`, `/robots.txt`, `/manifest.json`, `/.well-known`" + `.
Everything else under the domain is served from your bundle. Link to your blog
with ` + "`<a href=\"/blog\">`" + `.

## Recommended structure
` + "```" + `
index.html
about.html            (optional extra pages — link them relatively)
assets/
  app.css
  app.js
images/
  hero.webp
favicon.ico
` + "```" + `

## Deploy
VayuOS → Website → **Deploy a custom build** → choose your .zip → **Deploy**.
Then pick **Custom uploaded website** and **Save & publish**. Use **Roll back**
to instantly restore the previous upload.

## A minimal, compliant example
` + "```html" + `
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>My Site</title>
  <link rel="stylesheet" href="assets/app.css">
</head>
<body>
  <header><h1>My Site</h1><nav><a href="/blog">Blog</a></nav></header>
  <main><img src="images/hero.webp" alt="" width="1200" height="600"></main>
  <script src="assets/app.js"></script>
</body>
</html>
` + "```" + `
`
}
