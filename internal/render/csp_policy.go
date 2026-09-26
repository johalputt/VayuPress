// SPDX-License-Identifier: Apache-2.0

package render

// csp_policy.go — a site's own outside services, merged into its CSP.
//
// The baseline stays strict and stays the default. A site may add what it
// needs, on one rule: passive content (styles, fonts, images, audio and video,
// embeds, form destinations) from any origin the operator names; code only
// from the services in cspServices, whose origins are fixed here. A typed
// script origin is refused — above all a public CDN, which serves anyone's
// code, so allowing it lets any injection load a script of its choosing and
// quietly cancels most of what the policy is for.

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Policy modes.
const (
	PolicyStrict = "strict" // the baseline, byte for byte
	PolicyCustom = "custom" // the baseline plus the site's allowances
	PolicyReport = "report" // allowances, sent Report-Only: nothing is blocked
)

// ReportOnlyFor is how long report-only lasts before the site's allowances
// are enforced again. A mode that blocks nothing must not be forgotten on.
const ReportOnlyFor = 7 * 24 * time.Hour

// SitePolicy is one site's outside services.
type SitePolicy struct {
	Mode     string              `json:"mode"`
	Services []string            `json:"services,omitempty"`
	Sources  map[string][]string `json:"sources,omitempty"`
	// InlineStyles adds style-src 'unsafe-inline', which many embedded widgets
	// need. Styles cannot run code; script inline is never offered.
	InlineStyles bool `json:"inline_styles,omitempty"`
	// ReportUntil is when report-only ends. Set on save, never by the client.
	ReportUntil time.Time `json:"report_until,omitempty"`
}

// CustomDirectives are the directives an operator may add origins to by hand,
// in the order the console lists them. Each loads passive content.
var CustomDirectives = []string{"style-src", "font-src", "img-src", "media-src", "frame-src", "form-action"}

// CSPService is one vetted outside service.
type CSPService struct {
	ID, Name string
	Sources  map[string][]string
}

// RunsCode reports whether the service loads script, which the console says
// on its row.
func (s CSPService) RunsCode() bool { return len(s.Sources["script-src"]) > 0 }

// cspServices is the catalogue. Every origin here is reviewed and fixed in
// code; the console offers these, and only these, as a way to allow scripts.
var cspServices = []CSPService{
	{ID: "google-fonts", Name: "Google Fonts", Sources: map[string][]string{
		"style-src": {"https://fonts.googleapis.com"}, "font-src": {"https://fonts.gstatic.com"}}},
	{ID: "google-analytics", Name: "Google Analytics", Sources: map[string][]string{
		"script-src":  {"https://www.googletagmanager.com"},
		"connect-src": {"https://*.google-analytics.com", "https://*.analytics.google.com", "https://*.googletagmanager.com"}}},
	{ID: "google-tag-manager", Name: "Google Tag Manager", Sources: map[string][]string{
		"script-src":  {"https://www.googletagmanager.com"},
		"connect-src": {"https://*.googletagmanager.com"},
		"frame-src":   {"https://www.googletagmanager.com"}}},
	{ID: "microsoft-clarity", Name: "Microsoft Clarity", Sources: map[string][]string{
		"script-src":  {"https://www.clarity.ms", "https://*.clarity.ms"},
		"connect-src": {"https://*.clarity.ms"}}},
	{ID: "plausible", Name: "Plausible Analytics", Sources: map[string][]string{
		"script-src": {"https://plausible.io"}, "connect-src": {"https://plausible.io"}}},
	{ID: "youtube", Name: "YouTube", Sources: map[string][]string{
		"frame-src": {"https://www.youtube.com", "https://www.youtube-nocookie.com"}}},
	{ID: "vimeo", Name: "Vimeo", Sources: map[string][]string{
		"frame-src": {"https://player.vimeo.com"}}},
	{ID: "stripe", Name: "Stripe", Sources: map[string][]string{
		"script-src":  {"https://js.stripe.com"},
		"frame-src":   {"https://js.stripe.com", "https://hooks.stripe.com"},
		"connect-src": {"https://api.stripe.com"}}},
	{ID: "cloudflare-turnstile", Name: "Cloudflare Turnstile", Sources: map[string][]string{
		"script-src": {"https://challenges.cloudflare.com"}, "frame-src": {"https://challenges.cloudflare.com"}}},
	{ID: "hcaptcha", Name: "hCaptcha", Sources: map[string][]string{
		"script-src":  {"https://hcaptcha.com", "https://*.hcaptcha.com"},
		"frame-src":   {"https://hcaptcha.com", "https://*.hcaptcha.com"},
		"style-src":   {"https://hcaptcha.com", "https://*.hcaptcha.com"},
		"connect-src": {"https://hcaptcha.com", "https://*.hcaptcha.com"}}},
	{ID: "calendly", Name: "Calendly", Sources: map[string][]string{
		"script-src": {"https://assets.calendly.com"}, "style-src": {"https://assets.calendly.com"},
		"frame-src": {"https://calendly.com"}}},
	{ID: "typeform", Name: "Typeform", Sources: map[string][]string{
		"script-src": {"https://embed.typeform.com"}, "style-src": {"https://embed.typeform.com"},
		"frame-src": {"https://form.typeform.com"}}},
	{ID: "crisp", Name: "Crisp chat", Sources: map[string][]string{
		"script-src":  {"https://client.crisp.chat"},
		"style-src":   {"https://client.crisp.chat"},
		"font-src":    {"https://client.crisp.chat"},
		"img-src":     {"https://client.crisp.chat", "https://image.crisp.chat"},
		"connect-src": {"https://client.crisp.chat", "wss://client.relay.crisp.chat"}}},
	{ID: "mailchimp", Name: "Mailchimp signup forms", Sources: map[string][]string{
		"form-action": {"https://*.list-manage.com"}}},
}

// CSPServices returns the catalogue, for the console.
func CSPServices() []CSPService { return cspServices }

func serviceByID(id string) (CSPService, bool) {
	for _, s := range cspServices {
		if s.ID == id {
			return s, true
		}
	}
	return CSPService{}, false
}

// hostPattern is a DNS name: labels of letters, digits and hyphens, a dot
// between each, a letter somewhere in the last. An optional "*." may lead.
var hostPattern = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidateSource checks one origin an operator typed for a directive. The
// grammar is closed: https://host, https://host:port, or https://*.host.
// Anything else is refused, each with the reason the console shows.
func ValidateSource(directive, src string) error {
	custom := false
	for _, d := range CustomDirectives {
		custom = custom || d == directive
	}
	switch {
	case directive == "script-src" || directive == "connect-src":
		return errors.New("scripts and their connections come only from the listed services, which are reviewed; a typed origin could load anyone's code")
	case !custom:
		return fmt.Errorf("%q is not a directive a site can add to", directive)
	}
	if strings.ContainsAny(src, "'\"; ,\t\n") {
		return errors.New("an origin is one address: no quotes, spaces, commas or semicolons")
	}
	rest, ok := strings.CutPrefix(src, "https://")
	if !ok {
		return errors.New("an origin starts with https://")
	}
	if strings.ContainsAny(rest, "/?#@") {
		return errors.New("an origin is a host only, with no path")
	}
	host := rest
	if h, port, err := net.SplitHostPort(rest); err == nil {
		if n := len(port); n == 0 || n > 5 || strings.Trim(port, "0123456789") != "" {
			return errors.New("the port must be a number")
		}
		host = h
	}
	if net.ParseIP(strings.Trim(host, "[]")) != nil {
		return errors.New("an origin is a name, not an IP address")
	}
	if len(host) > 253 || !hostPattern.MatchString(host) {
		return errors.New("that is not a host name (a * may only lead, as in *.example.com)")
	}
	return nil
}

// Normalize validates a policy as it is saved: a known mode, known services,
// valid sources, each list sorted and without repeats. It reports the first
// problem in the operator's words.
func (p SitePolicy) Normalize() (SitePolicy, error) {
	out := SitePolicy{Mode: p.Mode, InlineStyles: p.InlineStyles, ReportUntil: p.ReportUntil}
	switch p.Mode {
	case PolicyStrict, PolicyCustom, PolicyReport:
	case "":
		out.Mode = PolicyStrict
	default:
		return SitePolicy{}, fmt.Errorf("unknown mode %q", p.Mode)
	}
	for _, id := range p.Services {
		if _, ok := serviceByID(id); !ok {
			return SitePolicy{}, fmt.Errorf("unknown service %q", id)
		}
	}
	out.Services = dedupeSorted(p.Services)
	for dir, list := range p.Sources {
		for _, src := range list {
			src = strings.ToLower(strings.TrimSpace(src))
			if err := ValidateSource(dir, src); err != nil {
				return SitePolicy{}, fmt.Errorf("%s %s: %w", dir, src, err)
			}
			if out.Sources == nil {
				out.Sources = map[string][]string{}
			}
			out.Sources[dir] = append(out.Sources[dir], src)
		}
		if len(out.Sources[dir]) > 0 {
			out.Sources[dir] = dedupeSorted(out.Sources[dir])
		}
	}
	return out, nil
}

// Enforced is the mode in force at now: report-only past its end is custom.
func (p SitePolicy) Enforced(now time.Time) string {
	if p.Mode == PolicyReport && !now.Before(p.ReportUntil) {
		return PolicyCustom
	}
	return p.Mode
}

// additions is every origin the policy adds, by directive.
func (p SitePolicy) additions() map[string][]string {
	add := map[string][]string{}
	for _, id := range p.Services {
		if s, ok := serviceByID(id); ok {
			for d, list := range s.Sources {
				add[d] = append(add[d], list...)
			}
		}
	}
	for d, list := range p.Sources {
		for _, src := range list {
			// A stored policy is trusted no further than one being saved.
			if ValidateSource(d, src) == nil {
				add[d] = append(add[d], src)
			}
		}
	}
	if p.InlineStyles {
		add["style-src"] = append(add["style-src"], "'unsafe-inline'")
	}
	return add
}

// Apply merges the policy into a Content-Security-Policy header value that a
// handler has already built — the baseline, an embed page's, an ad page's or
// an eval-opted bundle's — so every page type gains the same allowances and
// nothing else changes. A strict policy returns the header untouched. A
// directive the header lacks is created with 'self' first, because its
// absence meant default-src 'self'. The result still passes the Tor strip.
func (p SitePolicy) Apply(csp string) string {
	if p.Mode == PolicyStrict {
		return csp
	}
	add := p.additions()
	if len(add) == 0 {
		return csp
	}
	parts := strings.Split(csp, ";")
	seen := map[string]bool{}
	for i, part := range parts {
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}
		name := fields[0]
		seen[name] = true
		extra, ok := add[name]
		if !ok {
			continue
		}
		have := map[string]bool{}
		broadHTTPS := false
		for _, f := range fields[1:] {
			have[f] = true
			broadHTTPS = broadHTTPS || f == "https:"
		}
		for _, src := range dedupeSorted(extra) {
			// img-src already admits every https: origin; repeating one adds
			// bytes and nothing else.
			if have[src] || (broadHTTPS && strings.HasPrefix(src, "https://")) {
				continue
			}
			fields = append(fields, src)
		}
		parts[i] = " " + strings.Join(fields, " ")
	}
	parts[0] = strings.TrimSpace(parts[0])
	var missing []string
	for d := range add {
		if !seen[d] {
			missing = append(missing, d)
		}
	}
	sort.Strings(missing)
	for _, d := range missing {
		parts = append(parts, " "+d+" 'self' "+strings.Join(dedupeSorted(add[d]), " "))
	}
	return applyOnionCSP(strings.Join(parts, ";"))
}

// ServiceFor names the catalogue service whose sources for directive cover
// origin, so a blocked script can be answered with the service that loads it
// rather than with a typed origin. A "*." source covers any deeper name.
func ServiceFor(directive, origin string) (CSPService, bool) {
	for _, s := range cspServices {
		for _, src := range s.Sources[directive] {
			if src == origin {
				return s, true
			}
			if scheme, host, ok := strings.Cut(src, "://*."); ok {
				if rest, found := strings.CutPrefix(origin, scheme+"://"); found && strings.HasSuffix(rest, "."+host) {
					return s, true
				}
			}
		}
	}
	return CSPService{}, false
}
