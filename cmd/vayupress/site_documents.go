// SPDX-License-Identifier: Apache-2.0

package main

// site_documents.go — serving a template website from its document
// (ADR-0161): the newest published revision when the site has one, otherwise
// its legacy flat content through sitedoc.FromLegacy, so a site nobody has
// touched renders the same text, in the same order, with the same links as it
// did before documents existed.

import (
	"context"
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/johalputt/vayupress/internal/bizsite"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/seo"
	"github.com/johalputt/vayupress/internal/sitedoc"
)

// siteScope is the site a public request is for, as domain_id is stored: ""
// for the primary, the domain id for a hosted domain. It follows the same
// rule as siteSourceFor, so the document and the legacy settings can never
// describe two different sites for one request.
func (a *App) siteScope(r *http.Request) (scope, host string) {
	if a.multiDomain(r) {
		if d, ok := activeDomain(r); ok && !d.IsPrimary {
			return d.ID, d.Host
		}
	}
	return "", strings.TrimSpace(config.Cfg.Domain)
}

// publishedSiteDoc is the newest published revision of a site's document.
func publishedSiteDoc(ctx context.Context, scope string) (sitedoc.Document, bool) {
	if dbpkg.DB == nil {
		return sitedoc.Document{}, false
	}
	var raw string
	if err := dbpkg.Reader().QueryRowContext(ctx,
		`SELECT doc FROM site_revisions WHERE domain_id=? ORDER BY id DESC LIMIT 1`, scope).Scan(&raw); err != nil {
		return sitedoc.Document{}, false
	}
	d, err := sitedoc.Parse([]byte(raw))
	if err != nil {
		// Only this binary writes revisions, through Marshal, so this means the
		// row was altered by hand or a later schema was rolled back. Serving the
		// legacy site keeps the domain up; the log says why it looks older.
		logging.LogError("website", "published site document does not parse; serving the legacy site", err.Error())
		return sitedoc.Document{}, false
	}
	return d, true
}

// siteDocument is what the request's site serves, and the design it wears.
func (a *App) siteDocument(r *http.Request) (mode string, tpl bizsite.Template, doc sitedoc.Document) {
	mode, tpl, content := a.bizSettings(r)
	scope, _ := a.siteScope(r)
	if d, ok := publishedSiteDoc(r.Context(), scope); ok {
		return mode, tpl, d
	}
	return mode, tpl, sitedoc.FromLegacy(tpl, content)
}

// renderSitePage writes the page at slug of the request's site, reporting
// false when the site has no such page.
func (a *App) renderSitePage(w http.ResponseWriter, r *http.Request, slug string) bool {
	mode, tpl, doc := a.siteDocument(r)
	o := a.siteRenderOptions(r, mode, tpl)
	o.Stylesheet = "/site.css?v=" + siteCSSVersion(o.Template, doc)
	o.ContactScript = string(render.ContactJSLink())
	return writeSitePage(w, doc, slug, o)
}

// siteRenderOptions is how the request's site is dressed and addressed.
func (a *App) siteRenderOptions(r *http.Request, mode string, tpl bizsite.Template) sitedoc.Options {
	// Preview: the console's design picker shows a design selected but not
	// yet saved. Unknown keys are ignored.
	if pv, ok := previewTemplate(r); ok {
		tpl = pv
	}
	o := sitedoc.Options{Template: tpl, BlogURL: bizBlogURL(mode), Icon: "/favicon.ico"}
	// From the registered domain, never the request's Host header: a canonical
	// URL a client can choose is one a client can point at somebody else.
	if _, host := a.siteScope(r); host != "" {
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		o.Origin = seo.Origin(host)
	}
	return o
}

func writeSitePage(w http.ResponseWriter, doc sitedoc.Document, slug string, o sitedoc.Options) bool {
	p, ok := doc.Page(slug)
	if !ok {
		return false
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(sitedoc.Render(doc, p, o)))
	return true
}

// sitePageSlug is the document page a path names: one lowercase segment, as
// sitedoc validates slugs.
var sitePageSlug = regexp.MustCompile(`^/([a-z0-9][a-z0-9-]{0,62})/?$`)

// serveSitePageIfActive serves a document page for a path no post or route
// claimed, when the request's site serves a template website at its root.
func (a *App) serveSitePageIfActive(w http.ResponseWriter, r *http.Request) bool {
	m := sitePageSlug.FindStringSubmatch(r.URL.Path)
	if m == nil || !a.bizRootActive(r) {
		return false
	}
	return a.renderSitePage(w, r, m[1])
}

// handleSiteGalleryJS serves the gallery viewer (sitedoc.GalleryJS). Its URL
// carries a hash of its content, so it can be cached for a long time.
func handleSiteGalleryJS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write([]byte(sitedoc.GalleryJS))
}
