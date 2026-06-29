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
	// Welcome is sent to a new member the first time they confirm their email
	// (the sign-up welcome). It is informational only — it carries no token.
	Welcome Kind = "welcome"
	// PaymentPending is sent when a reader checks out via the direct/offline
	// gateway: it carries the order reference, amount, and the operator's
	// payment instructions so the payer knows exactly how to pay.
	PaymentPending Kind = "payment_pending"
	// PaymentConfirmed is the receipt sent once an order is marked paid: it
	// confirms the tier the payer now has access to and the amount received.
	PaymentConfirmed Kind = "payment_confirmed"
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
		Subject: "🔑 Your magic sign-in link for {{.Domain}}",
		Text:    "Hi there! 👋\r\n\r\nHere's your magic link to sign in to {{.Domain}} — no password needed. ✨\r\n\r\n👉 {{.Link}}\r\n\r\n⏳ This link is valid for {{.TTLMinutes}} minutes and can be used once.\r\n\r\nIf you didn't request this, you can safely ignore this email. 🛡️\r\n\r\nSee you inside! 🚀",
		HTML: `<div style="margin:0;padding:0;background:#f4f6fb;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif">
  <div style="max-width:480px;margin:0 auto;padding:32px 20px">
    <div style="background:#ffffff;border-radius:16px;overflow:hidden;box-shadow:0 4px 24px rgba(20,30,60,0.08)">
      <div style="background:linear-gradient(135deg,#6366f1,#8b5cf6);padding:36px 32px;text-align:center">
        <div style="font-size:44px;line-height:1">🔑</div>
        <h1 style="margin:12px 0 0;color:#ffffff;font-size:22px;font-weight:700">Your magic sign-in link</h1>
      </div>
      <div style="padding:32px">
        <p style="margin:0 0 16px;color:#1f2937;font-size:16px">Hi there! 👋</p>
        <p style="margin:0 0 24px;color:#4b5563;font-size:15px;line-height:1.6">Tap the button below to sign in to <strong>{{.Domain}}</strong> — no password needed. ✨</p>
        <div style="text-align:center;margin:0 0 24px">
          <a href="{{.Link}}" style="display:inline-block;background:linear-gradient(135deg,#6366f1,#8b5cf6);color:#ffffff;text-decoration:none;font-weight:600;font-size:16px;padding:14px 36px;border-radius:999px">🚀 Sign in now</a>
        </div>
        <p style="margin:0 0 8px;color:#6b7280;font-size:13px;text-align:center">⏳ Valid for {{.TTLMinutes}} minutes · one-time use</p>
      </div>
      <div style="padding:18px 32px;background:#f9fafb;border-top:1px solid #eef0f4">
        <p style="margin:0;color:#9ca3af;font-size:12px;line-height:1.5">🛡️ If you didn't request this, you can safely ignore this email — no one can sign in without the link.</p>
      </div>
    </div>
  </div>
</div>`,
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
	Welcome: {
		Subject: "🎉 Welcome to {{.Domain}} — you're officially in!",
		Text:    "🎉 Welcome aboard!\r\n\r\nYou're officially a member of {{.Domain}} — we're so glad you're here. 💜\r\n\r\nHere's what you can do now:\r\n📰 Read the latest stories\r\n🔖 Save and manage your account\r\n💬 Join the conversation in the comments\r\n\r\n👉 Open your account: {{.Link}}\r\n\r\nWhenever you want to sign in, just enter your email and we'll send you a magic link. 🔑✨\r\n\r\nThanks for joining — you're awesome! 🚀",
		HTML: `<div style="margin:0;padding:0;background:#f4f6fb;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif">
  <div style="max-width:480px;margin:0 auto;padding:32px 20px">
    <div style="background:#ffffff;border-radius:16px;overflow:hidden;box-shadow:0 4px 24px rgba(20,30,60,0.08)">
      <div style="background:linear-gradient(135deg,#ec4899,#8b5cf6);padding:40px 32px;text-align:center">
        <div style="font-size:48px;line-height:1">🎉</div>
        <h1 style="margin:12px 0 4px;color:#ffffff;font-size:24px;font-weight:700">Welcome aboard!</h1>
        <p style="margin:0;color:rgba(255,255,255,0.9);font-size:15px">You're officially a member of {{.Domain}} 💜</p>
      </div>
      <div style="padding:32px">
        <p style="margin:0 0 18px;color:#4b5563;font-size:15px;line-height:1.6">We're so glad you're here. Here's what you can do now:</p>
        <table style="width:100%;border-collapse:collapse;margin:0 0 24px">
          <tr><td style="padding:8px 0;font-size:15px;color:#1f2937">📰&nbsp;&nbsp;Read the latest stories</td></tr>
          <tr><td style="padding:8px 0;font-size:15px;color:#1f2937">🔖&nbsp;&nbsp;Save and manage your account</td></tr>
          <tr><td style="padding:8px 0;font-size:15px;color:#1f2937">💬&nbsp;&nbsp;Join the conversation in the comments</td></tr>
        </table>
        <div style="text-align:center;margin:0 0 20px">
          <a href="{{.Link}}" style="display:inline-block;background:linear-gradient(135deg,#ec4899,#8b5cf6);color:#ffffff;text-decoration:none;font-weight:600;font-size:16px;padding:14px 36px;border-radius:999px">✨ Open your account</a>
        </div>
        <p style="margin:0;color:#6b7280;font-size:13px;text-align:center;line-height:1.6">🔑 To sign in any time, just enter your email and we'll send you a magic link — no password to remember!</p>
      </div>
      <div style="padding:18px 32px;background:#f9fafb;border-top:1px solid #eef0f4;text-align:center">
        <p style="margin:0;color:#9ca3af;font-size:12px">Thanks for joining — you're awesome! 🚀</p>
      </div>
    </div>
  </div>
</div>`,
	},
	PaymentPending: {
		Subject: "Complete your {{.TierName}} membership — order {{.Reference}}",
		Text:    "Hi {{.Name}},\r\n\r\nThank you for subscribing to the {{.TierName}} plan on {{.Domain}}.\r\n\r\nAmount due: {{.Amount}} {{.Currency}} ({{.Cadence}})\r\nOrder reference: {{.Reference}}\r\n\r\nPlease complete your payment using the instructions below and quote your order reference so we can match it to your account:\r\n\r\n{{.Instructions}}\r\n\r\nAccess unlocks as soon as we confirm your payment. If you have any questions, reply to this email.",
		HTML:    `<p>Hi <strong>{{.Name}}</strong>,</p><p>Thank you for subscribing to the <strong>{{.TierName}}</strong> plan on {{.Domain}}.</p><p><strong>Amount due:</strong> {{.Amount}} {{.Currency}} ({{.Cadence}})<br><strong>Order reference:</strong> {{.Reference}}</p><p>Please complete your payment using the instructions below and quote your order reference so we can match it to your account:</p><pre style="white-space:pre-wrap;background:#f6f6f6;padding:12px;border-radius:6px">{{.Instructions}}</pre><p style="color:#888;font-size:13px">Access unlocks as soon as we confirm your payment. If you have any questions, just reply to this email.</p>`,
	},
	PaymentConfirmed: {
		Subject: "Payment received — welcome to {{.TierName}}",
		Text:    "Hi {{.Name}},\r\n\r\nWe've received your payment of {{.Amount}} {{.Currency}} for the {{.TierName}} plan on {{.Domain}}. Your membership is now active and all {{.TierName}} content is unlocked.\r\n\r\nOrder reference: {{.Reference}}\r\n\r\nManage your membership any time: {{.Link}}\r\n\r\nThank you for your support!",
		HTML:    `<p>Hi <strong>{{.Name}}</strong>,</p><p>We've received your payment of <strong>{{.Amount}} {{.Currency}}</strong> for the <strong>{{.TierName}}</strong> plan on {{.Domain}}. Your membership is now active and all {{.TierName}} content is unlocked.</p><p><strong>Order reference:</strong> {{.Reference}}</p><p><a href="{{.Link}}">Manage your membership</a></p><p>Thank you for your support!</p>`,
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
