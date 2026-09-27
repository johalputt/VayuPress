// SPDX-License-Identifier: Apache-2.0

package docsite

import (
	"bytes"
	"html/template"
	"io/fs"
	"net/url"
	"path"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/johalputt/vayupress/internal/markdown"
)

// ugc is the sanitiser every rendered document passes through. The documents
// come from the repository, which is trusted as the binary is, but a page is
// published to anyone, so it is held to what a comment would be.
var ugc = bluemonday.UGCPolicy()

// heading is one entry of a page's "On this page".
type heading struct {
	ID, Text string
	Sub      bool // an h3 under the h2 before it
}

// rendered is a document as a page shows it.
type rendered struct {
	Body    template.HTML
	Title   string    // the document's own h1, taken out of the body
	TOC     []heading // its h2s and h3s
	Summary string    // its first paragraph, as text
}

// linker turns the links a document wrote for GitHub into links on the site.
type linker struct {
	repo   fs.FS
	source string // repository web address
	commit string
	docs   map[string]bool // slugs that have a page
	files  map[string]bool // repository files the bundle must carry, filled as found
}

// renderDoc renders the Markdown file at src (a repository path).
func (l *linker) renderDoc(src string, md []byte) rendered {
	var buf bytes.Buffer
	if err := markdown.Document.Convert(md, &buf); err != nil {
		return rendered{Body: template.HTML("<pre>" + template.HTMLEscapeString(string(md)) + "</pre>")}
	}
	nodes, err := html.ParseFragment(strings.NewReader(ugc.Sanitize(buf.String())), &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"})
	if err != nil {
		return rendered{Body: template.HTML(ugc.Sanitize(buf.String()))} //nolint:gosec // sanitised above
	}
	var out rendered
	dir := path.Dir(src)
	for i := 0; i < len(nodes); i++ {
		n := nodes[i]
		// The page draws the title itself, so the document's own h1 leaves the
		// body; and an ADR's header block (its status and date, which the page
		// shows as facts) goes with it.
		if n.Type == html.ElementNode && n.DataAtom == atom.H1 && out.Title == "" {
			out.Title = strings.TrimSpace(text(n))
			nodes = append(nodes[:i], nodes[i+1:]...)
			next := i
			for next < len(nodes) && nodes[next].Type == html.TextNode && strings.TrimSpace(nodes[next].Data) == "" {
				next++
			}
			if next < len(nodes) && isStatusBlock(nodes[next]) {
				nodes = append(nodes[:next], nodes[next+1:]...)
			}
			i--
			continue
		}
		l.walk(n, dir, &out)
	}
	var b strings.Builder
	for _, n := range nodes {
		_ = html.Render(&b, n)
	}
	out.Body = template.HTML(b.String()) //nolint:gosec // sanitised by ugc, then only links rewritten
	return out
}

// isStatusBlock reports whether n is an ADR's header: a list or paragraph that
// opens with its status ("- **Status:** Accepted", "**Status**: Accepted").
func isStatusBlock(n *html.Node) bool {
	if n.Type != html.ElementNode || (n.DataAtom != atom.Ul && n.DataAtom != atom.P) {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(text(n)), "Status")
}

func (l *linker) walk(n *html.Node, dir string, out *rendered) {
	if n.Type == html.ElementNode {
		switch n.DataAtom {
		case atom.A:
			l.rewrite(n, "href", dir)
		case atom.Img:
			l.rewrite(n, "src", dir)
			setAttr(n, "loading", "lazy")
		case atom.H2, atom.H3:
			if id := attr(n, "id"); id != "" {
				out.TOC = append(out.TOC, heading{ID: id, Text: strings.TrimSpace(text(n)), Sub: n.DataAtom == atom.H3})
			}
		case atom.P:
			if out.Summary == "" && n.Parent == nil {
				out.Summary = strings.Join(strings.Fields(text(n)), " ")
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		l.walk(c, dir, out)
	}
}

// rewrite resolves one link. A link to another document becomes its page; to a
// file under docs/ (a screenshot), that file, carried in the bundle; to
// anything else in the repository, the file on the forge at this commit. A link
// that leaves the repository is dropped rather than guessed at.
func (l *linker) rewrite(n *html.Node, key, dir string) {
	raw := attr(n, key)
	if raw == "" || strings.HasPrefix(raw, "#") {
		return
	}
	u, err := url.Parse(raw)
	if err != nil {
		removeAttr(n, key)
		return
	}
	if u.Scheme != "" || u.Host != "" {
		return // somewhere else, as written
	}
	// The sanitiser marks every link nofollow; one that stays on the site is
	// the site's own.
	removeAttr(n, "rel")
	p := u.Path
	if strings.HasPrefix(p, "/") {
		p = strings.TrimPrefix(p, "/") // GitHub reads a leading slash as the repository root
	} else {
		p = path.Join(dir, p)
	}
	p = path.Clean(p)
	frag := ""
	if u.Fragment != "" {
		frag = "#" + u.Fragment
	}
	switch {
	case p == ".." || strings.HasPrefix(p, "../"):
		removeAttr(n, key)
	case p == "docs/adr/INDEX.md":
		setAttr(n, key, "/decisions/"+frag)
	case strings.HasPrefix(p, "docs/") && strings.HasSuffix(p, ".md") && l.docs[strings.TrimSuffix(strings.TrimPrefix(p, "docs/"), ".md")]:
		setAttr(n, key, "/docs/"+strings.TrimSuffix(strings.TrimPrefix(p, "docs/"), ".md")+"/"+frag)
	case strings.HasPrefix(p, "docs/") && carried(p) && exists(l.repo, p):
		l.files[p] = true
		setAttr(n, key, "/"+p)
	default:
		setAttr(n, key, strings.TrimSuffix(l.source, "/")+"/blob/"+l.commit+"/"+p+frag)
	}
}

// carried is what a bundle may hold of a document's own files: pictures and
// the like. Markdown and source files are linked on the forge instead.
func carried(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".svg", ".pdf":
		return true
	}
	return false
}

func exists(fsys fs.FS, p string) bool {
	fi, err := fs.Stat(fsys, p)
	return err == nil && fi.Mode().IsRegular()
}

func text(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(text(c))
	}
	return b.String()
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func setAttr(n *html.Node, key, val string) {
	for i, a := range n.Attr {
		if a.Key == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

func removeAttr(n *html.Node, key string) {
	for i, a := range n.Attr {
		if a.Key == key {
			n.Attr = append(n.Attr[:i], n.Attr[i+1:]...)
			return
		}
	}
}
