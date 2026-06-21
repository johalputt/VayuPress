package main

// admin_legacy.go — legacy admin redirection into VayuOS (`/os`).
//
// As of v1.5.0 the canonical admin surface is VayuOS, mounted at `/os`. The
// three historical surfaces — the classic console (`/admin`), Admin v2
// (`/admin/v2`), and Admin v3 (`/admin/v3`) — are legacy and redirect into the
// `/os` equivalent (302; ADR-0069 Stage 3 will make these permanent 301s in
// v1.6.0 before the handlers are deleted).
//
// Operators who need a deprecated surface for one more release can set the
// environment escape hatch ADMIN_LEGACY=1, which keeps the v2 pages live and
// renders a dismissible deprecation banner pointing at VayuOS.
//
// The API surface (/api/v1/*) and the operator console sub-pages (/admin/modes,
// /admin/faults, …) are unaffected — they have a separate lifecycle.

import (
	"net/http"
	"os"
	"strings"
)

// legacyRemovalRelease is the release in which the legacy admin handlers are
// deleted (ADR-0069 Stage 3). Surfaced in the deprecation banner so operators
// can plan.
const legacyRemovalRelease = "v1.6.0"

// adminLegacyEnabled reports whether the ADMIN_LEGACY escape hatch is set, which
// keeps the deprecated Admin v2 surface reachable for one more release.
func adminLegacyEnabled() bool {
	v := strings.TrimSpace(os.Getenv("ADMIN_LEGACY"))
	return v == "1" || strings.EqualFold(v, "true")
}

// legacyToOSPath maps a deprecated admin path (`/admin`, `/admin/v2[/...]`, or
// `/admin/v3[/...]`) to its VayuOS (`/os`) equivalent, preserving any trailing
// segments (e.g. an editor slug). The bare roots map to the VayuOS dashboard.
func legacyToOSPath(p string) string {
	switch {
	case p == "/admin", p == "/admin/v2", p == "/admin/v3":
		return "/os"
	case strings.HasPrefix(p, "/admin/v2/"):
		return "/os/" + strings.TrimPrefix(p, "/admin/v2/")
	case strings.HasPrefix(p, "/admin/v3/"):
		return "/os/" + strings.TrimPrefix(p, "/admin/v3/")
	default:
		return "/os"
	}
}

// legacyRedirect returns a handler that 302-redirects the current path to its
// VayuOS (`/os`) equivalent. Used for the legacy admin surfaces.
func legacyRedirect() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, legacyToOSPath(r.URL.Path), http.StatusFound)
	}
}

// legacyDeprecationBanner renders the dismissible banner shown atop legacy Admin
// v2 pages when the escape hatch is enabled. The dismiss state is remembered in
// localStorage by a single nonce-gated inline script (CSP-clean: no inline
// styles, no eval). All markup is static, so there is nothing to escape.
func legacyDeprecationBanner(nonce string) string {
	return `<div class="legacy-banner" data-legacy-banner role="status">
  <div class="legacy-banner__text">
    <strong>This admin surface is deprecated.</strong>
    <span>Everything now lives in the faster VayuOS. This surface will be removed in ` + legacyRemovalRelease + `.</span>
  </div>
  <div class="legacy-banner__actions">
    <a class="btn btn-primary btn-sm" href="/os">Switch to VayuOS</a>
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
