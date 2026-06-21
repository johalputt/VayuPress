package main

// admin_legacy.go — Admin v2 soft-deprecation (ADR-0069 Stage 2).
//
// With Stage 1 parity complete (every v2 editorial task now exists in v3,
// including convert-to-blocks per ADR-0073), v2 enters soft deprecation:
//
//   - By default, the v2 page routes and the classic console root (/admin)
//     redirect to their v3 equivalents (302 — a temporary redirect; Stage 3
//     will make these permanent 301s before the handlers are deleted).
//   - Operators who need the old surface for one more release can set the
//     environment escape hatch ADMIN_LEGACY=1, which keeps the v2 pages live
//     and renders a dismissible deprecation banner pointing at v3 with the
//     scheduled removal release.
//
// The API surface (/api/v1/*) and the operator console sub-pages (/admin/modes,
// /admin/faults, …) are unaffected — they have a separate lifecycle.

import (
	"net/http"
	"os"
	"strings"
)

// legacyRemovalRelease is the release in which the v2 handlers are deleted
// (ADR-0069 Stage 3). Surfaced in the deprecation banner so operators can plan.
const legacyRemovalRelease = "v1.6.0"

// adminLegacyEnabled reports whether the ADMIN_LEGACY escape hatch is set, which
// keeps the deprecated Admin v2 surface reachable for one more release.
func adminLegacyEnabled() bool {
	v := strings.TrimSpace(os.Getenv("ADMIN_LEGACY"))
	return v == "1" || strings.EqualFold(v, "true")
}

// v2ToV3Path maps a deprecated /admin/v2[/...] path to its v3 equivalent,
// preserving any trailing segments (e.g. an editor slug). The bare classic
// console root "/admin" maps to the v3 dashboard.
func v2ToV3Path(p string) string {
	switch {
	case p == "/admin":
		return "/admin/v3"
	case p == "/admin/v2":
		return "/admin/v3"
	case strings.HasPrefix(p, "/admin/v2/"):
		return "/admin/v3/" + strings.TrimPrefix(p, "/admin/v2/")
	default:
		return "/admin/v3"
	}
}

// legacyRedirect returns a handler that 302-redirects the current path to its
// v3 equivalent. Used while v2 is soft-deprecated and the escape hatch is off.
func legacyRedirect() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, v2ToV3Path(r.URL.Path), http.StatusFound)
	}
}

// legacyDeprecationBanner renders the dismissible banner shown atop Admin v2
// pages when the escape hatch is enabled. The dismiss state is remembered in
// localStorage by a single nonce-gated inline script (CSP-clean: no inline
// styles, no eval). All markup is static, so there is nothing to escape.
func legacyDeprecationBanner(nonce string) string {
	return `<div class="legacy-banner" data-legacy-banner role="status">
  <div class="legacy-banner__text">
    <strong>Admin v2 is deprecated.</strong>
    <span>Everything here now lives in the faster Admin v3. This surface will be removed in ` + legacyRemovalRelease + `.</span>
  </div>
  <div class="legacy-banner__actions">
    <a class="btn btn-primary btn-sm" href="/admin/v3">Switch to Admin v3</a>
    <button type="button" class="btn btn-ghost btn-sm" data-legacy-dismiss aria-label="Dismiss deprecation notice">Dismiss</button>
  </div>
</div>
<script nonce="` + nonce + `">
(function(){
  var KEY='vp_legacy_banner_dismissed';
  var el=document.querySelector('[data-legacy-banner]');
  if(!el) return;
  try{ if(localStorage.getItem(KEY)==='1'){ el.remove(); return; } }catch(e){}
  var btn=el.querySelector('[data-legacy-dismiss]');
  if(btn) btn.addEventListener('click',function(){
    try{ localStorage.setItem(KEY,'1'); }catch(e){}
    el.remove();
  });
})();
</script>`
}
