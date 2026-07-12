package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/domain"
)

// middleware_domain.go — VayuDomains host resolution (Stage 1).
//
// This middleware annotates every request with the registered domain that owns
// its Host header and does nothing else. It is a total pass-through: johal.in
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
