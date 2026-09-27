// SPDX-License-Identifier: Apache-2.0

package docsite

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

// siteDir holds what the site itself says, beside the documents: its pages,
// its navigation, its pictures and the files it serves as they are.
const siteDir = "docs/site"

// skipDirs are the folders under docs/ that hold no documents of their own.
var skipDirs = map[string]bool{"docs/screenshots": true, "docs/assets": true, siteDir: true}

// doc is one document with a page.
type doc struct {
	Slug  string // under /docs/: "OPERATIONS", "security/trust-model", "adr/ADR-0164-…"
	Src   string // repository path
	Group string
	R     rendered
	ADR   *adr
}

// Href is the document's address on the site.
func (d *doc) Href() string { return "/docs/" + d.Slug + "/" }

// adr is what the register says of one decision.
type adr struct {
	Num     string // "ADR-0164"
	Summary string // the register's one-line account of it
	Status  string
	Owner   string
	Filter  string // the owner chip it answers to: its owner, or "Other"
	Date    time.Time
	New     bool // decided in the week before the sync
}

// release is one version in the changelog.
type release struct {
	Version string
	Date    time.Time
	Summary string
	Body    []byte // its Markdown, heading excluded
}

// Slug is the release's address under /changes/.
func (r release) Slug() string { return strings.NewReplacer("/", "-", " ", "-").Replace(r.Version) }

// navGroup is one heading of the docs sidebar.
type navGroup struct {
	Group string   `json:"group"`
	Docs  []string `json:"docs"`
}

// navFile is docs/site/nav.json: the sidebar's headings in reading order, and
// documents kept out of it (a template, say), which still have their pages.
type navFile struct {
	Groups []navGroup `json:"groups"`
	Hide   []string   `json:"hide"`
}

// loadDocs reads every document under docs/ with the linker, so the links
// between them resolve to pages.
func loadDocs(repo fs.FS, l *linker) ([]*doc, error) {
	var srcs []string
	err := fs.WalkDir(repo, "docs", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && skipDirs[p]:
			return fs.SkipDir
		case d.IsDir() || !strings.HasSuffix(p, ".md") || p == "docs/adr/INDEX.md":
			return nil
		}
		srcs = append(srcs, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, p := range srcs {
		l.docs[strings.TrimSuffix(strings.TrimPrefix(p, "docs/"), ".md")] = true
	}
	docs := make([]*doc, 0, len(srcs))
	for _, p := range srcs {
		md, err := fs.ReadFile(repo, p)
		if err != nil {
			return nil, err
		}
		d := &doc{Slug: strings.TrimSuffix(strings.TrimPrefix(p, "docs/"), ".md"), Src: p, R: l.renderDoc(p, md)}
		d.R.Title = guideTitle(d.R.Title, d.Slug)
		docs = append(docs, d)
	}
	return docs, nil
}

// guideTitle drops the product's name from a guide's title where the heading
// carries it for a reader on GitHub ("VayuPress Installation Guide", "Trust
// Model — VayuPress"): on vayupress.com every page is VayuPress's.
func guideTitle(title, slug string) string {
	t := strings.TrimSpace(title)
	if i := strings.Index(t, " — VayuPress"); i > 0 {
		t = t[:i]
	}
	t = strings.TrimPrefix(t, "VayuPress — ")
	if rest := strings.TrimPrefix(t, "VayuPress "); rest != t && rest != "" {
		t = strings.ToUpper(rest[:1]) + rest[1:]
	}
	if t == "" {
		t = strings.ReplaceAll(path.Base(slug), "-", " ")
	}
	return t
}

// indexRow is one row of an ADR table: | [ADR-0164](file.md) | summary | status | owner | date |
var indexRow = regexp.MustCompile(`^\|\s*\[(ADR-\d+)\]\(([^)]+)\)\s*\|\s*(.*?)\s*\|\s*(.*?)\s*\|\s*(.*?)\s*\|\s*(.*?)\s*\|\s*$`)

// headerField reads an ADR's own header for a field its register row lacks:
// "- **Status:** Accepted" or "**Date**: 2024-01-01".
var headerField = regexp.MustCompile(`(?m)^[-*\s]*\*\*(Status|Date):?\*\*:?\s*(.+?)\s*$`)

// attachADRs gives each decision its register row: docs/adr/INDEX.md first,
// the decision's own header for anything the register does not say.
func attachADRs(repo fs.FS, docs []*doc, synced time.Time) {
	rows := map[string][]string{}
	if idx, err := fs.ReadFile(repo, "docs/adr/INDEX.md"); err == nil {
		sc := bufio.NewScanner(bytes.NewReader(idx))
		for sc.Scan() {
			if m := indexRow.FindStringSubmatch(sc.Text()); m != nil {
				rows[path.Base(m[2])] = m[3:]
			}
		}
	}
	for _, d := range docs {
		if !strings.HasPrefix(d.Slug, "adr/") {
			continue
		}
		base := path.Base(d.Src)
		a := &adr{Num: adrNum(base)}
		if r, ok := rows[base]; ok {
			a.Summary, a.Status, a.Owner = r[0], r[1], r[2]
			a.Date, _ = time.Parse("2006-01-02", r[3])
		}
		if md, err := fs.ReadFile(repo, d.Src); err == nil && (a.Status == "" || a.Date.IsZero()) {
			for _, m := range headerField.FindAllStringSubmatch(string(md), 4) {
				switch v := strings.Trim(m[2], "* "); {
				case m[1] == "Status" && a.Status == "":
					a.Status = v
				case m[1] == "Date" && a.Date.IsZero():
					a.Date, _ = time.Parse("2006-01-02", v)
				}
			}
		}
		a.New = !a.Date.IsZero() && synced.Sub(a.Date) < 7*24*time.Hour
		d.ADR, d.Group = a, "Decisions"
		// "ADR-0164 — Outside services…" and "ADR-0001: SQLite…" both read as
		// the words after the number.
		d.R.Title = strings.TrimLeft(strings.TrimPrefix(d.R.Title, a.Num), " —-:·")
	}
}

func adrNum(base string) string {
	parts := strings.SplitN(base, "-", 3)
	if len(parts) >= 2 {
		return parts[0] + "-" + parts[1]
	}
	return strings.TrimSuffix(base, ".md")
}

// groupDocs places the guides under the sidebar's headings. A guide nav.json
// does not name still gets its page, under "More", so a new document is on the
// site the moment it lands rather than when someone remembers the navigation.
func groupDocs(repo fs.FS, docs []*doc) []navGroup {
	var nav navFile
	if b, err := fs.ReadFile(repo, siteDir+"/nav.json"); err == nil {
		_ = json.Unmarshal(b, &nav)
	}
	bySlug := map[string]*doc{}
	for _, d := range docs {
		bySlug[d.Slug] = d
	}
	for _, slug := range nav.Hide {
		if d, ok := bySlug[slug]; ok && d.Group == "" {
			d.Group = "Hidden"
		}
	}
	var out []navGroup
	for _, g := range nav.Groups {
		var kept []string
		for _, s := range g.Docs {
			if d, ok := bySlug[s]; ok && d.ADR == nil && d.Group == "" {
				d.Group = g.Group
				kept = append(kept, s)
			}
		}
		if len(kept) > 0 {
			out = append(out, navGroup{Group: g.Group, Docs: kept})
		}
	}
	var more []string
	for _, d := range docs {
		if d.Group == "" {
			d.Group = "More"
			more = append(more, d.Slug)
		}
	}
	sort.Slice(more, func(i, j int) bool { return bySlug[more[i]].R.Title < bySlug[more[j]].R.Title })
	if len(more) > 0 {
		out = append(out, navGroup{Group: "More", Docs: more})
	}
	return out
}

// releaseHeading is a version's heading: "## [3.17.84] — 2026-09-27".
var releaseHeading = regexp.MustCompile(`^## \[([^\]]+)\]\s*(?:[—–-]\s*(\d{4}-\d{2}-\d{2}))?`)

// parseChangelog splits CHANGELOG.md into its releases, newest first, leaving
// out the unreleased section: the site says what has shipped.
func parseChangelog(md []byte) []release {
	var out []release
	var cur *release
	var body bytes.Buffer
	flush := func() {
		if cur != nil {
			cur.Body = bytes.TrimSpace(append([]byte(nil), body.Bytes()...))
			cur.Summary = firstParagraph(cur.Body)
			out = append(out, *cur)
		}
		body.Reset()
	}
	for _, line := range strings.SplitAfter(string(md), "\n") {
		if m := releaseHeading.FindStringSubmatch(line); m != nil {
			flush()
			cur = nil
			if !strings.EqualFold(m[1], "Unreleased") {
				d, _ := time.Parse("2006-01-02", m[2])
				cur = &release{Version: m[1], Date: d}
			}
			continue
		}
		if cur != nil {
			body.WriteString(line)
		}
	}
	flush()
	return out
}

// firstParagraph is a release's own summary, when it opens with one: the
// paragraph before its first subsection.
func firstParagraph(md []byte) string {
	for _, para := range strings.Split(string(md), "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		if strings.HasPrefix(para, "#") || strings.HasPrefix(para, "-") || strings.HasPrefix(para, "|") {
			return ""
		}
		return strings.Join(strings.Fields(para), " ")
	}
	return ""
}

// page is one of the site's own pages: docs/site/pages/<name>.md, with its
// address and description in a front matter block.
type page struct {
	Path        string // where it is served: "/about.html", "/vayumail/privacy/"
	Title       string // the tab's and the search's name for it
	Kicker      string // the line above its heading
	Description string
	R           rendered
}

// loadPages reads the site's own pages. A page may name the project's own
// figures, {releases} and {decisions}, and gets them as the build counted them,
// so a page never goes on quoting a number the project has left behind.
func loadPages(repo fs.FS, l *linker, figures *strings.Replacer) []page {
	entries, err := fs.ReadDir(repo, siteDir+"/pages")
	if err != nil {
		return nil
	}
	var out []page
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		src := siteDir + "/pages/" + e.Name()
		raw, err := fs.ReadFile(repo, src)
		if err != nil {
			continue
		}
		front, body := splitFront(raw)
		p := page{Path: front["path"], Title: front["title"], Kicker: front["kicker"], Description: front["description"],
			R: l.renderDoc(src, []byte(figures.Replace(string(body))))}
		if p.Path == "" {
			p.Path = "/" + strings.TrimSuffix(e.Name(), ".md") + "/"
		}
		if p.Title == "" {
			p.Title = p.R.Title
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// splitFront separates a "---" front matter block of "key: value" lines.
func splitFront(raw []byte) (map[string]string, []byte) {
	front := map[string]string{}
	s := string(raw)
	if !strings.HasPrefix(s, "---\n") {
		return front, raw
	}
	end := strings.Index(s[4:], "\n---\n")
	if end < 0 {
		return front, raw
	}
	for _, line := range strings.Split(s[4:4+end], "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			front[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return front, []byte(s[4+end+5:])
}
