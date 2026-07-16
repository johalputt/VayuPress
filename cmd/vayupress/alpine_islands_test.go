package main

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
)

// mustSampleKeys returns a couple of representative keys for rendering tests.
func mustSampleKeys() []apikeys.Key {
	scoped := apikeys.NewPermissions()
	scoped.Grant(apikeys.SectionPosts, apikeys.ActionRead)
	return []apikeys.Key{
		{ID: "k1", Label: "CI deploy", Prefix: "vp_ci00000", Scope: apikeys.ScopeExternal, Active: true, Permissions: scoped},
		{ID: "k2", Label: "Zapier", Prefix: "vp_zap0000", Scope: apikeys.ScopeExternal, Active: true, Permissions: scoped},
	}
}

// TestVendoredAlpineIsEvalFree is the load-bearing CSP guard for ADR-0136: the
// vendored Alpine build MUST be the eval-free CSP build, or it would demand
// 'unsafe-eval' and break the strict policy. If someone ever swaps in the
// standard Alpine build (which contains `new Function`), this fails loudly.
func TestVendoredAlpineIsEvalFree(t *testing.T) {
	b, err := fs.ReadFile(embeddedStaticFS, "js/alpine-csp.min.js")
	if err != nil {
		t.Fatalf("vendored alpine-csp.min.js missing: %v", err)
	}
	src := string(b)
	for _, banned := range []string{"eval(", "new Function", "Function(", "AsyncFunction"} {
		if strings.Contains(src, banned) {
			t.Errorf("vendored Alpine contains %q — not the eval-free CSP build (would require unsafe-eval)", banned)
		}
	}
	if !strings.Contains(src, `version:"3.15.`) {
		t.Error("vendored Alpine version marker missing (expected the 3.15.x CSP build)")
	}
	// The island registry and the Alpine build must both be embedded.
	if _, err := fs.ReadFile(embeddedStaticFS, "js/vayu-islands.js"); err != nil {
		t.Fatalf("vayu-islands.js not embedded: %v", err)
	}
}

// TestIslandRegistryIsCSPBuildSafe pins that the registry uses the CSP-build
// contract: components are registered as named Alpine.data factories on
// alpine:init (never inline expressions), and the registry itself uses no eval.
func TestIslandRegistryIsCSPBuildSafe(t *testing.T) {
	b, err := fs.ReadFile(embeddedStaticFS, "js/vayu-islands.js")
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	src := string(b)
	if !strings.Contains(src, `addEventListener('alpine:init'`) {
		t.Error("registry must register components on the alpine:init event")
	}
	for _, name := range []string{"filterList", "disclosure", "copyable"} {
		if !strings.Contains(src, `Alpine.data('`+name+`'`) {
			t.Errorf("registry missing Alpine.data(%q) factory", name)
		}
	}
	for _, banned := range []string{"eval(", "new Function"} {
		if strings.Contains(src, banned) {
			t.Errorf("island registry uses %q — forbidden under the CSP", banned)
		}
	}
}

// TestAdminFootLoadsAlpineInOrder verifies the admin shell foot loads the island
// registry BEFORE the Alpine build (so alpine:init is armed first), both
// deferred and same-origin (script-src 'self'), and that the layout stays
// CSP-safe with the additions.
func TestAdminFootLoadsAlpineInOrder(t *testing.T) {
	out := adminOSShellFoot("test-nonce", "")
	islands := strings.Index(out, "/os/static/js/vayu-islands.js")
	alpine := strings.Index(out, "/os/static/js/alpine-csp.min.js")
	if islands < 0 || alpine < 0 {
		t.Fatalf("foot missing island/alpine scripts (islands=%d alpine=%d)", islands, alpine)
	}
	if islands > alpine {
		t.Error("vayu-islands.js must load before alpine-csp.min.js so alpine:init is armed first")
	}
	if !strings.Contains(out, `src="/os/static/js/alpine-csp.min.js?v=`) || !strings.Contains(out, `defer></script>`) {
		t.Error("alpine build must be versioned + deferred")
	}
	assertCSPSafe(t, "adminOSShellFoot+alpine", out)
}

// TestAPIKeysFilterIsland proves the API Keys list carries the additive filter
// island (x-data + per-row filter text) without breaking CSP-safety.
func TestAPIKeysFilterIsland(t *testing.T) {
	keys := mustSampleKeys()
	out := osAPIKeysOwnSection(keys)
	assertCSPSafe(t, "osAPIKeysOwnSection+island", out)
	for _, want := range []string{`x-data="filterList"`, `x-model="q"`, `@input="apply()"`, `data-filter-text=`, `data-filter-empty`} {
		if !strings.Contains(out, want) {
			t.Errorf("API Keys list missing filter-island hook %q", want)
		}
	}
}
