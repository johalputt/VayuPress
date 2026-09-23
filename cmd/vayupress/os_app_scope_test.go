// SPDX-License-Identifier: Apache-2.0

package main

// os_app_scope_test.go — an installed console stays an app.
//
// The manifest's scope is "/os/". Scope matching is a plain prefix test, so a
// link to "/os" — no slash — is OUTSIDE the app: an installed console that
// followed it dropped out of app mode and showed the address bar. That was the
// sidebar's Dashboard link, the phone's bottom bar, the post-login redirect and
// the command palette, all at once.
//
// The scope is read from the manifest the browser receives, not restated here,
// so a change to either side is judged against the other.

import (
	"encoding/json"
	htmpl "html/template"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func osManifestScope(t *testing.T) string {
	t.Helper()
	rr := httptest.NewRecorder()
	(&App{}).handleOSManifest(rr, httptest.NewRequest(http.MethodGet, "/os/manifest.webmanifest", nil))
	var m struct {
		Scope    string `json:"scope"`
		StartURL string `json:"start_url"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil || m.Scope == "" {
		t.Fatalf("manifest: %v %s", err, rr.Body.String())
	}
	if !strings.HasPrefix(m.StartURL, m.Scope) {
		t.Fatalf("start_url %q is outside scope %q", m.StartURL, m.Scope)
	}
	return m.Scope
}

var consoleHref = regexp.MustCompile(`(?:href|data-nav)="(/os[^"]*)"`)

func TestEveryConsoleLinkStaysInsideTheInstalledAppsScope(t *testing.T) {
	scope := osManifestScope(t)
	for _, level := range []int{accessAuthor, accessEditor, accessAdmin} {
		page := adminOSLayout("N", "Dashboard", "dashboard", &osSettings{SiteName: "Demo", AccessLevel: level}, htmpl.HTML("<p>x</p>"))
		found := 0
		for _, m := range consoleHref.FindAllStringSubmatch(page, -1) {
			found++
			if !strings.HasPrefix(m[1], scope) {
				t.Errorf("access level %d: %q is outside the app scope %q — an installed console "+
					"following it leaves app mode and shows the address bar", level, m[1], scope)
			}
		}
		if found == 0 {
			t.Fatalf("access level %d: no console link found — the assertion above checked nothing", level)
		}
	}
	if !strings.HasPrefix(osHome, scope) {
		t.Errorf("osHome %q is outside the app scope %q", osHome, scope)
	}
}
