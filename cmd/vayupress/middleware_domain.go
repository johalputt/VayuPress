package main

import (
	"context"
	"net/http"

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
