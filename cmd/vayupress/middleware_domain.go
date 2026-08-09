// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
)

// middleware_domain.go — VayuDomains host resolution (Stage 1).
//
// This middleware annotates every request with the registered domain that owns
// its Host header and does nothing else. It is a total pass-through: example.test
// (the primary domain) is served exactly as before, because the resolved domain
// for the primary host is the primary record and no handler yet branches on it.
// Later stages read the active domain from the context to scope content, mail
// and members per host without touching this resolution path.

type ctxKeyDomain struct{}

// domainMiddleware resolves the request Host to a registered domain and stores
// it in the request context. Resolution never fails the request: an unknown or
// disabled host falls back to the primary domain, preserving today's behaviour
// where a single install answers on any host it receives.
func (a *App) domainMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.domains == nil {
			next.ServeHTTP(w, r)
			return
		}
		d, err := a.domains.Resolve(r.Context(), r.Host)
		if err != nil {
			// Unknown/disabled host → fall back to the primary domain so the
			// install keeps answering as it always has.
			if p, ok := a.domains.Primary(r.Context()); ok {
				d = p
			} else {
				next.ServeHTTP(w, r)
				return
			}
		}
		ctx := context.WithValue(r.Context(), ctxKeyDomain{}, d)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// activeDomain returns the resolved domain for the request, if the domain
// middleware ran and the registry was seeded.
func activeDomain(r *http.Request) (domain.Domain, bool) {
	d, ok := r.Context().Value(ctxKeyDomain{}).(domain.Domain)
	return d, ok
}

// contentScope returns the VayuDomains content scope for the request: the empty
// string for the primary domain (or when host resolution did not run), or a
// secondary domain's id. It matches the article repo's scope convention
// (dbpkg.ScopeAll disables filtering; "" is the primary; an id is a secondary),
// so public reads serve only the active domain's content (ADR-0132, Stage 2b).
func (a *App) contentScope(r *http.Request) string {
	if a.domains == nil {
		return ""
	}
	if d, ok := activeDomain(r); ok && !d.IsPrimary {
		return d.ID
	}
	return ""
}

// multiDomain reports whether per-domain content routing is active (at least one
// secondary domain is registered). When false, the public hot paths take their
// original, byte-identical route with zero added work.
func (a *App) multiDomain(r *http.Request) bool {
	return a.domains != nil && a.domains.HasSecondaries(r.Context())
}

// memberScope returns the VayuDomains scope a new member signup should be
// attributed to: the active domain when a secondary domain is registered
// (multiDomain), or "" (the primary) otherwise — so a single-domain install is
// byte-identical. Login lookups stay keyed by the globally-unique email, so this
// scope only governs signup attribution and per-domain counts (Stage 4).
func (a *App) memberScope(r *http.Request) string {
	if a.multiDomain(r) {
		return a.contentScope(r)
	}
	return ""
}

// brandForRequest returns the branded SiteSettings to render the current
// request's public pages with, and ok=true, ONLY when a secondary domain has its
// own stored brand. In every other case — a single-domain install, the primary
// domain, or a secondary domain with no brand set — it returns ok=false so the
// caller takes its original render path (the global active settings) verbatim.
// This keeps example.test byte-identical: the primary hot path pays only one cached
// HasSecondaries bool and then calls the exact same renderer it always did.
func (a *App) brandForRequest(r *http.Request) (render.SiteSettings, bool) {
	if !a.multiDomain(r) {
		return render.SiteSettings{}, false
	}
	d, ok := activeDomain(r)
	if !ok || d.IsPrimary {
		return render.SiteSettings{}, false
	}

	// ADR-0153 Phase 4. A hosted domain is rendered from ITS OWN settings scope,
	// not from the primary's with six fields painted over the top.
	//
	// The old path started at render.GetActiveSettings() — the operator's own
	// live settings — and overlaid whatever brand fields the domain had set. That
	// is why a hosted domain was the operator's site wearing a different name:
	// the other 321 keys, the whole theme among them, were never the domain's to
	// begin with.
	//
	// Now the scope is the source. An unset key resolves to the PRODUCT DEFAULT
	// (D2), so a domain that has chosen nothing looks like a clean install rather
	// than like the studio's blog.
	if sc := settings.ForDomain(d.ID); sc.Valid() && a.siteSettings != nil {
		if vals, err := a.siteSettings.GetAll(r.Context(), sc); err == nil {
			return siteSettingsFromValues(vals), true
		}
	}

	// The store could not answer. Fall back to the brand overlay rather than to
	// nothing: serving the primary's full settings under a client's hostname is
	// the failure this phase removes, and half the old behaviour beats all of it.
	b, ok := d.Brand()
	if !ok {
		return render.SiteSettings{}, false
	}
	s := render.GetActiveSettings()
	applyBrand(&s, b)
	return s, true
}

// siteSettingsFromValues builds the render view of one scope's settings.
//
// This mapping existed four times, copy-pasted, in the theme and settings save
// handlers — so a key added to one and not the others produced a value that
// saved correctly and rendered as empty until someone noticed. One function
// now, called by all of them.
func siteSettingsFromValues(v map[string]string) render.SiteSettings {
	return render.SiteSettings{
		Name:            v[settings.KeySiteName],
		Tagline:         v[settings.KeySiteTagline],
		Description:     v[settings.KeySiteDescription],
		Author:          v[settings.KeySiteAuthor],
		AuthorBio:       v[settings.KeyAuthorBio],
		ShowMembership:  v[settings.KeyMembershipButtons] == "true",
		PrimaryLight:    v[settings.KeyThemePrimaryLight],
		PrimaryDark:     v[settings.KeyThemePrimaryDark],
		AccentLight:     v[settings.KeyThemeAccentLight],
		AccentDark:      v[settings.KeyThemeAccentDark],
		CustomCSS:       v[settings.KeyThemeCustomCSS],
		Keywords:        v[settings.KeyHeadKeywords],
		ThemeColor:      v[settings.KeyHeadThemeColor],
		Robots:          v[settings.KeyHeadRobots],
		VerifyGoogle:    v[settings.KeyHeadVerifyGoogle],
		VerifyBing:      v[settings.KeyHeadVerifyBing],
		NavJSON:         v[settings.KeyNavItems],
		FooterJSON:      v[settings.KeyFooterConfig],
		OGImage:         render.OGImagePath(v[settings.KeyThemeOGImage]),
		ShowHero:        v[settings.KeyHomeHero] == "true",
		CommentsEnabled: v[settings.KeyFeatureComments] != "off",
	}
}

// applyBrand overlays a secondary domain's non-empty branding fields onto a copy
// of the site settings. Every blank field inherits the primary's value, so a
// domain can re-brand just its name and keep the rest of the operator's design.
func applyBrand(s *render.SiteSettings, b domain.Brand) {
	if b.SiteName != "" {
		s.Name = b.SiteName
	}
	if b.Tagline != "" {
		s.Tagline = b.Tagline
	}
	if b.Description != "" {
		s.Description = b.Description
	}
	if b.AccentLight != "" {
		s.AccentLight = b.AccentLight
	}
	if b.AccentDark != "" {
		s.AccentDark = b.AccentDark
	}
	if b.ThemeColor != "" {
		s.ThemeColor = b.ThemeColor
	}
}

// acceptsSecondaryMailDomain reports whether host is a registered, active,
// mail_enabled *secondary* domain (VayuDomains Stage 3b). The primary mail domain
// is handled separately by the mail engine's cfg.Domain checks; this covers only
// the opt-in secondary mail domains. With none registered it is always false, so
// the mail acceptance and Maildir-resolution paths stay byte-identical. It is
// consulted on the SMTP recipient-acceptance hot path, so it leans on the
// registry's in-memory TTL cache rather than hitting the DB per call.
func (a *App) acceptsSecondaryMailDomain(host string) bool {
	if a.domains == nil {
		return false
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	d, err := a.domains.Resolve(context.Background(), host)
	if err != nil {
		return false
	}
	return !d.IsPrimary && d.MailEnabled && d.Status == domain.StatusActive
}

// registeredHosts returns every host this install answers for, whatever its
// status. It backs the analytics referrer exclusion, where the question is "is
// this referrer us?" — and a domain that is disabled today still produced the
// rows recorded while it was live, so filtering by status would leave exactly
// those rows in the operator's list as phantom external referrals.
//
// The primary is not added here: the analytics store always excludes
// config.Cfg.Domain, so a nil hook stays byte-identical to the single-domain
// behaviour. Reads come from the registry's 30-second snapshot cache.
func (a *App) registeredHosts() []string {
	if a.domains == nil {
		return nil
	}
	list, err := a.domains.List(context.Background())
	if err != nil {
		// Returning nothing narrows the exclusion to the primary — the old
		// behaviour — rather than widening it. A registry we cannot read is a
		// reason to report a referrer we might have hidden, never a reason to
		// hide one we should have shown.
		return nil
	}
	out := make([]string, 0, len(list))
	for _, d := range list {
		if d.Host != "" {
			out = append(out, d.Host)
		}
	}
	return out
}

// mailSecondaryHosts returns the hosts of active, mail_enabled secondary domains
// (VayuDomains Stage 3b). It is empty on a single-domain install, so the mail
// admin UI that consumes it stays byte-identical there.
func (a *App) mailSecondaryHosts(ctx context.Context) []string {
	if a.domains == nil {
		return nil
	}
	list, err := a.domains.List(ctx)
	if err != nil {
		return nil
	}
	var out []string
	for _, d := range list {
		if !d.IsPrimary && d.MailEnabled && d.Status == domain.StatusActive {
			out = append(out, d.Host)
		}
	}
	return out
}

// domCacheDir sanitises a domain id into a filesystem-safe cache subdirectory
// name. Ids are already hex, but a path segment is never trusted blindly.
func domCacheDir(id string) string {
	var b strings.Builder
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			b.WriteRune(c)
		default:
			b.WriteByte('_')
		}
	}
	s := b.String()
	if s == "" {
		return "x"
	}
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}
