// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/users"
)

// A client is a paying customer of the studio, not an employee of it. These
// tests are written from their side of the login: what can I reach that I was
// not given?

// The permissive default is the whole reason confinement exists. osPathMinLevel
// resolves an unenumerated non-API /os page to author level, so under the
// ladder alone a page added next year is reachable by everyone. Confinement must
// refuse it without anybody remembering to classify it.
func TestAPageNobodyClassifiedIsDeniedToClients(t *testing.T) {
	for _, p := range []string{
		"/os/zzz-future-feature",
		"/os/some-dashboard-widget",
		"/os/api/zzz-future",
	} {
		if clientPathAllowed(p) {
			t.Errorf("clientPathAllowed(%q) = true — an undeclared path must be refused, "+
				"or every page added after this one leaks to every client on the install", p)
		}
		// ...and the ladder agrees, so a bypass of one still meets the other.
		if lvl := accessLevelFor(users.RoleClient, false); lvl != accessMailOnly {
			t.Errorf("a client sits at level %d, not the floor — if confinement is ever "+
				"bypassed the ladder must still deny", lvl)
		}
	}
}

func TestClientCannotReachTheOperatorsConsole(t *testing.T) {
	denied := []string{
		"/os", "/os/posts", "/os/editor", "/os/media", "/os/settings",
		"/os/settings/security", "/os/apikeys", "/os/domains", "/os/storage",
		"/os/update", "/os/vayushield", "/os/monitoring", "/os/members",
		"/os/tor", "/os/world", "/os/backup",
		// Mailbox ADMINISTRATION, as distinct from reading one's own mail.
		"/os/vayumail", "/os/vayumail/accounts", "/os/vayumail/accounts/create",
		"/os/vayumail/accounts/delete", "/os/vayumail/dns", "/os/vayumail/pgp",
		"/os/vayumail/security",
		// Operator infrastructure state.
		"/os/api/vayuos/health", "/os/api/vayuos/security/check",
	}
	for _, p := range denied {
		if clientPathAllowed(p) {
			t.Errorf("a client can reach %q", p)
		}
	}
}

func TestClientCanReachTheThreeThingsTheyPayFor(t *testing.T) {
	allowed := []string{
		"/os/vayumail/inbox", "/os/vayumail/message/abc", "/os/vayumail/compose",
		"/os/vayumail/sent", "/os/vayumail/connect",
		"/os/api/vayuos/mail/recovery/status",
		"/os/profile", "/os/logout", "/os/static/css/admin-os.css",
	}
	for _, p := range allowed {
		if !clientPathAllowed(p) {
			t.Errorf("a client cannot reach %q, which is part of what they are sold", p)
		}
	}
}

// Prefix matching without a separator rule admits siblings. "/os/mysite" must
// not open "/os/mysite-secret", and "/os/vayumail/inbox" must not open
// "/os/vayumail/inboxes-of-everyone".
func TestClientSurfaceDoesNotAdmitSiblingRoutes(t *testing.T) {
	for _, p := range []string{
		"/os/mysite-secret", "/os/mysiteadmin",
		"/os/vayumail/inbox-all", "/os/profiles", "/os/staticky",
		"/os/logout-everyone",
	} {
		if clientPathAllowed(p) {
			t.Errorf("clientPathAllowed(%q) = true — a prefix test without an exact-or-child "+
				"rule admits sibling routes nobody declared", p)
		}
	}
}

// A client with no binding is an INVALID identity, never a client bound to the
// primary. The empty string is the primary domain's sentinel everywhere else in
// this codebase, so defaulting there would hand a customer the agency's own
// install — every post, every mailbox, every other client.
func TestAClientWithNoBindingIsRefusedRatherThanDefaulted(t *testing.T) {
	for _, id := range []string{"", "   ", "..", "../../etc", "NOTHEX", "a/b"} {
		if _, ok := clientScopeFor(users.RoleClient, id); ok {
			t.Errorf("clientScopeFor(client, %q) accepted the binding; an unusable binding "+
				"must refuse the session, not resolve to the primary domain", id)
		}
	}
	got, ok := clientScopeFor(users.RoleClient, "a1b2c3d4e5f6a1b2c3d4e5f6")
	if !ok || got != "a1b2c3d4e5f6a1b2c3d4e5f6" {
		t.Fatalf("a valid binding did not resolve: %q ok=%v", got, ok)
	}
	// A non-client never carries a scope, whatever is in the column.
	if _, ok := clientScopeFor(users.RoleAdmin, "a1b2c3d4e5f6a1b2c3d4e5f6"); ok {
		t.Error("an admin resolved a client scope — the role must gate the binding")
	}
}

// The surface table is an allowlist, so an entry that declares no audience is
// indistinguishable from a working one until a client reaches something. It must
// fail at process start, not in production.
func TestSurfaceEntryWithNoAudienceRefusesToBoot(t *testing.T) {
	saved := clientSurface
	defer func() {
		clientSurface = saved
		if recover() == nil {
			t.Error("an entry with audienceUnset did not panic; the zero value would then " +
				"silently mean 'not a client route', which is the correct answer only by luck")
		}
	}()
	clientSurface = []clientSurfaceEntry{{Prefix: "/os/oops"}} // Audience omitted
	mustValidateClientSurface()
}

func TestSurfaceRejectsMalformedEntries(t *testing.T) {
	for name, entries := range map[string][]clientSurfaceEntry{
		"not an /os prefix": {{Prefix: "/admin", Audience: audienceClient}},
		"trailing slash":    {{Prefix: "/os/thing/", Audience: audienceClient}},
		"duplicate": {
			{Prefix: "/os/mysite", Audience: audienceClient},
			{Prefix: "/os/mysite", Audience: audienceClient},
		},
	} {
		func() {
			saved := clientSurface
			defer func() {
				clientSurface = saved
				if recover() == nil {
					t.Errorf("%s did not panic", name)
				}
			}()
			clientSurface = entries
			mustValidateClientSurface()
		}()
	}
}

// osRoutes walks the REAL router and returns every registered /os path, so these
// assertions cannot drift from a list restated in a test.
func osRoutes(t *testing.T) []string {
	t.Helper()
	router := chi.NewRouter()
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				t.Skipf("route registration needs more App wiring than this test provides: %v", rec)
			}
		}()
		(&App{}).registerAdminOSUIRoutes(router)
	}()
	var out []string
	err := chi.Walk(router, func(_, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(strings.TrimSuffix(route, "/*"), "/")
		if strings.HasPrefix(route, "/os") {
			out = append(out, route)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the route table: %v", err)
	}
	if len(out) == 0 {
		t.Skip("no /os routes registered in this harness")
	}
	return out
}

// Every prefix in the client surface must match a route that actually exists.
//
// A surface entry pointing at nothing is worse than a missing one: the client's
// page 404s, the feature looks broken, and the cheapest repair an implementer
// reaches for is widening the entry — "/os/vayumail/inbox" becomes
// "/os/vayumail", which re-admits accounts/create, accounts/delete,
// accounts/update, the DNS panel and the security page in a one-word diff.
func TestEveryClientSurfaceEntryMatchesARealRoute(t *testing.T) {
	routes := osRoutes(t)
	for _, e := range clientSurface {
		if e.Audience != audienceClient {
			continue
		}
		p := strings.TrimSuffix(e.Prefix, "/")
		hit := false
		for _, rt := range routes {
			if rt == p || strings.HasPrefix(rt, p+"/") {
				hit = true
				break
			}
		}
		if !hit {
			t.Errorf("client surface entry %q matches no registered route. A dead entry makes "+
				"the client's page 404, and the obvious repair is to widen the prefix — which "+
				"is how the whole mailbox-administration surface gets re-admitted", e.Prefix)
		}
	}
}

// The inverse, and the one that matters most: no route that administers
// mailboxes, or exposes the operator's own console, may be reachable by a
// client. Asserted against the live route table so a route added under one of
// these prefixes next year is caught here rather than by a customer.
func TestNoAdministrativeRouteIsClientReachable(t *testing.T) {
	forbidden := []string{
		"/os/vayumail/accounts", "/os/vayumail/dns", "/os/vayumail/pgp",
		"/os/vayumail/security", "/os/apikeys", "/os/settings", "/os/domains",
		"/os/posts", "/os/editor", "/os/media", "/os/members", "/os/backup",
		"/os/update", "/os/storage", "/os/vayushield", "/os/tor", "/os/world",
	}
	for _, rt := range osRoutes(t) {
		for _, f := range forbidden {
			if rt != f && !strings.HasPrefix(rt, f+"/") {
				continue
			}
			if clientPathAllowed(rt) {
				t.Errorf("registered route %q is reachable by a client — it is under the "+
					"administrative prefix %q", rt, f)
			}
		}
	}
}
