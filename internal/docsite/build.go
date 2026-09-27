// SPDX-License-Identifier: Apache-2.0

package docsite

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/customsite"
)

//go:embed templates/*.html assets/site.css assets/site.js assets/updates.js
var design embed.FS

// themeDir is where a repository keeps a design of its own.
const themeDir = siteDir + "/theme"

// theme is the design a build draws with: the repository's own when it has
// one, else the one compiled in. A repository's theme replaces the built-in
// design whole (templates/*.html, assets/site.css, assets/site.js and, for the
// mirror's page, assets/updates.js) rather than file by file, so what renders
// is never half of one design and half of another, and a file it lacks is an
// error naming that file rather than a silent fall back.
//
// This is what lets the design change with a push, like the words: a new look
// for the site is a commit the install builds, not a release of the binary.
func theme(repo fs.FS) fs.FS {
	if _, err := fs.Stat(repo, themeDir); err == nil {
		if sub, err := fs.Sub(repo, themeDir); err == nil {
			return sub
		}
	}
	return design
}

// view is what every template reads: the site, and the page being drawn.
type view struct {
	*site
	Title       string
	Description string
	Path        string // the page's own address
	Section     string // which part of the bar is lit: product, docs, decisions, changes
	Doc         *doc
	Page        *page
	Release     *release
	ReleaseBody template.HTML
	ReleaseTOC  []heading
}

// site is what every page shares.
type site struct {
	In         Input
	CSS, JS    string // asset addresses, named by their content
	UpdatesJS  string
	Home       string // the product site's origin; empty on the product site itself
	Download   string // where Download goes
	Docs       []*doc
	Nav        []navView
	ADRs       []*doc // newest first
	Owners     []owner
	Releases   []release
	Latest     *release
	Feed       []feedRow
	Products   []product
	Searchable int
	Hero       heroShots
	// Pages is the site's own pages by address, so the footer links only the
	// ones this repository has: a template must not link a page nobody renders.
	Pages map[string]bool
}

type navView struct {
	Group string
	Docs  []*doc
}

type owner struct {
	Name  string
	Count int
}

type feedRow struct {
	When, Title, Note, Kind, Href string
	at                            time.Time
}

type product struct {
	Name, Text, Icon string
	ADR              *doc
}

// heroShots are the console pictures the home page stands on, from
// docs/site/img: each scheme's own, and a phone's.
type heroShots struct {
	Light, Dark, PhoneLight, PhoneDark, SplitLight, SplitDark string
}

// Build renders the whole site.
func Build(in Input) (Bundle, error) {
	if in.Repo == nil {
		return nil, errors.New("docsite: no repository to build from")
	}
	b := Bundle{}
	l := &linker{repo: in.Repo, source: in.Source, commit: in.Commit, docs: map[string]bool{}, files: map[string]bool{}}
	docs, err := loadDocs(in.Repo, l)
	if err != nil {
		return nil, fmt.Errorf("docsite: reading docs/: %w", err)
	}
	if len(docs) == 0 {
		return nil, errors.New("docsite: docs/ holds no documents")
	}
	attachADRs(in.Repo, docs, in.Synced)
	nav := groupDocs(in.Repo, docs)
	s := &site{In: in, Docs: docs}
	bySlug := map[string]*doc{}
	for _, d := range docs {
		bySlug[d.Slug] = d
		if d.ADR != nil {
			s.ADRs = append(s.ADRs, d)
		}
	}
	sort.Slice(s.ADRs, func(i, j int) bool { return s.ADRs[i].ADR.Num > s.ADRs[j].ADR.Num })
	for _, g := range nav {
		v := navView{Group: g.Group}
		for _, slug := range g.Docs {
			v.Docs = append(v.Docs, bySlug[slug])
		}
		s.Nav = append(s.Nav, v)
	}
	s.Owners = owners(s.ADRs)
	if md, err := fs.ReadFile(in.Repo, "CHANGELOG.md"); err == nil {
		s.Releases = parseChangelog(md)
	}
	if len(s.Releases) > 0 {
		s.Latest = &s.Releases[0]
	}
	pages := loadPages(in.Repo, l, strings.NewReplacer("{releases}", num(len(s.Releases)), "{decisions}", num(len(s.ADRs))))
	s.Products = products(bySlug)
	s.Feed = feed(in, s.ADRs, s.Releases)
	s.Searchable = len(docs) + len(s.Releases) + len(pages)
	s.Pages = map[string]bool{}
	for _, p := range pages {
		s.Pages[p.Path] = true
	}

	// Assets first: every page names them by their content.
	th := theme(in.Repo)
	css, js, err := assets(b, in, th)
	if err != nil {
		return nil, err
	}
	s.CSS, s.JS = css, js
	s.Home, s.Download = strings.TrimSuffix(in.Home, "/"), in.Download
	if s.Download == "" {
		s.Download = strings.TrimSuffix(in.Source, "/") + "/releases/latest"
	}

	t, err := template.New("").Funcs(funcs).ParseFS(th, "templates/*.html")
	if err != nil {
		return nil, err
	}
	render := func(file string, v view, tpl string) error {
		v.site = s
		var buf bytes.Buffer
		if err := t.ExecuteTemplate(&buf, tpl, v); err != nil {
			return fmt.Errorf("docsite: %s: %w", file, err)
		}
		b[file] = buf.Bytes()
		return nil
	}
	if in.Site == SiteUpdates {
		if s.Home == "" {
			return nil, errors.New("docsite: the mirror's page needs the product site's address")
		}
		if s.UpdatesJS, err = nameAsset(b, th, "assets/updates.js", ".js"); err != nil {
			return nil, err
		}
		if err := render("index.html", view{Title: "Updates", Description: "Verified VayuPress releases, for installs that cannot reach GitHub.", Path: "/", Section: "download"}, "updates"); err != nil {
			return nil, err
		}
		b["robots.txt"] = []byte("User-agent: *\nAllow: /\n")
		return b, refuseUndeployable(b)
	}
	s.Hero = hero(b, in.Repo)
	desc := "A website, a blog, mail, private chat and a Tor onion, from one binary on one server."
	if err := render("index.html", view{Title: "VayuPress", Description: desc, Path: "/", Section: "product"}, "home"); err != nil {
		return nil, err
	}
	if err := render("docs/index.html", view{Title: "Documentation", Description: "Every guide that ships with VayuPress.", Path: "/docs/", Section: "docs"}, "docs"); err != nil {
		return nil, err
	}
	if err := render("decisions/index.html", view{Title: "Decisions", Description: strconv.Itoa(len(s.ADRs)) + " architecture decision records.", Path: "/decisions/", Section: "decisions"}, "decisions"); err != nil {
		return nil, err
	}
	// The binary serves its register at /docs/adr, and links point there.
	if err := render("docs/adr/index.html", view{Title: "Decisions", Path: "/decisions/"}, "moved"); err != nil {
		return nil, err
	}
	for _, d := range docs {
		section := "docs"
		if d.ADR != nil {
			section = "decisions"
		}
		if err := render("docs/"+d.Slug+"/index.html", view{Title: docTitle(d), Description: d.R.Summary, Path: d.Href(), Section: section, Doc: d}, "doc"); err != nil {
			return nil, err
		}
	}
	if err := render("changes/index.html", view{Title: "Changes", Description: "Every release of VayuPress, newest first.", Path: "/changes/", Section: "changes"}, "changes"); err != nil {
		return nil, err
	}
	for i := range s.Releases {
		r := &s.Releases[i]
		rr := l.renderDoc("CHANGELOG.md", r.Body)
		if err := render("changes/"+r.Slug()+"/index.html", view{Title: r.Version, Description: r.Summary, Path: "/changes/" + r.Slug() + "/",
			Section: "changes", Release: r, ReleaseBody: rr.Body, ReleaseTOC: rr.TOC}, "release"); err != nil {
			return nil, err
		}
	}
	for i := range pages {
		p := &pages[i]
		file := strings.TrimPrefix(p.Path, "/")
		if file == "" || strings.HasSuffix(file, "/") {
			file += "index.html"
		}
		if err := render(file, view{Title: p.Title, Description: p.Description, Path: p.Path, Page: p}, "page"); err != nil {
			return nil, err
		}
	}
	// Files the documents link to, and what the site serves as it is.
	for p := range l.files {
		if data, err := fs.ReadFile(in.Repo, p); err == nil {
			b[p] = data
		}
	}
	if err := verbatim(b, in.Repo); err != nil {
		return nil, err
	}
	b["search.json"], err = searchIndex(s, pages)
	if err != nil {
		return nil, err
	}
	b["sitemap.xml"] = sitemap(b)
	b["robots.txt"] = []byte("User-agent: *\nAllow: /\nSitemap: /sitemap.xml\n")
	return b, refuseUndeployable(b)
}

func docTitle(d *doc) string {
	if d.ADR != nil {
		return d.ADR.Num + " · " + d.R.Title
	}
	return d.R.Title
}

// refuseUndeployable names any file a deploy would not take. A deploy refuses
// the whole bundle over one such file, and the site it was meant to replace
// stays up with nothing said; refused here, by name, the sync reports it.
func refuseUndeployable(b Bundle) error {
	var refused []string
	for name := range b {
		if !customsite.ExtAllowed(name) {
			refused = append(refused, name)
		}
	}
	if len(refused) > 0 {
		sort.Strings(refused)
		return fmt.Errorf("docsite: a bundle cannot carry %s", strings.Join(refused, ", "))
	}
	return nil
}

// owners are the register's filter chips. An owner with a handful of records
// has a chip of its own; the rest, and records the register gives no owner,
// share "Other", so the chips stay one row that says something.
func owners(adrs []*doc) []owner {
	n := map[string]int{}
	for _, d := range adrs {
		n[d.ADR.Owner]++
	}
	other := 0
	var out []owner
	for k, v := range n {
		if k != "" && v >= 3 {
			out = append(out, owner{k, v})
		} else {
			other += v
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	chip := map[string]bool{}
	for _, o := range out {
		chip[o.Name] = true
	}
	for _, d := range adrs {
		d.ADR.Filter = d.ADR.Owner
		if !chip[d.ADR.Owner] {
			d.ADR.Filter = "Other"
		}
	}
	if other > 0 {
		out = append(out, owner{"Other", other})
	}
	return out
}

// products are the six things the home page names, each with the decision
// that defines it. A decision the repository no longer has is left off rather
// than linked to nothing.
func products(bySlug map[string]*doc) []product {
	find := func(num string) *doc {
		for _, d := range bySlug {
			if d.ADR != nil && d.ADR.Num == num {
				return d
			}
		}
		return nil
	}
	return []product{
		{"Websites", "A site is a document of pages and sections, drafted, previewed and published.", "site", find("ADR-0161")},
		{"Blog", "Markdown in, a fast static page out, cached and paced so a crawl never takes it down.", "doc", find("ADR-0001")},
		{"VayuMail", "Your own mail server, with the records it needs checked for you, and any mail app.", "mail", find("ADR-0096")},
		{"VayuTalk", "Private chat on your own domain, shared by a link, with nothing in between.", "talk", find("ADR-0131")},
		{"Tor Space", "An onion address for the same site, with its own world in the console.", "tor", find("ADR-0143")},
		{"VayuShield", "Bots, floods and scrapers turned away before they cost a query, every verdict explained.", "shield", find("ADR-0111")},
	}
}

// feed is the home page's "Live from main": what the syncs brought in, with
// the decisions and releases the repository dates itself, newest first.
func feed(in Input, adrs []*doc, releases []release) []feedRow {
	var rows []feedRow
	for _, u := range in.Updates {
		rows = append(rows, feedRow{Title: u.Title, Note: u.Note, Kind: u.Kind, Href: u.Href, at: u.At, When: when(u.At, in.Synced, true)})
	}
	for _, d := range adrs {
		if !d.ADR.Date.IsZero() {
			rows = append(rows, feedRow{Title: d.ADR.Num, Note: d.R.Title, Kind: "Decision", Href: d.Href(), at: d.ADR.Date, When: when(d.ADR.Date, in.Synced, false)})
		}
	}
	// Several releases can ship in a day; two say the project is moving
	// without crowding out what it decided and wrote.
	shipped := 0
	for _, r := range releases {
		if r.Date.IsZero() || shipped == 2 {
			continue
		}
		shipped++
		note := clip(r.Summary, 72)
		if note == "" {
			note = "Released"
		}
		rows = append(rows, feedRow{Title: r.Version, Note: note, Kind: "Release", Href: "/changes/" + r.Slug() + "/", at: r.Date, When: when(r.Date, in.Synced, false)})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].at.After(rows[j].at) })
	if len(rows) > 6 {
		rows = rows[:6]
	}
	return rows
}

// when names a moment against the sync: "Today 12:31", "Yesterday", "24 Sep".
// A date without a time (a decision's, a release's) never claims an hour.
func when(t, now time.Time, hasTime bool) string {
	t, now = t.UTC(), now.UTC()
	day := func(x time.Time) time.Time { return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, time.UTC) }
	switch day(now).Sub(day(t)) {
	case 0:
		if hasTime {
			return "Today " + t.Format("15:04")
		}
		return "Today"
	case 24 * time.Hour:
		return "Yesterday"
	}
	if t.Year() != now.Year() {
		return t.Format("2 Jan 2006")
	}
	return t.Format("2 Jan")
}

// assets writes the stylesheet, the script and the fonts, and returns the
// first two's addresses. A bundle's files are cached for an hour, so each is
// named by its content: a new design is a new name, never a stale copy.
func assets(b Bundle, in Input, th fs.FS) (css, js string, err error) {
	named := func(src, ext string) (string, error) { return nameAsset(b, th, src, ext) }
	if css, err = named("assets/site.css", ".css"); err != nil {
		return "", "", err
	}
	if js, err = named("assets/site.js", ".js"); err != nil {
		return "", "", err
	}
	if in.Assets != nil {
		for _, f := range []string{"inter-latin-400.woff2", "inter-latin-500.woff2", "inter-latin-600.woff2",
			"space-grotesk-latin-500.woff2", "space-grotesk-latin-600.woff2", "jetbrains-mono-latin-400.woff2"} {
			if data, err := fs.ReadFile(in.Assets, "fonts/"+f); err == nil {
				b["assets/fonts/"+f] = data
			}
		}
		for _, f := range []string{"vayupress-mark-black.png", "vayupress-mark-white.png"} {
			if data, err := fs.ReadFile(in.Assets, "img/"+f); err == nil {
				b["assets/"+f] = data
			}
		}
	}
	return css, js, nil
}

// nameAsset writes one of the design's files under a name taken from its
// content, and returns its address.
func nameAsset(b Bundle, th fs.FS, src, ext string) (string, error) {
	data, err := fs.ReadFile(th, src)
	if err != nil {
		return "", fmt.Errorf("docsite: the design has no %s: %w", src, err)
	}
	sum := sha256.Sum256(data)
	p := "assets/" + strings.TrimSuffix(path.Base(src), ext) + "." + hex.EncodeToString(sum[:5]) + ext
	b[p] = data
	return "/" + p, nil
}

// hero carries the console pictures in docs/site/img into the bundle.
func hero(b Bundle, repo fs.FS) heroShots {
	take := func(name string) string {
		p := siteDir + "/img/" + name
		data, err := fs.ReadFile(repo, p)
		if err != nil {
			return ""
		}
		b["img/"+name] = data
		return "/img/" + name
	}
	return heroShots{take("hero-light.webp"), take("hero-dark.webp"), take("hero-phone-light.webp"), take("hero-phone-dark.webp"),
		take("split-light.webp"), take("split-dark.webp")}
}

// verbatim copies docs/site/public into the bundle's root as it is: files the
// site serves unchanged, such as .well-known/security.txt and a page with a
// design of its own.
func verbatim(b Bundle, repo fs.FS) error {
	root := siteDir + "/public"
	if _, err := fs.Stat(repo, root); err != nil {
		return nil
	}
	return fs.WalkDir(repo, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(repo, p)
		if err != nil {
			return err
		}
		b[strings.TrimPrefix(p, root+"/")] = data
		return nil
	})
}

// searchIndex is what the search box reads: every page's title, address, kind
// and first sentence, fetched once when the box first opens.
func searchIndex(s *site, pages []page) ([]byte, error) {
	type entry struct {
		T string `json:"t"`
		U string `json:"u"`
		K string `json:"k"`
		S string `json:"s,omitempty"`
	}
	var out []entry
	for _, d := range s.Docs {
		k := "Guide"
		if d.ADR != nil {
			k = "Decision"
		}
		out = append(out, entry{docTitle(d), d.Href(), k, clip(d.R.Summary, 160)})
	}
	for _, r := range s.Releases {
		out = append(out, entry{r.Version, "/changes/" + r.Slug() + "/", "Release", clip(r.Summary, 160)})
	}
	for _, p := range pages {
		out = append(out, entry{p.Title, p.Path, "Page", clip(p.Description, 160)})
	}
	return json.Marshal(out)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := strings.LastIndexByte(s[:n], ' ')
	if cut < n/2 {
		cut = n
	}
	return s[:cut] + "…"
}

// sitemap lists every page the bundle holds, by its address.
func sitemap(b Bundle) []byte {
	var urls []string
	for p := range b {
		if path.Base(p) == "index.html" && p != "docs/adr/index.html" {
			urls = append(urls, "/"+strings.TrimSuffix(p, "index.html"))
		} else if strings.HasSuffix(p, ".html") && path.Base(p) != "index.html" {
			urls = append(urls, "/"+p)
		}
	}
	sort.Strings(urls)
	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, u := range urls {
		buf.WriteString("<url><loc>" + template.HTMLEscapeString(u) + "</loc></url>\n")
	}
	buf.WriteString("</urlset>\n")
	return buf.Bytes()
}

// num writes a count the way the page says it: 234,615.
func num(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

var funcs = template.FuncMap{
	"short": func(sha string) string {
		if len(sha) > 8 {
			return sha[:8]
		}
		return sha
	},
	"num": num,
	"longdate": func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.Format("2 January 2006")
	},
	"shortdate": func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.Format("2 Jan 2006")
	},
	"stamp": func(t time.Time) string { return "at " + t.UTC().Format("15:04 UTC, 2 Jan") },
	"iso":   func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
	"latestADRs": func(adrs []*doc, n int) []*doc {
		if len(adrs) > n {
			return adrs[:n]
		}
		return adrs
	},
	"lower": strings.ToLower,
	"slug":  func(s string) string { return strings.ReplaceAll(strings.ToLower(s), " ", "-") },
}
