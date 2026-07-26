// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Console scripts are served from an explicit per-file route allowlist. Adding a
// new .js file and a <script src> for it is not enough — without a matching
// r.Get the request 404s, the script never loads, and the feature is silently
// inert: the page renders, the buttons are there, and nothing happens when you
// click them.
//
// This has now happened twice. The trending widget shipped with no route (see the
// note on handleTrendingWidgetJS), and the account-recovery panel shipped the same
// way in v3.15.40 — the card rendered in full, and every control was dead.
// Reviewing for it does not work, because the missing line is in a different file
// from the change that needs it. So the build checks.

var consoleScriptRef = regexp.MustCompile(`/os/static/js/([A-Za-z0-9._-]+\.js)`)

// TestEveryReferencedConsoleScriptHasARoute walks the Go sources for console
// script references and asserts each one is actually served.
func TestEveryReferencedConsoleScriptHasARoute(t *testing.T) {
	root := filepath.Join("..", "..")

	referenced := map[string]string{} // file -> where it was referenced
	registered := map[string]bool{}

	err := filepath.Walk(filepath.Join(root, "cmd"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, readErr := os.ReadFile(path) // #nosec G304 -- walking this repository
		if readErr != nil {
			return nil
		}
		body := string(b)
		rel, _ := filepath.Rel(root, path)
		for _, m := range consoleScriptRef.FindAllStringSubmatch(body, -1) {
			name := m[1]
			// A route registration looks like r.Get("/os/static/js/x.js", …).
			if strings.Contains(body, `r.Get("/os/static/js/`+name+`"`) {
				registered[name] = true
			}
			// A reference in a <script src> is a use, not a registration.
			if strings.Contains(body, `src="/os/static/js/`+name+`?v=`) ||
				strings.Contains(body, `src="/os/static/js/`+name+`"`) {
				if _, seen := referenced[name]; !seen {
					referenced[name] = filepath.ToSlash(rel)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(referenced) == 0 {
		t.Fatal("found no console script references — the detector is broken, not the code")
	}

	var missing []string
	for name, where := range referenced {
		if !registered[name] {
			missing = append(missing, name+" (referenced from "+where+")")
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("console script has no route and will 404: %s\n"+
			"    add: r.Get(\"/os/static/js/%s\", serveAdminOSAsset(\"js/%s\", \"application/javascript; charset=utf-8\"))",
			m, strings.Fields(m)[0], strings.Fields(m)[0])
	}
}

// TestEveryServedConsoleScriptExistsOnDisk is the mirror image: a route pointing
// at a file that was renamed or deleted also 404s, and just as quietly.
func TestEveryServedConsoleScriptExistsOnDisk(t *testing.T) {
	root := filepath.Join("..", "..")
	b, err := os.ReadFile(filepath.Join(root, "cmd", "vayupress", "admin_os_ui.go"))
	if err != nil {
		t.Fatalf("read admin_os_ui.go: %v", err)
	}
	routes := regexp.MustCompile(`r\.Get\("/os/static/js/([A-Za-z0-9._-]+\.js)"`).FindAllStringSubmatch(string(b), -1)
	if len(routes) == 0 {
		t.Fatal("found no console script routes — the detector is broken")
	}
	for _, m := range routes {
		name := m[1]
		if _, err := os.Stat(filepath.Join(root, "static", "js", name)); err != nil {
			t.Errorf("route serves /os/static/js/%s but static/js/%s does not exist", name, name)
		}
	}
}
