// Package emailtmpl provides operator-customisable templates for the
// transactional emails VayuPress sends (magic-link sign-in, comment-approval
// notice, newsletter confirmation, newsletter broadcast wrapper).
//
// Templates are Go text/template strings persisted in the site-settings store
// and cached in memory. Each template ships with a safe built-in default, so an
// operator who customises nothing still gets working emails, and a template
// that fails to parse falls back to its default rather than breaking delivery.
//
// Security: templates render PLAIN TEXT and a separately-escaped HTML body. The
// HTML variants use html/template so interpolated values are auto-escaped; the
// text variants use text/template. Operators editing templates are trusted
// admins, but auto-escaping still prevents accidental breakage from data values.
package emailtmpl

import (
	"bytes"
	htmltmpl "html/template"
	"strings"
	"sync"
	texttmpl "text/template"
)

// Kind identifies a transactional email template.
type Kind string

const (
	MagicLink         Kind = "magic_link"
	CommentApproved   Kind = "comment_approved"
	NewsletterConfirm Kind = "newsletter_confirm"
)

// Rendered is the output of a template: a subject plus text and HTML bodies.
type Rendered struct {
	Subject string
	Text    string
	HTML    string
}

// templateSet bundles the three parseable parts of one email.
type templateSet struct {
	Subject string // text/template
	Text    string // text/template
	HTML    string // html/template
}

// defaults are the built-in templates. {{.Field}} placeholders are resolved
// against the per-kind data maps documented at each call site.
var defaults = map[Kind]templateSet{
	MagicLink: {
		Subject: "Your sign-in link",
		Text:    "Sign in to {{.Domain}} by opening this link (valid {{.TTLMinutes}} minutes):\r\n\r\n{{.Link}}\r\n\r\nIf you did not request this, you can ignore this email.",
		HTML:    `<p>Sign in to <strong>{{.Domain}}</strong>:</p><p><a href="{{.Link}}">Sign in</a> (valid {{.TTLMinutes}} minutes)</p><p style="color:#888;font-size:13px">If you did not request this, you can ignore this email.</p>`,
	},
	CommentApproved: {
		Subject: "Your comment is live",
		Text:    "Hi {{.Author}},\r\n\r\nYour comment on {{.Link}} has been approved and is now live.\r\n\r\nThank you for contributing!",
		HTML:    `<p>Hi <strong>{{.Author}}</strong>,</p><p>Your comment on <a href="{{.Link}}">{{.Slug}}</a> has been approved and is now live.</p><p>Thank you for contributing!</p>`,
	},
	NewsletterConfirm: {
		Subject: "Confirm your subscription",
		Text:    "Confirm your subscription to {{.Domain}} by opening this link:\r\n\r\n{{.Link}}",
		HTML:    `<p>Confirm your subscription to <strong>{{.Domain}}</strong>:</p><p><a href="{{.Link}}">Confirm subscription</a></p>`,
	},
}

// SettingsKey maps a kind+part to the site-settings key used for persistence.
// e.g. SettingsKey(MagicLink, "subject") -> "email.magic_link.subject".
func SettingsKey(k Kind, part string) string {
	return "email." + string(k) + "." + part
}

// Store renders email templates, honouring operator overrides supplied via Set.
type Store struct {
	mu        sync.RWMutex
	overrides map[Kind]templateSet
}

// New returns a Store with no overrides (all kinds use their built-in default).
func New() *Store {
	return &Store{overrides: map[Kind]templateSet{}}
}

// Set installs operator overrides for a kind. Empty fields fall back to the
// built-in default for that part, so an operator can override just the subject.
func (s *Store) Set(k Kind, subject, text, html string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	def := defaults[k]
	set := templateSet{Subject: def.Subject, Text: def.Text, HTML: def.HTML}
	if strings.TrimSpace(subject) != "" {
		set.Subject = subject
	}
	if strings.TrimSpace(text) != "" {
		set.Text = text
	}
	if strings.TrimSpace(html) != "" {
		set.HTML = html
	}
	s.overrides[k] = set
}

// active returns the effective template set for k (override or default).
func (s *Store) active(k Kind) templateSet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if set, ok := s.overrides[k]; ok {
		return set
	}
	return defaults[k]
}

// Render renders kind k against data. Any part that fails to parse or execute
// falls back to the built-in default for that part, so delivery never breaks.
func (s *Store) Render(k Kind, data map[string]interface{}) Rendered {
	set := s.active(k)
	def := defaults[k]
	return Rendered{
		Subject: renderText(set.Subject, def.Subject, data),
		Text:    renderText(set.Text, def.Text, data),
		HTML:    renderHTML(set.HTML, def.HTML, data),
	}
}

// renderText executes a text/template, falling back to fallback on any error.
func renderText(tmpl, fallback string, data map[string]interface{}) string {
	out, err := execText(tmpl, data)
	if err != nil {
		if out2, err2 := execText(fallback, data); err2 == nil {
			return out2
		}
		return fallback
	}
	return out
}

func execText(tmpl string, data map[string]interface{}) (string, error) {
	t, err := texttmpl.New("e").Option("missingkey=zero").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// renderHTML executes an html/template, falling back to fallback on any error.
func renderHTML(tmpl, fallback string, data map[string]interface{}) string {
	out, err := execHTML(tmpl, data)
	if err != nil {
		if out2, err2 := execHTML(fallback, data); err2 == nil {
			return out2
		}
		return fallback
	}
	return out
}

func execHTML(tmpl string, data map[string]interface{}) (string, error) {
	t, err := htmltmpl.New("e").Option("missingkey=zero").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// DefaultSubject exposes the built-in subject for a kind (used by admin UIs to
// show the current default when no override is set).
func DefaultSubject(k Kind) string { return defaults[k].Subject }

// DefaultText exposes the built-in text body for a kind.
func DefaultText(k Kind) string { return defaults[k].Text }

// DefaultHTML exposes the built-in HTML body for a kind.
func DefaultHTML(k Kind) string { return defaults[k].HTML }
