// Command vayudocs renders the VayuPress documentation tree (guides, the security
// model, operations runbooks and every ADR) to static HTML for the GitHub Pages
// marketing site, so vayupress.com/docs serves the same docs the running binary
// serves at <site>/docs.
//
// It deliberately mirrors cmd/vayupress/handlers_docs.go: the same goldmark +
// bluemonday pipeline, the same DOM (doc-shell / doc-side / doc-prose / doc-card)
// and the same static/css/docs.css — so the Pages docs and the in-binary docs
// look identical. It is cgo-free (no SQLite import), so the Pages workflow builds
// it in seconds without a full engine build.
//
// Usage: vayudocs -src docs -css static/css/docs.css -out _site/docs
package main

import (
	"flag"
	"fmt"
	"html"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
)

// skipDirs are the image-heavy documentation folders that carry no prose — kept
// out of the docs site exactly as they are kept out of the binary's embed.
var skipDirs = map[string]bool{"screenshots": true, "assets": true, "site": true}

type docEntry struct {
	Slug  string // URL under /docs (no .md): "OPERATIONS", "security/trust-model", "adr/ADR-0001-sqlite-first"
	Title string
	Group string
	File  string // absolute source path
	adr   bool
}

type docGroup struct {
	Name    string
	Entries []*docEntry
}

var (
	gm = goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.Table),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)
	ugc = bluemonday.UGCPolicy()
)

func main() {
	src := flag.String("src", "docs", "documentation source directory")
	out := flag.String("out", "_site/docs", "output directory for the rendered docs site")
	css := flag.String("css", "static/css/docs.css", "path to docs.css to copy into the output")
	flag.Parse()

	groups, adrs, err := index(*src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "vayudocs:", err)
		os.Exit(1)
	}
	if err := generate(*out, *css, groups, adrs); err != nil {
		fmt.Fprintln(os.Stderr, "vayudocs:", err)
		os.Exit(1)
	}
	n := len(adrs)
	for _, g := range groups {
		n += len(g.Entries)
	}
	fmt.Printf("vayudocs: rendered %d pages into %s\n", n, *out)
}

// index walks the source tree and returns the ordered non-ADR groups plus the
// ADR list (newest first).
func index(src string) ([]docGroup, []*docEntry, error) {
	byGroup := map[string][]*docEntry{}
	var adrs []*docEntry

	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(src, p)
		rel = filepath.ToSlash(rel)
		if strings.EqualFold(rel, "adr/INDEX.md") {
			return nil
		}
		slug := strings.TrimSuffix(rel, ".md")
		e := &docEntry{Slug: slug, File: p}
		switch {
		case strings.HasPrefix(slug, "adr/"):
			e.Group = "Architecture Decisions"
			e.adr = true
			e.Title = adrTitle(strings.TrimPrefix(slug, "adr/"))
			adrs = append(adrs, e)
		default:
			e.Group = "Guides"
			if i := strings.IndexByte(slug, '/'); i >= 0 {
				e.Group = sectionTitle(slug[:i])
			}
			e.Title = firstHeading(p, humanize(path.Base(slug)))
			byGroup[e.Group] = append(byGroup[e.Group], e)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(adrs, func(i, j int) bool { return adrs[i].Slug > adrs[j].Slug })

	var groups []docGroup
	for _, name := range groupOrder(byGroup) {
		es := byGroup[name]
		sort.Slice(es, func(i, j int) bool { return es[i].Title < es[j].Title })
		groups = append(groups, docGroup{Name: name, Entries: es})
	}
	return groups, adrs, nil
}

func generate(outDir, cssPath string, groups []docGroup, adrs []*docEntry) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	// Copy the shared stylesheet so /docs/docs.css serves it (single style source).
	cssBytes, err := os.ReadFile(cssPath)
	if err != nil {
		return fmt.Errorf("read css %s: %w", cssPath, err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "docs.css"), cssBytes, 0o644); err != nil {
		return err
	}

	sidebar := sidebarHTML(groups, len(adrs), "")

	// Index landing.
	var idx strings.Builder
	idx.WriteString(`<div class="doc-intro"><h1>VayuPress Documentation</h1><p>Every guide, operations runbook, security note and architecture decision that ships with VayuPress — hosted here for public audit.</p></div>`)
	for _, g := range groups {
		idx.WriteString(`<section class="doc-section"><h2>` + html.EscapeString(g.Name) + `</h2><div class="doc-cards">`)
		for _, e := range g.Entries {
			idx.WriteString(card(e))
		}
		idx.WriteString(`</div></section>`)
	}
	idx.WriteString(`<section class="doc-section"><h2>Architecture Decisions</h2><div class="doc-cards"><a class="doc-card" href="/docs/adr/"><b>ADR Registry</b><span>` + itoa(len(adrs)) + ` decision records</span></a></div></section>`)
	if err := writePage(outDir, "", "VayuPress Documentation", sidebar, idx.String()); err != nil {
		return err
	}

	// ADR list.
	var al strings.Builder
	al.WriteString(`<div class="doc-crumb"><a href="/docs/">Docs</a> / Architecture Decisions</div>`)
	al.WriteString(`<div class="doc-intro"><h1>Architecture Decision Records</h1><p>` + itoa(len(adrs)) + ` records that document why VayuPress is built the way it is — newest first.</p></div><div class="doc-cards">`)
	for _, e := range adrs {
		al.WriteString(card(e))
	}
	al.WriteString(`</div>`)
	if err := writePage(outDir, "adr", "Architecture Decisions", sidebarHTML(groups, len(adrs), "adr"), al.String()); err != nil {
		return err
	}

	// Every doc + ADR page.
	all := append([]*docEntry{}, adrs...)
	for _, g := range groups {
		all = append(all, g.Entries...)
	}
	for _, e := range all {
		raw, err := os.ReadFile(e.File)
		if err != nil {
			return err
		}
		crumb := `<div class="doc-crumb"><a href="/docs/">Docs</a>`
		if e.adr {
			crumb += ` / <a href="/docs/adr/">ADRs</a>`
		} else if i := strings.IndexByte(e.Slug, '/'); i >= 0 {
			crumb += ` / ` + html.EscapeString(sectionTitle(e.Slug[:i]))
		}
		crumb += `</div>`
		body := crumb + `<article class="doc-prose">` + renderMarkdown(raw) + `</article>`
		if err := writePage(outDir, e.Slug, e.Title, sidebarHTML(groups, len(adrs), e.Slug), body); err != nil {
			return err
		}
	}
	return nil
}

// writePage renders one page to <outDir>/<slug>/index.html (dir-per-doc so
// GitHub Pages serves clean /docs/<slug>/ URLs). slug "" is the docs root.
func writePage(outDir, slug, title, sidebar, body string) error {
	dir := outDir
	if slug != "" {
		dir = filepath.Join(outDir, filepath.FromSlash(slug))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "index.html"), []byte(shell(title, sidebar, body)), 0o644)
}

func shell(title, sidebar, body string) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">`)
	b.WriteString(`<title>` + html.EscapeString(title) + ` · VayuPress Docs</title>`)
	b.WriteString(`<meta name="robots" content="index,follow">`)
	b.WriteString(`<link rel="stylesheet" href="/docs/docs.css">`)
	b.WriteString(`</head><body class="doc-body">`)
	b.WriteString(`<header class="doc-top"><a class="doc-brand" href="/docs/">Vayu<span>Press</span> Docs</a>`)
	b.WriteString(`<nav><a href="/">&larr; Site</a><a href="/docs/">All docs</a><a href="/docs/adr/">ADRs</a></nav></header>`)
	b.WriteString(`<div class="doc-shell"><aside class="doc-side">` + sidebar + `</aside><main class="doc-main">`)
	b.WriteString(body)
	b.WriteString(`</main></div>`)
	b.WriteString(`<footer class="doc-foot">Rendered from the VayuPress documentation for public audit · <a href="/docs/">Docs home</a> · <a href="https://github.com/johalputt/vayupress">Source</a></footer>`)
	b.WriteString(`</body></html>`)
	return b.String()
}

func sidebarHTML(groups []docGroup, adrCount int, active string) string {
	var b strings.Builder
	for _, g := range groups {
		b.WriteString(`<h4>` + html.EscapeString(g.Name) + `</h4><ul>`)
		for _, e := range g.Entries {
			cls := ""
			if e.Slug == active {
				cls = ` class="is-active"`
			}
			b.WriteString(`<li><a` + cls + ` href="/docs/` + docHref(e.Slug) + `/">` + html.EscapeString(e.Title) + `</a></li>`)
		}
		b.WriteString(`</ul>`)
	}
	adrCls := ""
	if active == "adr" {
		adrCls = ` class="is-active"`
	}
	b.WriteString(`<h4>Decisions</h4><ul><li><a` + adrCls + ` href="/docs/adr/">Architecture Decisions (` + itoa(adrCount) + `)</a></li></ul>`)
	return b.String()
}

func card(e *docEntry) string {
	return `<a class="doc-card" href="/docs/` + docHref(e.Slug) + `/"><b>` + html.EscapeString(e.Title) + `</b><span>/docs/` + html.EscapeString(e.Slug) + `</span></a>`
}

func renderMarkdown(md []byte) string {
	var buf strings.Builder
	if err := gm.Convert(md, &buf); err != nil {
		return "<pre>" + html.EscapeString(string(md)) + "</pre>"
	}
	return ugc.Sanitize(buf.String())
}

// ── small helpers (mirror cmd/vayupress/handlers_docs.go) ────────────────────

func docHref(slug string) string { return slug } // slugs are safe filename chars

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

func firstHeading(file, fallback string) string {
	b, err := os.ReadFile(file)
	if err != nil {
		return fallback
	}
	for _, line := range strings.SplitN(string(b), "\n", 60) {
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

func itoa(n int) string { return fmt.Sprintf("%d", n) }
