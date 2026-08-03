// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_scope.go — which site a console page is acting on (ADR-0153 D3).
//
// The scope lives in the URL: /os/d/{id}/theme edits {id}'s theme, /os/theme
// edits the primary's. It is deliberately NOT session state.
//
// The alternative — an "acting on: test.example" switcher in the top bar — is
// less routing work and has one failure mode that cannot be defended against:
// the operator believes they are editing domain A, they are editing B, and
// NOTHING IN THE REQUEST distinguishes the two cases. No guard can catch it, no
// test can reproduce it, and the damage is a client's site silently rewritten
// with another client's settings. With the scope in the path, a write cannot be
// mis-addressed by a stale switcher, the page is bookmarkable, and a screenshot
// in a support thread says which site it is.

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/settings"
)

// ctxScopeKeyType is the context key for a resolved console scope.
type ctxScopeKeyType struct{}

var ctxScopeKey = ctxScopeKeyType{}

// ctxScopedDomainKeyType carries the resolved domain record alongside the scope,
// so a page can render its host without a second registry lookup.
type ctxScopedDomainKeyType struct{}

var ctxScopedDomainKey = ctxScopedDomainKeyType{}

// osScope returns the settings scope a console request is acting on.
//
// Absent a per-domain route this is the PRIMARY, which is what every existing
// /os page means and has always meant. That default is safe precisely because
// it is explicit: the primary is a real scope with a real owner, not an unset
// one, and the pages that use it are the operator's own.
func osScope(r *http.Request) settings.Scope {
	if sc, ok := r.Context().Value(ctxScopeKey).(settings.Scope); ok && sc.Valid() {
		return sc
	}
	return settings.ForPrimary()
}

// osScopedDomain returns the hosted domain a per-domain console page is acting
// on. The second value is false on the primary's own pages.
func osScopedDomain(r *http.Request) (domain.Domain, bool) {
	d, ok := r.Context().Value(ctxScopedDomainKey).(domain.Domain)
	return d, ok
}

// scopedDomainMiddleware resolves {id} from the path into a settings scope.
//
// Everything it refuses, it refuses by NOT ROUTING — a request that cannot be
// resolved never reaches a handler holding a scope, so there is no path where a
// page renders against a domain nobody proved exists.
func (a *App) scopedDomainMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(chi.URLParam(r, "id"))
		d, ok := a.lookupDomainByID(r, id)
		if !ok {
			// An id that names nothing is not an error to explain, it is a page
			// that does not exist. Explaining would confirm which ids are real.
			http.Redirect(w, r, "/os/domains", http.StatusSeeOther)
			return
		}
		if d.IsPrimary {
			// The primary has its own pages, and they are the ones the operator
			// already knows. Two URLs meaning the same settings is two places to
			// look when something is wrong.
			http.Redirect(w, r, "/os/website", http.StatusSeeOther)
			return
		}
		sc := settings.ForDomain(d.ID)
		if !sc.Valid() {
			// A registry row with a blank id would resolve to the PRIMARY's
			// sentinel, handing this page the operator's own settings under a
			// hosted domain's URL. Refuse rather than resolve.
			http.Redirect(w, r, "/os/domains", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), ctxScopeKey, sc)
		ctx = context.WithValue(ctx, ctxScopedDomainKey, d)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// lookupDomainByID finds a registered domain by its id.
func (a *App) lookupDomainByID(r *http.Request, id string) (domain.Domain, bool) {
	if a.domains == nil || strings.TrimSpace(id) == "" {
		return domain.Domain{}, false
	}
	list, err := a.domains.List(r.Context())
	if err != nil {
		return domain.Domain{}, false
	}
	for i := range list {
		if list[i].ID == id {
			return list[i], true
		}
	}
	return domain.Domain{}, false
}

// requireScopeMatchesPath refuses a write whose body names a different domain
// from the one in the URL.
//
// Silent rescoping is the tempting alternative and it is worse than an error:
// it reports SUCCESS for an attempt to edit somebody else's site, so the
// attempt is invisible and so is the bug that caused it. Returning false means
// the caller writes an error and nothing is saved.
func requireScopeMatchesPath(r *http.Request, bodyDomainID string) bool {
	bodyDomainID = strings.TrimSpace(bodyDomainID)
	if bodyDomainID == "" {
		return true // the body named nothing; the path is the only authority
	}
	d, ok := osScopedDomain(r)
	if !ok {
		return false
	}
	return bodyDomainID == d.ID
}
