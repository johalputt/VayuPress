// SPDX-License-Identifier: Apache-2.0

package bizsite

// Package bizsite renders the small-business website that VayuPress can serve
// at the root domain alongside the blog (blog.<domain>) and VayuMail
// (mail.<domain>). One content model powers every template; each template is a
// design personality (layout + typography + accent styling) selected and edited
// entirely from VayuOS — no code, no terminal.
//
// The page itself is drawn by internal/sitedoc from a site document; this
// package keeps the designs (catalogue, stylesheets, sample content) and the
// legacy flat content model that sitedoc.FromLegacy reads.

import (
	"encoding/json"
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

// EffectiveContent is the content a site actually renders: its stored content,
// or — when neither a name nor a tagline has been written — the template's own
// sample content. One rule, used by the public page, both editors and the
// sample-content check, so none of them can describe a different site.
func EffectiveContent(t Template, raw string) Content {
	c := ParseContent(raw)
	if c.Name == "" && c.Tagline == "" {
		return t.Defaults
	}
	return c
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
.vb-cta{display:inline-block;background:var(--vb-accent,#0f766e);color:var(--vb-on-accent,#fff) !important;padding:.8rem 2rem;border-radius:var(--vb-radius,8px);font-weight:600}
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
.vb-hero--left{text-align:left}
.vb-hero--left .vb-hero-inner{max-width:72rem;margin:0 auto}
.vb-hero--left .vb-tagline{margin-left:0}
.vb-hero--split{display:grid;grid-template-columns:minmax(0,1.1fr) minmax(0,1fr);gap:2.5rem;align-items:center;text-align:left;max-width:72rem;margin:0 auto;padding:4.5rem 1.5rem}
.vb-hero--split .vb-hero-img{position:static;inset:auto;order:2;width:100%;height:auto;aspect-ratio:4/3;border-radius:var(--vb-radius,10px)}
.vb-hero--split .vb-hero-img+.vb-hero-inner{background:none;padding:0;max-width:none;margin:0}
.vb-hero--split .vb-tagline{margin-left:0}
.vb-services--list{grid-template-columns:1fr;max-width:44rem}
.vb-gallery--wide{grid-template-columns:repeat(auto-fill,minmax(340px,1fr))}
.vb-zoom{display:block;cursor:zoom-in}
.vb-lightbox{max-width:min(92vw,1200px);padding:0;border:0;border-radius:var(--vb-radius,10px);background:var(--vb-surface,#fff);color:var(--vb-text,#1a1a18)}
.vb-lightbox::backdrop{background:rgba(0,0,0,.82)}
.vb-lightbox img{display:block;max-width:100%;max-height:80vh;margin:0 auto}
.vb-lightbox-cap{margin:0;padding:.8rem 1.2rem;font-size:.95rem}
.vb-lightbox-close{position:absolute;top:.5rem;right:.5rem;border:0;border-radius:6px;padding:.4rem .8rem;background:var(--vb-accent,#0f766e);color:var(--vb-on-accent,#fff);font:inherit;cursor:pointer}
@media(max-width:640px){.vb-nav{flex-direction:column;gap:.7rem}.vb-hero{padding:3.5rem 1.25rem 3rem}.vb-section{padding:2.8rem 0}.vb-hero--split{grid-template-columns:1fr}.vb-hero--split .vb-hero-img{order:0}}
@media(prefers-color-scheme:dark){body.vb{--vb-bg:#101210;--vb-surface:#181b18;--vb-text:#eceee9;--vb-line:rgba(255,255,255,.1)}}
`

// DemoFields names the fields of c that still hold a template's sample content.
//
// Every template ships believable sample content so a new site is a complete
// page to edit rather than an empty form — and nothing stopped that sample from
// going live. vayupress.johal.in served Bistro's "Maison Olive", its menu and
// its opening hours as the real business. Checked against EVERY template, not
// just the active one: switching design keeps the content, so Bistro's sample
// can sit under any template. The result is in a stable order.
func DemoFields(c Content) []string {
	same := func(a, b string) bool { a = strings.TrimSpace(a); return a != "" && a == strings.TrimSpace(b) }
	seen := map[string]bool{}
	for _, t := range All() {
		d := t.Defaults
		for field, hit := range map[string]bool{
			"name":    same(c.Name, d.Name),
			"tagline": same(c.Tagline, d.Tagline),
			"about":   same(c.About, d.About),
			"hours":   same(c.Hours, d.Hours),
			"cta":     same(c.CTA, d.CTA),
		} {
			if hit {
				seen[field] = true
			}
		}
		for _, s := range c.Services {
			for _, ds := range d.Services {
				if same(s.Title, ds.Title) {
					seen["services"] = true
				}
			}
		}
	}
	var out []string
	for _, f := range []string{"name", "tagline", "about", "hours", "cta", "services"} {
		if seen[f] {
			out = append(out, f)
		}
	}
	return out
}
