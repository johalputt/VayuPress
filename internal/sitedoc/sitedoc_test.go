// SPDX-License-Identifier: Apache-2.0

package sitedoc

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/johalputt/vayupress/internal/bizsite"
)

// visible walks a page's <body> and returns what a visitor reads and follows,
// in order: every text run (whitespace collapsed) and every link and image
// address. <head> is left out — it is where the document renderer is meant to
// differ, carrying the SEO the old one lacked.
func visible(t *testing.T, page string) (texts, links []string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inBody bool) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "head", "script":
				return
			case "body":
				inBody = true
			}
			for _, a := range n.Attr {
				if inBody && (a.Key == "href" || a.Key == "src") {
					links = append(links, n.Data+" "+a.Val)
				}
			}
		}
		if inBody && n.Type == html.TextNode {
			if s := strings.Join(strings.Fields(n.Data), " "); s != "" {
				texts = append(texts, s)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inBody)
		}
	}
	walk(doc, false)
	return texts, links
}

// fullLegacy is content with every section the old renderer can draw, so the
// comparison covers all of them.
func fullLegacy(t bizsite.Template) bizsite.Content {
	c := t.Defaults
	c.Name = "Harbour & Co"
	c.Tagline = "Fresh <fish>, \"daily\""
	c.About = "First line.\nSecond line."
	c.Phone = "+44 20 7946 0000"
	c.Email = "hello@harbour.example"
	c.Address = "1 Quay St\nPortsmouth"
	c.Hours = "Mon–Fri 9–5\nSat 10–2"
	c.CTA = "Book"
	c.CTALink = "/book"
	c.HeroImg = "/media/hero.webp"
	c.Services = []bizsite.Service{{Title: "Crab", Desc: "Dressed", Price: "£9"}, {Title: "", Desc: "dropped"}, {Title: "Oysters"}}
	c.Gallery = []string{"/media/a.webp", "", "https://cdn.example/b.jpg"}
	c.SectionA = "Our catch"
	c.ShowBlog = true
	return c
}

// The promise that lets documents ship with nothing changing: a site stored
// the old way renders through FromLegacy to the same visible text, in the same
// order, with the same links, as the old renderer drew it — for every design.
func TestFromLegacyRendersTheSameSite(t *testing.T) {
	sparse := func(tpl bizsite.Template) bizsite.Content {
		c := fullLegacy(tpl)
		c.CTALink, c.HeroImg, c.Gallery, c.SectionA, c.ShowBlog = "", "", nil, "", false
		return c
	}
	for _, tpl := range bizsite.All() {
		for _, c := range []bizsite.Content{fullLegacy(tpl), sparse(tpl)} {
			legacyParity(t, tpl, c)
		}
	}
}

func legacyParity(t *testing.T, tpl bizsite.Template, c bizsite.Content) {
	t.Helper()
	old := legacyRender(tpl, c, "/blog")
	d := FromLegacy(tpl, c)
	home, _ := d.Home()
	now := Render(d, home, Options{Template: tpl, BlogURL: "/blog"})

	ot, ol := visible(t, old)
	nt, nl := visible(t, now)
	if strings.Join(ot, "|") != strings.Join(nt, "|") {
		t.Errorf("%s: visible text differs\nold: %q\nnew: %q", tpl.Key, ot, nt)
	}
	if strings.Join(ol, "|") != strings.Join(nl, "|") {
		t.Errorf("%s: links differ\nold: %q\nnew: %q", tpl.Key, ol, nl)
	}
	if !strings.Contains(now, `class="vb vb--`+tpl.Key+`"`) {
		t.Errorf("%s: the page does not carry its design's class, so the template CSS would not apply", tpl.Key)
	}
}

// Legacy content was never validated, so FromLegacy filters what the
// validator would refuse: a script or data link, an image that is neither on
// the site nor https. What remains is escaped like everything else.
func TestLegacyContentCannotCarryMarkupOrUnsafeLinks(t *testing.T) {
	tpl := bizsite.All()[0]
	c := bizsite.Content{
		Name: `Evil<script>alert(1)</script>`, Tagline: `"><img src=x onerror=1>`,
		About: "<b>bold</b>", CTA: "Go", CTALink: "javascript:alert(1)",
		HeroImg:  "data:image/svg+xml,<svg onload=1>",
		Services: []bizsite.Service{{Title: `<svg onload=1>`, Price: `<i>`}},
		Gallery:  []string{`https://x.example/y.jpg" onerror="1`, "javascript:alert(2)", "/media/ok.webp"},
		ShowBlog: true,
	}
	d := FromLegacy(tpl, c)
	home, _ := d.Home()
	out := Render(d, home, Options{Template: tpl, BlogURL: "https://blog.example.com"})
	for _, bad := range []string{"<script>alert", "<img src=x", "<svg onload", "<b>bold</b>", `.jpg" onerror`, "javascript:", "data:image"} {
		if strings.Contains(out, bad) {
			t.Errorf("legacy content reached the page unsafely: %q", bad)
		}
	}
	for _, want := range []string{`href="#contact"`, `src="/media/ok.webp"`, `href="https://blog.example.com"`} {
		if !strings.Contains(out, want) {
			t.Errorf("filtering removed more than the unsafe parts: missing %s", want)
		}
	}
}

// Every design's sample renders every section, and its stylesheet URL carries
// the design key so switching designs busts a cached /site.css.
func TestEveryDesignRendersItsSample(t *testing.T) {
	for _, tpl := range bizsite.All() {
		d := FromLegacy(tpl, tpl.Defaults)
		home, _ := d.Home()
		out := Render(d, home, Options{Template: tpl})
		for _, want := range []string{"vb-nav", "vb-hero", "vb-service", "vb-contact", "vb-footer",
			`class="vb vb--` + tpl.Key + `"`, `href="/site.css?v=` + tpl.Key + `"`} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: sample page missing %s", tpl.Key, want)
			}
		}
	}
}

// The deliberate difference: the old nav linked About and the services heading
// with nothing behind them.
func TestFromLegacyLinksOnlySectionsThatExist(t *testing.T) {
	tpl := bizsite.All()[0]
	c := bizsite.Content{Name: "Only a name", Tagline: "and a tagline"}
	d := FromLegacy(tpl, c)
	home, _ := d.Home()
	_, links := visible(t, Render(d, home, Options{Template: tpl}))
	for _, l := range links {
		if l == "a #about" || l == "a #services" {
			t.Errorf("the nav links %q to a section that is not on the page", l)
		}
	}
	if err := Validate(d); err != nil {
		t.Errorf("a legacy site with only a name does not validate: %v", err)
	}
}

func validDoc() Document {
	return Document{V: Version, Name: "Harbour", Pages: []Page{
		{Slug: "", Sections: []Section{
			{ID: "top", Kind: KindHero, Body: "Fresh fish", CTA: "Book", CTALink: "/book", Image: &Image{Src: "/media/h.webp"}},
			{ID: "about", Kind: KindText, Heading: "About", Body: "One.\nTwo."},
			{ID: "menu", Kind: KindItems, Heading: "Menu", Items: []Item{{Title: "Crab", Price: "£9"}}},
			{ID: "pics", Kind: KindGallery, Heading: "Gallery", Images: []Image{{Src: "https://cdn.example/a.jpg", Alt: "The quay at dawn"}}},
			{ID: "contact", Kind: KindContact, Heading: "Contact", Phone: "+44 1", Email: "a@b.example", Form: true},
		}},
		{Slug: "menu", Title: "Menu", InNav: true, Sections: []Section{{ID: "list", Kind: KindText, Body: "x"}}},
	}}
}

func TestAValidDocumentRoundTrips(t *testing.T) {
	b, err := Marshal(validDoc())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	d, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	again, _ := json.Marshal(d)
	if string(again) != string(b) {
		t.Errorf("round trip changed the document\n%s\n%s", b, again)
	}
}

// One seed per rule, each asserting the path the editor will point at.
func TestValidationNamesTheFieldThatFailed(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(*Document)
		path  string
		msgIs string
	}{
		{"wrong version", func(d *Document) { d.V = 2 }, "v", "must be"},
		{"no pages", func(d *Document) { d.Pages = nil }, "pages", "home page"},
		{"no home", func(d *Document) { d.Pages[0].Slug = "start" }, "pages", "empty slug"},
		{"too many pages", func(d *Document) {
			for i := 0; i < MaxPages-1; i++ { // 21 pages: one over
				d.Pages = append(d.Pages, Page{Slug: "p" + string(rune('a'+i))})
			}
		}, "pages", "limit"},
		{"bad slug", func(d *Document) { d.Pages[1].Slug = "Menu" }, "pages[1].slug", "lowercase"},
		{"duplicate slug", func(d *Document) { d.Pages[1].Slug = "" }, "pages[1].slug", "another page"},
		{"multi-line name", func(d *Document) { d.Name = "a\nb" }, "name", "single line"},
		{"long title", func(d *Document) { d.Pages[1].Title = strings.Repeat("t", 121) }, "pages[1].title", "limit"},
		{"control character", func(d *Document) { d.Pages[0].Sections[1].Body = "a\x00b" }, "pages[0].sections[1].body", "control"},
		{"bad section id", func(d *Document) { d.Pages[0].Sections[1].ID = "About Us" }, "pages[0].sections[1].id", "lowercase"},
		{"duplicate section id", func(d *Document) { d.Pages[0].Sections[2].ID = "about" }, "pages[0].sections[2].id", "another section"},
		{"hero not first", func(d *Document) { d.Pages[0].Sections[1].Kind = KindHero; d.Pages[0].Sections[1].Body = "x" }, "pages[0].sections[1].kind", "first"},
		{"unknown kind", func(d *Document) { d.Pages[0].Sections[1].Kind = "video" }, "pages[0].sections[1].kind", "not a section kind"},
		{"foreign field", func(d *Document) { d.Pages[0].Sections[1].Phone = "1" }, "pages[0].sections[1].phone", "not a field of a text"},
		{"two forms", func(d *Document) {
			d.Pages[0].Sections = append(d.Pages[0].Sections, Section{ID: "c2", Kind: KindContact, Form: true})
		}, "pages[0].sections[5].form", "one contact form"},
		{"too many sections", func(d *Document) {
			for i := 0; i < MaxSections; i++ {
				d.Pages[1].Sections = append(d.Pages[1].Sections, Section{ID: "s" + strings.Repeat("x", i%30) + string(rune('a'+i%26)), Kind: KindText})
			}
		}, "pages[1].sections", "limit"},
		{"multi-line tagline", func(d *Document) { d.Pages[0].Sections[0].Body = "a\nb" }, "pages[0].sections[0].body", "single line"},
		{"link without label", func(d *Document) { d.Pages[0].Sections[0].CTA = "" }, "pages[0].sections[0].cta", "label"},
		{"javascript link", func(d *Document) { d.Pages[0].Sections[0].CTALink = "javascript:alert(1)" }, "pages[0].sections[0].cta_link", "must start with"},
		{"protocol-relative link", func(d *Document) { d.Pages[0].Sections[0].CTALink = "//evil.example/x" }, "pages[0].sections[0].cta_link", "must start with"},
		{"backslash link", func(d *Document) { d.Pages[0].Sections[0].CTALink = "/\\evil.example" }, "pages[0].sections[0].cta_link", "must start with"},
		{"hostless https", func(d *Document) { d.Pages[0].Sections[0].CTALink = "https:///x" }, "pages[0].sections[0].cta_link", "no host"},
		{"empty mailto", func(d *Document) { d.Pages[0].Sections[0].CTALink = "mailto:" }, "pages[0].sections[0].cta_link", "empty"},
		{"spaced link", func(d *Document) { d.Pages[0].Sections[0].CTALink = "/a b" }, "pages[0].sections[0].cta_link", "spaces"},
		{"http image", func(d *Document) { d.Pages[0].Sections[0].Image.Src = "http://cdn.example/a.jpg" }, "pages[0].sections[0].image.src", "https://"},
		{"data image", func(d *Document) { d.Pages[0].Sections[3].Images[0].Src = "data:image/png;base64,AAAA" }, "pages[0].sections[3].images[0].src", "https://"},
		{"empty image", func(d *Document) { d.Pages[0].Sections[3].Images[0].Src = "" }, "pages[0].sections[3].images[0].src", "required"},
		{"gallery without alt", func(d *Document) { d.Pages[0].Sections[3].Images[0].Alt = " " }, "pages[0].sections[3].images[0].alt", "describe"},
		{"item without title", func(d *Document) { d.Pages[0].Sections[2].Items[0].Title = "" }, "pages[0].sections[2].items[0].title", "required"},
		{"too many items", func(d *Document) {
			for i := 0; i < MaxItems; i++ {
				d.Pages[0].Sections[2].Items = append(d.Pages[0].Sections[2].Items, Item{Title: "x"})
			}
		}, "pages[0].sections[2].items", "limit"},
		{"bad email", func(d *Document) { d.Pages[0].Sections[4].Email = "not-an-address" }, "pages[0].sections[4].email", "not an email"},
	}
	for _, c := range cases {
		d := validDoc()
		c.edit(&d)
		err := Validate(d)
		var fe *FieldError
		if !errors.As(err, &fe) {
			t.Errorf("%s: got %v, want a FieldError at %s", c.name, err, c.path)
			continue
		}
		if fe.Path != c.path || !strings.Contains(fe.Msg, c.msgIs) {
			t.Errorf("%s: got %q, want %s: …%s…", c.name, fe.Error(), c.path, c.msgIs)
		}
	}
	if err := Validate(validDoc()); err != nil {
		t.Fatalf("the fixture itself is invalid, so every case above proves nothing: %v", err)
	}
}

// What a click may follow, stated independently of the validator.
func TestAcceptableLinks(t *testing.T) {
	for _, ok := range []string{"https://example.com/x", "http://example.com", "mailto:a@b.example", "tel:+441234", "/menu", "#contact"} {
		if err := link("l", ok); err != nil {
			t.Errorf("%q was refused: %v", ok, err)
		}
	}
}

func TestParseRefusesUnknownFieldsAndOversize(t *testing.T) {
	b, _ := Marshal(validDoc())
	withTypo := strings.Replace(string(b), `"name":`, `"title_typo":"x","name":`, 1)
	if _, err := Parse([]byte(withTypo)); err == nil || !strings.Contains(err.Error(), "title_typo") {
		t.Errorf("a misspelt field was accepted: %v", err)
	}
	if _, err := Parse([]byte(string(b) + `{}`)); err == nil || !strings.Contains(err.Error(), "after the document") {
		t.Errorf("trailing content was accepted: %v", err)
	}
	big := make([]byte, MaxBytes+1)
	if _, err := Parse(big); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("an oversized document was accepted: %v", err)
	}
}

// Every string a document holds reaches the page as text. The markup a
// hostile editor or assistant might try must arrive as characters, never as
// elements — including inside the structured data, where a "</script>" would
// otherwise end the element early.
func TestEveryFieldIsEscaped(t *testing.T) {
	x := `</script><script>alert(1)</script><b onclick="y">`
	d := Document{V: Version, Name: x, ShowBlog: true, Pages: []Page{{Slug: "", Title: x, Description: x, Sections: []Section{
		{ID: "top", Kind: KindHero, Eyebrow: x, Heading: x, Body: x, CTA: x, CTALink: "/a?q=\"><b>", Image: &Image{Src: "/m.png?\"><b>", Alt: x}},
		{ID: "t", Kind: KindText, Nav: `<b onclick="y">n</b>`, Heading: x, Body: x},
		{ID: "i", Kind: KindItems, Items: []Item{{Title: x, Desc: x, Price: `<b onclick="y">1</b>`}}},
		{ID: "g", Kind: KindGallery, Images: []Image{{Src: "/g.png", Alt: x}}},
		{ID: "c", Kind: KindContact, Phone: x, Email: "a@b.example", Address: x, Hours: x},
	}}}}
	if err := Validate(d); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	home, _ := d.Home()
	out := Render(d, home, Options{Template: bizsite.All()[0], Origin: "https://s.example", BlogURL: "/blog"})
	if strings.Contains(out, "<script>alert") || strings.Contains(out, "<b ") || strings.Contains(out, "<b>") {
		t.Errorf("markup from a field reached the page as markup:\n%s", out)
	}
	if n := strings.Count(out, "</script>"); n != 1 {
		t.Errorf("the page has %d </script> tags, want exactly the structured data's own", n)
	}
}

func TestTheHeadCarriesWhatSearchAndPreviewsRead(t *testing.T) {
	d := validDoc()
	home, _ := d.Home()
	out := Render(d, home, Options{Template: bizsite.All()[0], Origin: "https://s.example", Icon: "/favicon.png"})
	for _, want := range []string{
		`<title>Harbour — Fresh fish</title>`,
		`<meta name="description" content="Fresh fish">`,
		`<link rel="canonical" href="https://s.example/">`,
		`<meta property="og:image" content="https://s.example/media/h.webp">`,
		`<meta name="twitter:card" content="summary_large_image">`,
		`<link rel="icon" href="/favicon.png">`,
		`"@type":"LocalBusiness"`, `"telephone":"+44 1"`, `"url":"https://s.example/"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("home page head is missing %s", want)
		}
	}
	menu, _ := d.Page("menu")
	out = Render(d, menu, Options{Template: bizsite.All()[0], Origin: "https://s.example"})
	for _, want := range []string{
		`<title>Menu — Harbour</title>`,
		`<meta name="description" content="x">`,
		`<link rel="canonical" href="https://s.example/menu">`,
		`<meta name="twitter:card" content="summary">`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("menu page head is missing %s", want)
		}
	}
	// Without an origin, nothing relative is emitted where an absolute
	// address is required.
	out = Render(d, home, Options{Template: bizsite.All()[0]})
	if strings.Contains(out, "canonical") || strings.Contains(out, "og:") {
		t.Error("canonical or social cards were emitted without an origin to make them absolute")
	}
}

func TestNavigationListsSectionsAndOtherPages(t *testing.T) {
	d := validDoc()
	d.Pages[0].Sections[1].Nav = "About"
	home, _ := d.Home()
	_, links := visible(t, Render(d, home, Options{Template: bizsite.All()[0]}))
	if !contains(links, "a #about") || !contains(links, "a /menu") {
		t.Errorf("home nav %q lacks the About anchor or the Menu page", links)
	}
	menu, _ := d.Page("menu")
	_, links = visible(t, Render(d, menu, Options{Template: bizsite.All()[0]}))
	if contains(links, "a /menu") {
		t.Errorf("the Menu page links to itself in its nav: %q", links)
	}
}

func TestTheContactScriptLoadsOnlyWithAForm(t *testing.T) {
	d := validDoc()
	o := Options{Template: bizsite.All()[0], ContactScript: `<script src="/static/js/contact.js" defer></script>`}
	home, _ := d.Home()
	if out := Render(d, home, o); !strings.Contains(out, o.ContactScript) || !strings.Contains(out, `id="vayu-contact"`) {
		t.Error("a page with a contact form has no form element or no script to draw it")
	}
	menu, _ := d.Page("menu")
	if out := Render(d, menu, o); strings.Contains(out, "contact.js") {
		t.Error("a page without a form loads the contact script")
	}
}
