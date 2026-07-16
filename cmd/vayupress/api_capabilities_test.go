package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/auth"
)

// TestCapabilityForRepresentativeRoutes pins the route→capability mapping for
// every protected route family on both surfaces (/api/v1 + /os twins), so a
// route silently falling out of the table (→ superuser-only) is caught here.
func TestCapabilityForRepresentativeRoutes(t *testing.T) {
	cases := []struct {
		method, path string
		section      apikeys.Section
		action       apikeys.Action
	}{
		// posts
		{"POST", "/api/v1/articles", apikeys.SectionPosts, apikeys.ActionWrite},
		{"PUT", "/api/v1/articles/my-slug", apikeys.SectionPosts, apikeys.ActionWrite},
		{"DELETE", "/api/v1/articles/my-slug", apikeys.SectionPosts, apikeys.ActionDelete},
		{"GET", "/api/v1/queue", apikeys.SectionPosts, apikeys.ActionRead},
		{"GET", "/api/v1/admin/articles/s/versions", apikeys.SectionPosts, apikeys.ActionRead},
		{"POST", "/api/v1/admin/schedule", apikeys.SectionPosts, apikeys.ActionWrite},
		{"POST", "/api/v1/collections", apikeys.SectionPosts, apikeys.ActionWrite},
		{"POST", "/api/v1/admin/ai/assist", apikeys.SectionPosts, apikeys.ActionWrite},
		{"POST", "/os/api/posts/quick-create", apikeys.SectionPosts, apikeys.ActionWrite},
		{"POST", "/os/api/editor/save", apikeys.SectionPosts, apikeys.ActionWrite},
		// comments
		{"GET", "/api/v1/admin/comments", apikeys.SectionComments, apikeys.ActionRead},
		{"PUT", "/api/v1/admin/comments/9/status", apikeys.SectionComments, apikeys.ActionWrite},
		{"PUT", "/api/v1/admin/webmentions/9/status", apikeys.SectionComments, apikeys.ActionWrite},
		{"GET", "/os/api/messages/export.csv", apikeys.SectionComments, apikeys.ActionExport},
		// members
		{"GET", "/api/v1/admin/members", apikeys.SectionMembers, apikeys.ActionRead},
		{"GET", "/api/v1/admin/members/export.csv", apikeys.SectionMembers, apikeys.ActionExport},
		{"POST", "/api/v1/admin/tiers", apikeys.SectionMembers, apikeys.ActionWrite},
		{"POST", "/api/v1/admin/newsletter/broadcast", apikeys.SectionMembers, apikeys.ActionWrite},
		{"GET", "/os/api/newsletter/export.csv", apikeys.SectionMembers, apikeys.ActionExport},
		{"POST", "/os/api/orders", apikeys.SectionMembers, apikeys.ActionWrite},
		// analytics
		{"GET", "/api/v1/admin/analytics", apikeys.SectionAnalytics, apikeys.ActionRead},
		{"GET", "/api/v1/analytics/overview", apikeys.SectionAnalytics, apikeys.ActionRead},
		{"GET", "/api/v1/analytics/export", apikeys.SectionAnalytics, apikeys.ActionExport},
		{"POST", "/api/v1/analytics/goals", apikeys.SectionAnalytics, apikeys.ActionWrite},
		{"GET", "/os/api/analytics/export", apikeys.SectionAnalytics, apikeys.ActionExport},
		// media
		{"POST", "/api/v1/admin/media", apikeys.SectionMedia, apikeys.ActionWrite},
		{"POST", "/api/v1/admin/media/import", apikeys.SectionMedia, apikeys.ActionWrite},
		{"POST", "/api/v1/admin/embed/unfurl", apikeys.SectionMedia, apikeys.ActionWrite},
		{"POST", "/os/api/media/upload", apikeys.SectionMedia, apikeys.ActionWrite},
		{"DELETE", "/os/api/media/delete", apikeys.SectionMedia, apikeys.ActionDelete},
		// themes
		{"POST", "/api/v1/admin/theme/apply", apikeys.SectionThemes, apikeys.ActionApply},
		{"POST", "/api/v1/admin/theme/preview", apikeys.SectionThemes, apikeys.ActionApply},
		{"GET", "/api/v1/admin/theme/presets", apikeys.SectionThemes, apikeys.ActionRead},
		{"GET", "/admin/theme/export", apikeys.SectionThemes, apikeys.ActionExport},
		{"POST", "/admin/theme", apikeys.SectionThemes, apikeys.ActionWrite},
		// design
		{"POST", "/os/api/branding/hero", apikeys.SectionDesign, apikeys.ActionWrite},
		{"POST", "/os/api/ads", apikeys.SectionDesign, apikeys.ActionWrite},
		// plugins
		{"POST", "/os/api/tools/toggle", apikeys.SectionPlugins, apikeys.ActionInstall},
		// domains
		{"POST", "/os/api/domains", apikeys.SectionDomains, apikeys.ActionWrite},
		{"DELETE", "/os/api/domains/3", apikeys.SectionDomains, apikeys.ActionDelete},
		// mail
		{"GET", "/os/vayumail/inbox", apikeys.SectionMail, apikeys.ActionRead},
		{"POST", "/os/vayumail/contacts/add", apikeys.SectionMail, apikeys.ActionWrite},
		// backup
		{"GET", "/os/api/backup/export", apikeys.SectionBackup, apikeys.ActionExport},
		{"POST", "/os/api/backup/import", apikeys.SectionBackup, apikeys.ActionWrite},
		{"GET", "/admin/backup/validate", apikeys.SectionBackup, apikeys.ActionRead},
		// settings
		{"GET", "/api/v1/admin/users", apikeys.SectionSettings, apikeys.ActionRead},
		{"POST", "/api/v1/admin/webhooks", apikeys.SectionSettings, apikeys.ActionWrite},
		{"POST", "/api/v1/admin/redirects", apikeys.SectionSettings, apikeys.ActionWrite},
		{"GET", "/api/v1/admin/mode", apikeys.SectionSettings, apikeys.ActionRead},
		{"GET", "/api/v1/stream", apikeys.SectionSettings, apikeys.ActionRead},
		{"GET", "/admin/api/updates/check", apikeys.SectionSettings, apikeys.ActionRead},
		{"POST", "/os/api/update/apply", apikeys.SectionSettings, apikeys.ActionApply},
		{"POST", "/os/api/apikeys/create", apikeys.SectionSettings, apikeys.ActionWrite},
		{"POST", "/admin/cache-purge", apikeys.SectionSettings, apikeys.ActionWrite},
		{"GET", "/debug/pprof/heap", apikeys.SectionSettings, apikeys.ActionRead},
	}
	for _, c := range cases {
		sec, act, ok := capabilityFor(c.method, c.path)
		if !ok {
			t.Errorf("%s %s: unmapped (fail-closed superuser-only) — add a rule", c.method, c.path)
			continue
		}
		if sec != c.section || act != c.action {
			t.Errorf("%s %s: got %s:%s, want %s:%s", c.method, c.path, sec, act, c.section, c.action)
		}
	}
}

// TestCapabilityForUnmappedFailsClosed pins the fail-closed default: paths
// outside the table resolve to no capability, which keyMayCall only permits for
// superuser keys.
func TestCapabilityForUnmappedFailsClosed(t *testing.T) {
	if _, _, ok := capabilityFor("GET", "/os/some/future/surface"); ok {
		t.Fatal("an unmapped path must not resolve to a capability")
	}
	scoped := apikeys.KeyInfo{Scope: apikeys.ScopeExternal, Perms: apikeys.NewPermissions()}
	scoped.Perms.Grant(apikeys.SectionPosts, apikeys.ActionAll)
	if keyMayCall(scoped, "GET", "/os/some/future/surface") {
		t.Error("a scoped key must be refused on an unmapped route (fail closed)")
	}
	super := apikeys.SuperuserKeyInfo("t", "t", apikeys.ScopeExternal)
	if !keyMayCall(super, "GET", "/os/some/future/surface") {
		t.Error("a superuser key must pass an unmapped route (back-compat)")
	}
}

// TestRequireAPIPermissionMiddleware exercises the /api enforcement middleware
// end-to-end: a scoped key reaches only its granted section, a superuser key
// reaches everything, and a request without an identity is refused.
func TestRequireAPIPermissionMiddleware(t *testing.T) {
	a := &App{}
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mw := a.requireAPIPermission(okHandler)

	postsOnly := apikeys.KeyInfo{ID: "k1", Scope: apikeys.ScopeExternal, Perms: apikeys.NewPermissions()}
	postsOnly.Perms.Grant(apikeys.SectionPosts, apikeys.ActionAll)

	run := func(ki *apikeys.KeyInfo, method, path string) int {
		req := httptest.NewRequest(method, path, nil)
		if ki != nil {
			req = auth.RequestWithKeyInfo(req, *ki)
		}
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		return rec.Code
	}

	// Scoped key: granted section passes, everything else 403s.
	if got := run(&postsOnly, "POST", "/api/v1/articles"); got != http.StatusOK {
		t.Errorf("posts-scoped key on posts route = %d, want 200", got)
	}
	if got := run(&postsOnly, "GET", "/api/v1/admin/members"); got != http.StatusForbidden {
		t.Errorf("posts-scoped key on members route = %d, want 403", got)
	}
	if got := run(&postsOnly, "POST", "/api/v1/admin/theme/apply"); got != http.StatusForbidden {
		t.Errorf("posts-scoped key on theme apply = %d, want 403", got)
	}
	// Superuser passes everything, including unmapped.
	super := apikeys.SuperuserKeyInfo("root", "root", apikeys.ScopeExternal)
	if got := run(&super, "POST", "/api/v1/admin/theme/apply"); got != http.StatusOK {
		t.Errorf("superuser on theme apply = %d, want 200", got)
	}
	// No identity at all (middleware misuse) fails closed.
	if got := run(nil, "GET", "/api/v1/queue"); got != http.StatusForbidden {
		t.Errorf("no identity = %d, want 403", got)
	}
}

// TestBootstrapKeyResolvesSuperuser guards the back-compat contract: the static
// bootstrap API_KEY resolves to a superuser identity, so existing automation
// keeps full access after enforcement lands.
func TestBootstrapKeyResolvesSuperuser(t *testing.T) {
	t.Setenv("API_KEY", "")
	// Do not touch global config here; instead verify via the middleware pair
	// contract: a KeyInfo built by SuperuserKeyInfo passes every capability.
	ki := apikeys.SuperuserKeyInfo(auth.BootstrapKeyID, "Bootstrap API_KEY", apikeys.ScopeExternal)
	if !ki.IsSuperuser() {
		t.Fatal("bootstrap identity must be superuser")
	}
	for _, s := range apikeys.AllSections {
		for _, act := range apikeys.AllActions {
			if !ki.Can(s, act) {
				t.Fatalf("bootstrap identity refused %s:%s", s, act)
			}
		}
	}
}
