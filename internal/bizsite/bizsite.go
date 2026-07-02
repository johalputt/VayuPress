package bizsite

// Package bizsite renders the small-business website that VayuPress can serve
// at the root domain alongside the blog (blog.<domain>) and VayuMail
// (mail.<domain>). One content model powers every template; each template is a
// design personality (layout + typography + accent styling) selected and edited
// entirely from VayuOS — no code, no terminal.
//
// Rendering is CSP-strict: no inline styles or scripts; the stylesheet is
// served same-origin at /site.css (base CSS + the active template's CSS).
// Every operator string is HTML-escaped at this render barrier.

import (
	"encoding/json"
	"html"
	"strings"
)

// Service is one offering row (a dish, product, course, treatment, …).
type Service struct {
	Title string `json:"title"`
	Desc  string `json:"desc"`
	Price string `json:"price,omitempty"`
}

// Content is the operator-editable content for the business site.
type Content struct {
	Name     string    `json:"name"`
	Tagline  string    `json:"tagline"`
	About    string    `json:"about"`
	Phone    string    `json:"phone,omitempty"`
	Email    string    `json:"email,omitempty"`
	Address  string    `json:"address,omitempty"`
	Hours    string    `json:"hours,omitempty"`
	CTA      string    `json:"cta,omitempty"`      // hero button label
	CTALink  string    `json:"ctaLink,omitempty"`  // hero button target
	HeroImg  string    `json:"heroImg,omitempty"`  // optional hero image URL
	Services []Service `json:"services,omitempty"` // offerings grid
	Gallery  []string  `json:"gallery,omitempty"`  // image URLs
	SectionA string    `json:"sectionA,omitempty"` // services heading override
	ShowBlog bool      `json:"showBlog"`           // link the blog in nav/footer
}

// ParseContent decodes stored JSON, tolerating empty input.
func ParseContent(raw string) Content {
	var c Content
	if strings.TrimSpace(raw) == "" {
		return c
	}
	_ = json.Unmarshal([]byte(raw), &c)
	return c
}

// esc is a short alias for the HTML-escape barrier.
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

// Render returns the complete business-site HTML page. blogURL ("" hides the
// blog link) and nonce feed the shared chrome; the template only shapes CSS.
func Render(t Template, c Content, blogURL string) string {
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
	b.WriteString(`<title>` + esc(name) + ` — ` + esc(c.Tagline) + `</title>`)
	b.WriteString(`<meta name="description" content="` + esc(c.Tagline) + `">`)
	b.WriteString(`<link rel="stylesheet" href="/site.css">`)
	b.WriteString(`</head><body class="vb vb--` + esc(t.Key) + `">`)

	// Nav
	b.WriteString(`<nav class="vb-nav"><a class="vb-brand" href="/">` + esc(name) + `</a><div class="vb-nav-links">`)
	b.WriteString(`<a href="#about">About</a><a href="#services">` + esc(servHead) + `</a>`)
	if len(c.Gallery) > 0 {
		b.WriteString(`<a href="#gallery">Gallery</a>`)
	}
	b.WriteString(`<a href="#contact">Contact</a>`)
	if c.ShowBlog && blogURL != "" {
		b.WriteString(`<a href="` + esc(blogURL) + `">Blog</a>`)
	}
	b.WriteString(`</div></nav>`)

	// Hero
	b.WriteString(`<header class="vb-hero">`)
	if strings.TrimSpace(c.HeroImg) != "" {
		b.WriteString(`<img class="vb-hero-img" src="` + esc(c.HeroImg) + `" alt="" loading="lazy">`)
	}
	b.WriteString(`<div class="vb-hero-inner"><span class="vb-eyebrow">` + esc(t.Eyebrow) + `</span>`)
	b.WriteString(`<h1>` + esc(name) + `</h1>`)
	if c.Tagline != "" {
		b.WriteString(`<p class="vb-tagline">` + esc(c.Tagline) + `</p>`)
	}
	if c.CTA != "" {
		link := strings.TrimSpace(c.CTALink)
		if link == "" {
			link = "#contact"
		}
		b.WriteString(`<a class="vb-cta" href="` + esc(link) + `">` + esc(c.CTA) + `</a>`)
	}
	b.WriteString(`</div></header><main class="vb-main">`)

	// About
	if strings.TrimSpace(c.About) != "" {
		b.WriteString(`<section class="vb-section" id="about"><h2>About</h2>` + paragraphs(c.About) + `</section>`)
	}

	// Services / menu / programmes
	if len(c.Services) > 0 {
		b.WriteString(`<section class="vb-section" id="services"><h2>` + esc(servHead) + `</h2><div class="vb-services">`)
		for _, s := range c.Services {
			if strings.TrimSpace(s.Title) == "" {
				continue
			}
			b.WriteString(`<div class="vb-service"><div class="vb-service-head"><h3>` + esc(s.Title) + `</h3>`)
			if s.Price != "" {
				b.WriteString(`<span class="vb-price">` + esc(s.Price) + `</span>`)
			}
			b.WriteString(`</div>`)
			if s.Desc != "" {
				b.WriteString(`<p>` + esc(s.Desc) + `</p>`)
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
			b.WriteString(`<img src="` + esc(g) + `" alt="" loading="lazy">`)
		}
		b.WriteString(`</div></section>`)
	}

	// Contact
	b.WriteString(`<section class="vb-section vb-contact" id="contact"><h2>Contact</h2><div class="vb-contact-grid">`)
	if c.Phone != "" {
		b.WriteString(`<div><span class="vb-label">Phone</span><a href="tel:` + esc(strings.ReplaceAll(c.Phone, " ", "")) + `">` + esc(c.Phone) + `</a></div>`)
	}
	if c.Email != "" {
		b.WriteString(`<div><span class="vb-label">Email</span><a href="mailto:` + esc(c.Email) + `">` + esc(c.Email) + `</a></div>`)
	}
	if c.Address != "" {
		b.WriteString(`<div><span class="vb-label">Address</span><span>` + esc(c.Address) + `</span></div>`)
	}
	if c.Hours != "" {
		b.WriteString(`<div><span class="vb-label">Hours</span><span class="vb-hours">` + esc(c.Hours) + `</span></div>`)
	}
	b.WriteString(`</div></section></main>`)

	// Footer
	b.WriteString(`<footer class="vb-footer"><span>` + esc(name) + `</span>`)
	if c.ShowBlog && blogURL != "" {
		b.WriteString(`<a href="` + esc(blogURL) + `">Blog</a>`)
	}
	b.WriteString(`<span class="vb-powered">Powered by VayuPress</span></footer>`)
	b.WriteString(`</body></html>`)
	return b.String()
}

// CSS returns the full stylesheet for a template: shared base + personality.
func CSS(t Template) string { return baseCSS + t.CSS }

// baseCSS is the shared, template-agnostic layout. Clean and modern-minimal:
// flat colour, hairline rules, generous whitespace — no gradients, no glows.
const baseCSS = `*,*::before,*::after{box-sizing:border-box}
body.vb{margin:0;font-family:system-ui,-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;background:var(--vb-bg,#fcfcfa);color:var(--vb-text,#1a1a18);line-height:1.65;-webkit-font-smoothing:antialiased}
.vb a{color:var(--vb-accent,#0f766e);text-decoration:none}
.vb a:hover{text-decoration:underline}
.vb-nav{display:flex;align-items:center;justify-content:space-between;gap:1rem;max-width:72rem;margin:0 auto;padding:1.3rem 1.5rem}
.vb-brand{font-weight:700;font-size:1.1rem;color:var(--vb-text,#1a1a18) !important;letter-spacing:-.01em}
.vb-nav-links{display:flex;gap:1.4rem;font-size:.95rem;flex-wrap:wrap}
.vb-nav-links a{color:var(--vb-text,#1a1a18);opacity:.75}
.vb-nav-links a:hover{opacity:1;text-decoration:none;color:var(--vb-accent,#0f766e)}
.vb-hero{position:relative;text-align:center;padding:5.5rem 1.5rem 5rem;border-bottom:1px solid var(--vb-line,rgba(0,0,0,.08))}
.vb-hero-img{position:absolute;inset:0;width:100%;height:100%;object-fit:cover;z-index:0}
.vb-hero-img+.vb-hero-inner{position:relative;z-index:1;background:color-mix(in srgb,var(--vb-bg,#fcfcfa) 82%,transparent);padding:2.5rem;border-radius:12px;max-width:44rem;margin:0 auto}
.vb-eyebrow{display:block;font-size:.72rem;letter-spacing:.22em;text-transform:uppercase;color:var(--vb-accent,#0f766e);margin-bottom:1rem}
.vb-hero h1{font-size:clamp(2.4rem,6vw,4rem);margin:0 0 .8rem;letter-spacing:-.03em;line-height:1.08}
.vb-tagline{font-size:1.15rem;opacity:.72;max-width:38rem;margin:0 auto 2rem}
.vb-cta{display:inline-block;background:var(--vb-accent,#0f766e);color:#fff !important;padding:.8rem 2rem;border-radius:var(--vb-radius,8px);font-weight:600}
.vb-cta:hover{text-decoration:none;opacity:.92}
.vb-main{max-width:72rem;margin:0 auto;padding:0 1.5rem}
.vb-section{padding:4rem 0;border-bottom:1px solid var(--vb-line,rgba(0,0,0,.07))}
.vb-section h2{font-size:1.7rem;letter-spacing:-.02em;margin:0 0 1.6rem}
.vb-section p{max-width:44rem;opacity:.85}
.vb-services{display:grid;grid-template-columns:repeat(auto-fill,minmax(260px,1fr));gap:1.4rem}
.vb-service{border:1px solid var(--vb-line,rgba(0,0,0,.09));border-radius:var(--vb-radius,10px);padding:1.4rem 1.5rem;background:var(--vb-surface,#fff)}
.vb-service-head{display:flex;align-items:baseline;justify-content:space-between;gap:.8rem}
.vb-service h3{margin:0 0 .4rem;font-size:1.08rem;letter-spacing:-.01em}
.vb-service p{margin:.3rem 0 0;font-size:.95rem;opacity:.75}
.vb-price{font-weight:700;color:var(--vb-accent,#0f766e);white-space:nowrap}
.vb-gallery{display:grid;grid-template-columns:repeat(auto-fill,minmax(220px,1fr));gap:1rem}
.vb-gallery img{width:100%;aspect-ratio:4/3;object-fit:cover;border-radius:var(--vb-radius,10px)}
.vb-contact-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(220px,1fr));gap:1.6rem}
.vb-label{display:block;font-size:.72rem;letter-spacing:.14em;text-transform:uppercase;opacity:.55;margin-bottom:.3rem}
.vb-hours{white-space:pre-line}
.vb-footer{display:flex;align-items:center;justify-content:center;gap:1.6rem;padding:2.4rem 1.5rem;font-size:.9rem;opacity:.75;flex-wrap:wrap}
.vb-powered{opacity:.6}
@media(max-width:640px){.vb-nav{flex-direction:column;gap:.7rem}.vb-hero{padding:3.5rem 1.25rem 3rem}.vb-section{padding:2.8rem 0}}
@media(prefers-color-scheme:dark){body.vb{--vb-bg:#101210;--vb-surface:#181b18;--vb-text:#eceee9;--vb-line:rgba(255,255,255,.1)}}
`
