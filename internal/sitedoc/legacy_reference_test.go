// SPDX-License-Identifier: Apache-2.0

package sitedoc

// legacy_reference_test.go — the page renderer that served template websites
// before documents, frozen here as the reference TestFromLegacyRendersTheSameSite
// compares against. A copy rather than a call: the reference must not change
// when somebody improves the live renderer, or parity would be checked against
// itself.

import (
	"html"
	"strings"

	"github.com/johalputt/vayupress/internal/bizsite"
)

func legacyEsc(s string) string { return html.EscapeString(s) }

func legacyParagraphs(s string) string {
	var b strings.Builder
	for _, p := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(p); t != "" {
			b.WriteString("<p>" + legacyEsc(t) + "</p>")
		}
	}
	return b.String()
}

func legacyRender(t bizsite.Template, c bizsite.Content, blogURL string) string {
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name = "Your Business"
	}
	servHead := strings.TrimSpace(c.SectionA)
	if servHead == "" {
		servHead = t.ServicesLabel
	}

	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">`)
	b.WriteString(`<title>` + legacyEsc(name) + ` — ` + legacyEsc(c.Tagline) + `</title>`)
	b.WriteString(`<meta name="description" content="` + legacyEsc(c.Tagline) + `">`)
	// Version the stylesheet URL by the active template key so switching designs
	// busts the browser/CDN cache immediately — /site.css is cacheable but its
	// contents change with the template, and without this a design change would
	// keep serving the previous design's CSS until the cache expired.
	b.WriteString(`<link rel="stylesheet" href="/site.css?v=` + legacyEsc(t.Key) + `">`)
	b.WriteString(`</head><body class="vb vb--` + legacyEsc(t.Key) + `">`)

	// Nav
	b.WriteString(`<nav class="vb-nav"><a class="vb-brand" href="/">` + legacyEsc(name) + `</a><div class="vb-nav-links">`)
	b.WriteString(`<a href="#about">About</a><a href="#services">` + legacyEsc(servHead) + `</a>`)
	if len(c.Gallery) > 0 {
		b.WriteString(`<a href="#gallery">Gallery</a>`)
	}
	b.WriteString(`<a href="#contact">Contact</a>`)
	if c.ShowBlog && blogURL != "" {
		b.WriteString(`<a href="` + legacyEsc(blogURL) + `">Blog</a>`)
	}
	b.WriteString(`</div></nav>`)

	// Hero
	b.WriteString(`<header class="vb-hero">`)
	if strings.TrimSpace(c.HeroImg) != "" {
		b.WriteString(`<img class="vb-hero-img" src="` + legacyEsc(c.HeroImg) + `" alt="" loading="lazy">`)
	}
	b.WriteString(`<div class="vb-hero-inner"><span class="vb-eyebrow">` + legacyEsc(t.Eyebrow) + `</span>`)
	b.WriteString(`<h1>` + legacyEsc(name) + `</h1>`)
	if c.Tagline != "" {
		b.WriteString(`<p class="vb-tagline">` + legacyEsc(c.Tagline) + `</p>`)
	}
	if c.CTA != "" {
		link := strings.TrimSpace(c.CTALink)
		if link == "" {
			link = "#contact"
		}
		b.WriteString(`<a class="vb-cta" href="` + legacyEsc(link) + `">` + legacyEsc(c.CTA) + `</a>`)
	}
	b.WriteString(`</div></header><main class="vb-main">`)

	// About
	if strings.TrimSpace(c.About) != "" {
		b.WriteString(`<section class="vb-section" id="about"><h2>About</h2>` + legacyParagraphs(c.About) + `</section>`)
	}

	// Services / menu / programmes
	if len(c.Services) > 0 {
		b.WriteString(`<section class="vb-section" id="services"><h2>` + legacyEsc(servHead) + `</h2><div class="vb-services">`)
		for _, s := range c.Services {
			if strings.TrimSpace(s.Title) == "" {
				continue
			}
			b.WriteString(`<div class="vb-service"><div class="vb-service-head"><h3>` + legacyEsc(s.Title) + `</h3>`)
			if s.Price != "" {
				b.WriteString(`<span class="vb-price">` + legacyEsc(s.Price) + `</span>`)
			}
			b.WriteString(`</div>`)
			if s.Desc != "" {
				b.WriteString(`<p>` + legacyEsc(s.Desc) + `</p>`)
			}
			b.WriteString(`</div>`)
		}
		b.WriteString(`</div></section>`)
	}

	// Gallery
	if len(c.Gallery) > 0 {
		b.WriteString(`<section class="vb-section" id="gallery"><h2>Gallery</h2><div class="vb-gallery">`)
		for _, g := range c.Gallery {
			if strings.TrimSpace(g) == "" {
				continue
			}
			b.WriteString(`<img src="` + legacyEsc(g) + `" alt="" loading="lazy">`)
		}
		b.WriteString(`</div></section>`)
	}

	// Contact
	b.WriteString(`<section class="vb-section vb-contact" id="contact"><h2>Contact</h2><div class="vb-contact-grid">`)
	if c.Phone != "" {
		b.WriteString(`<div><span class="vb-label">Phone</span><a href="tel:` + legacyEsc(strings.ReplaceAll(c.Phone, " ", "")) + `">` + legacyEsc(c.Phone) + `</a></div>`)
	}
	if c.Email != "" {
		b.WriteString(`<div><span class="vb-label">Email</span><a href="mailto:` + legacyEsc(c.Email) + `">` + legacyEsc(c.Email) + `</a></div>`)
	}
	if c.Address != "" {
		b.WriteString(`<div><span class="vb-label">Address</span><span>` + legacyEsc(c.Address) + `</span></div>`)
	}
	if c.Hours != "" {
		b.WriteString(`<div><span class="vb-label">Hours</span><span class="vb-hours">` + legacyEsc(c.Hours) + `</span></div>`)
	}
	b.WriteString(`</div></section></main>`)

	// Footer
	b.WriteString(`<footer class="vb-footer"><span>` + legacyEsc(name) + `</span>`)
	if c.ShowBlog && blogURL != "" {
		b.WriteString(`<a href="` + legacyEsc(blogURL) + `">Blog</a>`)
	}
	b.WriteString(`<span class="vb-powered">Powered by VayuPress</span></footer>`)
	b.WriteString(`</body></html>`)
	return b.String()
}
