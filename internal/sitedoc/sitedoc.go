// SPDX-License-Identifier: Apache-2.0

// Package sitedoc is a template website as a document: an ordered list of
// pages, each an ordered list of typed sections (ADR-0161).
//
// Every field is plain text or a validated URL, and the renderer escapes all of
// it, so a document cannot carry script, style or markup. That is what keeps a
// site under the install's strict Content-Security-Policy with no per-site
// exception, and what lets the operator, a client and an assistant edit it
// through one validator: whatever reaches Render has passed Validate.
package sitedoc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Version is the document schema version written by this package.
const Version = 1

// Limits (ADR-0161 §3). Generous for a small-business site, and small enough
// that a document is always cheap to parse, store per revision and render.
const (
	MaxBytes    = 256 << 10
	MaxPages    = 20
	MaxSections = 40
	MaxItems    = 60
)

// Kind names what a section is. Each kind has its own fields; a field that
// belongs to another kind is refused rather than ignored, so an editor or an
// assistant learns at once that what it wrote will not be shown.
type Kind string

// The section kinds.
const (
	KindHero    Kind = "hero"
	KindText    Kind = "text"
	KindItems   Kind = "items"
	KindGallery Kind = "gallery"
	KindContact Kind = "contact"
)

// Document is a whole site.
type Document struct {
	V int `json:"v"`
	// Name is the business: the brand in the nav, the footer, and every
	// page's title.
	Name string `json:"name"`
	// ShowBlog links the blog from the nav and footer.
	ShowBlog bool `json:"show_blog,omitempty"`
	// Style is the site's own brand over its design; nil keeps the design's.
	Style *Style `json:"style,omitempty"`
	Pages []Page `json:"pages"`
}

// Style is a site's brand: the few choices that make a design its own. Each
// is a closed choice or a validated colour, never CSS, so the stylesheet
// built from it (StyleCSS) cannot carry anything but these values.
type Style struct {
	// Accent is the brand colour, #rrggbb: buttons, links, prices.
	Accent string `json:"accent,omitempty"`
	// Font is a typeface family from Fonts; "" keeps the design's.
	Font string `json:"font,omitempty"`
	// Corners is a rounding from Corners; "" keeps the design's.
	Corners string `json:"corners,omitempty"`
}

// Page is one address on the site. The home page has the empty slug; every
// other page is served at /<slug>.
type Page struct {
	Slug        string    `json:"slug"`
	Title       string    `json:"title,omitempty"`
	Description string    `json:"description,omitempty"`
	InNav       bool      `json:"in_nav,omitempty"`
	Sections    []Section `json:"sections"`
}

// Section is one block of a page. Which fields apply depends on Kind.
type Section struct {
	ID   string `json:"id"`
	Kind Kind   `json:"kind"`
	// Nav, when set, lists this section in its page's navigation under that
	// label, as an in-page link.
	Nav string `json:"nav,omitempty"`

	Heading string `json:"heading,omitempty"` // every kind; hero: defaults to the site name
	Eyebrow string `json:"eyebrow,omitempty"` // hero: defaults to the design's label
	Body    string `json:"body,omitempty"`    // hero (the tagline), text
	CTA     string `json:"cta,omitempty"`     // hero
	CTALink string `json:"cta_link,omitempty"`
	Image   *Image `json:"image,omitempty"` // hero

	Items  []Item  `json:"items,omitempty"`  // items
	Images []Image `json:"images,omitempty"` // gallery

	Phone   string `json:"phone,omitempty"` // contact
	Email   string `json:"email,omitempty"`
	Address string `json:"address,omitempty"`
	Hours   string `json:"hours,omitempty"`
	// Form shows a message form that posts to the install's contact endpoint.
	Form bool `json:"form,omitempty"`

	// Variant is the section's layout, from Variants[Kind]; "" is the first.
	Variant string `json:"variant,omitempty"`
}

// Variants are the layouts each kind of section offers. The first is what an
// empty Variant means.
var Variants = map[Kind][]string{
	KindHero:    {"center", "left", "split"},
	KindText:    {"standard"},
	KindItems:   {"grid", "list"},
	KindGallery: {"grid", "wide"},
	KindContact: {"standard"},
}

// Item is one offering: a dish, a product, a course, a treatment.
type Item struct {
	Title string `json:"title"`
	Desc  string `json:"desc,omitempty"`
	Price string `json:"price,omitempty"`
}

// Image is a picture and its text alternative.
type Image struct {
	Src string `json:"src"`
	Alt string `json:"alt,omitempty"`
}

// Home returns the home page. A validated document always has one.
func (d Document) Home() (Page, bool) { return d.Page("") }

// Page returns the page served at slug.
func (d Document) Page(slug string) (Page, bool) {
	for _, p := range d.Pages {
		if p.Slug == slug {
			return p, true
		}
	}
	return Page{}, false
}

// FieldError names the field that failed and why, in the form an editor can
// point at: pages[1].sections[0].images[2].alt.
type FieldError struct {
	Path string
	Msg  string
}

func (e *FieldError) Error() string { return e.Path + ": " + e.Msg }

func fail(path, format string, a ...any) error {
	return &FieldError{Path: path, Msg: fmt.Sprintf(format, a...)}
}

// Parse decodes and validates a stored or submitted document. Unknown fields
// are refused for the reason kind-foreign fields are: a misspelt field that
// is silently dropped is content that silently vanishes.
func Parse(raw []byte) (Document, error) {
	if len(raw) > MaxBytes {
		return Document{}, fail("document", "is %d bytes; the limit is %d", len(raw), MaxBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var d Document
	if err := dec.Decode(&d); err != nil {
		return Document{}, fail("document", "is not a valid site document: %v", err)
	}
	if dec.More() {
		return Document{}, fail("document", "has content after the document")
	}
	if err := Validate(d); err != nil {
		return Document{}, err
	}
	return d, nil
}

// Marshal encodes a validated document for storage.
func Marshal(d Document) ([]byte, error) {
	d.V = Version
	if err := Validate(d); err != nil {
		return nil, err
	}
	b, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	if len(b) > MaxBytes {
		return nil, fail("document", "is %d bytes; the limit is %d", len(b), MaxBytes)
	}
	return b, nil
}

var (
	slugRE      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	sectionIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
)

// Validate reports the first thing wrong with d, or nil.
func Validate(d Document) error {
	if d.V != Version {
		return fail("v", "must be %d", Version)
	}
	if err := line("name", d.Name, 120); err != nil {
		return err
	}
	if d.Style != nil {
		if err := validateStyle(*d.Style); err != nil {
			return err
		}
	}
	if len(d.Pages) == 0 {
		return fail("pages", "a site needs a home page")
	}
	if len(d.Pages) > MaxPages {
		return fail("pages", "has %d pages; the limit is %d", len(d.Pages), MaxPages)
	}
	seen := map[string]bool{}
	for i, p := range d.Pages {
		at := fmt.Sprintf("pages[%d]", i)
		if p.Slug != "" && !slugRE.MatchString(p.Slug) {
			return fail(at+".slug", "%q must be lowercase letters, digits and hyphens, starting with a letter or digit, at most 63", p.Slug)
		}
		if seen[p.Slug] {
			return fail(at+".slug", "%q is used by another page", p.Slug)
		}
		seen[p.Slug] = true
		if err := validatePage(at, p); err != nil {
			return err
		}
	}
	if !seen[""] {
		return fail("pages", "a site needs a home page (the page with an empty slug)")
	}
	return nil
}

func validatePage(at string, p Page) error {
	if err := line(at+".title", p.Title, 120); err != nil {
		return err
	}
	if err := line(at+".description", p.Description, 300); err != nil {
		return err
	}
	if len(p.Sections) > MaxSections {
		return fail(at+".sections", "has %d sections; the limit is %d", len(p.Sections), MaxSections)
	}
	ids := map[string]bool{}
	forms := 0
	for j, s := range p.Sections {
		sat := fmt.Sprintf("%s.sections[%d]", at, j)
		if !sectionIDRE.MatchString(s.ID) {
			return fail(sat+".id", "%q must be lowercase letters, digits and hyphens, at most 40", s.ID)
		}
		if ids[s.ID] {
			return fail(sat+".id", "%q is used by another section on this page", s.ID)
		}
		ids[s.ID] = true
		// A hero is the page's header, rendered above its main content. One,
		// and first, or the page's structure would contradict its markup.
		if s.Kind == KindHero && j != 0 {
			return fail(sat+".kind", "a hero must be the first section of its page")
		}
		if s.Form {
			forms++
			// The form is found by a fixed element id; a second would be dead.
			if forms > 1 {
				return fail(sat+".form", "a page can carry one contact form")
			}
		}
		if err := validateSection(sat, s); err != nil {
			return err
		}
	}
	return nil
}

func validateSection(at string, s Section) error {
	if err := line(at+".nav", s.Nav, 40); err != nil {
		return err
	}
	if err := line(at+".heading", s.Heading, 200); err != nil {
		return err
	}
	used := map[string]bool{
		"eyebrow": s.Eyebrow != "", "body": s.Body != "", "cta": s.CTA != "",
		"cta_link": s.CTALink != "", "image": s.Image != nil, "items": len(s.Items) > 0,
		"images": len(s.Images) > 0, "phone": s.Phone != "", "email": s.Email != "",
		"address": s.Address != "", "hours": s.Hours != "", "form": s.Form,
	}
	var allowed []string
	switch s.Kind {
	case KindHero:
		allowed = []string{"eyebrow", "body", "cta", "cta_link", "image"}
	case KindText:
		allowed = []string{"body"}
	case KindItems:
		allowed = []string{"items"}
	case KindGallery:
		allowed = []string{"images"}
	case KindContact:
		allowed = []string{"phone", "email", "address", "hours", "form"}
	default:
		return fail(at+".kind", "%q is not a section kind (hero, text, items, gallery, contact)", s.Kind)
	}
	for _, f := range []string{"eyebrow", "body", "cta", "cta_link", "image", "items", "images", "phone", "email", "address", "hours", "form"} {
		if used[f] && !contains(allowed, f) {
			return fail(at+"."+f, "is not a field of a %s section", s.Kind)
		}
	}

	if s.Variant != "" && !contains(Variants[s.Kind], s.Variant) {
		return fail(at+".variant", "%q is not a layout of a %s section (%s)", s.Variant, s.Kind, strings.Join(Variants[s.Kind], ", "))
	}

	checks := []error{
		line(at+".eyebrow", s.Eyebrow, 80),
		text(at+".body", s.Body, 20000),
		line(at+".cta", s.CTA, 80),
		link(at+".cta_link", s.CTALink),
		line(at+".phone", s.Phone, 60),
		line(at+".email", s.Email, 254),
		text(at+".address", s.Address, 500),
		text(at+".hours", s.Hours, 500),
	}
	for _, err := range checks {
		if err != nil {
			return err
		}
	}
	// The hero's body is its tagline, one line under the heading; line breaks
	// in it would be collapsed by the page, so they are refused here instead.
	if s.Kind == KindHero {
		if err := line(at+".body", s.Body, 300); err != nil {
			return err
		}
	}
	if s.CTALink != "" && s.CTA == "" {
		return fail(at+".cta", "a button link needs a button label")
	}
	if s.Email != "" && !plausibleEmail(s.Email) {
		return fail(at+".email", "%q is not an email address", s.Email)
	}
	if s.Image != nil {
		if err := image(at+".image", *s.Image, false); err != nil {
			return err
		}
	}
	if len(s.Items) > MaxItems {
		return fail(at+".items", "has %d items; the limit is %d", len(s.Items), MaxItems)
	}
	for k, it := range s.Items {
		iat := fmt.Sprintf("%s.items[%d]", at, k)
		if strings.TrimSpace(it.Title) == "" {
			return fail(iat+".title", "is required")
		}
		for _, err := range []error{line(iat+".title", it.Title, 200), text(iat+".desc", it.Desc, 2000), line(iat+".price", it.Price, 40)} {
			if err != nil {
				return err
			}
		}
	}
	if len(s.Images) > MaxItems {
		return fail(at+".images", "has %d images; the limit is %d", len(s.Images), MaxItems)
	}
	for k, img := range s.Images {
		// A gallery picture is content, so it must say what it shows. The hero
		// image may be decorative and is exempt: an empty alt is the correct
		// markup for decoration, and a forced one would be noise to a screen
		// reader.
		if err := image(fmt.Sprintf("%s.images[%d]", at, k), img, true); err != nil {
			return err
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// line is a single-line field: no line breaks, no control characters.
func line(path, s string, max int) error {
	if strings.ContainsAny(s, "\r\n") {
		return fail(path, "must be a single line")
	}
	return text(path, s, max)
}

// text is a field that may hold line breaks. Other control characters are
// refused: they render as nothing, so text that contains them is not the text
// the page shows.
func text(path, s string, max int) error {
	if !utf8.ValidString(s) {
		return fail(path, "is not valid UTF-8")
	}
	if n := utf8.RuneCountInString(s); n > max {
		return fail(path, "is %d characters; the limit is %d", n, max)
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return fail(path, "contains a control character (U+%04X)", r)
		}
	}
	return nil
}

// link accepts what a visitor's click can safely follow: a web address, a
// mail or phone link, a path on this site, or a fragment on this page. Not
// javascript:, not data:, and not a protocol-relative //host, which reads as a
// site path and leaves the site.
func link(path, s string) error {
	if s == "" {
		return nil
	}
	if err := line(path, s, 2000); err != nil {
		return err
	}
	if strings.ContainsAny(s, " \t") {
		return fail(path, "%q contains spaces", s)
	}
	switch {
	case strings.HasPrefix(s, "#"):
		return nil
	case strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//") && !strings.HasPrefix(s, "/\\"):
		return nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return fail(path, "%q is not a link", s)
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "http":
		if u.Host == "" {
			return fail(path, "%q has no host", s)
		}
		return nil
	case "mailto", "tel":
		if u.Opaque == "" {
			return fail(path, "%q is empty", s)
		}
		return nil
	}
	return fail(path, "%q must start with https://, http://, mailto:, tel:, / or #", s)
}

// image accepts a picture on this site (/…) or on the web over https.
func image(path string, img Image, needAlt bool) error {
	if err := line(path+".alt", img.Alt, 300); err != nil {
		return err
	}
	if needAlt && strings.TrimSpace(img.Alt) == "" {
		return fail(path+".alt", "describe what the picture shows — it is what a screen reader says and what shows if it fails to load")
	}
	src := img.Src
	if err := line(path+".src", src, 2000); err != nil {
		return err
	}
	if src == "" {
		return fail(path+".src", "is required")
	}
	if strings.ContainsAny(src, " \t") {
		return fail(path+".src", "%q contains spaces", src)
	}
	if strings.HasPrefix(src, "/") && !strings.HasPrefix(src, "//") && !strings.HasPrefix(src, "/\\") {
		return nil
	}
	if u, err := url.Parse(src); err == nil && strings.EqualFold(u.Scheme, "https") && u.Host != "" {
		return nil
	}
	return fail(path+".src", "%q must be a path on this site (/media/…) or an https:// address", src)
}

func plausibleEmail(s string) bool {
	at := strings.LastIndexByte(s, '@')
	return at > 0 && at < len(s)-1 && !strings.ContainsAny(s, " <>\"") && strings.Contains(s[at:], ".")
}
