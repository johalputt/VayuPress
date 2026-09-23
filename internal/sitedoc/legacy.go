// SPDX-License-Identifier: Apache-2.0

package sitedoc

import (
	"strings"

	"github.com/johalputt/vayupress/internal/bizsite"
)

// FromLegacy is the document equivalent of a site stored before documents
// existed: the flat bizsite.Content, which the old renderer drew as one page.
// A site with no document renders through this, so the day documents ship
// nothing changes; TestFromLegacyRendersTheSameSite holds it to the same
// visible text, in the same order, with the same links as that renderer.
//
// Three differences are deliberate, and are fixes rather than drift:
//   - A link or image source the validator would refuse (javascript:, data:,
//     an image neither on the site nor https) is dropped; legacy content was
//     never validated.
//   - The old nav linked About and the services heading even when those
//     sections were empty, pointing at anchors that did not exist; here a
//     section that is not there is not in the nav.
//   - The old gallery forced alt="" on every picture. The document keeps the
//     alt empty — inventing one would be worse — and the validator asks for it
//     on the first save from the editor.
func FromLegacy(t bizsite.Template, c bizsite.Content) Document {
	servHead := strings.TrimSpace(c.SectionA)
	if servHead == "" {
		servHead = t.ServicesLabel
	}
	hero := Section{ID: "top", Kind: KindHero, Body: c.Tagline, CTA: c.CTA}
	// Legacy content was never validated. What the validator would refuse —
	// a javascript: or data: link, an image neither on the site nor https — is
	// dropped here, so the renderer's reliance on validated links holds for a
	// legacy site too. A dropped button link falls back to #contact, as an
	// empty one always did.
	if l := strings.TrimSpace(c.CTALink); c.CTA != "" && link("", l) == nil {
		hero.CTALink = l
	}
	if src := strings.TrimSpace(c.HeroImg); safeSrc(src) {
		hero.Image = &Image{Src: src}
	}
	sections := []Section{hero}
	if strings.TrimSpace(c.About) != "" {
		sections = append(sections, Section{ID: "about", Kind: KindText, Nav: "About", Heading: "About", Body: c.About})
	}
	var items []Item
	for _, s := range c.Services {
		if strings.TrimSpace(s.Title) != "" {
			items = append(items, Item{Title: s.Title, Desc: s.Desc, Price: s.Price})
		}
	}
	if len(items) > 0 {
		sections = append(sections, Section{ID: "services", Kind: KindItems, Nav: servHead, Heading: servHead, Items: items})
	}
	var images []Image
	for _, g := range c.Gallery {
		if safeSrc(g) {
			images = append(images, Image{Src: g})
		}
	}
	if len(images) > 0 {
		sections = append(sections, Section{ID: "gallery", Kind: KindGallery, Nav: "Gallery", Heading: "Gallery", Images: images})
	}
	sections = append(sections, Section{
		ID: "contact", Kind: KindContact, Nav: "Contact", Heading: "Contact",
		Phone: c.Phone, Email: c.Email, Address: c.Address, Hours: c.Hours,
	})
	return Document{
		V: Version, Name: c.Name, ShowBlog: c.ShowBlog,
		Pages: []Page{{Slug: "", Sections: sections}},
	}
}

// safeSrc reports whether src is an image source the validator accepts.
func safeSrc(src string) bool {
	return src != "" && image("", Image{Src: src}, false) == nil
}
