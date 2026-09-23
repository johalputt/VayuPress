// SPDX-License-Identifier: Apache-2.0

package sitedoc

import (
	"encoding/json"
	"html"
	"strings"
	"unicode/utf8"

	"github.com/johalputt/vayupress/internal/bizsite"
)

// Options are what a page needs from outside its document.
type Options struct {
	// Template is the design. Its stylesheet styles the vb-* markup below, so
	// every existing template dresses a document without change.
	Template bizsite.Template
	// BlogURL is where the blog lives; "" hides the blog link.
	BlogURL string
	// Origin is the site's scheme and host ("https://example.com"). It makes
	// the canonical URL and social cards absolute; "" leaves them out rather
	// than emit relative ones, which crawlers and card scrapers ignore.
	Origin string
	// Icon is the site's icon, as a path on the site; "" omits the link.
	Icon string
	// ContactScript is the <script> element of the contact-form widget,
	// included only on a page that carries a form.
	ContactScript string
	// Stylesheet is the address of the site's stylesheet: its design and its
	// brand. The caller versions it, so a change to either reaches a visitor
	// whose browser cached the last one. Empty means /site.css for the design.
	Stylesheet string
	// InlineCSS, when set, is written into the page in place of the linked
	// stylesheet. It is for the editor's preview: a frame sandboxed to an
	// opaque origin, whose requests carry no session, so a stylesheet behind
	// sign-in would never load there.
	InlineCSS string
}

func esc(s string) string { return html.EscapeString(s) }

// paragraphs renders newline-separated text as <p> blocks.
func paragraphs(s string) string {
	var b strings.Builder
	for _, p := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(p); t != "" {
			b.WriteString("<p>" + esc(t) + "</p>")
		}
	}
	return b.String()
}

// siteName is the name every page shows; a document with none still renders
// as a complete page.
func (d Document) siteName() string {
	if n := strings.TrimSpace(d.Name); n != "" {
		return n
	}
	return "Your Business"
}

func pagePath(slug string) string { return "/" + slug }

// Render returns the complete HTML page p of document d. d must have passed
// Validate or come from FromLegacy, which applies the same link and image
// rules: the renderer escapes every string it writes, but relies on those
// rules for which links and image sources are acceptable at all.
func Render(d Document, p Page, o Options) string {
	name := d.siteName()
	var hero *Section
	if len(p.Sections) > 0 && p.Sections[0].Kind == KindHero {
		hero = &p.Sections[0]
	}

	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">`)
	writeHead(&b, d, p, hero, o)
	switch sheet := o.Stylesheet; {
	case o.InlineCSS != "":
		// "</" would end the element and let what follows be read as markup.
		// The stylesheet is built from validated values and never holds it;
		// one that does is left out rather than let through.
		if !strings.Contains(o.InlineCSS, "</") {
			b.WriteString(`<style>` + o.InlineCSS + `</style>`)
		}
	case sheet == "":
		b.WriteString(`<link rel="stylesheet" href="/site.css?v=` + esc(o.Template.Key) + `">`)
	default:
		b.WriteString(`<link rel="stylesheet" href="` + esc(sheet) + `">`)
	}
	b.WriteString(`</head><body class="vb vb--` + esc(o.Template.Key) + `">`)

	b.WriteString(`<nav class="vb-nav"><a class="vb-brand" href="/">` + esc(name) + `</a><div class="vb-nav-links">`)
	for _, s := range p.Sections {
		if s.Nav != "" {
			b.WriteString(`<a href="#` + esc(s.ID) + `">` + esc(s.Nav) + `</a>`)
		}
	}
	for _, other := range d.Pages {
		if other.InNav && other.Slug != p.Slug {
			b.WriteString(`<a href="` + esc(pagePath(other.Slug)) + `">` + esc(navLabel(other)) + `</a>`)
		}
	}
	if d.ShowBlog && o.BlogURL != "" {
		b.WriteString(`<a href="` + esc(o.BlogURL) + `">Blog</a>`)
	}
	b.WriteString(`</div></nav>`)

	rest := p.Sections
	if hero != nil {
		writeHero(&b, *hero, name, o.Template)
		rest = rest[1:]
	}
	b.WriteString(`<main class="vb-main">`)
	form, gallery := false, false
	for _, s := range rest {
		form = form || s.Form
		gallery = gallery || (s.Kind == KindGallery && len(s.Images) > 0)
		writeSection(&b, s)
	}
	b.WriteString(`</main>`)

	b.WriteString(`<footer class="vb-footer"><span>` + esc(name) + `</span>`)
	if d.ShowBlog && o.BlogURL != "" {
		b.WriteString(`<a href="` + esc(o.BlogURL) + `">Blog</a>`)
	}
	b.WriteString(`<span class="vb-powered">Powered by VayuPress</span></footer>`)
	if form {
		b.WriteString(o.ContactScript)
	}
	if gallery {
		b.WriteString(GalleryScriptTag())
	}
	b.WriteString(`</body></html>`)
	return b.String()
}

func navLabel(p Page) string {
	if p.Title != "" {
		return p.Title
	}
	if p.Slug == "" {
		return "Home"
	}
	return p.Slug
}

func writeHero(b *strings.Builder, s Section, name string, t bizsite.Template) {
	b.WriteString(`<header class="vb-hero` + variantClass("vb-hero", s) + `" id="` + esc(s.ID) + `">`)
	if s.Image != nil {
		// Not lazy: the hero is the first thing on screen, and lazy-loading the
		// largest paint of the page delays it.
		b.WriteString(`<img class="vb-hero-img" src="` + esc(s.Image.Src) + `" alt="` + esc(s.Image.Alt) + `" fetchpriority="high">`)
	}
	eyebrow := s.Eyebrow
	if eyebrow == "" {
		eyebrow = t.Eyebrow
	}
	heading := s.Heading
	if heading == "" {
		heading = name
	}
	b.WriteString(`<div class="vb-hero-inner"><span class="vb-eyebrow">` + esc(eyebrow) + `</span>`)
	b.WriteString(`<h1>` + esc(heading) + `</h1>`)
	if s.Body != "" {
		b.WriteString(`<p class="vb-tagline">` + esc(s.Body) + `</p>`)
	}
	if s.CTA != "" {
		link := s.CTALink
		if link == "" {
			link = "#contact"
		}
		b.WriteString(`<a class="vb-cta" href="` + esc(link) + `">` + esc(s.CTA) + `</a>`)
	}
	b.WriteString(`</div></header>`)
}

// variantClass is the modifier class for a section's layout. The first
// layout of each kind is the design's own and needs none.
func variantClass(block string, s Section) string {
	if s.Variant == "" || s.Variant == Variants[s.Kind][0] {
		return ""
	}
	return " " + block + "--" + s.Variant
}

func writeSection(b *strings.Builder, s Section) {
	class := "vb-section"
	if s.Kind == KindContact {
		class += " vb-contact"
	}
	b.WriteString(`<section class="` + class + `" id="` + esc(s.ID) + `">`)
	if s.Heading != "" {
		b.WriteString(`<h2>` + esc(s.Heading) + `</h2>`)
	}
	switch s.Kind {
	case KindText:
		b.WriteString(paragraphs(s.Body))
	case KindItems:
		b.WriteString(`<div class="vb-services` + variantClass("vb-services", s) + `">`)
		for _, it := range s.Items {
			b.WriteString(`<div class="vb-service"><div class="vb-service-head"><h3>` + esc(it.Title) + `</h3>`)
			if it.Price != "" {
				b.WriteString(`<span class="vb-price">` + esc(it.Price) + `</span>`)
			}
			b.WriteString(`</div>`)
			if it.Desc != "" {
				b.WriteString(`<p>` + esc(it.Desc) + `</p>`)
			}
			b.WriteString(`</div>`)
		}
		b.WriteString(`</div>`)
	case KindGallery:
		b.WriteString(`<div class="vb-gallery` + variantClass("vb-gallery", s) + `">`)
		for _, img := range s.Images {
			// Each picture links to itself: with no script it opens full size,
			// and the gallery script turns that into a viewer on the page.
			b.WriteString(`<a class="vb-zoom" href="` + esc(img.Src) + `"><img src="` + esc(img.Src) + `" alt="` + esc(img.Alt) + `" loading="lazy" decoding="async"></a>`)
		}
		b.WriteString(`</div>`)
	case KindContact:
		b.WriteString(`<div class="vb-contact-grid">`)
		if s.Phone != "" {
			b.WriteString(`<div><span class="vb-label">Phone</span><a href="tel:` + esc(strings.ReplaceAll(s.Phone, " ", "")) + `">` + esc(s.Phone) + `</a></div>`)
		}
		if s.Email != "" {
			b.WriteString(`<div><span class="vb-label">Email</span><a href="mailto:` + esc(s.Email) + `">` + esc(s.Email) + `</a></div>`)
		}
		// Address and hours keep their line breaks (vb-hours is pre-line): an
		// address typed on three lines is three lines on the page.
		if s.Address != "" {
			b.WriteString(`<div><span class="vb-label">Address</span><span class="vb-hours">` + esc(s.Address) + `</span></div>`)
		}
		if s.Hours != "" {
			b.WriteString(`<div><span class="vb-label">Hours</span><span class="vb-hours">` + esc(s.Hours) + `</span></div>`)
		}
		b.WriteString(`</div>`)
		if s.Form {
			// The widget draws the form into this element; its own heading is
			// suppressed because the section already has one.
			b.WriteString(`<div id="vayu-contact" class="vb-contact-form" data-heading=""></div>`)
		}
	}
	b.WriteString(`</section>`)
}

// writeHead is what a search engine, a link preview and a browser tab read:
// title, description, canonical address, Open Graph and Twitter cards, the
// business as structured data, and the icon.
func writeHead(b *strings.Builder, d Document, p Page, hero *Section, o Options) {
	name := d.siteName()
	title := name
	switch {
	case p.Slug == "" && p.Title == "" && hero != nil && hero.Body != "":
		title = name + " — " + hero.Body
	case p.Title != "" && p.Title != name:
		title = p.Title + " — " + name
	}
	desc := describe(p, hero)
	b.WriteString(`<title>` + esc(title) + `</title>`)
	if desc != "" {
		b.WriteString(`<meta name="description" content="` + esc(desc) + `">`)
	}
	if o.Icon != "" {
		b.WriteString(`<link rel="icon" href="` + esc(o.Icon) + `">`)
	}
	img := pageImage(p, o.Origin)
	if o.Origin != "" {
		canonical := o.Origin + pagePath(p.Slug)
		b.WriteString(`<link rel="canonical" href="` + esc(canonical) + `">`)
		b.WriteString(`<meta property="og:type" content="website">`)
		b.WriteString(`<meta property="og:site_name" content="` + esc(name) + `">`)
		b.WriteString(`<meta property="og:title" content="` + esc(title) + `">`)
		b.WriteString(`<meta property="og:url" content="` + esc(canonical) + `">`)
		if desc != "" {
			b.WriteString(`<meta property="og:description" content="` + esc(desc) + `">`)
		}
		card := "summary"
		if img != "" {
			card = "summary_large_image"
			b.WriteString(`<meta property="og:image" content="` + esc(img) + `">`)
		}
		b.WriteString(`<meta name="twitter:card" content="` + card + `">`)
	}
	writeBusiness(b, d, o.Origin, desc, img)
}

// describe is the page's description: the one written for it, or the hero's
// tagline, or the opening of the first text.
func describe(p Page, hero *Section) string {
	if p.Description != "" {
		return p.Description
	}
	if hero != nil && hero.Body != "" {
		return hero.Body
	}
	for _, s := range p.Sections {
		if s.Kind == KindText && s.Body != "" {
			first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(s.Body), "\n", 2)[0])
			if utf8.RuneCountInString(first) > 160 {
				r := []rune(first)
				first = strings.TrimSpace(string(r[:157])) + "…"
			}
			return first
		}
	}
	return ""
}

// pageImage is the picture a link preview shows: the hero's, else the first
// in a gallery, as an absolute address.
func pageImage(p Page, origin string) string {
	var src string
	for _, s := range p.Sections {
		if s.Kind == KindHero && s.Image != nil {
			src = s.Image.Src
			break
		}
		if s.Kind == KindGallery && len(s.Images) > 0 {
			src = s.Images[0].Src
			break
		}
	}
	if strings.HasPrefix(src, "/") {
		if origin == "" {
			return ""
		}
		return origin + src
	}
	return src
}

// writeBusiness emits the business as schema.org LocalBusiness, from the first
// contact section in the site. json.Marshal escapes <, > and &, so no string
// in the document can close the script element it sits in.
func writeBusiness(b *strings.Builder, d Document, origin, desc, img string) {
	ld := map[string]string{
		"@context": "https://schema.org", "@type": "LocalBusiness", "name": d.siteName(),
	}
	if origin != "" {
		ld["url"] = origin + "/"
	}
	if desc != "" {
		ld["description"] = desc
	}
	if img != "" {
		ld["image"] = img
	}
contact:
	for _, p := range d.Pages {
		for _, s := range p.Sections {
			if s.Kind != KindContact {
				continue
			}
			for k, v := range map[string]string{"telephone": s.Phone, "email": s.Email, "address": strings.Join(strings.Fields(strings.ReplaceAll(s.Address, "\n", ", ")), " ")} {
				if v != "" {
					ld[k] = v
				}
			}
			break contact
		}
	}
	j, err := json.Marshal(ld)
	if err != nil {
		return
	}
	b.WriteString(`<script type="application/ld+json">` + string(j) + `</script>`)
}
