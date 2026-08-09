// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"

	"github.com/johalputt/vayupress/internal/customsite"
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
		if err := a.siteSettings.SetMany(r.Context(), osScope(r), map[string]string{
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

	// osScope, not ForPrimary. Mounted unscoped this is still the primary, so the
	// operator's own branding page is unchanged; mounted under /os/d/{id} it
	// writes THAT domain's mark.
	//
	// It used to be ForPrimary at every mount, including the one reached from a
	// hosted domain's Theme Studio. The control said "Logo & favicon" on that
	// domain's page and silently replaced the install-wide mark for every site on
	// the box — an operator who uploaded a client's logo rebranded their own
	// studio, and nothing said so.
	if err := a.siteSettings.SetMany(r.Context(), osScope(r), map[string]string{
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
		// A HOSTED DOMAIN SERVING ITS OWN BUNDLE MUST NOT WEAR THE PRIMARY'S MARK.
		//
		// Reported from a live install: a client domain published a hand-built
		// site and the browser tab showed the studio's logo. The bundle carried no
		// favicon, the browser asked this origin for /favicon.ico, and the route
		// answered with the primary's brand for every host on the box. Nothing was
		// misconfigured — the isolation simply stopped one route short of the
		// thing a visitor actually looks at.
		//
		// When a bundle is deployed the operator authored the whole site, so its
		// icon — or its deliberate absence — is authoritative. A miss returns 404
		// rather than falling through: an empty tab icon is a small cosmetic gap,
		// and another business's logo on a client's domain is not.
		// Resolved from what the bundle DECLARES, not from three guessed root
		// names. Guessing was wrong about the bundles this project itself builds:
		// the marketing site declares assets/favicon-32.png and carries nothing at
		// its root, so /favicon.ico on that domain answered 404 while the site had
		// an icon all along and every page referenced it.
		if dir := a.customSiteDir(r); customsite.Deployed(dir) {
			if p, has := customsite.IconPath(dir); has && customsite.Serve(w, r, dir, p) {
				return
			}
			http.NotFound(w, r)
			return
		}
		// The same reasoning one step further out: a hosted domain that has its
		// own mark wears it. Only when it has none does the primary's stand in —
		// that fallback is left alone deliberately, because removing it would
		// blank the tab icon on every domain that has never uploaded one.
		if a.siteSettings != nil {
			if b, ct, ok := a.brandMark(r.Context(), settings.ForDomain(a.contentScope(r))); ok {
				serveFaviconBytes(w, r, b, ct)
				return
			}
			if b, ct, ok := a.brandMark(r.Context(), settings.ForPrimary()); ok {
				serveFaviconBytes(w, r, b, ct)
				return
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

// brandMark returns the mark stored for one scope.
//
// The Valid() check is a SECOND line, not the one holding the boundary, and it
// is worth being accurate about which is which: settings.Store.Get already
// returns the product default for an unset scope and refuses to fall through to
// the primary's stored value, with a comment recording that inheritance as the
// defect it was. This guard exists so that a reader of THIS function does not
// have to know that, and so a future default for brand.favicon_type — today an
// empty string — could not turn "no scope" into "has a mark".
func (a *App) brandMark(ctx context.Context, scope settings.Scope) ([]byte, string, bool) {
	if a.siteSettings == nil || !scope.Valid() {
		return nil, "", false
	}
	enc := a.siteSettings.Get(ctx, scope, settings.KeyBrandFavicon)
	if enc == "" {
		return nil, "", false
	}
	b, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(b) == 0 {
		return nil, "", false
	}
	ct := a.siteSettings.Get(ctx, scope, settings.KeyBrandFaviconType)
	if ct == "" {
		ct = "image/png"
	}
	return b, ct, true
}

// siteBundleDir returns a hosted domain's deployed bundle directory, and false
// when it has none.
//
// The guard against customSiteRoot() is the load-bearing line. customSiteDirFor
// falls back to the PRIMARY's directory for a blank or non-hex id — a deliberate
// choice there, where "a wrong site rather than an escape" is the right trade for
// a serving path. Here it would be the operator's own bundle icon appearing on a
// client's card, which is the exact confusion this whole change exists to remove.
func siteBundleDir(id string) (string, bool) {
	dir := customSiteDirFor(id)
	if dir == customSiteRoot() {
		return "", false
	}
	if !customsite.Deployed(dir) {
		return "", false
	}
	return dir, true
}

// siteHasOwnMark reports whether a hosted site has a logo of its OWN — either
// uploaded through its branding page, or shipped inside its deployed bundle.
//
// The bundle half was missing when this shipped, and the omission was visible on
// the operator's own install: three sites served hand-built bundles carrying
// their own favicon, the public path had preferred that icon for a year, and the
// console still drew a generic globe for all three. The data existed; only this
// question failed to ask for it.
func (a *App) siteHasOwnMark(ctx context.Context, id string) bool {
	if a.hasBrandMark(ctx, settings.ForDomain(id)) {
		return true
	}
	dir, ok := siteBundleDir(id)
	if !ok {
		return false
	}
	_, has := customsite.IconPath(dir)
	return has
}

// hasBrandMark reports whether a scope has a mark WITHOUT decoding it.
//
// It reads the type key, which is a short MIME string, rather than the image.
// The Optimize page asks this once per hosted site on every render, and reading
// a megabyte of base64 eleven times to decide whether to draw an icon is the
// kind of cost that only shows up on the install with the most sites.
func (a *App) hasBrandMark(ctx context.Context, scope settings.Scope) bool {
	if a.siteSettings == nil || !scope.Valid() {
		return false
	}
	return a.siteSettings.Get(ctx, scope, settings.KeyBrandFaviconType) != ""
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

// handleOSScopedBrandMark serves one hosted domain's own mark to the console.
//
// The console runs on the operator's host, so it cannot fetch a client domain's
// /favicon.ico — that is a different origin, and on a Tor install reaching for it
// would be a clearnet callback from a page that must not make one. This route
// answers from the database instead, over the connection the operator is already
// on.
//
// 404 rather than the primary's mark when this domain has none. The panel draws
// its neutral globe in that case, and a card showing the studio's logo above a
// client's hostname is the confusion this whole change exists to remove.
func (a *App) handleOSScopedBrandMark(w http.ResponseWriter, r *http.Request) {
	d, ok := osScopedDomain(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if b, ct, ok := a.brandMark(r.Context(), settings.ForDomain(d.ID)); ok {
		serveFaviconBytes(w, r, b, ct)
		return
	}
	// Then the icon the site's own bundle declares. An uploaded mark wins because
	// it is the more recent, more deliberate statement of what this logo is.
	if dir, ok := siteBundleDir(d.ID); ok {
		if p, has := customsite.IconPath(dir); has && customsite.Serve(w, r, dir, p) {
			return
		}
	}
	http.NotFound(w, r)
}
