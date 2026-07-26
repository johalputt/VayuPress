// SPDX-License-Identifier: Apache-2.0

package main

// handlers_docs.go — the public documentation site at /docs.
//
// VayuPress's guides, operations runbooks, security model and every Architecture
// Decision Record ship as markdown inside the binary (DocsFS + DocsADRFS) and are
// rendered here for public audit — no docs subdomain, no third-party host. The
// site is read-only and GET-only, and reuses the same sanitising markdown
// renderer as the VayuOS ADR registry (goldmark + bluemonday UGC), so
// operator-authored docs can never turn a page into an injection surface.
//
// Path handling is allowlist-based: the index is built once from the files
// actually embedded at build time, and a request can only ever resolve to one of
// those exact files — no caller-supplied path can escape the docs tree.

import (
	"html"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	rootassets "github.com/johalputt/vayupress"
)

// docsContentFS re-roots DocsFS so members are web-relative ("OPERATIONS.md",
// "security/trust-model.md") instead of prefixed with "docs/".
var docsContentFS = func() fs.FS {
	if sub, err := fs.Sub(rootassets.DocsFS, "docs"); err == nil {
		return sub
	}
	return rootassets.DocsFS
}()

type docEntry struct {
	Slug  string // URL under /docs (no .md): "OPERATIONS", "security/trust-model", "adr/ADR-0001-sqlite-first"
	Title string
	Group string
	adr   bool
}

func (e *docEntry) read() ([]byte, error) {
	if e.adr {
		return fs.ReadFile(embeddedADRFS, strings.TrimPrefix(e.Slug, "adr/")+".md")
	}
	return fs.ReadFile(docsContentFS, e.Slug+".md")
}

var (
	docsOnce    sync.Once
	docsBySlug  map[string]*docEntry
	docsByLower map[string]*docEntry
	docGroups   []docGroup
	docADRs     []*docEntry
)

type docGroup struct {
	Name    string
	Entries []*docEntry
}

// aliasSection maps the first path segment of the legacy documentation anchors
// still referenced by API error hints (e.g. /docs/operations/replay,
// /docs/api/errors, /docs/governance/budgets) to the real guide that covers them,
// so every hint link lands on a live page even though those deep paths have no
// dedicated file.
var aliasSection = map[string]string{
	"operations": "OPERATIONS",
	"api":        "API-REFERENCE",
	"governance": "CI-GOVERNANCE",
}

// buildDocsIndex walks the embedded docs once and indexes every markdown file.
func buildDocsIndex() {
	docsBySlug = map[string]*docEntry{}
	docsByLower = map[string]*docEntry{}
	groups := map[string][]*docEntry{}

	_ = fs.WalkDir(docsContentFS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		slug := strings.TrimSuffix(p, ".md")
		group := "Guides"
		if i := strings.IndexByte(slug, '/'); i >= 0 {
			group = sectionTitle(slug[:i])
		}
		e := &docEntry{Slug: slug, Title: docTitle(docsContentFS, p, humanize(path.Base(slug))), Group: group}
		registerDoc(e)
		groups[group] = append(groups[group], e)
		return nil
	})

	if entries, err := fs.ReadDir(embeddedADRFS, "."); err == nil {
		for _, ent := range entries {
			n := ent.Name()
			if ent.IsDir() || !strings.HasSuffix(n, ".md") || strings.EqualFold(n, "INDEX.md") {
				continue
			}
			base := strings.TrimSuffix(n, ".md")
			e := &docEntry{Slug: "adr/" + base, Title: adrTitle(base), Group: "Architecture Decisions", adr: true}
			registerDoc(e)
			docADRs = append(docADRs, e)
		}
		sort.Slice(docADRs, func(i, j int) bool { return docADRs[i].Slug > docADRs[j].Slug }) // newest first
	}

	for _, name := range groupOrder(groups) {
		es := groups[name]
		sort.Slice(es, func(i, j int) bool { return es[i].Title < es[j].Title })
		docGroups = append(docGroups, docGroup{Name: name, Entries: es})
	}
}

func registerDoc(e *docEntry) {
	docsBySlug[e.Slug] = e
	docsByLower[strings.ToLower(e.Slug)] = e
}

// resolveDoc maps a cleaned request path to an embedded doc: exact match, then
// case-insensitive, then the legacy-anchor alias by first segment.
func resolveDoc(p string) (*docEntry, bool) {
	if e, ok := docsBySlug[p]; ok {
		return e, true
	}
	if e, ok := docsByLower[strings.ToLower(p)]; ok {
		return e, true
	}
	seg := p
	if i := strings.IndexByte(p, '/'); i >= 0 {
		seg = p[:i]
	}
	if target, ok := aliasSection[strings.ToLower(seg)]; ok {
		if e, ok := docsBySlug[target]; ok {
			return e, true
		}
	}
	return nil, false
}

// handleDocs serves the public documentation site. Admin-free, GET-only.
func (a *App) handleDocs(w http.ResponseWriter, r *http.Request) {
	docsOnce.Do(buildDocsIndex)
	p := strings.Trim(path.Clean("/"+chi.URLParam(r, "*")), "/")
	if p == "." {
		p = ""
	}
	switch {
	case p == "":
		a.renderDocsIndex(w, r)
	case p == "adr":
		a.renderDocsADRList(w, r)
	default:
		e, ok := resolveDoc(p)
		if !ok || strings.Contains(p, "..") {
			a.renderDocsNotFound(w, r, p)
			return
		}
		raw, err := e.read()
		if err != nil {
			a.renderDocsNotFound(w, r, p)
			return
		}
		crumb := `<div class="doc-crumb"><a href="/docs">Docs</a>`
		switch {
		case e.adr:
			crumb += ` / <a href="/docs/adr">ADRs</a>`
		default:
			if i := strings.IndexByte(e.Slug, '/'); i >= 0 {
				crumb += ` / ` + html.EscapeString(sectionTitle(e.Slug[:i]))
			}
		}
		crumb += `</div>`
		body := crumb + `<article class="doc-prose">` + string(renderMarkdownDocument(raw)) + `</article>`
		a.writeDocsHTML(w, r, http.StatusOK, e.Title, e.Slug, body)
	}
}

func (a *App) renderDocsIndex(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	b.WriteString(`<div class="doc-intro"><h1>VayuPress Documentation</h1><p>Every guide, operations runbook, security note and architecture decision that ships with this build — hosted here for public audit. No subdomain and no third-party host: these pages are rendered from markdown embedded in the running binary.</p></div>`)
	for _, g := range docGroups {
		b.WriteString(`<section class="doc-section"><h2>` + html.EscapeString(g.Name) + `</h2><div class="doc-cards">`)
		for _, e := range g.Entries {
			b.WriteString(`<a class="doc-card" href="/docs/` + docHref(e.Slug) + `"><b>` + html.EscapeString(e.Title) + `</b><span>/docs/` + html.EscapeString(e.Slug) + `</span></a>`)
		}
		b.WriteString(`</div></section>`)
	}
	b.WriteString(`<section class="doc-section"><h2>Architecture Decisions</h2><div class="doc-cards"><a class="doc-card" href="/docs/adr"><b>ADR Registry</b><span>` + strconv.Itoa(len(docADRs)) + ` decision records</span></a></div></section>`)
	a.writeDocsHTML(w, r, http.StatusOK, "VayuPress Documentation", "", b.String())
}

func (a *App) renderDocsADRList(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	b.WriteString(`<div class="doc-crumb"><a href="/docs">Docs</a> / Architecture Decisions</div>`)
	b.WriteString(`<div class="doc-intro"><h1>Architecture Decision Records</h1><p>` + strconv.Itoa(len(docADRs)) + ` records that document why VayuPress is built the way it is — newest first.</p></div>`)
	b.WriteString(`<div class="doc-cards">`)
	for _, e := range docADRs {
		b.WriteString(`<a class="doc-card" href="/docs/` + docHref(e.Slug) + `"><b>` + html.EscapeString(e.Title) + `</b><span>` + html.EscapeString(strings.TrimPrefix(e.Slug, "adr/")) + `</span></a>`)
	}
	b.WriteString(`</div>`)
	a.writeDocsHTML(w, r, http.StatusOK, "Architecture Decisions", "adr", b.String())
}

func (a *App) renderDocsNotFound(w http.ResponseWriter, r *http.Request, p string) {
	body := `<div class="doc-crumb"><a href="/docs">Docs</a> / Not found</div>` +
		`<div class="doc-intro"><h1>Page not found</h1><p>No document at <code>/docs/` + html.EscapeString(p) + `</code>. Browse everything below or from the sidebar.</p></div>` +
		`<p><a class="doc-card" href="/docs">← All documentation</a></p>`
	a.writeDocsHTML(w, r, http.StatusNotFound, "Not found", "", body)
}

// writeDocsHTML wraps a body fragment in the docs shell and writes it.
func (a *App) writeDocsHTML(w http.ResponseWriter, r *http.Request, status int, title, active, body string) {
	docsOnce.Do(buildDocsIndex)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, docsShell(title, active, body))
}

func docsShell(title, active, body string) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">`)
	b.WriteString(`<title>` + html.EscapeString(title) + ` · VayuPress Docs</title>`)
	b.WriteString(`<meta name="robots" content="index,follow">`)
	b.WriteString(`<link rel="stylesheet" href="/theme.css">`)
	b.WriteString(`<link rel="stylesheet" href="/static/css/docs.css?v=` + assetVer("css/docs.css") + `">`)
	b.WriteString(`</head><body class="doc-body">`)
	b.WriteString(`<header class="doc-top"><a class="doc-brand" href="/docs">Vayu<span>Press</span> Docs</a>`)
	b.WriteString(`<nav><a href="/">&larr; Site</a><a href="/docs">All docs</a><a href="/docs/adr">ADRs</a></nav></header>`)
	b.WriteString(`<div class="doc-shell"><aside class="doc-side">` + docsSidebar(active) + `</aside><main class="doc-main">`)
	b.WriteString(body)
	b.WriteString(`</main></div>`)
	b.WriteString(`<footer class="doc-foot">Rendered from markdown shipped inside the VayuPress binary for public audit · <a href="/docs">Docs home</a> · <a href="https://github.com/johalputt/vayupress">Source</a></footer>`)
	b.WriteString(`</body></html>`)
	return b.String()
}

func docsSidebar(active string) string {
	var b strings.Builder
	for _, g := range docGroups {
		b.WriteString(`<h4>` + html.EscapeString(g.Name) + `</h4><ul>`)
		for _, e := range g.Entries {
			cls := ""
			if e.Slug == active {
				cls = ` class="is-active"`
			}
			b.WriteString(`<li><a` + cls + ` href="/docs/` + docHref(e.Slug) + `">` + html.EscapeString(e.Title) + `</a></li>`)
		}
		b.WriteString(`</ul>`)
	}
	adrCls := ""
	if active == "adr" {
		adrCls = ` class="is-active"`
	}
	b.WriteString(`<h4>Decisions</h4><ul><li><a` + adrCls + ` href="/docs/adr">Architecture Decisions (` + strconv.Itoa(len(docADRs)) + `)</a></li></ul>`)
	return b.String()
}

// ── small helpers ──────────────────────────────────────────────────────────

func docHref(slug string) string {
	segs := strings.Split(slug, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

func sectionTitle(s string) string {
	s = strings.ReplaceAll(s, "-", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func humanize(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "-", " "), "_", " ")
}

func adrTitle(base string) string {
	parts := strings.SplitN(base, "-", 3)
	if len(parts) >= 3 && strings.EqualFold(parts[0], "ADR") {
		return parts[0] + "-" + parts[1] + " · " + strings.ReplaceAll(parts[2], "-", " ")
	}
	return strings.ReplaceAll(base, "-", " ")
}

// docTitle returns the first H1 in the file, falling back to a humanised name.
// It scans only the head of the file so building the index stays cheap.
func docTitle(fsys fs.FS, name, fallback string) string {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return fallback
	}
	lines := strings.SplitN(string(b), "\n", 60)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return fallback
}

func groupOrder(groups map[string][]*docEntry) []string {
	pref := []string{"Guides", "Architecture", "Security", "Operations", "Governance", "Compatibility", "Reliability", "Release", "Plugins"}
	seen := map[string]bool{}
	out := make([]string, 0, len(groups))
	for _, g := range pref {
		if _, ok := groups[g]; ok {
			out = append(out, g)
			seen[g] = true
		}
	}
	rest := make([]string, 0, len(groups))
	for g := range groups {
		if !seen[g] {
			rest = append(rest, g)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}
